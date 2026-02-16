# SPEC-0017: REST API with OpenAPI Specification and Swagger UI

## Overview

Claude Ops' Go web dashboard (SPEC-0008, ADR-0008) currently serves only HTML endpoints rendered via HTMX. This specification defines a JSON REST API under `/api/v1/` that exposes all dashboard data and actions programmatically, accompanied by an OpenAPI 3.1 specification file and an embedded Swagger UI for interactive documentation.

The API provides programmatic access to sessions, events, memories, cooldowns, configuration, and health status. It enables integration with external tools (Grafana, Slack bots, CI/CD pipelines), CLI-based automation via `curl`, and typed client generation from the OpenAPI spec.

This specification implements [ADR-0017: REST API with OpenAPI Specification and Swagger UI](/docs/adrs/ADR-0017-rest-api-with-openapi-and-swagger-ui.md).

## Definitions

- **REST API**: A set of HTTP endpoints that accept and return JSON, following REST conventions for resource naming, HTTP methods, and status codes.
- **OpenAPI 3.1**: A machine-readable specification format for describing REST APIs, enabling documentation generation, client generation, and contract testing.
- **Swagger UI**: A browser-based interactive API explorer that renders an OpenAPI specification into a navigable, executable documentation interface.
- **API Version Prefix**: The `/api/v1/` path prefix that namespaces all API endpoints, enabling future breaking changes under `/api/v2/` without affecting existing clients.
- **Pagination**: A query parameter pattern (`limit` and `offset`) for controlling the number and starting position of results returned by list endpoints.

## Requirements

### SPEC-0017-REQ-1: API Route Registration

All JSON API endpoints MUST be registered under the `/api/v1/` path prefix on the existing HTTP server. The API routes MUST coexist with the existing HTML dashboard routes on the same `ServeMux` and port. API handlers MUST be registered in the `registerRoutes()` method of the `Server` struct alongside the existing HTML route registrations.

#### Scenario: API routes registered on same server
WHEN the web server starts
THEN all `/api/v1/*` routes MUST be registered on the same `http.ServeMux` as the HTML routes
AND the server MUST serve both HTML and JSON endpoints on the same port

#### Scenario: API route does not conflict with HTML routes
WHEN a client requests `GET /sessions`
THEN the server MUST return the HTML sessions page
AND WHEN a client requests `GET /api/v1/sessions`
THEN the server MUST return a JSON array of sessions

#### Scenario: Unknown API route
WHEN a client requests a path under `/api/v1/` that does not match any registered route
THEN the server MUST return HTTP 404 with a JSON error body

### SPEC-0017-REQ-2: JSON Content Type

All API responses MUST set the `Content-Type` header to `application/json`. All API request bodies MUST be parsed as JSON (`application/json`). API endpoints MUST NOT accept or return `text/html`, `application/x-www-form-urlencoded`, or `multipart/form-data`.

#### Scenario: Successful response content type
WHEN a client sends `GET /api/v1/sessions`
THEN the response MUST include the header `Content-Type: application/json`

#### Scenario: Request with JSON body
WHEN a client sends `POST /api/v1/memories` with `Content-Type: application/json` and a valid JSON body
THEN the server MUST parse the body as JSON and process the request

#### Scenario: Request with non-JSON body
WHEN a client sends `POST /api/v1/memories` with `Content-Type: application/x-www-form-urlencoded`
THEN the server MUST return HTTP 415 Unsupported Media Type with a JSON error body

### SPEC-0017-REQ-3: Sessions List Endpoint

The server MUST expose `GET /api/v1/sessions` that returns a JSON array of session objects ordered by `started_at` descending. The endpoint MUST support `limit` and `offset` query parameters for pagination. The default limit MUST be 50. Each session object MUST include: `id`, `tier`, `model`, `status`, `started_at`, `ended_at`, `exit_code`, `cost_usd`, `num_turns`, `duration_ms`, `trigger`, `prompt_text`, and `parent_session_id`.

#### Scenario: List sessions with defaults
WHEN a client sends `GET /api/v1/sessions`
THEN the response MUST be HTTP 200
AND the body MUST be a JSON object with a `sessions` array containing up to 50 session objects ordered by `started_at` descending

#### Scenario: List sessions with custom limit
WHEN a client sends `GET /api/v1/sessions?limit=10`
THEN the response MUST contain at most 10 session objects

