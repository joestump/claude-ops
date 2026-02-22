# SPEC-0024: OpenAI-Compatible Chat Endpoint — Design

## Architecture Overview

The chat endpoint is a compatibility shim that translates between the OpenAI Chat Completions wire format and Claude Ops' existing session machinery. It adds two routes to the existing HTTP server and introduces a stream format transformer. No new subsystems are required.

```
Client (iOS/Android app)
    │
    │  POST /v1/chat/completions
    │  Authorization: Bearer <key>
    │  {"messages":[{"role":"user","content":"restart jellyfin"}],"stream":true}
    ▼
internal/web/chat_handler.go
    │  1. Authenticate (constant-time compare vs CLAUDEOPS_CHAT_API_KEY)
    │  2. Extract last user message as prompt
    │  3. Call Manager.TriggerAdHoc(prompt)  ──────────────────────────────┐
    │  4. Subscribe to session output stream                                │
    │                                                                       ▼
    │                                               internal/session/manager.go
    │                                                   │  (existing ad-hoc trigger)
    │                                                   │  Spawns CLI subprocess
    │                                                   │  --output-format stream-json
    │                                                   │
    │                                               stream-json events
    │                                                   │
    │  5. Transform stream-json events → OpenAI SSE chunks  ◄──────────────┘
    │     (internal/web/openai.go)
    │
    ▼
SSE stream to client:
    data: {"choices":[{"delta":{"role":"assistant","content":"Checking..."}}]}
    data: {"choices":[{"delta":{"tool_calls":[{"function":{"name":"Bash",...}}]}}]}
    data: [DONE]
```

## Package Structure

```
internal/web/
    chat_handler.go      # POST /v1/chat/completions, GET /v1/models handlers
    openai.go            # OpenAI schema types + stream-json → OpenAI SSE transformer
    server.go            # route registration (add /v1/* routes to mux)
```

No new packages. The chat functionality is co-located with the existing web handlers.

## stream-json → OpenAI SSE Transformation

The Claude Code CLI emits `--output-format stream-json` events. The transformer maps these to OpenAI SSE chunks:

| stream-json event type | OpenAI chunk |
|------------------------|--------------|
| `assistant` with text content | `choices[0].delta.content` delta |
| `tool_use` (Bash, Read, Glob, etc.) | `choices[0].delta.tool_calls[0]` with `function.name` and `function.arguments` |
| `result` (session end) | finish chunk with `finish_reason: "stop"` |
| `error` | finish chunk with `finish_reason: "stop"`, error text in content |

