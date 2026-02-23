---
status: accepted
date: 2026-02-22
---

# SPEC-0024: OpenAI-Compatible Chat Endpoint

## Overview

Claude Ops exposes an OpenAI-compatible `/v1/chat/completions` endpoint on its existing HTTP server, enabling operators to use any OpenAI-compatible iOS or Android app as a mobile interface. Operators configure their app with the Claude Ops base URL and a pre-shared API key. Messages trigger ad-hoc sessions via the existing session manager, and responses stream back in OpenAI SSE format — including tool call deltas so apps that render tool invocations do so natively.

Apprise continues to handle proactive outbound push notifications. This endpoint handles only the response path: the operator receives an Apprise alert, opens their preferred chat app, and issues a command without opening a browser.

See [ADR-0020: OpenAI-Compatible Chat Endpoint for Mobile Access](../../adrs/ADR-0020-openai-compatible-chat-endpoint.md) for the decision rationale.

## Definitions

- **OpenAI-compatible client**: Any HTTP client that can talk to the OpenAI Chat Completions API format, including iOS apps (Opencat, ChatBox), Android apps, desktop LLM clients, `curl`, and code using the OpenAI Python or JS SDKs.
- **Chat session**: An ad-hoc Claude Ops monitoring session triggered by a message to the chat endpoint. Identical to sessions triggered via the dashboard or REST API (SPEC-0013, ADR-0013).
- **Stream-json output**: The `--output-format stream-json` output emitted by the Claude Code CLI subprocess, containing structured events for tool uses, content, and session metadata (ADR-0011, SPEC-0011).
- **OpenAI SSE format**: The server-sent events format used by the OpenAI Chat Completions streaming API, where each event is `data: <JSON>\n\n` and the final event is `data: [DONE]\n\n`.
- **Tool call delta**: An OpenAI streaming chunk that represents a function/tool invocation in the `choices[].delta.tool_calls` field.
- **CLAUDEOPS_CHAT_API_KEY**: Environment variable containing the pre-shared API key required to authenticate requests to the chat endpoint.
- **Starting tier**: The permission tier at which a chat-triggered session begins, determined by the `model` field in the request. Defaults to Tier 1 if the model is unrecognized or omitted.
- **Per-tier tool config**: The `--allowedTools` and `--disallowedTools` values configured for a specific tier, used when that tier's session is spawned (see ADR-0023).

## Requirements

### REQ-1: Endpoint Registration

The server MUST expose two routes on the existing HTTP server:

- `POST /v1/chat/completions` — OpenAI-compatible chat completions
- `GET /v1/models` — OpenAI-compatible model listing

These routes MUST be registered on the same `http.ServeMux` as existing dashboard and API routes. The `/v1/` prefix is intentionally separate from `/api/v1/` to match the base URL convention of OpenAI-compatible clients (clients set base URL to `https://host/v1`, then call `/v1/chat/completions`).

#### Scenario: Chat routes registered at startup

Given the web server starts
When route registration completes
Then `POST /v1/chat/completions` and `GET /v1/models` MUST be registered and reachable

#### Scenario: Chat routes coexist with dashboard and API routes

Given requests to `/`, `/sessions`, `/api/v1/sessions`, and `/v1/chat/completions`
When each request is received
Then each MUST be handled by the correct handler without interference

### REQ-2: Authentication

The endpoint MUST require Bearer token authentication matching `CLAUDEOPS_CHAT_API_KEY`. Requests without an `Authorization` header or with an incorrect token MUST return HTTP 401. If `CLAUDEOPS_CHAT_API_KEY` is not set or is empty, the endpoint MUST return HTTP 503 Service Unavailable for all requests.

The token is compared using constant-time equality to prevent timing attacks.

#### Scenario: Valid token accepted

Given `CLAUDEOPS_CHAT_API_KEY=secret` is set
When a client sends `POST /v1/chat/completions` with `Authorization: Bearer secret`
Then the server MUST process the request

#### Scenario: Missing token rejected

Given `CLAUDEOPS_CHAT_API_KEY=secret` is set
When a client sends `POST /v1/chat/completions` with no `Authorization` header
Then the server MUST return HTTP 401 with an OpenAI-format error body

#### Scenario: Invalid token rejected

Given `CLAUDEOPS_CHAT_API_KEY=secret` is set
When a client sends `POST /v1/chat/completions` with `Authorization: Bearer wrong`
Then the server MUST return HTTP 401 with an OpenAI-format error body

#### Scenario: Endpoint disabled when key not set