#### Scenario: List sessions with offset
WHEN a client sends `GET /api/v1/sessions?limit=10&offset=20`
THEN the response MUST skip the first 20 sessions and return the next 10

#### Scenario: Empty session list
WHEN no sessions exist in the database
THEN the response MUST be HTTP 200 with `{"sessions": []}`

### SPEC-0017-REQ-4: Session Detail Endpoint

The server MUST expose `GET /api/v1/sessions/{id}` that returns a single session object with its escalation chain. The response MUST include the session's fields plus `parent_session` (the parent session object if this session was escalated), `child_sessions` (array of child session objects), and `chain_cost` (total cost across the escalation chain). If the session ID does not exist, the server MUST return HTTP 404.

#### Scenario: Get existing session
WHEN a client sends `GET /api/v1/sessions/42`
AND session 42 exists
THEN the response MUST be HTTP 200 with the session object including escalation chain data

#### Scenario: Get session with escalation chain
WHEN a client sends `GET /api/v1/sessions/42`
AND session 42 has parent_session_id=40 and a child session 43
THEN the response MUST include `parent_session` as the session 40 object
AND `child_sessions` as an array containing the session 43 object
AND `chain_cost` as the sum of cost_usd across sessions 40, 42, and 43

#### Scenario: Get nonexistent session
WHEN a client sends `GET /api/v1/sessions/99999`
AND no session with ID 99999 exists
THEN the response MUST be HTTP 404 with a JSON error body

#### Scenario: Invalid session ID format
WHEN a client sends `GET /api/v1/sessions/abc`
THEN the response MUST be HTTP 400 with a JSON error body

### SPEC-0017-REQ-5: Session Trigger Endpoint

The server MUST expose `POST /api/v1/sessions/trigger` that accepts a JSON body with a `prompt` field and triggers an ad-hoc session. On success, the server MUST return HTTP 201 with the created session object including its `id`. If a session is already running, the server MUST return HTTP 409 Conflict. If the `prompt` field is missing or empty, the server MUST return HTTP 400.

#### Scenario: Trigger ad-hoc session successfully
WHEN a client sends `POST /api/v1/sessions/trigger` with body `{"prompt": "Check nginx status"}`
AND no session is currently running
THEN the response MUST be HTTP 201
AND the body MUST include the created session's `id`

#### Scenario: Trigger session while one is running
WHEN a client sends `POST /api/v1/sessions/trigger` with body `{"prompt": "Check nginx"}`
AND a session is already running
THEN the response MUST be HTTP 409 with a JSON error body indicating a session is already in progress

#### Scenario: Trigger with missing prompt
WHEN a client sends `POST /api/v1/sessions/trigger` with body `{}`
THEN the response MUST be HTTP 400 with a JSON error body indicating prompt is required

#### Scenario: Trigger with empty prompt
WHEN a client sends `POST /api/v1/sessions/trigger` with body `{"prompt": ""}`
THEN the response MUST be HTTP 400 with a JSON error body indicating prompt is required

### SPEC-0017-REQ-6: Events List Endpoint

The server MUST expose `GET /api/v1/events` that returns a JSON array of event objects ordered by `created_at` descending. The endpoint MUST support `limit` and `offset` query parameters. The default limit MUST be 100. The endpoint MUST support optional `level` and `service` query parameters for filtering. Each event object MUST include: `id`, `session_id`, `level`, `service`, `message`, and `created_at`.

#### Scenario: List events with defaults
WHEN a client sends `GET /api/v1/events`
THEN the response MUST be HTTP 200
AND the body MUST be a JSON object with an `events` array containing up to 100 event objects

#### Scenario: Filter events by level
WHEN a client sends `GET /api/v1/events?level=critical`
THEN all returned events MUST have `level` equal to `"critical"`

#### Scenario: Filter events by service
WHEN a client sends `GET /api/v1/events?service=nginx`
THEN all returned events MUST have `service` equal to `"nginx"`

#### Scenario: Combined filters
WHEN a client sends `GET /api/v1/events?level=warning&service=nginx&limit=5`
THEN the response MUST contain at most 5 events matching both filters

### SPEC-0017-REQ-7: Memories List Endpoint

The server MUST expose `GET /api/v1/memories` that returns a JSON array of memory objects ordered by `confidence` descending. The endpoint MUST support `limit` and `offset` query parameters. The default limit MUST be 200. The endpoint MUST support optional `service` and `category` query parameters for filtering. Each memory object MUST include: `id`, `service`, `category`, `observation`, `confidence`, `active`, `created_at`, `updated_at`, `session_id`, and `tier`.