Tool inputs are JSON-encoded and placed in `function.arguments` as a string (matching OpenAI's function calling format). The `index` field on tool call deltas starts at 0 and increments for each tool call in the session.

### Streaming implementation

The session manager already provides a mechanism for the dashboard's SSE endpoint to subscribe to stream-json output. The chat handler reuses this subscription, applying the OpenAI transformer before writing to the HTTP response.

For `stream: false`, the handler collects all output until the session ends, then returns the assembled completion as a single JSON response.

## Authentication

```go
func (s *Server) authenticateChatRequest(r *http.Request) bool {
    key := os.Getenv("CLAUDEOPS_CHAT_API_KEY")
    if key == "" {
        return false // endpoint disabled
    }
    auth := r.Header.Get("Authorization")
    token, _ := strings.CutPrefix(auth, "Bearer ")
    return subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1
}
```

Constant-time comparison prevents timing side-channels. The key is read from the environment on each request (not cached) so it can be rotated without a restart.

## OpenAI Schema Types

Minimal Go structs matching the OpenAI Chat Completions wire format, defined in `internal/web/openai.go`:

```go
type ChatRequest struct {
    Model    string          `json:"model"`
    Messages []ChatMessage   `json:"messages"`
    Stream   bool            `json:"stream"`
}

type ChatMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type ChatCompletionChunk struct {
    ID      string   `json:"id"`
    Object  string   `json:"object"`
    Model   string   `json:"model"`
    Choices []Choice `json:"choices"`
}

type Choice struct {
    Index        int    `json:"index"`
    Delta        Delta  `json:"delta"`
    FinishReason string `json:"finish_reason,omitempty"`
}

type Delta struct {
    Role      string     `json:"role,omitempty"`
    Content   string     `json:"content,omitempty"`
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
    Index    int          `json:"index"`
    Type     string       `json:"type"`
    Function ToolFunction `json:"function"`
}

type ToolFunction struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON-encoded string
}
```

These types are only used internally — they are not added to the OpenAPI spec at `/api/openapi.yaml` since `/v1/` routes are outside the `/api/v1/` namespace.

## Session Conflict Handling

```go
err := s.sessionManager.TriggerAdHoc(prompt)
if errors.Is(err, session.ErrSessionRunning) {
    w.WriteHeader(http.StatusTooManyRequests)
    json.NewEncoder(w).Encode(openAIError{
        Error: openAIErrorDetail{
            Message: "A session is already running. Try again shortly.",
            Type:    "rate_limit_error",
            Code:    "rate_limit_exceeded",
        },
    })
    return
}
```

HTTP 429 is used (not 409) because OpenAI-compatible clients handle 429 as a retryable rate limit error with backoff, which is the correct behavior.

## Models Endpoint

```go
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(map[string]interface{}{
        "object": "list",
        "data": []map[string]interface{}{
            {
                "id":       "claude-ops",
                "object":   "model",
                "created":  1700000000,
                "owned_by": "claude-ops",
            },
        },
    })
}
```

This endpoint is unauthenticated so apps that probe it before prompting for credentials work correctly.

## Trade-offs and Limitations

**Stateless sessions**: Each request creates a new session. The `messages` history from the client is not injected as agent context. This is intentional — Claude Ops sessions are monitoring runs, not general-purpose chat. The agent's persistent memory (SPEC-0015) provides cross-session operational knowledge about services.

**Token counts**: The CLI subprocess does not expose token-level usage. All `usage` fields are 0. Some apps display token counts in the UI; these will show as 0. This is acceptable — token cost is tracked per-session in the Claude Ops dashboard.

**Tool call format**: OpenAI tool calls use a `function.arguments` string (JSON-encoded). stream-json tool inputs are already JSON objects, so the transformer JSON-encodes them to strings. This is correct per the OpenAI spec but means the arguments appear double-encoded in the raw wire format.

**No system message support**: System messages in the `messages` array are ignored. The agent's behavior is governed by its tier prompt file and `CLAUDE.md`, not by client-supplied system prompts. This is a deliberate security choice — operators cannot override the agent's behavior via the chat interface.

**Single API key**: There is one shared key, not per-operator credentials. If the key is compromised, rotate `CLAUDEOPS_CHAT_API_KEY` and restart. All active sessions complete before the new key takes effect (the key is read per-request, so new requests after the restart use the new key immediately).

## Network Exposure

The `/v1/` endpoint is served on the same port as the dashboard. For home-lab deployments:

- **Recommended**: Expose via Caddy reverse proxy (already in use) with TLS termination. The existing Caddy config likely already exposes the dashboard — extending it to cover `/v1/` requires no additional containers.
- **Alternative**: Tailscale/WireGuard VPN access. Operators already likely use this for dashboard access.
- **Not recommended**: Directly expose the Claude Ops port without TLS.

## Relationship to Existing Endpoints

The chat endpoint is additive — it does not change any existing behavior:

| Path | Purpose | Auth |
|------|---------|------|
| `/` | HTML dashboard | None (dashboard is internal) |
| `/api/v1/*` | REST API (SPEC-0017) | None (internal) |
| `/v1/chat/completions` | OpenAI chat (SPEC-0024) | Bearer token |
| `/v1/models` | Model listing | None |

The `/api/v1/sessions/trigger` REST endpoint remains unchanged and continues to work independently of the chat endpoint.
