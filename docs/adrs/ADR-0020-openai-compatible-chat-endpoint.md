---
status: accepted
date: 2026-02-22
decision-makers: Joe Stump
supersedes: []
amended: 2026-02-23
---

# ADR-0020: OpenAI-Compatible Chat Endpoint for Mobile Access

## Context and Problem Statement

Claude Ops currently uses Apprise (ADR-0004) for one-way outbound notifications: daily digests, auto-remediation reports, and human-attention alerts. This works well for "fire and forget" alerting, but operators have no way to respond to these notifications from within their chat platform of choice.

When an operator receives an alert that "Jellyfin is down," the current workflow requires them to open the Claude Ops dashboard in a browser, navigate to the session trigger form (ADR-0013), and type a prompt. This context switch is friction-heavy, especially on mobile or during off-hours.

The user wants to **query and command Claude Ops from a mobile device** — ideally from a native app with a polished chat interface. Different operators may prefer different apps; the solution must work with the existing ecosystem rather than building or maintaining platform-specific adapters.

Additionally, the original implementation ignored the `model` field in chat requests — all chat sessions started at Tier 1 (Haiku, observe-only). An operator who receives a "Jellyfin is down" alert and wants to immediately request a restart cannot do so directly; they must wait for the scheduled Tier 1 → Tier 2 escalation cycle. Operators should be able to select which tier they chat with so they can get the appropriate level of capability for their intent.

## Decision Drivers

* **Operators already have preferred chat apps** — the iOS and Android ecosystem has many OpenAI-compatible LLM client apps (Opencat, ChatBox, etc.) that support a custom API base URL. These apps have polished UIs, message history, and native tool call display built in.
* **Apprise already handles push** — proactive alerts arrive via ntfy/Apprise push notifications. The mobile access problem is the *response* path: once you've been notified, how do you issue a command without opening a browser?
* **Leverage existing infrastructure** — the system already has a REST API (ADR-0017), an ad-hoc session trigger mechanism (ADR-0013), and stream-json output parsing (ADR-0011). The chat endpoint is a thin compatibility layer on top.
* **Avoid maintaining platform adapters** — building Telegram, Slack, and Discord adapters requires platform-specific bot registration, polling loop lifecycle management, reconnection handling, and ongoing maintenance as upstream APIs change.
* **Fits "no application code" spirit** — third-party apps maintain the chat UI; Claude Ops exposes a standard interface.

## Considered Options

1. **OpenAI-compatible `/v1/chat/completions` endpoint** — extend the existing REST API server with an OpenAI-compatible endpoint; operators point any compatible app at it.
2. **Bidirectional notification gateway** — dedicated Go package with `ChatAdapter` interface, Telegram long-poll, Slack Socket Mode, Discord Gateway WebSocket adapters.
3. **Webhook receiver for inbound + Apprise for outbound** — generic webhook endpoint that chat platforms POST inbound messages to; requires public-facing URL.
4. **Matrix as universal bridge protocol** — run a Matrix homeserver plus platform bridges; Claude Ops speaks only the Matrix CS API.

## Decision Outcome

Chosen option: **OpenAI-compatible `/v1/chat/completions` endpoint**, because it provides mobile access with a polished UI through the existing app ecosystem, requires minimal new code (a thin adapter on the existing session trigger mechanism), avoids platform-specific adapter maintenance entirely, and naturally displays tool invocations since OpenAI-compatible apps already handle the `tool_calls` delta format.

Apprise continues to handle proactive outbound notifications. The chat endpoint handles the response path: receive an alert via push → open your preferred chat app → type a command.

### Endpoint Design

The server exposes two new routes on the existing HTTP server:

```
POST /v1/chat/completions    — OpenAI-compatible chat
GET  /v1/models             — list available "models" (Claude Ops tiers)
```

The `/v1/` prefix is separate from `/api/v1/` to match OpenAI client expectations (base URL is `https://your-claudeops/v1`).

**Request format** (subset of OpenAI Chat Completions):

```json
{
  "model": "claude-ops",
  "messages": [
    {"role": "user", "content": "restart jellyfin"}
  ],
  "stream": true
}
```

The handler extracts the last `user` message as the ad-hoc session prompt and calls `Manager.TriggerAdHoc(prompt, startTier)`. The `startTier` is determined by the `model` field in the request (see Tier Selection below).

**Streaming response** (when `stream: true`): SSE stream of OpenAI-format chunks:

```
data: {"id":"co-123","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant","content":"Checking jellyfin..."}}]}
data: {"id":"co-123","object":"chat.completion.chunk","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"Bash","arguments":"{\"command\":\"docker restart jellyfin\"}"}}]}}]}
data: [DONE]
```

Tool invocations from the stream-json output are mapped to OpenAI `tool_calls` deltas, which compatible apps render natively as tool call blocks.