Given `CLAUDEOPS_CHAT_API_KEY` is not set
When a client sends `POST /v1/chat/completions`
Then the server MUST return HTTP 503 with an OpenAI-format error body

### REQ-3: Request Parsing

The server MUST accept a JSON request body conforming to the OpenAI Chat Completions request schema. The server MUST extract the text content of the last message in the `messages` array with `role: "user"` as the ad-hoc session prompt. The server MUST support the `stream` boolean field to determine whether to respond with SSE or synchronous JSON.

The `model` field MUST be mapped to a starting tier for the session according to the following table:

| Model ID | Starting Tier |
|----------|--------------|
| `claude-ops` | 1 |
| `claude-ops-tier1` | 1 |
| `claude-ops-tier2` | 2 |
| `claude-ops-tier3` | 3 |

Unrecognized model IDs MUST default to Tier 1 and MUST NOT return an error.

If the `messages` array is empty or contains no `user` messages, the server MUST return HTTP 400.

#### Scenario: User message extracted as prompt

Given a request body `{"model":"claude-ops","messages":[{"role":"user","content":"restart jellyfin"}],"stream":true}`
When the handler parses the request
Then it MUST use `"restart jellyfin"` as the ad-hoc session prompt

#### Scenario: Last user message used when multiple messages present

Given a request body with messages `[{role:user, content:"hello"}, {role:assistant, content:"hi"}, {role:user, content:"restart nginx"}]`
When the handler parses the request
Then it MUST use `"restart nginx"` as the prompt

#### Scenario: Empty messages array rejected

Given a request body `{"model":"claude-ops","messages":[]}`
When the handler parses the request
Then the server MUST return HTTP 400 with an OpenAI-format error body

#### Scenario: No user messages rejected

Given a request body with messages containing only `role: "system"` entries
When the handler parses the request
Then the server MUST return HTTP 400 with an OpenAI-format error body

#### Scenario: claude-ops-tier2 model maps to starting tier 2

Given a request body with `"model": "claude-ops-tier2"`
When the handler processes the request
Then it MUST trigger a session starting at Tier 2 with Tier 2 permissions and prompt

#### Scenario: claude-ops-tier3 model maps to starting tier 3

Given a request body with `"model": "claude-ops-tier3"`
When the handler processes the request
Then it MUST trigger a session starting at Tier 3 with Tier 3 permissions and prompt

#### Scenario: Unrecognized model defaults to tier 1

Given a request body with `"model": "gpt-4"`
When the handler processes the request
Then it MUST trigger a session starting at Tier 1
And MUST NOT return an error for the unknown model name

### REQ-4: Session Triggering

The handler MUST trigger an ad-hoc session by calling `Manager.TriggerAdHoc(prompt, startTier)` — where `startTier` is derived from the `model` field per REQ-3. The `startTier` determines which tier prompt file and tool config the session begins with. Escalation from the starting tier to higher tiers follows normal handoff rules (SPEC-0016).

Only one session may run at a time. If a session is already running when a request arrives, the server MUST return a busy signal to the client rather than queuing or dropping the request silently. The busy signal SHOULD be a valid assistant response (HTTP 200 with a message explaining the agent is currently busy and summarising recent activity) so that conversational clients display something useful. A bare HTTP error (e.g. 429) is acceptable when a proper assistant response cannot be generated.

#### Scenario: Ad-hoc session triggered successfully

Given no session is currently running
When the handler calls `TriggerAdHoc("restart jellyfin", 1)`
Then a new ad-hoc session MUST start at Tier 1

#### Scenario: Tier 2 session triggered directly

Given no session is currently running and the request specifies `"model": "claude-ops-tier2"`
When the handler calls `TriggerAdHoc("restart jellyfin", 2)`
Then a new ad-hoc session MUST start at Tier 2 using the Tier 2 prompt and tool configuration

#### Scenario: Session conflict returns a busy signal

Given a session is already running
When the chat endpoint receives a request
Then the server MUST return a busy signal to the caller
And the response SHOULD be a valid assistant message (HTTP 200) explaining that Claude Ops is busy and summarising recent findings
And the response MAY fall back to an HTTP error response if an assistant message cannot be generated

### REQ-5: Streaming Response

When `stream: true`, the server MUST return an SSE stream with `Content-Type: text/event-stream`. Each SSE event MUST be formatted as `data: <JSON>\n\n` where the JSON conforms to the OpenAI chat completion chunk schema. The final event MUST be `data: [DONE]\n\n`.

The stream MUST include:

