package web

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Governing: SPEC-0024 REQ-1 (Endpoint Registration), REQ-2 (Authentication), ADR-0020

// handleChatCompletions handles POST /v1/chat/completions.
// Governing: SPEC-0024 REQ-2 (Authentication), REQ-3 (Request Parsing), REQ-4 (Session Triggering), REQ-9 (Stateless Sessions), ADR-0020
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Governing: SPEC-0024 REQ-2 — read key from env on each request (supports rotation without restart)
	apiKey := os.Getenv("CLAUDEOPS_CHAT_API_KEY")
	if apiKey == "" {
		writeChatError(w, http.StatusServiceUnavailable, "Chat endpoint is disabled (CLAUDEOPS_CHAT_API_KEY not set)", "service_unavailable", "chat_endpoint_disabled")
		return
	}

	// Governing: SPEC-0024 REQ-2 — Bearer token extraction and constant-time comparison
	auth := r.Header.Get("Authorization")
	token, _ := strings.CutPrefix(auth, "Bearer ")
	if auth == "" || subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
		writeChatError(w, http.StatusUnauthorized, "Invalid API key", "authentication_error", "invalid_api_key")
		return
	}

	// Governing: SPEC-0024 REQ-3 — parse request body
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeChatError(w, http.StatusBadRequest, "Invalid request body", "invalid_request_error", "invalid_request")
		return
	}

	// Governing: SPEC-0024 REQ-3 — extract last user message as prompt
	// Governing: SPEC-0024 REQ-9 (Stateless Sessions) — only last user message is used;
	// messages history from the client is NOT injected as conversation context.
	var prompt string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			prompt = strings.TrimSpace(req.Messages[i].Content)
			break
		}
	}
	if prompt == "" {
		writeChatError(w, http.StatusBadRequest, "No user message found in messages array", "invalid_request_error", "invalid_request")
		return
	}

	// Governing: SPEC-0024 REQ-4 — trigger ad-hoc session via existing session manager
	sessionID, err := s.mgr.TriggerAdHoc(prompt)
	if err != nil {
		// Governing: SPEC-0024 REQ-4 — session conflict returns 429
		writeChatError(w, http.StatusTooManyRequests, "A session is already running. Try again shortly.", "rate_limit_error", "rate_limit_exceeded")
		return
	}

	// Return a minimal synchronous response indicating the session was triggered.
	// Full streaming (REQ-5) and synchronous wait (REQ-6) are implemented in #473.
	// Governing: SPEC-0024 REQ-6 (Synchronous Response) — non-streaming placeholder
	w.Header().Set("Content-Type", "application/json")
	resp := ChatCompletion{
		ID:     fmt.Sprintf("chatcmpl-%d", sessionID),
		Object: "chat.completion",
		Model:  "claude-ops",
		Choices: []CompletionChoice{
			{
				Index: 0,
				Message: ChatMessage{
					Role:    "assistant",
					Content: fmt.Sprintf("Session %d triggered. Streaming response will be available in a future release.", sessionID),
				},
				FinishReason: "stop",
			},
		},
		Usage: ChatUsage{},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		writeChatError(w, http.StatusInternalServerError, "Failed to encode response", "server_error", "internal_error")
	}
}

// handleModels handles GET /v1/models.
// Governing: SPEC-0024 REQ-8 (Models Endpoint), ADR-0020
// This endpoint is unauthenticated — apps probe it before asking for credentials.
func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"object": "list",
		"data": []map[string]any{
			{
				"id":       "claude-ops",
				"object":   "model",
				"created":  1700000000,
				"owned_by": "claude-ops",
			},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeChatError writes an OpenAI-format error response.
// Governing: SPEC-0024 REQ-7 (Error Response Format)
func writeChatError(w http.ResponseWriter, status int, message, errType, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(OpenAIError{
		Error: OpenAIErrorDetail{
			Message: message,
			Type:    errType,
			Code:    code,
		},
	})
}