**Synchronous response** (when `stream: false` or omitted): waits for session completion, returns:

```json
{
  "id": "co-123",
  "object": "chat.completion",
  "model": "claude-ops",
  "choices": [{
    "message": {"role": "assistant", "content": "<final response text>"},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
}
```

Token counts are set to 0 — Claude Ops does not have token-level visibility from the CLI subprocess output.

### Authentication

The endpoint requires a Bearer token matching `CLAUDEOPS_CHAT_API_KEY`. If the env var is not set, the endpoint is disabled and returns HTTP 503. Requests without a valid token return HTTP 401.

```
Authorization: Bearer <CLAUDEOPS_CHAT_API_KEY>
```

This matches how OpenAI-compatible apps configure authentication.

### Tier Selection

The `model` field in the request maps to a starting tier for the ad-hoc session:

| Model ID | Starting Tier | Underlying Model | Tool Capabilities |
|----------|--------------|------------------|-------------------|
| `claude-ops` | 1 (default) | `$CLAUDEOPS_TIER1_MODEL` | Observe only |
| `claude-ops-tier1` | 1 | `$CLAUDEOPS_TIER1_MODEL` | Observe only |
| `claude-ops-tier2` | 2 | `$CLAUDEOPS_TIER2_MODEL` | Safe remediation |
| `claude-ops-tier3` | 3 | `$CLAUDEOPS_TIER3_MODEL` | Full remediation |

Unrecognized model IDs default to Tier 1. Each tier uses its own configured `--allowedTools` and `--disallowedTools` (per ADR-0023), and its own tier prompt file.

When a chat session starts at Tier 2 or Tier 3 directly, escalation still applies: if the starting tier writes a handoff requesting a higher tier, the supervisor escalates normally. Starting at Tier 2 means you skip the Tier 1 observation phase — the session begins with Tier 2 permissions and its prompt directly.

### `/v1/models` Response

Returns an OpenAI-compatible models list exposing the three Claude Ops tiers:

```json
{
  "object": "list",
  "data": [
    {"id": "claude-ops",       "object": "model", "created": 1700000000, "owned_by": "claude-ops"},
    {"id": "claude-ops-tier1", "object": "model", "created": 1700000000, "owned_by": "claude-ops"},
    {"id": "claude-ops-tier2", "object": "model", "created": 1700000000, "owned_by": "claude-ops"},
    {"id": "claude-ops-tier3", "object": "model", "created": 1700000000, "owned_by": "claude-ops"}
  ]
}
```

### Session Conflict Handling

If a session is already running when the chat endpoint receives a request, it returns HTTP 429 Too Many Requests (not 409, since OpenAI clients better handle 429 for rate limiting). The response body follows OpenAI error format:

```json
{"error": {"message": "A session is already running. Try again shortly.", "type": "rate_limit_error", "code": "rate_limit_exceeded"}}
```

### Integration Points

- **Session Manager** (`internal/session`): calls `Manager.TriggerAdHoc(prompt, startTier)`. The `startTier` is passed through to `runEscalationChain`, which begins at that tier rather than always at Tier 1.
- **Web Server** (`internal/web`): registers `/v1/chat/completions` and `/v1/models` on the existing mux. The stream-json to OpenAI SSE conversion is a thin transform in the handler. `handleModels` returns all four model IDs (the alias `claude-ops` plus the three explicit tier IDs).
- **Config** (`internal/config`): `CLAUDEOPS_CHAT_API_KEY` controls access. If unset, the endpoint is disabled. Per-tier tool configs (`Tier1AllowedTools`, `Tier2AllowedTools`, `Tier3AllowedTools`, `Tier1DisallowedTools`, `Tier2DisallowedTools`, `Tier3DisallowedTools`) determine what each tier session may do; `runTier` selects the appropriate config by tier number.
- **Apprise**: unaffected. Proactive outbound notifications continue unchanged.

### Consequences

**Positive:**

* Operators get a polished mobile chat interface immediately, using apps they already have, without any new app development.
* Tool invocations are displayed natively in OpenAI-compatible apps — no custom rendering needed.
* Zero platform-specific adapter code: no Telegram bot token setup, no Slack app registration, no Discord bot, no polling loop lifecycle management.
* The implementation is a thin layer on existing infrastructure (session trigger + stream-json parsing), amounting to one new handler and a stream format transformer.
* Any OpenAI-compatible client works: iOS apps, Android apps, desktop clients, `curl`, Python scripts using the `openai` SDK.
* Apprise push notifications continue unchanged; this only adds the response path.

**Negative:**

