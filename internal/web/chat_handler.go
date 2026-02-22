package web

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

// Governing: SPEC-0024 REQ-1 (Endpoint Registration), REQ-2 (Authentication), ADR-0020

// handleChatCompletions handles POST /v1/chat/completions.
// Governing: SPEC-0024 REQ-2 (Authentication), REQ-3 (Request Parsing), REQ-4 (Session Triggering),
// REQ-5 (Streaming Response), REQ-6 (Synchronous Response), REQ-9 (Stateless Sessions), ADR-0020
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

	// Generate a request ID for the response.
	// Governing: SPEC-0024 REQ-10 — id starts with recognizable prefix
	requestID := fmt.Sprintf("chatcmpl-%s", uuid.New().String())

	if req.Stream {
		s.handleChatStream(w, r, sessionID, requestID)
	} else {
		s.handleChatSync(w, r, sessionID, requestID)
	}
}

// handleChatStream implements SSE streaming for stream:true requests.
// Governing: SPEC-0024 REQ-5 (Streaming Response), ADR-0020
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request, sessionID int64, requestID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeChatError(w, http.StatusInternalServerError, "Streaming not supported", "server_error", "internal_error")
		return
	}

	if s.rawHub == nil {
		writeChatError(w, http.StatusInternalServerError, "Streaming not available", "server_error", "internal_error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsubscribe := s.rawHub.Subscribe(int(sessionID))
	defer unsubscribe()

	// Governing: SPEC-0024 REQ-5 — send role indicator in first chunk
	sendSSEChunk(w, flusher, requestID, ChatCompletionChunk{
		ID:     requestID,
		Object: "chat.completion.chunk",
		Model:  "claude-ops",
		Choices: []Choice{{
			Index: 0,
			Delta: Delta{Role: "assistant"},
		}},
	})

	ctx := r.Context()
	var toolCallIndex int
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-ch:
			if !ok {
				// Session ended — send finish chunk and [DONE]
				sendSSEChunk(w, flusher, requestID, ChatCompletionChunk{
					ID:     requestID,
					Object: "chat.completion.chunk",
					Model:  "claude-ops",
					Choices: []Choice{{
						Index:        0,
						Delta:        Delta{},
						FinishReason: "stop",
					}},
				})
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}

			chunks := s.rawEventToChunks(raw, requestID, &toolCallIndex)
			for _, chunk := range chunks {
				sendSSEChunk(w, flusher, requestID, chunk)
			}
		}
	}
}

// handleChatSync implements the synchronous response for stream:false requests.
// Governing: SPEC-0024 REQ-6 (Synchronous Response), ADR-0020
func (s *Server) handleChatSync(w http.ResponseWriter, r *http.Request, sessionID int64, requestID string) {
	if s.rawHub == nil {
		// Fallback: return a minimal response with session ID
		w.Header().Set("Content-Type", "application/json")
		resp := ChatCompletion{
			ID:     requestID,
			Object: "chat.completion",
			Model:  "claude-ops",
			Choices: []CompletionChoice{{
				Index: 0,
				Message: ChatMessage{
					Role:    "assistant",
					Content: fmt.Sprintf("Session %d triggered.", sessionID),
				},
				FinishReason: "stop",
			}},
			Usage: ChatUsage{},
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	ch, unsubscribe := s.rawHub.Subscribe(int(sessionID))
	defer unsubscribe()

	// Collect all assistant text content until the session ends.
	var contentParts []string
	ctx := r.Context()
loop:
	for {
		select {
		case <-ctx.Done():
			writeChatError(w, http.StatusInternalServerError, "Request cancelled", "server_error", "request_cancelled")
			return
		case raw, ok := <-ch:
			if !ok {
				break loop
			}
			text := extractAssistantText(raw)
			if text != "" {
				contentParts = append(contentParts, text)
			}
		}
	}

	fullContent := strings.Join(contentParts, "")

	// Governing: SPEC-0024 REQ-6 — synchronous response with zeroed usage
	w.Header().Set("Content-Type", "application/json")
	resp := ChatCompletion{
		ID:     requestID,
		Object: "chat.completion",
		Model:  "claude-ops",
		Choices: []CompletionChoice{{
			Index: 0,
			Message: ChatMessage{
				Role:    "assistant",
				Content: fullContent,
			},
			FinishReason: "stop",
		}},
		Usage: ChatUsage{},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		writeChatError(w, http.StatusInternalServerError, "Failed to encode response", "server_error", "internal_error")
	}
}

// rawEventToChunks converts a raw NDJSON stream-json event into zero or more OpenAI SSE chunks.
// Governing: SPEC-0024 REQ-5 — event type mapping to OpenAI chunk format
func (s *Server) rawEventToChunks(raw string, requestID string, toolCallIndex *int) []ChatCompletionChunk {
	var evt struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype,omitempty"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text,omitempty"`
				Name  string          `json:"name,omitempty"`
				Input json.RawMessage `json:"input,omitempty"`
			} `json:"content"`
		} `json:"message,omitempty"`
		Result  string `json:"result,omitempty"`
		IsError bool   `json:"is_error,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return nil
	}

	var chunks []ChatCompletionChunk

	switch evt.Type {
	case "assistant":
		for _, block := range evt.Message.Content {
			switch block.Type {
			case "text":
				text := block.Text
				if text == "" {
					continue
				}
				chunks = append(chunks, ChatCompletionChunk{
					ID:     requestID,
					Object: "chat.completion.chunk",
					Model:  "claude-ops",
					Choices: []Choice{{
						Index: 0,
						Delta: Delta{Content: text},
					}},
				})
			case "tool_use":
				args := "{}"
				if len(block.Input) > 0 {
					args = string(block.Input)
				}
				idx := *toolCallIndex
				*toolCallIndex++
				chunks = append(chunks, ChatCompletionChunk{
					ID:     requestID,
					Object: "chat.completion.chunk",
					Model:  "claude-ops",
					Choices: []Choice{{
						Index: 0,
						Delta: Delta{
							ToolCalls: []ToolCall{{
								Index: idx,
								Type:  "function",
								Function: ToolFunction{
									Name:      block.Name,
									Arguments: args,
								},
							}},
						},
					}},
				})
			}
		}

	case "result":
		// Session end — send finish chunk with error text if applicable
		chunk := ChatCompletionChunk{
			ID:     requestID,
			Object: "chat.completion.chunk",
			Model:  "claude-ops",
			Choices: []Choice{{
				Index:        0,
				FinishReason: "stop",
			}},
		}
		if evt.IsError && evt.Result != "" {
			chunk.Choices[0].Delta = Delta{Content: evt.Result}
		} else {
			chunk.Choices[0].Delta = Delta{}
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}

// extractAssistantText pulls text content from an assistant stream-json event.
func extractAssistantText(raw string) string {
	var evt struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"content"`
		} `json:"message,omitempty"`
		Result  string `json:"result,omitempty"`
		IsError bool   `json:"is_error,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return ""
	}

	switch evt.Type {
	case "assistant":
		var parts []string
		for _, block := range evt.Message.Content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "")
	case "result":
		if evt.IsError && evt.Result != "" {
			return evt.Result
		}
		return evt.Result
	}
	return ""
}

// sendSSEChunk marshals a chunk and writes it as an SSE data event.
func sendSSEChunk(w http.ResponseWriter, flusher http.Flusher, _ string, chunk ChatCompletionChunk) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
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