#### Scenario: List memories with defaults
WHEN a client sends `GET /api/v1/memories`
THEN the response MUST be HTTP 200
AND the body MUST be a JSON object with a `memories` array ordered by `confidence` descending

#### Scenario: Filter memories by service
WHEN a client sends `GET /api/v1/memories?service=nginx`
THEN all returned memories MUST have `service` equal to `"nginx"`

#### Scenario: Filter memories by category
WHEN a client sends `GET /api/v1/memories?category=config`
THEN all returned memories MUST have `category` equal to `"config"`

### SPEC-0017-REQ-8: Memory Create Endpoint

The server MUST expose `POST /api/v1/memories` that accepts a JSON body and creates a new memory record. The request body MUST include `category` and `observation` fields. The request body MAY include `service`, `confidence`, and `active` fields. If `confidence` is not provided, the default MUST be 0.7. If `active` is not provided, the default MUST be `true`. On success, the server MUST return HTTP 201 with the created memory object including its `id`. If required fields are missing, the server MUST return HTTP 400.

#### Scenario: Create memory with required fields
WHEN a client sends `POST /api/v1/memories` with body `{"category": "config", "observation": "nginx uses port 8080"}`
THEN the response MUST be HTTP 201
AND the body MUST include the created memory with an `id`, `confidence` of 0.7, and `active` of true

#### Scenario: Create memory with all fields
WHEN a client sends `POST /api/v1/memories` with body `{"service": "nginx", "category": "config", "observation": "listens on 8080", "confidence": 0.9, "active": true}`
THEN the response MUST be HTTP 201 with all fields reflected in the response

#### Scenario: Create memory with missing required fields
WHEN a client sends `POST /api/v1/memories` with body `{"service": "nginx"}`
THEN the response MUST be HTTP 400 with a JSON error body indicating category and observation are required

### SPEC-0017-REQ-9: Memory Update Endpoint

The server MUST expose `PUT /api/v1/memories/{id}` that accepts a JSON body and updates an existing memory record. The request body MUST include `observation`, `confidence`, and `active` fields. On success, the server MUST return HTTP 200 with the updated memory object. If the memory ID does not exist, the server MUST return HTTP 404. If required fields are missing, the server MUST return HTTP 400.

#### Scenario: Update existing memory
WHEN a client sends `PUT /api/v1/memories/5` with body `{"observation": "updated text", "confidence": 0.8, "active": true}`
AND memory 5 exists
THEN the response MUST be HTTP 200 with the updated memory object

#### Scenario: Update nonexistent memory
WHEN a client sends `PUT /api/v1/memories/99999` with body `{"observation": "x", "confidence": 0.5, "active": false}`
AND no memory with ID 99999 exists
THEN the response MUST be HTTP 404 with a JSON error body

#### Scenario: Update with missing fields
WHEN a client sends `PUT /api/v1/memories/5` with body `{"observation": "updated"}`
THEN the response MUST be HTTP 400 with a JSON error body indicating confidence and active are required

### SPEC-0017-REQ-10: Memory Delete Endpoint

The server MUST expose `DELETE /api/v1/memories/{id}` that deletes an existing memory record. On success, the server MUST return HTTP 204 No Content with an empty body. If the memory ID does not exist, the server MUST return HTTP 404.

#### Scenario: Delete existing memory
WHEN a client sends `DELETE /api/v1/memories/5`
AND memory 5 exists
THEN the response MUST be HTTP 204 with no body
AND subsequent `GET /api/v1/memories/5` requests MUST return HTTP 404

#### Scenario: Delete nonexistent memory
WHEN a client sends `DELETE /api/v1/memories/99999`
AND no memory with ID 99999 exists
THEN the response MUST be HTTP 404 with a JSON error body

### SPEC-0017-REQ-11: Cooldowns List Endpoint

The server MUST expose `GET /api/v1/cooldowns` that returns a JSON array of recent cooldown action summaries within the last 24 hours. Each cooldown object MUST include: `service`, `action_type`, `count`, and `last_action` (timestamp). The results MUST be ordered by `last_action` descending.

#### Scenario: List cooldowns
WHEN a client sends `GET /api/v1/cooldowns`
THEN the response MUST be HTTP 200
AND the body MUST be a JSON object with a `cooldowns` array

