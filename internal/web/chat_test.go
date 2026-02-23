package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Governing: SPEC-0024 REQ-8 (Models Endpoint), ADR-0020

func TestModelsEndpointReturns200(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models: expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["object"] != "list" {
		t.Fatalf("expected object 'list', got %v", resp["object"])
	}
	data, ok := resp["data"].([]any)
	if !ok || len(data) == 0 {
		t.Fatal("expected non-empty data array")
	}
	model := data[0].(map[string]any)
	if model["id"] != "claude-ops" {
		t.Fatalf("expected model id 'claude-ops', got %v", model["id"])
	}
}

func TestModelsEndpointNoAuthRequired(t *testing.T) {
	e := newTestEnv(t)
	// Set a key so we can verify models doesn't require it.
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "secret-key")

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models without auth: expected 200, got %d", w.Code)
	}
}

func TestModelsEndpointContentType(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("expected application/json, got %q", ct)
	}
}

// Governing: SPEC-0024 REQ-2 (Authentication), ADR-0020

func TestChatCompletionsKeyNotSet(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when key unset, got %d", w.Code)
	}

	var errResp OpenAIError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Error.Type != "service_unavailable" {
		t.Fatalf("expected error type 'service_unavailable', got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "chat_endpoint_disabled" {
		t.Fatalf("expected error code 'chat_endpoint_disabled', got %q", errResp.Error.Code)
	}
}

func TestChatCompletionsMissingAuth(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "secret-key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	// No Authorization header
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth header, got %d", w.Code)
	}

	var errResp OpenAIError
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Error.Type != "authentication_error" {
		t.Fatalf("expected error type 'authentication_error', got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "invalid_api_key" {
		t.Fatalf("expected error code 'invalid_api_key', got %q", errResp.Error.Code)
	}
}

func TestChatCompletionsInvalidToken(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "secret-key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", w.Code)
	}
}

// closeRawHubOnTrigger returns an onTrigger callback that closes the raw hub session.
func closeRawHubOnTrigger(e *testEnv) func(int64) {
	return func(id int64) {
		// Small delay to ensure the handler has time to subscribe.
		time.Sleep(10 * time.Millisecond)
		e.rawHub.Close(int(id))
	}
}

func TestChatCompletionsValidToken(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "valid-key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"restart jellyfin"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer valid-key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp ChatCompletion
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("expected object 'chat.completion', got %q", resp.Object)
	}
	if resp.Model != "claude-ops" {
		t.Fatalf("expected model 'claude-ops', got %q", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("expected finish_reason 'stop', got %q", resp.Choices[0].FinishReason)
	}
}

func TestChatCompletionsEmptyMessages(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty messages, got %d", w.Code)
	}

	var errResp OpenAIError
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Error.Type != "invalid_request_error" {
		t.Fatalf("expected error type 'invalid_request_error', got %q", errResp.Error.Type)
	}
}

func TestChatCompletionsNoUserMessages(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"system","content":"be helpful"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when no user messages, got %d", w.Code)
	}
}

func TestChatCompletionsSessionConflict(t *testing.T) {
	trigger := &mockTrigger{nextErr: fmt.Errorf("session already running")}
	e := newTestEnvWithTrigger(t, trigger)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"status"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on session conflict, got %d", w.Code)
	}

	var errResp OpenAIError
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Error.Type != "rate_limit_error" {
		t.Fatalf("expected error type 'rate_limit_error', got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "rate_limit_exceeded" {
		t.Fatalf("expected error code 'rate_limit_exceeded', got %q", errResp.Error.Code)
	}
}

// Governing: SPEC-0024 REQ-3 (Request Parsing) — last user message extracted as prompt

func TestChatCompletionsLastUserMessageExtracted(t *testing.T) {
	trigger := &mockTrigger{nextID: 5}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"restart nginx"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify the last user message was extracted as the prompt.
	if trigger.lastPrompt != "restart nginx" {
		t.Fatalf("expected prompt 'restart nginx', got %q", trigger.lastPrompt)
	}

	var resp ChatCompletion
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Fatalf("expected id prefix 'chatcmpl-', got %q", resp.ID)
	}
}

// Governing: SPEC-0024 REQ-3 — single user message extracted correctly

func TestChatCompletionsSingleUserMessage(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Close the raw hub session after trigger so the streaming handler terminates.
	trigger.onTrigger = closeRawHubOnTrigger(e)

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"restart jellyfin"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if trigger.lastPrompt != "restart jellyfin" {
		t.Fatalf("expected prompt 'restart jellyfin', got %q", trigger.lastPrompt)
	}
}

// Governing: SPEC-0024 REQ-3 — model field accepted; unrecognized names map to tier 1 and are echoed back

