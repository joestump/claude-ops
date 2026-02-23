package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/joestump/claude-ops/internal/db"
)

// generateBusyResponse returns an LLM-generated first-person response that
// explains Claude Ops is currently busy running a session and summarises what
// it has found so far.  Falls back to a static message on any error.
func generateBusyResponse(ctx context.Context, database *db.DB, apiKey string) string {
	fallback := "I'm currently busy running a monitoring session. Please try again in a few minutes."

	if database == nil {
		return fallback
	}

	session, err := database.RunningSession()
	if err != nil || session == nil {
		return fallback
	}

	// Gather recent events from this session for context.
	events, _ := database.ListEvents(10, 0, nil, nil)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are Claude Ops, an infrastructure monitoring agent currently running a Tier %d monitoring session (session #%d, started %s).\n", session.Tier, session.ID, session.StartedAt))
	sb.WriteString("A user has sent a message while you are busy. Write a brief 2–3 sentence first-person response:\n")
	sb.WriteString("1. Acknowledge that you received their message.\n")
	sb.WriteString("2. State that you are currently running an active monitoring session.\n")
	if len(events) > 0 {
		sb.WriteString("3. Briefly summarise the most recent findings below.\n\n")
		sb.WriteString("Recent findings (newest first):\n")
		for _, e := range events {
			svc := ""
			if e.Service != nil {
				svc = "[" + *e.Service + "] "
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s%s\n", e.Level, svc, e.Message))
		}
	} else {
		sb.WriteString("3. Let them know you will be available shortly.\n")
	}
	sb.WriteString("\nRespond only with the message text — no JSON, no markdown headers, no preamble.")

	systemPrompt := sb.String()

	if apiKey == "" {
		return fallback
	}

	payload, err := json.Marshal(map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 200,
		"system":     systemPrompt,
		"messages":   []map[string]any{{"role": "user", "content": "What are you up to right now?"}},
	})
	if err != nil {
		return fallback
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return fallback
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return fallback
	}
	defer resp.Body.Close() //nolint:errcheck

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Content) == 0 {
		return fallback
	}
	text := strings.TrimSpace(result.Content[0].Text)
	if text == "" {
		return fallback
	}
	return text
}
