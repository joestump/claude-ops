---
status: accepted
date: 2026-06-21
requires: [SPEC-0008, SPEC-0017]
---

# SPEC-0035: Upstream Model Auto-Discovery

## Overview

Claude Ops routes its tiered agents through an upstream Anthropic-compatible LLM
gateway (typically [LiteLLM](https://github.com/BerriAI/litellm)) configured via the
`ANTHROPIC_BASE_URL` environment variable. That gateway can route to many backing
models (Gemini, DeepSeek, Claude, etc.) and exposes the routable set through a
models-list endpoint (`GET /v1/models`).

Today the per-tier model fields (`tier1_model`, `tier2_model`, `tier3_model` — see
SPEC-0008 and SPEC-0017) are free-text strings: the operator must know and type model
identifiers blindly. This capability adds **auto-discovery** of the models available
on the upstream gateway and surfaces that list through the configuration API and the
dashboard configuration page, so the operator can assign any discovered model to any
escalation tier from a selectable list. When discovery is unavailable, the system
degrades gracefully to the existing free-text behavior.

This discovery SOURCE — the upstream gateway at `${ANTHROPIC_BASE_URL}/v1/models` — is
distinct from Claude Ops' OWN OpenAI-compatible models endpoint (`GET /v1/models`,
governed by SPEC-0024 / ADR-0020), which advertises the synthetic `claude-ops`,
`claude-ops-tier1/2/3` identifiers. The two MUST NOT be conflated. Tier model
identifiers discovered here are passed to the Claude Code CLI subprocess via `--model`
per SPEC-0010.

## Requirements

### Requirement: Upstream Model Query

The system MUST be able to query the upstream gateway's models endpoint to enumerate
the models it can route to.

- The system MUST query the upstream gateway's models endpoint at
  `${ANTHROPIC_BASE_URL}/v1/models`, derived from the configured `ANTHROPIC_BASE_URL`.
  Path joining MUST be tolerant of a trailing slash on the base URL. (The
  implementation queries this endpoint via the Anthropic Go SDK's models-list call,
  configured with the base URL — see design.md.)
- The system MUST parse the gateway's models-list response (a JSON object whose `data`
  array contains entries each with an `id` field) and extract the `id` of each entry
  into a list of model identifiers.
- The system MUST authenticate the request using the upstream credential
  (`ANTHROPIC_API_KEY`). If no key is configured, the request MUST still be attempted
  (some gateways are unauthenticated) but the absence MUST NOT cause a panic.
- A bounded HTTP timeout MUST apply to the request. The timeout SHOULD default to a
  small number of seconds (RECOMMENDED: 5 seconds) so a slow or unreachable gateway
  cannot stall configuration rendering.
- Returned model identifiers SHOULD be de-duplicated and presented in a stable
  (e.g. sorted) order.

#### Scenario: Gateway returns a model list

- **WHEN** `ANTHROPIC_BASE_URL` is set and the gateway responds `200` with a models-list
  body whose `data` array is `[{"id":"gemini-2.5-pro"},{"id":"deepseek-chat"},{"id":"claude-opus-4-8"}]`
- **THEN** the system extracts `["claude-opus-4-8","deepseek-chat","gemini-2.5-pro"]`
  (de-duplicated, sorted) as the discovered model list

#### Scenario: Trailing slash on base URL

- **WHEN** `ANTHROPIC_BASE_URL` is `https://litellm.example.com/`
- **THEN** the system queries `https://litellm.example.com/v1/models` without a doubled
  slash

#### Scenario: Upstream credential is sent

- **WHEN** the system queries the gateway and `ANTHROPIC_API_KEY` is set
- **THEN** the outgoing request carries the upstream credential in its authentication
  header (the Anthropic SDK sends `x-api-key`)

### Requirement: Discovered Model Caching and Refresh

The system SHOULD cache the discovered model list rather than querying the upstream
gateway on every configuration render or API request.

- The cached list MUST be stored with a last-refreshed timestamp and a configurable
  time-to-live (TTL). The TTL SHOULD default to a few minutes (RECOMMENDED: 5 minutes).
- A cached list whose age is within the TTL MUST be served without contacting the
  upstream gateway.
- When the cached list is older than the TTL, the system SHOULD refresh it from the
  upstream gateway on the next access.
- The system MUST support an explicit, on-demand refresh that bypasses the TTL and
  re-queries the upstream gateway immediately.
