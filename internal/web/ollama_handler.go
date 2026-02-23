package web

// Ollama-compatible API endpoints so clients that speak the Ollama protocol
// can point at Claude Ops without any configuration changes.
//
// Endpoint map:
//   GET  /api/version          → version string (unauthenticated)
//   GET  /api/tags             → list models in Ollama format (unauthenticated)
//   POST /api/chat             → chat completion (requires CLAUDEOPS_CHAT_API_KEY)
//   POST /api/generate         → text-completion alias for /api/chat
//
// Authentication follows the same convention as the OpenAI endpoints: if
// CLAUDEOPS_CHAT_API_KEY is set, requests to /api/chat and /api/generate must
// supply it as a Bearer token.  /api/tags and /api/version are always open.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// handleOllamaVersion handles GET /api/version.
func (s *Server) handleOllamaVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"version": "claude-ops"})
}

// ollamaModelEntry builds a single model entry in Ollama /api/tags format.
func ollamaModelEntry(name string) map[string]any {
	return map[string]any{
		"name":        name + ":latest",
		"model":       name + ":latest",
		"modified_at": time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"size":        0,
		"digest":      "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"details": map[string]any{
			"parent_model":       "",
			"format":             "claude",
			"family":             "claude-ops",
			"families":           []string{"claude-ops"},
			"parameter_size":     "unknown",
			"quantization_level": "unknown",
		},
	}
}

// handleOllamaTags handles GET /api/tags (unauthenticated).
// Returns the same four model IDs as the OpenAI /v1/models endpoint but in
// Ollama's response shape.
func (s *Server) handleOllamaTags(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"models": []map[string]any{
			ollamaModelEntry("claude-ops"),
			ollamaModelEntry("claude-ops-tier1"),
			ollamaModelEntry("claude-ops-tier2"),
			ollamaModelEntry("claude-ops-tier3"),
		},
	})
}

// ollamaRequest is the JSON body for POST /api/chat and POST /api/generate.
type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	// generate-style fields (treated as a single user message)
	Prompt string `json:"prompt"`
	Stream *bool  `json:"stream"` // nil means streaming (Ollama default)
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// handleOllamaChat handles POST /api/chat.
// handleOllamaGenerate handles POST /api/generate (same logic, generate style).
func (s *Server) handleOllamaChat(w http.ResponseWriter, r *http.Request) {
	s.ollamaDispatch(w, r, false)
}

func (s *Server) handleOllamaGenerate(w http.ResponseWriter, r *http.Request) {
	s.ollamaDispatch(w, r, true)
}