#### Scenario: Empty cooldowns
WHEN no cooldown actions have occurred in the last 24 hours
THEN the response MUST be HTTP 200 with `{"cooldowns": []}`

### SPEC-0017-REQ-12: Config Get Endpoint

The server MUST expose `GET /api/v1/config` that returns the current runtime configuration as a JSON object. The response MUST include: `interval`, `tier1_model`, `tier2_model`, `tier3_model`, `dry_run`, `max_tier`, `state_dir`, `results_dir`, and `repos_dir`.

#### Scenario: Get current config
WHEN a client sends `GET /api/v1/config`
THEN the response MUST be HTTP 200
AND the body MUST be a JSON object containing all configuration fields with their current values

### SPEC-0017-REQ-13: Config Update Endpoint

The server MUST expose `PUT /api/v1/config` that accepts a JSON body and updates runtime configuration. The endpoint MUST support updating `interval`, `tier1_model`, `tier2_model`, `tier3_model`, and `dry_run`. Updated values MUST be persisted to the SQLite config table and applied to the in-memory config. The response MUST be HTTP 200 with the full updated configuration object. If `interval` is provided, it MUST be a positive integer; otherwise the server MUST return HTTP 400.

#### Scenario: Update config interval
WHEN a client sends `PUT /api/v1/config` with body `{"interval": 1800}`
THEN the response MUST be HTTP 200
AND the `interval` field in the response MUST be 1800
AND subsequent `GET /api/v1/config` requests MUST return `interval` as 1800

#### Scenario: Update multiple config fields
WHEN a client sends `PUT /api/v1/config` with body `{"interval": 900, "dry_run": true, "tier1_model": "sonnet"}`
THEN all three fields MUST be updated and reflected in the response

#### Scenario: Update config with invalid interval
WHEN a client sends `PUT /api/v1/config` with body `{"interval": -1}`
THEN the response MUST be HTTP 400 with a JSON error body

#### Scenario: Partial config update
WHEN a client sends `PUT /api/v1/config` with body `{"dry_run": true}`
THEN only the `dry_run` field MUST be updated
AND all other fields MUST retain their previous values

### SPEC-0017-REQ-14: Health Endpoint

The server MUST expose `GET /api/v1/health` that returns a health check response. The response MUST be HTTP 200 with body `{"status": "ok"}`. This endpoint MUST NOT require authentication (if authentication is added in the future). The endpoint SHOULD respond within 100 milliseconds.

#### Scenario: Health check returns ok
WHEN a client sends `GET /api/v1/health`
THEN the response MUST be HTTP 200
AND the body MUST be `{"status": "ok"}`

#### Scenario: Health check response time
WHEN a client sends `GET /api/v1/health`
THEN the response SHOULD be returned within 100 milliseconds

### SPEC-0017-REQ-15: OpenAPI Specification File

The server MUST serve an OpenAPI 3.1 YAML specification file at `GET /api/openapi.yaml`. The specification file MUST be embedded in the Go binary via `//go:embed`. The specification MUST accurately describe all `/api/v1/` endpoints, their parameters, request bodies, and response schemas. The specification MUST include schema definitions for all domain objects (Session, Event, Memory, Cooldown, Config, Error).

#### Scenario: Serve OpenAPI spec
WHEN a client sends `GET /api/openapi.yaml`
THEN the response MUST be HTTP 200
AND the `Content-Type` header MUST be `text/yaml` or `application/yaml`
AND the body MUST be a valid OpenAPI 3.1 YAML document

#### Scenario: Spec describes all endpoints
WHEN the OpenAPI specification is parsed
THEN it MUST contain path definitions for all 12 API endpoints defined in this specification

#### Scenario: Spec validates
WHEN the OpenAPI specification is validated with an OpenAPI validator
THEN it MUST pass validation without errors

### SPEC-0017-REQ-16: Swagger UI

The server MUST serve Swagger UI at `/api/docs/`. Swagger UI static assets MUST be embedded in the Go binary via `//go:embed`. Swagger UI MUST be configured to load the OpenAPI specification from `/api/openapi.yaml`. Users MUST be able to execute API requests directly from Swagger UI via the "Try it out" feature.

#### Scenario: Swagger UI loads
WHEN a client navigates to `/api/docs/` in a browser
THEN the browser MUST render the Swagger UI interface
AND the UI MUST display all API endpoints from the OpenAPI specification

#### Scenario: Swagger UI executes requests
WHEN a user clicks "Try it out" on the `GET /api/v1/sessions` endpoint in Swagger UI
AND clicks "Execute"
THEN Swagger UI MUST send the request to the server and display the JSON response