1. **Content deltas** — assistant text output emitted as `choices[].delta.content` chunks.
2. **Tool call deltas** — tool invocations (Bash, Read, Glob, etc.) emitted as `choices[].delta.tool_calls` chunks, with `function.name` set to the tool name and `function.arguments` containing the tool input as a JSON string.
3. **Finish chunk** — a final chunk with `choices[].finish_reason: "stop"` and an empty delta.

Each chunk MUST include a consistent `id` field for the completion (generated once per request), `object: "chat.completion.chunk"`, and the `model` field set to `"claude-ops"`.

#### Scenario: Content streamed as deltas

Given a session produces assistant text "Restarting jellyfin container..."
When the text is streamed to the client
Then the SSE stream MUST include one or more chunks with `choices[0].delta.content` containing parts of the text

#### Scenario: Tool calls streamed as deltas

Given a session runs `Bash({"command":"docker restart jellyfin"})`
When the tool call is streamed
Then the SSE stream MUST include a chunk with `choices[0].delta.tool_calls[0].function.name` set to `"Bash"` and `function.arguments` containing `{"command":"docker restart jellyfin"}`

#### Scenario: Stream ends with [DONE]

Given a session completes
When the SSE stream concludes
Then the final event MUST be `data: [DONE]\n\n`

#### Scenario: Finish reason included

Given a session completes normally
When the finish chunk is emitted
Then it MUST include `choices[0].finish_reason: "stop"` and an empty delta

### REQ-6: Synchronous Response

When `stream: false` or the `stream` field is absent, the server MUST wait for the session to complete and return a single JSON response conforming to the OpenAI chat completion (non-streaming) schema. The response MUST include the full assistant message text in `choices[0].message.content`. The `usage` field MUST be present with `prompt_tokens`, `completion_tokens`, and `total_tokens` all set to 0 (Claude Ops does not have token-level visibility from CLI output).

#### Scenario: Synchronous response returns full text

Given a session completes with final response "Jellyfin restarted successfully"
When the client requested `stream: false`
Then the server MUST return HTTP 200 with `choices[0].message.content` equal to the full response text

#### Scenario: Usage fields present but zeroed

Given any synchronous response
Then the response body MUST include `"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`

### REQ-7: Error Response Format

All error responses MUST use the OpenAI error format, not the `{"error": "<string>"}` format used by `/api/v1/` endpoints:

```json
{
  "error": {
    "message": "<human-readable description>",
    "type": "<error_type>",
    "code": "<error_code>"
  }
}
```

HTTP status codes:
- 400: `invalid_request_error` — malformed request body, missing fields
- 401: `authentication_error` — missing or invalid API key
- 200 (preferred) or 429 (fallback): busy signal — session already running (see REQ-4)
- 503: `service_unavailable` — chat endpoint disabled (no API key configured)
- 500: `server_error` — internal session error

#### Scenario: 401 uses OpenAI error format

Given a request with an invalid API key
When the server returns 401
Then the body MUST be `{"error":{"message":"Invalid API key","type":"authentication_error","code":"invalid_api_key"}}`

#### Scenario: Busy signal uses assistant message format when possible

Given a session is already running
When the server cannot start a new session
Then the response SHOULD be HTTP 200 with a `ChatCompletion` body containing an assistant message explaining the agent is busy
And the message SHOULD include a summary of recent findings from the active session

### REQ-8: Models Endpoint

`GET /v1/models` MUST return an OpenAI-compatible model listing. The response MUST include four model entries exposing the three Claude Ops tiers plus the default alias:

- `claude-ops` — alias for Tier 1 (default, backward-compatible)
- `claude-ops-tier1` — Tier 1: observe only (Haiku)
- `claude-ops-tier2` — Tier 2: safe remediation (Sonnet)
- `claude-ops-tier3` — Tier 3: full remediation (Opus)

The response MUST conform to the OpenAI list models schema with `object: "list"` and a `data` array. This endpoint MUST be accessible to unauthenticated requests (apps probe this endpoint before asking for credentials in some implementations).

#### Scenario: Models endpoint returns all four model IDs

Given any client sends `GET /v1/models`
Then the server MUST return HTTP 200 with a JSON body containing all four model IDs: `claude-ops`, `claude-ops-tier1`, `claude-ops-tier2`, `claude-ops-tier3`

#### Scenario: Models endpoint accessible without auth

Given `CLAUDEOPS_CHAT_API_KEY=secret` is set
When a client sends `GET /v1/models` without an Authorization header
Then the server MUST return HTTP 200 (not 401)

### REQ-9: Stateless Sessions