func TestChatCompletionsModelFieldIgnored(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Use a model name that doesn't match any known tier — should still succeed.
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"check services"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with model='gpt-4', got %d", w.Code)
	}
	if trigger.lastPrompt != "check services" {
		t.Fatalf("expected prompt 'check services', got %q", trigger.lastPrompt)
	}
	// Governing: SPEC-0024 REQ-3 — unrecognized model names map to tier 1
	if trigger.lastStartTier != 1 {
		t.Fatalf("expected startTier 1 for unknown model, got %d", trigger.lastStartTier)
	}

	// Governing: SPEC-0024 REQ-3 — unrecognized model names are echoed back in the response.
	var resp ChatCompletion
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Model != "gpt-4" {
		t.Fatalf("expected response model 'gpt-4' (echoed from request), got %q", resp.Model)
	}
}

// Governing: SPEC-0024 REQ-9 (Stateless Sessions) — prior messages not injected as context

func TestChatCompletionsPriorMessagesNotInjected(t *testing.T) {
	trigger := &mockTrigger{nextID: 10}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Send a request with extensive history — only the last user message should be used.
	body := `{"model":"claude-ops","messages":[
		{"role":"system","content":"You are a monitoring bot"},
		{"role":"user","content":"what is the status of caddy?"},
		{"role":"assistant","content":"Caddy is healthy."},
		{"role":"user","content":"restart jellyfin"}
	]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Only the last user message should be the prompt — no system/assistant context.
	if trigger.lastPrompt != "restart jellyfin" {
		t.Fatalf("expected prompt 'restart jellyfin' (only last user message), got %q", trigger.lastPrompt)
	}
}

// Governing: SPEC-0024 REQ-3 — whitespace-only user message treated as empty

func TestChatCompletionsWhitespaceUserMessage(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"   "}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for whitespace-only user message, got %d", w.Code)
	}
}

// Governing: SPEC-0024 REQ-4 — 429 error body matches spec exactly

func TestChatCompletions429ErrorBody(t *testing.T) {
	trigger := &mockTrigger{nextErr: fmt.Errorf("session already running")}
	e := newTestEnvWithTrigger(t, trigger)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"status"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}

	var errResp OpenAIError
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Error.Message != "A session is already running. Try again shortly." {
		t.Fatalf("expected exact 429 message, got %q", errResp.Error.Message)
	}
	if errResp.Error.Type != "rate_limit_error" {
		t.Fatalf("expected type 'rate_limit_error', got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "rate_limit_exceeded" {
		t.Fatalf("expected code 'rate_limit_exceeded', got %q", errResp.Error.Code)
	}
}

// Governing: SPEC-0024 REQ-6 — synchronous response includes usage with zeroed tokens

func TestChatCompletionsUsageZeroed(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp ChatCompletion
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0 || resp.Usage.TotalTokens != 0 {
		t.Fatalf("expected all usage tokens to be 0, got prompt=%d completion=%d total=%d",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}
}

// Governing: SPEC-0024 REQ-3 — stream field defaults to false when omitted

func TestChatCompletionsStreamFieldDefaults(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Omit stream field entirely — should default to synchronous response.
	body := `{"model":"claude-ops","messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 without stream field, got %d", w.Code)
	}

	// Verify response is a complete ChatCompletion (not SSE chunks).
	var resp ChatCompletion
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("should be valid JSON (non-streaming): %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("expected object 'chat.completion', got %q", resp.Object)
	}
}

func TestChatCompletionsInvalidJSON(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{invalid"))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestChatCompletionsErrorFormatJSON(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Send request with wrong token to get 401 and verify error format.
	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("error response should be JSON, got %q", ct)
	}

	var errResp OpenAIError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("error response should be valid JSON: %v", err)
	}
	if errResp.Error.Message == "" {
		t.Fatal("error message should not be empty")
	}
}

// Governing: SPEC-0024 REQ-1 (Endpoint Registration) — routes coexist with existing routes

func TestChatRoutesCoexistWithDashboard(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Dashboard still works.
	reqDash := httptest.NewRequest("GET", "/", nil)
	wDash := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(wDash, reqDash)
	if wDash.Code != http.StatusOK {
		t.Fatalf("GET /: expected 200, got %d", wDash.Code)
	}

	// API v1 still works.
	reqAPI := httptest.NewRequest("GET", "/api/v1/health", nil)
	wAPI := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(wAPI, reqAPI)
	if wAPI.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/health: expected 200, got %d", wAPI.Code)
	}

	// Chat models endpoint works.
	reqModels := httptest.NewRequest("GET", "/v1/models", nil)
	wModels := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(wModels, reqModels)
	if wModels.Code != http.StatusOK {
		t.Fatalf("GET /v1/models: expected 200, got %d", wModels.Code)
	}

	// Chat completions endpoint works with auth.
	chatBody := `{"model":"claude-ops","messages":[{"role":"user","content":"test"}]}`
	reqChat := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody))
	reqChat.Header.Set("Authorization", "Bearer key")
	wChat := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(wChat, reqChat)
	if wChat.Code != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions: expected 200, got %d", wChat.Code)
	}
}