#### Scenario: Swagger UI assets are embedded
WHEN the Go binary is deployed without accompanying files
THEN Swagger UI MUST load correctly from the embedded assets

### SPEC-0017-REQ-17: Error Response Format

All API error responses MUST use a consistent JSON format: `{"error": "<message>"}`. The `error` field MUST be a human-readable string describing the problem. Error responses MUST use appropriate HTTP status codes: 400 for client errors (bad input), 404 for not found, 409 for conflict, 415 for unsupported media type, and 500 for server errors. The server MUST NOT return HTML error pages for API endpoints.

#### Scenario: Validation error format
WHEN a client sends an invalid request to any API endpoint
THEN the response body MUST be JSON matching the format `{"error": "<description>"}`
AND the `Content-Type` MUST be `application/json`

#### Scenario: Internal server error format
WHEN an API handler encounters a database error
THEN the response MUST be HTTP 500
AND the body MUST be `{"error": "internal server error"}`
AND the response MUST NOT expose internal error details to the client

#### Scenario: Not found error format
WHEN a client requests a resource that does not exist
THEN the response MUST be HTTP 404
AND the body MUST be `{"error": "<resource> not found"}`

### SPEC-0017-REQ-18: Pagination

List endpoints (`sessions`, `events`, `memories`) MUST support `limit` and `offset` integer query parameters. The `limit` parameter MUST control the maximum number of results returned. The `offset` parameter MUST control how many results to skip from the beginning. If `limit` is not provided, each endpoint MUST use its default (50 for sessions, 100 for events, 200 for memories). If `offset` is not provided, the default MUST be 0. If `limit` or `offset` is negative, the server MUST return HTTP 400.

#### Scenario: Default pagination
WHEN a client sends `GET /api/v1/sessions` without limit or offset
THEN the server MUST return up to 50 results starting from offset 0

#### Scenario: Custom pagination
WHEN a client sends `GET /api/v1/events?limit=25&offset=50`
THEN the server MUST skip the first 50 events and return up to 25

#### Scenario: Negative limit
WHEN a client sends `GET /api/v1/sessions?limit=-1`
THEN the response MUST be HTTP 400 with a JSON error body

#### Scenario: Negative offset
WHEN a client sends `GET /api/v1/sessions?offset=-5`
THEN the response MUST be HTTP 400 with a JSON error body

### SPEC-0017-REQ-19: Backward Compatibility

The existing HTML dashboard MUST continue to function identically after the API is added. All existing HTML routes (`/`, `/sessions`, `/sessions/{id}`, `/events`, `/memories`, `/cooldowns`, `/config`, etc.) MUST remain unchanged. The existing `POST` endpoints for HTML forms (`POST /sessions/trigger`, `POST /memories`, `POST /memories/{id}/update`, `POST /memories/{id}/delete`, `POST /config`) MUST continue to accept form-encoded data and return HTML redirects or HTMX responses. SSE streaming at `/sessions/{id}/stream` MUST continue to work.

#### Scenario: HTML dashboard unchanged
WHEN a browser requests `GET /sessions`
THEN the response MUST be the same HTML page as before the API was added

#### Scenario: HTML form submission unchanged
WHEN a browser submits `POST /memories` with form-encoded data
THEN the server MUST create the memory and redirect to `/memories` as before

#### Scenario: SSE streaming unchanged
WHEN a browser requests `GET /sessions/42/stream` with `Accept: text/event-stream`
THEN the server MUST stream session output via SSE as before

## References

- [ADR-0017: REST API with OpenAPI Specification and Swagger UI](/docs/adrs/ADR-0017-rest-api-with-openapi-and-swagger-ui.md)
- [SPEC-0008: Go/HTMX/DaisyUI Web Dashboard](/docs/openspec/specs/go-htmx-dashboard/spec.md)
- [internal/web/server.go](/internal/web/server.go) -- existing HTTP server and route registration
- [internal/web/handlers.go](/internal/web/handlers.go) -- existing HTML handler implementations
- [internal/db/db.go](/internal/db/db.go) -- database types and methods
- [internal/web/viewmodel.go](/internal/web/viewmodel.go) -- existing view model types
- [OpenAPI 3.1 Specification](https://spec.openapis.org/oas/v3.1.0)
- [Swagger UI](https://swagger.io/tools/swagger-ui/)