- Cache access MUST be safe for concurrent use (the configuration API, the dashboard,
  and any background refresh may read or update the cache simultaneously).

#### Scenario: Cache hit within TTL

- **WHEN** the model list was refreshed 30 seconds ago, the TTL is 5 minutes, and a
  client requests the available models
- **THEN** the system returns the cached list without issuing an upstream request

#### Scenario: Cache expiry triggers refresh

- **WHEN** the model list was refreshed 10 minutes ago, the TTL is 5 minutes, and a
  client requests the available models
- **THEN** the system re-queries the upstream gateway and updates the cache and its
  last-refreshed timestamp

#### Scenario: Explicit refresh bypasses TTL

- **WHEN** an operator triggers an explicit refresh while a within-TTL cached list
  exists
- **THEN** the system re-queries the upstream gateway immediately and replaces the
  cached list

### Requirement: Available Models API Endpoint

The configuration API MUST expose the discovered model list so clients can present a
selectable list.

- The system MUST provide `GET /api/v1/models/available` returning a JSON object
  containing the discovered model identifiers and cache-freshness metadata
  (at minimum: the list of model IDs, the last-refreshed timestamp, and a flag or field
  indicating whether discovery is currently available).
- The endpoint MUST support an explicit refresh, either via a query parameter
  (e.g. `?refresh=true`) on the `GET` or a companion `POST /api/v1/models/available/refresh`,
  that forces an upstream re-query before responding.
- When discovery is unavailable (see Graceful Degradation), the endpoint MUST still
  return `200` with an empty model list and a field indicating discovery is unavailable,
  rather than returning an error status.
- Responses MUST set `Content-Type: application/json` per SPEC-0017 REQ-2.

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | /api/v1/models/available | Required | List discovered upstream models with cache metadata |
| POST | /api/v1/models/available/refresh | Required | Force an upstream re-query and return the refreshed list |

#### Scenario: List available models

- **WHEN** an authenticated client sends `GET /api/v1/models/available` and discovery
  succeeded
- **THEN** the response is `200` with `Content-Type: application/json` containing the
  discovered model IDs, a `last_refreshed` timestamp, and `discovery_available: true`

#### Scenario: Discovery unavailable returns empty list, not an error

- **WHEN** `ANTHROPIC_BASE_URL` is unset and a client sends
  `GET /api/v1/models/available`
- **THEN** the response is `200` with an empty model list and `discovery_available: false`

### Requirement: Tier Model Assignment from Discovered Models

The operator MUST be able to assign any discovered model identifier to `tier1_model`,
`tier2_model`, or `tier3_model`.

- The configuration update paths (`PUT /api/v1/config` per SPEC-0017 REQ-13 and the
  HTML config form `POST` per SPEC-0008 REQ-10) MUST accept any discovered model ID as
  the value for a tier model field and persist it.
- Assigning a model ID that is NOT in the discovered list (e.g. a manually typed value)
  MUST still be allowed and persisted — discovery is an aid, not a constraint
  (see Graceful Degradation).
- A persisted tier model assignment MUST be used by the Claude Code CLI subprocess via
  `--model` for that tier per SPEC-0010, with no behavioral change to how the value is
  consumed.

#### Scenario: Assign a discovered model to a tier

- **WHEN** the operator selects `gemini-2.5-pro` (a discovered model) for `tier1_model`
  and saves the configuration
- **THEN** `tier1_model` is persisted as `gemini-2.5-pro` and used for Tier 1 CLI
  invocations

#### Scenario: Assign a model not in the discovered list

- **WHEN** the operator submits `some-private-model` as `tier3_model` and that ID is
  not in the discovered list
- **THEN** the value is accepted and persisted without error

### Requirement: Configuration UI Model Selection

The dashboard configuration page SHOULD present the discovered models as a selectable
list for the per-tier model fields.

- The config page SHOULD render the tier1/tier2/tier3 model fields as a selectable
  control (e.g. a dropdown populated from the discovered model list) when discovery is
  available.
- The currently configured value for each tier MUST be pre-selected, and if the current
  value is not among the discovered models, it MUST still be displayed and remain the
  selected/entered value.
- The page SHOULD provide a control to refresh the discovered model list on demand.
- When discovery is unavailable, the page MUST fall back to free-text input for the tier
  model fields (see Graceful Degradation).

