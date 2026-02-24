# SPEC-0025: Inbound Webhook Alert Ingestion

## Overview

Defines the inbound webhook endpoint that allows external monitoring and alerting tools (UptimeKuma, Grafana Alertmanager, PagerDuty, Healthchecks.io, or any generic HTTP webhook) to push alerts into Claude Ops and trigger an automated investigation session. An LLM intermediary synthesizes the arbitrary payload into a focused, plain-language investigation prompt before the session is started. Sessions triggered via webhook are identified with `trigger = "alert"` throughout the system. See ADR-0024.

## Requirements

### Requirement: Endpoint Registration

The system MUST expose a `POST /api/v1/webhook` endpoint. The endpoint MUST be registered in the server's route table alongside existing API routes.

#### Scenario: Endpoint exists

- **WHEN** a client sends `POST /api/v1/webhook` with a valid bearer token and any non-empty body
- **THEN** the server returns `202 Accepted` with a JSON body containing `session_id` and `status: "triggered"`

#### Scenario: Method not allowed

- **WHEN** a client sends `GET /api/v1/webhook`
- **THEN** the server returns `405 Method Not Allowed`

### Requirement: Bearer Token Authentication

The endpoint MUST require a valid bearer token. The token MUST be read from the `CLAUDEOPS_CHAT_API_KEY` environment variable on every request (supporting key rotation without restart). Comparison MUST be performed in constant time to prevent timing attacks.

#### Scenario: Valid token

- **WHEN** the `Authorization` header contains `Bearer <valid-key>` and `CLAUDEOPS_CHAT_API_KEY` is set to `<valid-key>`
- **THEN** the request proceeds to payload processing

#### Scenario: Invalid token

- **WHEN** the `Authorization` header contains an incorrect bearer token
- **THEN** the server returns `401 Unauthorized` with an error body and MUST NOT trigger a session

#### Scenario: Key not configured

- **WHEN** `CLAUDEOPS_CHAT_API_KEY` is unset or empty
- **THEN** the server returns `503 Service Unavailable` indicating the webhook endpoint is disabled

#### Scenario: Missing Authorization header

- **WHEN** the request contains no `Authorization` header
- **THEN** the server returns `401 Unauthorized`

### Requirement: Universal Payload Acceptance

The endpoint MUST accept any non-empty request body regardless of `Content-Type`. This MUST include but is not limited to: `application/json`, `application/x-www-form-urlencoded`, and `text/plain`. The raw body bytes MUST be passed to the LLM synthesis step as a UTF-8 string. The endpoint MUST NOT reject a request solely because the body does not conform to a known schema.

#### Scenario: JSON payload (UptimeKuma)

- **WHEN** the body is `{"heartbeat": {"status": 0, "msg": "Connection timeout"}, "monitor": {"name": "Gitea", "url": "https://gitea.stump.wtf"}}`
- **THEN** the payload is accepted and passed to LLM synthesis without modification

#### Scenario: Plain text payload

- **WHEN** the body is `Alert: disk usage on ie01 is at 95%`
- **THEN** the payload is accepted and passed to LLM synthesis

#### Scenario: Empty body

- **WHEN** the request body is empty or whitespace-only
- **THEN** the server returns `400 Bad Request` and MUST NOT trigger a session

### Requirement: LLM Prompt Synthesis

The system MUST call an LLM to convert the raw webhook payload into a focused plain-language investigation prompt before triggering a session. The synthesis model MUST be configurable via the `CLAUDEOPS_WEBHOOK_MODEL` environment variable and MUST default to `claude-haiku-4-5-20251001`. The system prompt for the synthesis call MUST instruct the model to produce a single, actionable investigation brief that a Claude Ops agent can act on directly.

#### Scenario: Successful synthesis

- **WHEN** the LLM synthesis call succeeds
- **THEN** the returned text is used verbatim as the session prompt passed to `TriggerAdHoc`

