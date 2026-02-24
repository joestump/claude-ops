# Inbound Webhook Alerts

Claude Ops can receive webhook alerts from external monitoring tools and automatically trigger an investigation session. The webhook endpoint accepts **any** HTTP POST body — UptimeKuma, Grafana Alertmanager, PagerDuty, Healthchecks.io, or a plain `curl` call — and uses an LLM to convert the payload into a focused investigation prompt before starting the session.

> **Spec**: SPEC-0025 · **ADR**: ADR-0024

---

## How It Works

```
External Tool  →  POST /api/v1/webhook  →  LLM synthesis  →  session (trigger="alert")
```

1. The tool sends a POST with its native payload (any JSON, form data, or plain text).
2. Claude Ops authenticates the request using a bearer token.
3. A small LLM (Haiku by default) reads the payload and writes a 2–4 sentence investigation brief.
4. Claude Ops starts an investigation session identical to a manual trigger — except the session is stored with `trigger="alert"` so alert-driven sessions are distinguishable in the dashboard and audit trail.
5. The endpoint returns `202 Accepted` immediately with the new `session_id`.

---

## Authentication

The webhook endpoint reuses the **`CLAUDEOPS_CHAT_API_KEY`** bearer token — the same key used for the OpenAI-compatible chat endpoint. No additional secrets are required.

Set the env var (or Docker Compose override):

```env
CLAUDEOPS_CHAT_API_KEY=your-strong-random-key
```

Every request must include:

```
Authorization: Bearer your-strong-random-key
```

Requests without a valid token receive `401 Unauthorized`. If `CLAUDEOPS_CHAT_API_KEY` is unset the endpoint returns `503 Service Unavailable`.

---

## Endpoint

```
POST /api/v1/webhook
```

### Headers

| Header | Required | Value |
|--------|----------|-------|
| `Authorization` | Yes | `Bearer <CLAUDEOPS_CHAT_API_KEY>` |
| `Content-Type` | No | Any (JSON, `text/plain`, form-encoded, etc.) |

### Request body

