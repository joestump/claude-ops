package web

// Governing: SPEC-0025 REQ "Endpoint Registration", REQ "Bearer Token Authentication",
// REQ "Universal Payload Acceptance", REQ "LLM Prompt Synthesis", REQ "Session Triggering",
// REQ "Alert Trigger Type", REQ "Busy Response", REQ "Webhook Model Configuration",
// REQ "Response Format"

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultWebhookSystemPrompt is the default synthesis prompt used to convert
// an arbitrary alert payload into a focused investigation brief.
// Governing: SPEC-0025 REQ "LLM Prompt Synthesis"
const defaultWebhookSystemPrompt = `You are an alert triage assistant for an infrastructure monitoring system called Claude Ops.
Given the raw body of an inbound webhook alert, write a single focused investigation brief
(2–4 sentences) that a Claude Ops agent can act on immediately. Identify: what service or
system is affected, what the problem appears to be, and what the agent should investigate
first. Output only the investigation brief — no preamble, no JSON, no markdown.`

// handleWebhook accepts inbound alert webhooks from external monitoring tools
// (UptimeKuma, Grafana Alertmanager, PagerDuty, Healthchecks.io, etc.), synthesises
// the arbitrary payload into a focused investigation prompt via an LLM, then triggers
// an ad-hoc Claude Ops session with trigger="alert".
//
// Governing: SPEC-0025 REQ "Endpoint Registration"
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// Governing: SPEC-0025 REQ "Bearer Token Authentication" — read CLAUDEOPS_CHAT_API_KEY
	// on every request to support key rotation without restart.
	chatAPIKey := os.Getenv("CLAUDEOPS_CHAT_API_KEY")
	if chatAPIKey == "" {
		// Governing: SPEC-0025 Scenario "Key not configured"
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "webhook disabled",
			"message": "CLAUDEOPS_CHAT_API_KEY is not configured; webhook endpoint is disabled",
		})
		return
	}

	// Governing: SPEC-0025 REQ "Bearer Token Authentication" — constant-time comparison.
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if authHeader == "" || token == authHeader || subtle.ConstantTimeCompare([]byte(token), []byte(chatAPIKey)) != 1 {
		// Governing: SPEC-0025 Scenario "Invalid token", Scenario "Missing Authorization header"
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	// Governing: SPEC-0025 REQ "Universal Payload Acceptance" — read raw body regardless of Content-Type.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	bodyStr := strings.TrimSpace(string(bodyBytes))

	// Governing: SPEC-0025 Scenario "Empty body"
	if bodyStr == "" {
		writeError(w, http.StatusBadRequest, "request body must not be empty")
		return
	}

	// Governing: SPEC-0025 REQ "Session Triggering" — extract optional tier from JSON before synthesis.
	startTier := 1
	bodyForSynth := bodyStr
	if json.Valid(bodyBytes) {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(bodyBytes, &raw); err == nil {
			if tierRaw, ok := raw["tier"]; ok {
				var t int
				if err := json.Unmarshal(tierRaw, &t); err == nil && t >= 1 {
					startTier = t
				}
				// Strip tier from payload sent to LLM so synthesis stays focused on alert content.
				delete(raw, "tier")
				if stripped, err := json.Marshal(raw); err == nil {
					bodyForSynth = string(stripped)
				}
			}
		}
	}

	// Clamp tier to MaxTier.
	// Governing: SPEC-0025 Scenario "Tier clamped to MaxTier"
	if s.cfg.MaxTier > 0 && startTier > s.cfg.MaxTier {
		startTier = s.cfg.MaxTier
	}

	// Governing: SPEC-0025 REQ "Webhook Model Configuration" — read model on every request.
	model := os.Getenv("CLAUDEOPS_WEBHOOK_MODEL")
	if model == "" {
		model = s.cfg.WebhookModel
	}
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}

	// Governing: SPEC-0025 REQ "LLM Prompt Synthesis" — customisable system prompt.
	systemPrompt := os.Getenv("CLAUDEOPS_WEBHOOK_SYSTEM_PROMPT")
	if systemPrompt == "" {
		systemPrompt = s.cfg.WebhookSystemPrompt
	}
	if systemPrompt == "" {
		systemPrompt = defaultWebhookSystemPrompt
	}

	// Synthesise the payload into an investigation prompt.
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	prompt, err := synthesizePrompt(r.Context(), bodyForSynth, model, systemPrompt, anthropicKey)
	if err != nil {
		// Governing: SPEC-0025 Scenario "Synthesis failure"
		log.Printf("webhook: synthesizePrompt failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   "synthesis failed",
			"message": "LLM prompt synthesis failed; the webhook alert could not be processed",
		})
		return
	}

	// Governing: SPEC-0025 REQ "Session Triggering" — trigger with trigger="alert".
	// Always return 202 regardless of busy state so upstream tools don't treat a
	// non-2xx as a delivery failure and retry/alert on the webhook itself.
	sessionID, err := s.mgr.TriggerAdHoc(prompt, startTier, "alert")
	if err != nil {
		if strings.Contains(err.Error(), "already running") || strings.Contains(err.Error(), "queue full") {
			log.Printf("webhook: session already running, alert acknowledged but not queued")
			writeJSON(w, http.StatusAccepted, map[string]any{
				"session_id": nil,
				"status":     "acknowledged",
				"message":    "a session is already in progress; this alert was received but not queued",
			})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Governing: SPEC-0025 REQ "Response Format" — 202 Accepted with session_id, status, tier.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"session_id": sessionID,
		"status":     "triggered",
		"tier":       startTier,
	})
}

// synthesizePrompt calls the Anthropic Messages API to convert a raw alert payload
// into a focused plain-language investigation brief for Claude Ops.
//
// Governing: SPEC-0025 REQ "LLM Prompt Synthesis"
func synthesizePrompt(ctx context.Context, payload, model, systemPrompt, apiKey string) (string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 400,
		"system":     systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": payload},
		},
	})
	if err != nil {
		return "", err
	}

	synthCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(synthCtx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	hc := &http.Client{Timeout: 35 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", &synthesisError{status: resp.StatusCode, body: string(body)}
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Content) == 0 {
		return "", &synthesisError{status: resp.StatusCode, body: "empty content array"}
	}

	text := strings.TrimSpace(result.Content[0].Text)
	if text == "" {
		// Governing: SPEC-0025 Scenario "Synthesis produces empty result"
		return "", &synthesisError{status: resp.StatusCode, body: "LLM returned empty text"}
	}

	return text, nil
}

// synthesisError is returned when the Anthropic API responds with a non-200 status
// or returns empty content.
type synthesisError struct {
	status int
	body   string
}

func (e *synthesisError) Error() string {
	return "synthesis API error (HTTP " + http.StatusText(e.status) + "): " + e.body
}