#### Scenario: Dropdown populated from discovered models

- **WHEN** discovery is available and the operator opens the config page
- **THEN** each tier model field offers the discovered models as selectable options with
  the current value pre-selected

#### Scenario: Current non-discovered value preserved in UI

- **WHEN** `tier2_model` is set to a value not present in the discovered list and the
  operator opens the config page
- **THEN** the current `tier2_model` value is shown as the selected/entered value and is
  not silently dropped

#### Scenario: Refresh control re-queries the gateway

- **WHEN** the operator activates the refresh control on the config page
- **THEN** the discovered model list is re-queried and the selectable options update

### Requirement: Graceful Degradation

The system MUST degrade gracefully when upstream model discovery cannot be performed,
and discovery failures MUST NOT break configuration rendering, retrieval, or saving.

- When `ANTHROPIC_BASE_URL` is unset, the system MUST treat discovery as unavailable
  and MUST NOT attempt an upstream request.
- When the upstream request fails, times out, returns a non-2xx status, or returns an
  unparseable body, the system MUST treat discovery as unavailable for that attempt and
  MUST surface the existing free-text configuration behavior.
- Rendering the dashboard config page and reading `GET /api/v1/config` MUST succeed even
  when discovery is unavailable.
- Saving configuration (HTML form `POST` and `PUT /api/v1/config`) MUST succeed even
  when discovery is unavailable, including persisting tier model values that are not in
  any discovered list.

#### Scenario: Base URL unset

- **WHEN** `ANTHROPIC_BASE_URL` is not configured
- **THEN** no upstream request is attempted, the config page renders with free-text tier
  model inputs, and configuration can still be saved

#### Scenario: Upstream timeout does not break config rendering

- **WHEN** the upstream gateway does not respond within the configured timeout while the
  operator loads the config page
- **THEN** the config page still renders (falling back to free-text inputs) and no error
  is shown that prevents saving

#### Scenario: Non-2xx upstream response

- **WHEN** the upstream gateway responds `503` to the models query
- **THEN** discovery is treated as unavailable for that attempt and the most recent
  successfully-cached list, if present, otherwise the free-text fallback, is used;
  configuration remains saveable

### Requirement: Upstream Call Security and Error Handling

The upstream discovery call MUST handle authentication, timeouts, and errors safely.

- The upstream `ANTHROPIC_API_KEY` MUST NOT be written to logs, error messages, API
  responses, or rendered HTML at any point.
- Discovery errors MUST be wrapped with contextual information at layer boundaries
  (e.g. "discover upstream models: request failed: ...") and MUST NOT be silently
  swallowed — each error MUST be returned to the caller or logged with sufficient
  (non-secret) context.
- Structured logging MUST be used for discovery error reporting (key-value pairs, not
  string interpolation of secrets).
- The discovery HTTP client MUST enforce the bounded timeout from the Upstream Model
  Query requirement and MUST use context propagation for cancellation across the request
  boundary.

#### Scenario: API key never appears in logs or responses

- **WHEN** any discovery outcome occurs (success, timeout, non-2xx, parse error)
- **THEN** the upstream API key value does not appear in any log line, error message,
  API response, or rendered page

#### Scenario: Discovery error is logged with context, not swallowed

- **WHEN** the upstream request returns a connection error
- **THEN** the error is logged with contextual, non-secret detail (e.g. the failing
  operation and status) and discovery is reported as unavailable rather than failing
  silently

## Security Requirements

This capability adds HTTP endpoints and makes outbound authenticated requests; the
following security requirements are MANDATORY.

> **Baseline note:** As of this writing the Claude Ops web layer
> (`internal/web/server.go`) wires `/api/v1/*` routes with no authentication,
> security-header, or CSRF middleware — the existing `/api/v1/config` endpoints are
> effectively unauthenticated. The requirements below define the regime the new
> endpoints MUST conform to; an implementer MUST NOT assume reusable auth/header/CSRF
> middleware already exists. Where a requirement says "the same regime as existing
> endpoints," it means: do not weaken whatever baseline is in place, and apply the
> protection if/when that baseline is established.

### Requirement: Authentication

The new `/api/v1/models/available` and `/api/v1/models/available/refresh` endpoints MUST
follow the same authentication regime as the existing `/api/v1/config` endpoints
(SPEC-0017) and MUST NOT be more permissive than those endpoints. They MUST NOT be
publicly accessible beyond whatever access the rest of `/api/v1/config` already permits,
since they reveal the operator's upstream model inventory.