#### Scenario: Synthesis failure

- **WHEN** the LLM synthesis API call returns an error or times out
- **THEN** the server returns `502 Bad Gateway` with a descriptive error and MUST NOT trigger a session

#### Scenario: Synthesis produces empty result

- **WHEN** the LLM returns an empty or whitespace-only response
- **THEN** the server returns `502 Bad Gateway` and MUST NOT trigger a session

#### Scenario: Custom synthesis model

- **WHEN** `CLAUDEOPS_WEBHOOK_MODEL` is set to `claude-sonnet-4-6`
- **THEN** the synthesis step uses that model instead of the default haiku model

### Requirement: Session Triggering

After successful synthesis, the system MUST invoke `TriggerAdHoc(prompt, startTier, "alert")` with the synthesized prompt and `trigger = "alert"`. The `startTier` MUST default to `1`. An optional `tier` field in a JSON request body MAY override `startTier` to `2` or `3`; if present, the value MUST be clamped to the configured `MaxTier`. The resulting session MUST be indistinguishable from a manually triggered session in all respects except the `trigger` field.

#### Scenario: Default tier

- **WHEN** the webhook payload contains no tier preference
- **THEN** the session starts at Tier 1

#### Scenario: Tier override via JSON

- **WHEN** the webhook body is JSON and contains `{"tier": 2, ...remaining payload...}`
- **THEN** the session starts at Tier 2 and the tier field is excluded from the payload passed to LLM synthesis

#### Scenario: Tier clamped to MaxTier

- **WHEN** the webhook body specifies `tier: 5` and `MaxTier` is `3`
- **THEN** the session starts at Tier 3

### Requirement: Alert Trigger Type

Sessions triggered via the webhook endpoint MUST be stored with `trigger = "alert"` in the database. The dashboard, session list, and REST API MUST surface this value. All existing query and filter surfaces that accept trigger values MUST treat `"alert"` as a valid value.

#### Scenario: Trigger label stored

- **WHEN** a webhook session is created
- **THEN** `SELECT trigger FROM sessions WHERE id = ?` returns `"alert"`

#### Scenario: Dashboard displays alert trigger

- **WHEN** the session detail page renders a webhook-triggered session
- **THEN** the trigger label reads `"alert"` (distinct from `"scheduled"`, `"manual"`, `"api"`, `"escalation"`)

### Requirement: Busy Response

The endpoint MUST return `409 Conflict` when `TriggerAdHoc` reports that a session is already running. The response body MUST be a JSON object describing why the request could not be fulfilled; it SHOULD NOT be an empty body.

#### Scenario: Session already running

- **WHEN** a webhook alert arrives while a session is already running
- **THEN** the server returns `409 Conflict` with `{"error": "session already running", "message": "..."}`

### Requirement: Webhook Model Configuration

The synthesis model MUST be independently configurable from other models in the system. The `CLAUDEOPS_WEBHOOK_MODEL` environment variable MUST be read on every request (supporting rotation without restart). The `--webhook-model` CLI flag MUST default to `claude-haiku-4-5-20251001`.

#### Scenario: Default model used

- **WHEN** `CLAUDEOPS_WEBHOOK_MODEL` is not set
- **THEN** synthesis uses `claude-haiku-4-5-20251001`

#### Scenario: Runtime model override

- **WHEN** `CLAUDEOPS_WEBHOOK_MODEL` is updated and the next request arrives
- **THEN** the new model is used without restarting the container

### Requirement: Response Format

On success, the endpoint MUST return `202 Accepted` with `Content-Type: application/json`. The response body MUST include `session_id` (integer) and `status` (string, value `"triggered"`). It SHOULD include `tier` (the tier at which the session was started).

#### Scenario: Success response shape

- **WHEN** a session is triggered successfully
- **THEN** the response body is `{"session_id": <N>, "status": "triggered", "tier": <T>}`