* Pull-based only: the chat endpoint does not push notifications. Operators must still receive alerts via Apprise/ntfy and then open their chat app to respond. There is no in-thread alert → reply UX as there would be with Telegram bidirectional.
* Network exposure: the endpoint must be accessible from mobile devices, which for home-lab deployments means either exposing it publicly (behind Caddy + TLS) or via VPN/Tailscale. This is not new — the existing dashboard has the same requirement.
* Token counts in responses are always 0, which may confuse apps that display usage metrics.
* Stateless: each message is a new session. Conversation history from the app is not injected as context — only the last user message is used as the prompt. Multi-turn conversation within the app does not build agent context.
* Authentication is a single shared API key, not per-user credentials. Anyone with the key can trigger sessions at any tier.
* Direct Tier 2/3 chat bypasses the Tier 1 observation phase. The Tier 1 grounding (check results, cooldown state, inventory context) is not pre-loaded when jumping directly to a higher tier. The operator-supplied prompt is the only context the higher-tier agent receives. This is intentional for interactive use but means the agent may lack the environmental context a scheduled escalation would provide.

### Confirmation

* An operator can add Claude Ops as a custom OpenAI API endpoint in their preferred iOS app and trigger `"restart jellyfin"` from their phone.
* The app displays tool invocations (Bash calls, file reads) as the session runs.
* When no `CLAUDEOPS_CHAT_API_KEY` is set, `/v1/chat/completions` returns 503.
* Requests with an invalid token return 401.
* Apprise notifications continue to work exactly as before.

## Pros and Cons of the Options

### OpenAI-Compatible `/v1/chat/completions` Endpoint

Extend the existing REST API server with OpenAI-compatible routes. Operators configure any compatible app with the Claude Ops base URL and API key.

* Good, because it requires no new infrastructure, no bot registrations, no polling loops, and no per-platform adapter code.
* Good, because tool call display is built into OpenAI-compatible apps — no custom rendering.
* Good, because any OpenAI-compatible client (iOS, Android, desktop, scripts) works without changes.
* Good, because it is a thin adapter on the existing session trigger mechanism — the hard parts are already implemented.
* Good, because third parties maintain the app UIs, polishing the mobile experience continuously.
* Bad, because it is pull-based — no in-thread proactive alerts. Operators need Apprise push to get notified, then open the app to respond.
* Bad, because conversation history in the app is not passed as context to the agent — each message is a fresh session.

### Bidirectional Notification Gateway

Dedicated `ChatAdapter` interface with Telegram long-poll, Slack Socket Mode, Discord Gateway WebSocket adapters. Alerts and responses happen in the same chat thread.

* Good, because it enables true in-thread bidirectional: alert arrives → reply in same thread.
* Good, because polling-based transports work behind NAT.
* Bad, because it requires significant custom Go code (adapters, polling loops, reconnection logic, command router, allowed-user lists).
* Bad, because each platform requires separate bot registration and credentials.
* Bad, because persistent connections (long-poll, WebSocket) require careful lifecycle management.
* Bad, because the system must maintain adapters as upstream APIs change.
* Bad, because it contradicts the "no application code" architecture principle.

### Webhook Receiver + Apprise Outbound

Generic `/api/v1/webhooks/{platform}` endpoint; chat platforms POST inbound messages; Apprise handles outbound.

* Good, because it keeps Apprise for outbound without changes.
* Bad, because webhooks require a publicly accessible URL — home-lab deployments behind NAT need a tunnel.
* Bad, because Apprise outbound and webhook inbound are separate systems with no shared state, making reply threading impossible.
* Bad, because each platform has different webhook verification requirements.

### Matrix as Universal Bridge Protocol

Matrix homeserver + mautrix bridges for Telegram, Slack, Discord. Claude Ops speaks only the Matrix CS API.

* Good, because one protocol implementation covers many platforms via bridges.
* Bad, because it requires deploying and maintaining a homeserver, bridges, and their respective databases.
* Bad, because bridge reliability varies significantly by platform.
* Bad, because the indirection (Claude Ops → Matrix → Bridge → Telegram) adds latency and failure points.

## More Information

* **ADR-0013** (Manual Ad-Hoc Session Runs): The chat endpoint calls `Manager.TriggerAdHoc()`, the identical path used by the dashboard trigger button. Session permissions and tier escalation apply identically.
* **ADR-0017** (REST API): The `/v1/` routes are registered on the same HTTP server and mux as the existing `/api/v1/` routes. The OpenAPI spec (SPEC-0017) is extended to document the chat endpoint routes.
* **ADR-0004** (Apprise): Apprise is not changed. The chat endpoint addresses the response path only; proactive notifications remain Apprise's responsibility.
* **ADR-0011** (Session Page CLI Output): The stream-json to OpenAI SSE transformation reuses the same stream-json event parsing developed for the dashboard's session page.
* **Package location:** Handler in `internal/web/chat_handler.go`. OpenAI SSE transform in `internal/web/openai.go`.
* **SPEC-0024**: The formal specification for this endpoint lives in `docs/openspec/specs/openai-chat-endpoint/spec.md`.