// ollamaDispatch is the shared implementation for /api/chat and /api/generate.
// generateStyle=true means the body uses "prompt" instead of "messages".
func (s *Server) ollamaDispatch(w http.ResponseWriter, r *http.Request, generateStyle bool) {
	// Authentication: required when CLAUDEOPS_CHAT_API_KEY is set.
	apiKey := os.Getenv("CLAUDEOPS_CHAT_API_KEY")
	if apiKey != "" {
		auth := r.Header.Get("Authorization")
		token, _ := strings.CutPrefix(auth, "Bearer ")
		if auth == "" || token != apiKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid api key"})
			return
		}
	}

	var req ollamaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	// Extract the prompt text.
	var prompt string
	if generateStyle {
		prompt = strings.TrimSpace(req.Prompt)
	} else {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				prompt = strings.TrimSpace(req.Messages[i].Content)
				break
			}
		}
	}
	if prompt == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no user message found"})
		return
	}

	startTier := modelToTier(req.Model)
	sessionID, err := s.mgr.TriggerAdHoc(prompt, startTier, "api")
	if err != nil {
		// Session already running — generate a first-person LLM busy response
		// and return it in the appropriate Ollama format.
		busyMsg := generateBusyResponse(r.Context(), s.db, os.Getenv("ANTHROPIC_API_KEY"))
		wantStream := req.Stream == nil || *req.Stream
		modelName := req.Model
		if modelName == "" {
			modelName = "claude-ops"
		}
		w.Header().Set("Content-Type", "application/json")
		if wantStream {
			flusher, ok := w.(http.Flusher)
			if ok {
				w.Header().Set("Content-Type", "application/x-ndjson")
				s.writeOllamaChunk(w, flusher, modelName, busyMsg, false, generateStyle)
				s.writeOllamaChunk(w, flusher, modelName, "", true, generateStyle)
				return
			}
		}
		// Non-streaming or no flusher: single JSON object.
		var resp map[string]any
		if generateStyle {
			resp = map[string]any{
				"model": modelName, "created_at": time.Now().UTC().Format(time.RFC3339Nano),
				"response": busyMsg, "done": true, "done_reason": "stop",
			}
		} else {
			resp = map[string]any{
				"model": modelName, "created_at": time.Now().UTC().Format(time.RFC3339Nano),
				"message": map[string]string{"role": "assistant", "content": busyMsg},
				"done": true, "done_reason": "stop",
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Ollama streams NDJSON by default; stream:false returns a single JSON object.
	wantStream := req.Stream == nil || *req.Stream
	modelName := req.Model
	if modelName == "" {
		modelName = "claude-ops"
	}

	if wantStream {
		s.ollamaStream(w, r, sessionID, modelName, generateStyle)
	} else {
		s.ollamaSync(w, r, sessionID, modelName, generateStyle)
	}
}

// ollamaStream writes Ollama NDJSON streaming chunks until the session ends.
func (s *Server) ollamaStream(w http.ResponseWriter, r *http.Request, sessionID int64, model string, generateStyle bool) {
	flusher, ok := w.(http.Flusher)
	if !ok || s.rawHub == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")

	ch, unsubscribe := s.rawHub.Subscribe(int(sessionID))
	defer unsubscribe()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-ch:
			if !ok {
				// Session ended — send done marker.
				s.writeOllamaChunk(w, flusher, model, "", true, generateStyle)
				return
			}
			text := extractAssistantText(raw)
			if text != "" {
				s.writeOllamaChunk(w, flusher, model, text, false, generateStyle)
			}
		}
	}
}

// writeOllamaChunk writes a single NDJSON line in Ollama format.
func (s *Server) writeOllamaChunk(w http.ResponseWriter, flusher http.Flusher, model, content string, done bool, generateStyle bool) {
	var chunk map[string]any
	if generateStyle {
		chunk = map[string]any{
			"model":      model,
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"response":   content,
			"done":       done,
		}
		if done {
			chunk["done_reason"] = "stop"
		}
	} else {
		chunk = map[string]any{
			"model":      model,
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"message":    map[string]string{"role": "assistant", "content": content},
			"done":       done,
		}
		if done {
			chunk["done_reason"] = "stop"
		}
	}
	data, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(w, "%s\n", data)
	flusher.Flush()
}

// ollamaSync collects all output and returns a single Ollama response object.
func (s *Server) ollamaSync(w http.ResponseWriter, r *http.Request, sessionID int64, model string, generateStyle bool) {
	if s.rawHub == nil {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"model":      model,
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"message":    map[string]string{"role": "assistant", "content": fmt.Sprintf("Session %d triggered.", sessionID)},
			"done":       true,
			"done_reason": "stop",
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	ch, unsubscribe := s.rawHub.Subscribe(int(sessionID))
	defer unsubscribe()

	var parts []string
	ctx := r.Context()
loop:
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-ch:
			if !ok {
				break loop
			}
			text := extractAssistantText(raw)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}

	content := strings.Join(parts, "")
	w.Header().Set("Content-Type", "application/json")
	var resp map[string]any
	if generateStyle {
		resp = map[string]any{
			"model":       model,
			"created_at":  time.Now().UTC().Format(time.RFC3339Nano),
			"response":    content,
			"done":        true,
			"done_reason": "stop",
		}
	} else {
		resp = map[string]any{
			"model":       model,
			"created_at":  time.Now().UTC().Format(time.RFC3339Nano),
			"message":     map[string]string{"role": "assistant", "content": content},
			"done":        true,
			"done_reason": "stop",
		}
	}
	_ = json.NewEncoder(w).Encode(resp)
}