| Method | Path | Auth | Justification |
|--------|------|------|---------------|
| GET | /api/v1/models/available | Required | Reveals upstream model inventory; same regime as /api/v1/config |
| POST | /api/v1/models/available/refresh | Required | Triggers an outbound authenticated upstream call |

#### Scenario: Unauthenticated request is rejected

- **WHEN** an unauthenticated client requests `GET /api/v1/models/available` and the
  deployment requires API authentication
- **THEN** the request is rejected with the same auth failure response as other
  `/api/v1/*` endpoints

### Requirement: Rate Limiting

The explicit-refresh path MUST be protected against abuse so it cannot be used to flood
the upstream gateway with model queries.

- On-demand refresh requests MUST be bounded such that repeated rapid refreshes collapse
  to at most one in-flight upstream query (e.g. single-flight) or are otherwise rate
  limited.

#### Scenario: Rapid refreshes collapse to one upstream call

- **WHEN** multiple refresh requests arrive within a very short window
- **THEN** at most one upstream model query is in flight at a time and the others receive
  its result

### Requirement: Request Body Size Limits

The `POST /api/v1/models/available/refresh` endpoint MUST bound any request body it
reads, consistent with other API write endpoints, to avoid unbounded memory use. The
response body read from the upstream gateway MUST also be bounded to a reasonable maximum
size.

#### Scenario: Oversized upstream response is bounded

- **WHEN** the upstream gateway returns an unexpectedly large response body
- **THEN** the system reads at most a bounded number of bytes and treats anything beyond
  the bound as a parse failure (discovery unavailable), without exhausting memory

### Requirement: Security Headers

Responses from the new endpoints MUST carry the same baseline security headers applied to
the rest of the Claude Ops HTTP surface (no weakening of existing header policy).

#### Scenario: Baseline headers present

- **WHEN** a client receives a response from `/api/v1/models/available`
- **THEN** the response carries the same baseline security headers as other API responses

### Requirement: CSRF Protection

The config form `POST` and any UI-triggered refresh that mutates server state MUST be
protected by the same CSRF protection as the existing config form (SPEC-0008). The
read-only `GET /api/v1/models/available` is not state-mutating and does not require CSRF
tokens.

#### Scenario: UI refresh honors existing CSRF protection

- **WHEN** a UI-initiated refresh mutates server-side cache state via a browser form/POST
- **THEN** it is subject to the same CSRF protection as the existing config form

### Requirement: Redirect Validation

This capability introduces no user-controlled redirects. The upstream request target is
derived solely from the operator-configured `ANTHROPIC_BASE_URL`; the system MUST NOT
follow a redirect to a host other than the configured base URL host without bound, and
MUST NOT take a redirect target from request input.

#### Scenario: Upstream target is not request-controlled

- **WHEN** a client calls the available-models endpoints
- **THEN** the upstream URL is computed only from `ANTHROPIC_BASE_URL`, never from client
  input

## Accessibility Requirements

This spec involves user-facing UI (the dashboard config page). The following
accessibility requirements are MANDATORY per WCAG 2.1 AA.

### WCAG 2.1 AA Compliance

All UI components produced by this spec MUST meet WCAG 2.1 Level AA conformance as the
minimum accessibility target.

### ARIA Landmarks

The config page structure MUST preserve existing ARIA landmark roles (`role="banner"`,
`role="navigation"`, `role="main"`, `role="contentinfo"`); the model-selection controls
MUST live within the `role="main"` content area.

### Icon-Only Controls

The refresh control, if rendered as an icon-only button, MUST include an `aria-label`
describing its purpose (e.g. `aria-label="Refresh available models"`).

### Dynamic Content Regions

The model-selection list and any "last refreshed" status that updates without a full page
reload (e.g. via HTMX swap) MUST use an `aria-live="polite"` region so assistive
technology announces the update.

### Keyboard Navigation

The model-selection control and the refresh control MUST be fully keyboard operable:
logical tab order, Enter/Space to activate the refresh control, and standard keyboard
interaction for the selection control (arrow keys for a native/ARIA listbox).

### Focus Management

After an on-demand refresh updates the model list in place, focus MUST NOT be lost — it
MUST remain on or return to the control that initiated the refresh (or a sensible
adjacent control).