// --- New tests for #473 ---

// Governing: SPEC-0024 REQ-5 — SSE streaming response format

func TestChatStreamingSSEFormat(t *testing.T) {
	trigger := &mockTrigger{nextID: 42}
	e := newTestEnvWithTrigger(t, trigger)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Publish assistant text and a result event, then close.
	trigger.onTrigger = func(id int64) {
		time.Sleep(10 * time.Millisecond)
		e.rawHub.Publish(int(id), `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`)
		time.Sleep(5 * time.Millisecond)
		e.rawHub.Publish(int(id), `{"type":"result","result":"done","is_error":false}`)
		time.Sleep(5 * time.Millisecond)
		e.rawHub.Close(int(id))
	}

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	output := w.Body.String()

	// Should contain data: lines with JSON chunks.
	if !strings.Contains(output, "data: ") {
		t.Fatal("expected SSE data: lines")
	}

	// Should end with data: [DONE]
	if !strings.Contains(output, "data: [DONE]") {
		t.Fatal("expected final [DONE] event")
	}

	// Parse individual chunks from the SSE output.
	chunks := parseSSEChunks(t, output)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// First chunk should have role indicator.
	if chunks[0].Choices[0].Delta.Role != "assistant" {
		t.Fatalf("first chunk should have role 'assistant', got %q", chunks[0].Choices[0].Delta.Role)
	}

	// Should have a content delta with "Hello world".
	foundContent := false
	for _, c := range chunks {
		if c.Choices[0].Delta.Content == "Hello world" {
			foundContent = true
			break
		}
	}
	if !foundContent {
		t.Fatal("expected a chunk with content 'Hello world'")
	}

	// All chunks should have correct object type.
	for _, c := range chunks {
		if c.Object != "chat.completion.chunk" {
			t.Fatalf("expected object 'chat.completion.chunk', got %q", c.Object)
		}
		if c.Model != "claude-ops" {
			t.Fatalf("expected model 'claude-ops', got %q", c.Model)
		}
	}
}

// Governing: SPEC-0024 REQ-5 — tool_use events mapped to tool_calls deltas

func TestChatStreamingToolCallDelta(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	trigger.onTrigger = func(id int64) {
		time.Sleep(10 * time.Millisecond)
		e.rawHub.Publish(int(id), `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"docker ps"}}]}}`)
		time.Sleep(5 * time.Millisecond)
		e.rawHub.Close(int(id))
	}

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"check"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	chunks := parseSSEChunks(t, w.Body.String())

	foundToolCall := false
	for _, c := range chunks {
		if len(c.Choices) > 0 && len(c.Choices[0].Delta.ToolCalls) > 0 {
			tc := c.Choices[0].Delta.ToolCalls[0]
			if tc.Type != "function" {
				t.Fatalf("expected tool call type 'function', got %q", tc.Type)
			}
			if tc.Function.Name != "Bash" {
				t.Fatalf("expected tool name 'Bash', got %q", tc.Function.Name)
			}
			// Arguments should be a JSON string.
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				t.Fatalf("tool arguments should be valid JSON: %v", err)
			}
			if args["command"] != "docker ps" {
				t.Fatalf("expected command 'docker ps', got %v", args["command"])
			}
			foundToolCall = true
			break
		}
	}
	if !foundToolCall {
		t.Fatal("expected a tool_call delta in streaming output")
	}
}

// Governing: SPEC-0024 REQ-6 — sync response collects all assistant text

func TestChatSyncResponseCollectsText(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	trigger.onTrigger = func(id int64) {
		time.Sleep(10 * time.Millisecond)
		e.rawHub.Publish(int(id), `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "}]}}`)
		e.rawHub.Publish(int(id), `{"type":"assistant","message":{"content":[{"type":"text","text":"world"}]}}`)
		e.rawHub.Publish(int(id), `{"type":"result","result":"All done.","is_error":false}`)
		time.Sleep(5 * time.Millisecond)
		e.rawHub.Close(int(id))
	}

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp ChatCompletion
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("expected object 'chat.completion', got %q", resp.Object)
	}
	// Content should include both assistant text blocks and the result.
	if !strings.Contains(resp.Choices[0].Message.Content, "Hello ") {
		t.Fatalf("expected content to contain 'Hello ', got %q", resp.Choices[0].Message.Content)
	}
	if !strings.Contains(resp.Choices[0].Message.Content, "world") {
		t.Fatalf("expected content to contain 'world', got %q", resp.Choices[0].Message.Content)
	}
}