Any non-empty body. JSON payloads may include an optional top-level `tier` field (integer 1–3) to override the starting escalation tier — see [Tier Override](#tier-override).

### Success response — `202 Accepted`

```json
{
  "session_id": 42,
  "status": "triggered",
  "tier": 1
}
```

Use `session_id` to poll `GET /api/v1/sessions/{id}` for results.

### Error responses

| Status | Meaning |
|--------|---------|
| `400 Bad Request` | Empty or whitespace-only body |
| `401 Unauthorized` | Missing or invalid bearer token |
| `202 Accepted` (`status: "acknowledged"`) | A session is already running; alert received but not queued |
| `502 Bad Gateway` | LLM synthesis failed (Anthropic API error or timeout) |
| `503 Service Unavailable` | `CLAUDEOPS_CHAT_API_KEY` is not configured |

---

## Configuration

| Env var | CLI flag | Default | Description |
|---------|----------|---------|-------------|
| `CLAUDEOPS_CHAT_API_KEY` | — | *(required)* | Bearer token for webhook auth |
| `CLAUDEOPS_WEBHOOK_MODEL` | `--webhook-model` | `claude-haiku-4-5-20251001` | Anthropic model used for payload synthesis |
| `CLAUDEOPS_WEBHOOK_SYSTEM_PROMPT` | `--webhook-system-prompt` | *(built-in default)* | Custom system prompt for the synthesis LLM |

All env vars are read on every request, so you can rotate `CLAUDEOPS_CHAT_API_KEY` or change the model without restarting the container.

### Default synthesis prompt

```
You are an alert triage assistant for an infrastructure monitoring system called Claude Ops.
Given the raw body of an inbound webhook alert, write a single focused investigation brief
(2–4 sentences) that a Claude Ops agent can act on immediately. Identify: what service or
system is affected, what the problem appears to be, and what the agent should investigate
first. Output only the investigation brief — no preamble, no JSON, no markdown.
```

You can override this entirely by setting `CLAUDEOPS_WEBHOOK_SYSTEM_PROMPT`.

---

## Tier Override

If the request body is JSON and contains a top-level `"tier"` field, Claude Ops uses that value as the starting escalation tier (clamped to `MaxTier`):

```json
{ "tier": 2, "monitor": { "name": "Gitea", "url": "https://gitea.example.com" } }
```

| `tier` value | Behaviour |
|-------------|-----------|
| `1` (default) | Tier 1 — Observe |
| `2` | Tier 2 — Investigate |
| `3` | Tier 3 — Remediate |
| `> MaxTier` | Clamped to `MaxTier` |

The `tier` field is stripped from the payload before it is sent to the synthesis LLM so it does not confuse the investigation brief.

---

## Integration Examples

### UptimeKuma

1. In UptimeKuma, open **Notifications** → **Add Notification**.
2. Select **Webhook** as the type.
3. Configure:
   - **Webhook URL**: `https://your-claudeops-host/api/v1/webhook`
   - **Request Method**: `POST`
   - **Content Type**: `application/json`
   - **Additional Headers**: `Authorization: Bearer your-strong-random-key`
4. Save and test. UptimeKuma sends a payload like:

```json
{
  "heartbeat": { "status": 0, "msg": "Connection timeout", "ping": -1 },
  "monitor": {
    "name": "Gitea",
    "url": "https://gitea.example.com",
    "type": "http"
  },
  "msg": "Gitea is down!"
}
```

Claude Ops will synthesise this into something like:

> Investigate why Gitea at gitea.example.com is unreachable. The UptimeKuma heartbeat
> shows a connection timeout (ping: -1). Start by checking DNS resolution, HTTP
> reachability, and the Gitea container status on the host.

### Grafana Alertmanager

Add a webhook receiver to your Alertmanager config:

```yaml
receivers:
  - name: claudeops
    webhook_configs:
      - url: https://your-claudeops-host/api/v1/webhook
        http_config:
          authorization:
            type: Bearer
            credentials: your-strong-random-key
        send_resolved: true
```

Grafana Alertmanager POSTs a JSON payload with an `alerts[]` array. Claude Ops passes the full payload to the synthesis LLM and generates an actionable brief.

### Healthchecks.io

In your Healthchecks.io project, add a **Webhook** integration:

- **URL**: `https://your-claudeops-host/api/v1/webhook`
- **Method**: `POST`
- **Request headers**: `Authorization: Bearer your-strong-random-key`
- **Request body** (Ping-Down template):

```json
{
  "name": "$NAME",
  "status": "$STATUS",
  "last_ping": "$LAST_PING",
  "tags": "$TAGS"
}
```

### Generic `curl` example

```bash
# Trigger an alert from a shell script or CI pipeline
curl -X POST https://your-claudeops-host/api/v1/webhook \
  -H "Authorization: Bearer your-strong-random-key" \
  -H "Content-Type: application/json" \
  -d '{
    "service": "postgres",
    "host": "ie01.example.com",
    "error": "FATAL: max connections reached",
    "connections": 500
  }'
```

Response:

```json
{ "session_id": 42, "status": "triggered", "tier": 1 }
```

Poll for results:

```bash
curl https://your-claudeops-host/api/v1/sessions/42
```

### Start at a higher tier

For critical alerts that warrant immediate remediation, include `"tier": 2` or `"tier": 3`:

```bash
curl -X POST https://your-claudeops-host/api/v1/webhook \
  -H "Authorization: Bearer your-strong-random-key" \
  -H "Content-Type: application/json" \
  -d '{ "tier": 2, "service": "postgres", "error": "replication lag > 30s" }'
```

---

## Concurrency

Claude Ops runs one session at a time. If a webhook alert arrives while a session is already running, the endpoint still returns `202 Accepted` — the alert was received, it just wasn't queued:

```json
{
  "session_id": null,
  "status": "acknowledged",
  "message": "a session is already in progress; this alert was received but not queued"
}
```

Returning `202` prevents upstream tools from treating the response as a delivery failure, which could trigger retries or secondary alerts. Check `status` in the response body to distinguish a newly triggered session (`"triggered"`) from one that was acknowledged-but-skipped (`"acknowledged"`).

---

## Dashboard

Alert-triggered sessions appear in the dashboard and session list with `trigger = alert`, distinct from `scheduled`, `manual`, `api`, and `escalation` sessions. The synthesised prompt is stored as the session's `prompt_text` and is visible on the session detail page.