Each request to the chat endpoint MUST trigger a new, independent ad-hoc session. The `messages` array history from the client MUST NOT be injected as conversation context into the agent's prompt. Only the last user message is used as the session prompt. Sessions do not persist conversation state between requests.

This is intentional: Claude Ops sessions are monitoring/remediation runs, not general-purpose conversations. The agent's session memory (SPEC-0015) provides cross-session operational knowledge.

#### Scenario: Each message triggers a new session

Given a client sends two sequential chat requests
When both complete
Then two separate session records MUST exist in the database

#### Scenario: Prior messages not injected as context

Given a client sends a message with `messages` containing previous assistant turns
When the session is triggered
Then only the last user message text MUST be used as the prompt
And prior assistant messages MUST NOT be prepended to the agent's context

### REQ-10: Compatibility with OpenAI Client Libraries

The endpoint MUST be usable with the official OpenAI Python and JavaScript SDKs by setting `base_url` to the Claude Ops server URL. The endpoint MUST NOT require any custom headers beyond `Authorization: Bearer <key>` and `Content-Type: application/json`.

#### Scenario: Python SDK compatible

Given an operator configures `openai.OpenAI(base_url="https://claudeops.example.com/v1", api_key="secret")`
When they call `client.chat.completions.create(model="claude-ops", messages=[{"role":"user","content":"status"}])`
Then the request MUST reach the Claude Ops handler and return a valid response

#### Scenario: No custom headers required

Given a client sends only standard OpenAI headers (`Authorization`, `Content-Type`, `Accept`)
Then the server MUST process the request without requiring any Claude Ops-specific headers

### REQ-11: Per-Tier Tool Enforcement for Chat Sessions

Chat sessions MUST use the tool configuration (allowed and disallowed tools) for the tier at which they start, as defined in ADR-0023. The Go `Config` struct MUST expose per-tier tool config fields (`Tier1AllowedTools`, `Tier1DisallowedTools`, `Tier2AllowedTools`, `Tier2DisallowedTools`, `Tier3AllowedTools`, `Tier3DisallowedTools`). The `runTier` function MUST select the appropriate allowed/disallowed tool strings by tier number when spawning the CLI subprocess.

If a per-tier tool config field is empty, `runTier` MUST fall back to the shared `AllowedTools`/`DisallowedTools` config values for backward compatibility.

#### Scenario: Tier 2 chat session uses Tier 2 tool config

Given `CLAUDEOPS_TIER2_ALLOWED_TOOLS` and `CLAUDEOPS_TIER2_DISALLOWED_TOOLS` are configured
When a chat request with `"model": "claude-ops-tier2"` triggers a session
Then the CLI subprocess MUST be invoked with `--allowedTools` set to the Tier 2 value
And `--disallowedTools` set to the Tier 2 value

#### Scenario: Tier 1 chat session uses Tier 1 tool config

Given `CLAUDEOPS_TIER1_ALLOWED_TOOLS` is configured
When a chat request with `"model": "claude-ops"` triggers a session
Then the CLI subprocess MUST be invoked with `--allowedTools` set to the Tier 1 value

#### Scenario: Falls back to shared config when per-tier config is absent

Given `CLAUDEOPS_TIER2_ALLOWED_TOOLS` is not set but `CLAUDEOPS_ALLOWED_TOOLS` is set
When a Tier 2 session is triggered
Then the CLI subprocess MUST use `CLAUDEOPS_ALLOWED_TOOLS` as the allowed tools value

## References

- [ADR-0020: OpenAI-Compatible Chat Endpoint for Mobile Access](../../adrs/ADR-0020-openai-compatible-chat-endpoint.md)
- [ADR-0013: Manual Ad-Hoc Session Runs from the Dashboard](../../adrs/ADR-0013-manual-ad-hoc-session-runs.md)
- [ADR-0017: REST API with OpenAPI Specification and Swagger UI](../../adrs/ADR-0017-rest-api-with-openapi-and-swagger-ui.md)
- [ADR-0011: Show CLI Activity Log and Formatted Response on Session Page](../../adrs/ADR-0011-session-page-cli-output-and-response.md)
- [SPEC-0017: REST API with OpenAPI Specification and Swagger UI](../rest-api/spec.md)
- [SPEC-0013: Manual Ad-Hoc Sessions](../manual-ad-hoc-sessions/spec.md)
- [SPEC-0015: Persistent Agent Memory](../persistent-agent-memory/spec.md)
- [OpenAI Chat Completions API Reference](https://platform.openai.com/docs/api-reference/chat)