// Governing: SPEC-0024 REQ-7 — all error codes use OpenAI error format

func TestChatErrorFormat400(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	// Invalid JSON body -> 400
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var errResp OpenAIError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("400 error should be valid OpenAI error JSON: %v", err)
	}
	if errResp.Error.Message == "" || errResp.Error.Type == "" || errResp.Error.Code == "" {
		t.Fatal("400 error should have message, type, and code fields")
	}
}

func TestChatErrorFormat401(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	var errResp OpenAIError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("401 error should be valid OpenAI error JSON: %v", err)
	}
	if errResp.Error.Type != "authentication_error" {
		t.Fatalf("401 should have type 'authentication_error', got %q", errResp.Error.Type)
	}
	if errResp.Error.Code != "invalid_api_key" {
		t.Fatalf("401 should have code 'invalid_api_key', got %q", errResp.Error.Code)
	}
}

func TestChatErrorFormat503(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}

	var errResp OpenAIError
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("503 error should be valid OpenAI error JSON: %v", err)
	}
	if errResp.Error.Type != "service_unavailable" {
		t.Fatalf("503 should have type 'service_unavailable', got %q", errResp.Error.Type)
	}
}

// Governing: SPEC-0024 REQ-10 — SDK compatibility: id prefix, object field

func TestChatResponseIDPrefix(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	var resp ChatCompletion
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Fatalf("id should start with 'chatcmpl-', got %q", resp.ID)
	}
}

// Governing: SPEC-0024 REQ-3 (model field maps to starting tier), ADR-0020 (Tier Selection)

func TestModelToTier(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"claude-ops", 1},
		{"claude-ops-tier1", 1},
		{"claude-ops-tier2", 2},
		{"claude-ops-tier3", 3},
		{"gpt-4", 1},
		{"", 1},
		{"unknown-model", 1},
	}
	for _, tc := range cases {
		got := modelToTier(tc.model)
		if got != tc.want {
			t.Errorf("modelToTier(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// Governing: SPEC-0024 REQ-8 (Models Endpoint returns all tier IDs), ADR-0020 (Tier Selection)

func TestModelsEndpointReturnsAllTierIDs(t *testing.T) {
	e := newTestEnv(t)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models: expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	data, ok := resp["data"].([]any)
	if !ok {
		t.Fatal("expected data array")
	}

	wantIDs := map[string]bool{
		"claude-ops":       false,
		"claude-ops-tier1": false,
		"claude-ops-tier2": false,
		"claude-ops-tier3": false,
	}
	for _, entry := range data {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		wantIDs[id] = true
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("expected model id %q in /v1/models response", id)
		}
	}
	if len(data) != 4 {
		t.Errorf("expected 4 model entries, got %d", len(data))
	}
}

// Governing: SPEC-0024 REQ-3 (model field maps to starting tier), ADR-0020 (Tier Selection)

func TestChatCompletionsTier2ModelTriggersWithStartTier2(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops-tier2","messages":[{"role":"user","content":"restart jellyfin"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with model='claude-ops-tier2', got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify that startTier=2 was passed to TriggerAdHoc.
	if trigger.lastStartTier != 2 {
		t.Errorf("expected startTier=2 for claude-ops-tier2, got %d", trigger.lastStartTier)
	}

	// Response model should echo back the requested tier model.
	var resp ChatCompletion
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Model != "claude-ops-tier2" {
		t.Errorf("expected response model 'claude-ops-tier2', got %q", resp.Model)
	}
}

func TestChatCompletionsTier3ModelTriggersWithStartTier3(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops-tier3","messages":[{"role":"user","content":"full redeployment"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with model='claude-ops-tier3', got %d", w.Code)
	}
	if trigger.lastStartTier != 3 {
		t.Errorf("expected startTier=3 for claude-ops-tier3, got %d", trigger.lastStartTier)
	}
}

func TestChatCompletionsDefaultModelTriggersWithStartTier1(t *testing.T) {
	trigger := &mockTrigger{nextID: 1}
	e := newTestEnvWithTrigger(t, trigger)
	trigger.onTrigger = closeRawHubOnTrigger(e)
	t.Setenv("CLAUDEOPS_CHAT_API_KEY", "key")

	body := `{"model":"claude-ops","messages":[{"role":"user","content":"status"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key")
	w := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with model='claude-ops', got %d", w.Code)
	}
	if trigger.lastStartTier != 1 {
		t.Errorf("expected startTier=1 for claude-ops, got %d", trigger.lastStartTier)
	}
}

// parseSSEChunks extracts ChatCompletionChunk objects from SSE output.
func parseSSEChunks(t *testing.T, body string) []ChatCompletionChunk {
	t.Helper()
	var chunks []ChatCompletionChunk
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Logf("skipping unparseable SSE chunk: %s", data)
			continue
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}
