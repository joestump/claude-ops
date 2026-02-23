---
sidebar_position: 7
---

# Mobile Chat

Claude Ops exposes an OpenAI-compatible chat endpoint so you can use any OpenAI-compatible app — Open WebUI, ChatBox, LM Studio, or a plain `curl` — as a mobile or desktop interface to your infrastructure agent.

## How it works

When you send a message, Claude Ops:

1. Authenticates your request via Bearer token
2. Extracts the last user message as a prompt
3. Triggers an ad-hoc monitoring session with that prompt
4. Streams the agent's response back in real time (or waits for completion if `stream: false`)

:::note Stateless sessions
Each request is a fresh session. Claude Ops does **not** inject your conversation history as context — only the last user message is sent to the agent. This keeps sessions isolated and predictable.
:::

## Setup

### 1. Set the API key

Add `CLAUDEOPS_CHAT_API_KEY` to your `.env` file. This is the Bearer token your chat app will use:

```env
CLAUDEOPS_CHAT_API_KEY=your-secret-key-here
```

If this variable is unset, the endpoint returns `503` (disabled). Pick a strong random value:

```bash
openssl rand -hex 32
```

### 2. Restart Claude Ops

```bash
docker compose restart claudeops
```

The key is read on every request, so you can rotate it without a restart if you prefer.

## Connecting a client

The chat endpoint follows the OpenAI API convention. Point your client at:

| Setting | Value |
|---------|-------|
| **Base URL** | `http://your-claudeops-host:8080/v1` |
| **API key** | your `CLAUDEOPS_CHAT_API_KEY` value |
| **Model** | `claude-ops` (returned by `/v1/models`) |

### Open WebUI

1. Go to **Settings → Connections → OpenAI API**
2. Set **API Base URL** to `http://your-claudeops-host:8080/v1`
3. Set **API Key** to your `CLAUDEOPS_CHAT_API_KEY`
4. Save — Open WebUI will fetch `/v1/models` automatically and show `claude-ops`

### curl

```bash
# Streaming
curl -N http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-key-here" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-ops",
    "stream": true,
    "messages": [{"role": "user", "content": "What services are currently unhealthy?"}]
  }'

# Synchronous (wait for full response)
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer your-secret-key-here" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-ops",
    "stream": false,
    "messages": [{"role": "user", "content": "Restart the nginx container on ie01"}]
  }'
```

## Endpoints

### `POST /v1/chat/completions`

Triggers an ad-hoc session and returns the agent's response.

**Authentication:** Bearer token (`Authorization: Bearer <key>`)

**Request body:**

```json
{
  "model": "claude-ops",
  "stream": true,
  "messages": [
    {"role": "user", "content": "Check the health of all services"}
  ]
}
```

`stream: true` — Server-Sent Events (SSE), token-by-token as the agent runs
`stream: false` — Waits for the session to complete and returns the full response

**Streaming response** (`stream: true`):

```
data: {"id":"chatcmpl-...","object":"chat.completion.chunk","model":"claude-ops","choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"id":"chatcmpl-...","object":"chat.completion.chunk","model":"claude-ops","choices":[{"index":0,"delta":{"content":"Checking all services..."}}]}

data: {"id":"chatcmpl-...","object":"chat.completion.chunk","model":"claude-ops","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

**Synchronous response** (`stream: false`):

```json
{
  "id": "chatcmpl-...",
  "object": "chat.completion",
  "model": "claude-ops",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "All services are healthy..."},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
}
```

### `GET /v1/models`

Returns the available model. This endpoint is **unauthenticated** — clients probe it before asking for credentials.

```json
{
  "object": "list",
  "data": [{"id": "claude-ops", "object": "model", "owned_by": "claude-ops"}]
}
```

## Error responses

| Status | Code | Cause |
|--------|------|-------|
| `401` | `invalid_api_key` | Missing or wrong Bearer token |
| `400` | `invalid_request` | Malformed JSON or no user message |
| `429` | `rate_limit_exceeded` | A session is already running — try again shortly |
| `503` | `chat_endpoint_disabled` | `CLAUDEOPS_CHAT_API_KEY` is not set |

All errors follow the OpenAI error format:

```json
{
  "error": {
    "message": "A session is already running. Try again shortly.",
    "type": "rate_limit_error",
    "code": "rate_limit_exceeded"
  }
}
```

## Security

- Keep `CLAUDEOPS_CHAT_API_KEY` out of version control — use `.env` (which is gitignored)
- Expose the port only on trusted networks; put Claude Ops behind a reverse proxy (Caddy, Nginx) with TLS if you access it over the internet
- Each request runs a full Claude agent session with the permissions of your configured tier — treat the API key like a root credential
