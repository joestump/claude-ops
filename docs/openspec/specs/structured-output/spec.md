---
status: proposed
date: 2026-03-21
---

# SPEC-0031: Structured Output via JSON Schema

## Overview

Claude Ops extracts structured data (events, memories, escalation decisions, service check results) from LLM agent responses. Currently, this extraction relies on text markers (`[EVENT:...]`, `[MEMORY:...]`) parsed via regex from the assistant text stream, and a filesystem-based handoff JSON file for escalation decisions. This specification formalizes the migration to Claude Code's `--json-schema` structured output, which constrains the LLM's final response to a validated JSON Schema and eliminates regex-based extraction entirely.

See [ADR-0030: Structured Output via JSON Schema](../../adrs/ADR-0030-structured-output-json-schema.md) for the decision rationale.

## Definitions

- **Structured output**: The `structured_output` field in the Claude Code CLI's JSON response, populated when `--json-schema <path>` is passed. Contains a JSON object conforming to the provided schema.
- **Response schema**: The JSON Schema file (`schemas/agent-response.json`) that defines the shape of every agent tier's structured output. Passed to the CLI via `--json-schema`.
- **Text marker**: The legacy `[EVENT:level[:service]] message` and `[MEMORY:category[:service]] observation` patterns parsed via regex from assistant text blocks. Replaced by structured output.
- **Handoff file**: The legacy `$CLAUDEOPS_STATE_DIR/handoff.json` file written by a tier to request escalation. Replaced by the `escalation` object in structured output.
- **Result event**: The final event in the stream-json NDJSON output (type `result`), which contains session metadata (cost, duration, turns) and, when `--json-schema` is used, the `structured_output` field.

## Requirements

### REQ-1: Response Schema Definition

The system MUST define a JSON Schema file at `schemas/agent-response.json` that specifies the structure of agent responses. The schema MUST define the following top-level fields:

- `summary` (string, REQUIRED): Brief summary of findings and actions taken.
- `events` (array of event objects, REQUIRED): Notable occurrences discovered or actions taken during the session.
- `memories` (array of memory objects, OPTIONAL): Operational knowledge to persist across sessions.
- `escalation` (object, REQUIRED): Whether the session recommends escalation to a higher tier.
- `services_checked` (array of service status objects, REQUIRED): Services inspected during the session with their observed status.

The schema file MUST be valid JSON Schema (draft-07 or later). The schema MUST be stored in version control and copied into the Docker image at build time.

#### Scenario: Schema file exists and is valid JSON Schema

Given the project repository
When a developer inspects `schemas/agent-response.json`
Then the file MUST contain a valid JSON Schema document
And the schema MUST define `summary`, `events`, `escalation`, and `services_checked` as required properties

#### Scenario: Schema is available inside the Docker container

Given the Dockerfile copies the schema to `/app/schemas/agent-response.json`
When the session manager starts inside the container
Then the schema file MUST be readable at the expected path

#### Scenario: Schema defines the correct top-level structure

Given the response schema
When an LLM response is validated against it
Then a response with `summary`, `events`, `escalation`, and `services_checked` fields MUST pass validation
And a response missing `escalation` MUST fail validation

### REQ-2: Event Object Schema

Each object in the `events` array MUST conform to the following schema:

- `level` (string, REQUIRED): One of `"info"`, `"warning"`, `"critical"`. The enum values MUST match the `level` column values in the existing `events` SQLite table.
- `message` (string, REQUIRED): A human-readable description of the event.
- `service` (string, OPTIONAL): The name of the service the event relates to. SHOULD be included when the event concerns a specific service.

The schema MUST enforce the level enum constraint so that invalid values (e.g., `"warn"`, `"error"`, `"CRITICAL"`) are rejected at generation time.

#### Scenario: Valid event with all fields

Given the agent emits an event about Jellyfin
When the structured output contains `{"level": "critical", "service": "jellyfin", "message": "HTTP 502 for 5 consecutive checks"}`
Then the event MUST pass schema validation
And the event MUST be insertable into the events table without transformation

#### Scenario: Event with only required fields

Given the agent emits a general event not tied to a specific service
When the structured output contains `{"level": "info", "message": "All 12 services healthy"}`
Then the event MUST pass schema validation
And the `service` field MUST be treated as NULL when inserting into the events table

#### Scenario: Event with invalid level is rejected

Given the LLM attempts to emit an event with level "warn"
When `--json-schema` constrains the output
Then the LLM MUST NOT produce `"level": "warn"` because it is not in the enum
And the level MUST be one of "info", "warning", or "critical"

### REQ-3: Escalation Decision Schema

The `escalation` object in the response MUST include:

- `needed` (boolean, REQUIRED): Whether the session recommends escalation to a higher tier.
- `reason` (string, REQUIRED when `needed` is `true`): Why escalation is needed.
- `context` (string, OPTIONAL): Investigation findings, diagnostic output, and other context for the next tier.
- `failed_checks` (array of strings, OPTIONAL): Identifiers of checks that failed, enabling the next tier to focus its investigation.

When `escalation.needed` is `true`, the session manager MUST trigger the next tier using the reason, context, and failed_checks to construct the escalation prompt context.

When `escalation.needed` is `false`, the session manager MUST NOT spawn a higher-tier session.

The `escalation` object MUST replace the filesystem-based `handoff.json` mechanism from ADR-0016/SPEC-0016 for communicating escalation decisions from the agent to the supervisor.

#### Scenario: Tier 1 recommends escalation

Given a Tier 1 session discovers failing services
When the structured output contains `{"escalation": {"needed": true, "reason": "Jellyfin and Postgres both returning 502", "context": "HTTP checks failed for 3 consecutive cycles...", "failed_checks": ["jellyfin-http", "postgres-http"]}}`
Then the session manager MUST create a new Tier 2 session with `parent_session_id` linking to the Tier 1 session
And the Tier 2 session's `--append-system-prompt` MUST include the reason, context, and failed_checks

#### Scenario: Tier 1 finds all services healthy

Given a Tier 1 session completes with no issues
When the structured output contains `{"escalation": {"needed": false}}`
Then the session manager MUST NOT spawn a Tier 2 session
And the monitoring cycle MUST proceed to the sleep interval

#### Scenario: Handoff file is no longer required

Given the session manager uses structured output for escalation decisions
When a tier session completes
Then the session manager MUST NOT check for `$CLAUDEOPS_STATE_DIR/handoff.json`
And the escalation decision MUST be read from `structured_output.escalation`

### REQ-4: CLI Integration

The session manager MUST pass `--json-schema <path>` to every `claude` CLI invocation for all tiers (1, 2, and 3), alongside the existing `--output-format stream-json`, `--allowedTools`, `--disallowedTools`, and `--append-system-prompt` flags.

The schema file path MUST be configurable via the `CLAUDEOPS_SCHEMA_PATH` environment variable, defaulting to `/app/schemas/agent-response.json`.

The session manager MUST parse the `structured_output` field from the final `result` event in the stream-json NDJSON output.

If the `structured_output` field is absent or null in the result event (e.g., due to a CLI version that does not support `--json-schema`), the session manager SHOULD fall back to text marker parsing as a degraded mode.

#### Scenario: CLI invocation includes json-schema flag

Given the session manager starts a Tier 1 session
When it constructs the `claude` CLI command
Then the command MUST include `--json-schema /app/schemas/agent-response.json`
And the command MUST also include `--output-format stream-json`

#### Scenario: Structured output parsed from result event

Given the CLI session completes and emits the final `result` event
When the result event contains a `structured_output` field
Then the session manager MUST deserialize the `structured_output` JSON
And the deserialized object MUST conform to the response schema

#### Scenario: Fallback to text markers when structured output is absent

Given a CLI version that does not support `--json-schema`
When the result event does not contain a `structured_output` field
Then the session manager MUST fall back to parsing `[EVENT:...]` and `[MEMORY:...]` text markers from the assistant text
And the session manager SHOULD log a warning indicating structured output was not available

### REQ-5: Real-Time Streaming Compatibility

The system MUST preserve real-time activity display via `--output-format stream-json` as specified in ADR-0011 and SPEC-0011. Adding `--json-schema` MUST NOT block, delay, or degrade the real-time activity feed.

The session manager MUST continue to process `assistant` and `user` events from the stream in real time, publishing formatted activity lines to the SSE hub for browser display.

Structured output extraction MUST occur only when the final `result` event is received. Event insertion, memory persistence, and escalation decision processing MUST happen after the stream completes, not during streaming.

#### Scenario: Activity log streams in real time during a session

Given a session is running with both `--output-format stream-json` and `--json-schema`
When the LLM invokes a tool and receives a result
Then the tool call and result MUST be published to the SSE hub immediately
And the browser MUST display the activity within the existing polling/streaming interval

#### Scenario: Structured output does not delay the activity feed

Given the session manager processes stream-json events sequentially
When an `assistant` event arrives during the stream
Then it MUST be formatted and published to the hub immediately
And it MUST NOT wait for the `result` event or structured output parsing

#### Scenario: Events inserted after session completes

Given the structured output contains 5 events
When the `result` event is received with the `structured_output` field
Then all 5 events MUST be inserted into the events table after the result is processed
And the events MUST NOT have been inserted earlier from text marker parsing (no duplicates)

### REQ-6: Event Insertion

The session manager MUST insert each object from the `structured_output.events` array into the `events` SQLite table. Each event MUST be inserted with:

- `session_id`: The current session's ID.
- `level`: From the event object's `level` field.
- `service`: From the event object's `service` field (NULL if absent).
- `message`: From the event object's `message` field.
- `created_at`: The current timestamp.

This insertion path MUST replace the text marker regex parsing path for event extraction. When structured output is available, the session manager MUST NOT also parse text markers for events (to avoid duplicates).

#### Scenario: Multiple events inserted from structured output

Given the structured output contains `[{"level": "info", "message": "All services healthy"}, {"level": "warning", "service": "jellyfin", "message": "Response time degraded to 3.2s"}]`
When the session manager processes the result event
Then 2 rows MUST be inserted into the events table
And the first row MUST have `level="info"`, `service=NULL`, and the info message
And the second row MUST have `level="warning"`, `service="jellyfin"`, and the warning message

#### Scenario: No events emitted produces no rows

Given the structured output contains `"events": []`
When the session manager processes the result event
Then 0 rows MUST be inserted into the events table
And no error MUST be logged

#### Scenario: Events have correct session_id

Given session #42 is running
When the structured output contains events
Then each inserted event row MUST have `session_id = 42`

### REQ-7: Memory Persistence

The session manager MUST insert each object from the `structured_output.memories` array into the `memories` SQLite table. Each memory MUST be inserted following the existing memory lifecycle rules from ADR-0015/SPEC-0015:

- New memories MUST be inserted with `confidence: 0.7` (default).
- If a memory matches an existing memory (by key similarity), the existing memory's confidence SHOULD be reinforced.
- The `key` field MUST be mapped to the memories table's `category` and `service` fields. When the key contains a service name prefix (e.g., `"jellyfin:timing"`), the service MUST be extracted and stored separately.

This insertion path MUST replace the text marker regex parsing path for memory extraction. When structured output is available, the session manager MUST NOT also parse text markers for memories.

#### Scenario: Memory inserted from structured output

Given the structured output contains `"memories": [{"key": "jellyfin:timing", "value": "Takes 60s to start after restart due to DB lock release"}]`
When the session manager processes the result event
Then a row MUST be inserted into the memories table
And the row MUST have `service="jellyfin"`, `category="timing"`, and the observation text

#### Scenario: General memory without service prefix

Given the structured output contains `"memories": [{"key": "remediation", "value": "DNS checks sometimes fail transiently during WireGuard reconnects"}]`
When the session manager processes the result event
Then a row MUST be inserted with `service=NULL` and `category="remediation"`

#### Scenario: No memories emitted produces no rows

Given the structured output contains `"memories": []` or the `memories` field is absent
When the session manager processes the result event
Then 0 rows MUST be inserted into the memories table

### REQ-8: Backward Compatibility

During the rollout period, the system SHOULD support both text marker parsing and structured output extraction. The session manager MUST use the following precedence:

1. If `structured_output` is present and non-null in the result event, use it exclusively for events, memories, and escalation decisions. Do NOT also parse text markers.
2. If `structured_output` is absent or null, fall back to text marker parsing for events and memories, and handoff file checking for escalation decisions.

Text marker parsing MAY be removed entirely once all tier prompts have been updated to describe the response schema and the `--json-schema` flag is confirmed working in production.

The tier prompt files (`tier1-observe.md`, `tier2-investigate.md`, `tier3-remediate.md`) MUST be updated to describe the expected response schema. The existing text marker format documentation (`[EVENT:...]`, `[MEMORY:...]`) MUST be removed from the prompts once structured output is the primary extraction path.

#### Scenario: Structured output takes precedence over text markers

Given a session produces both text markers in the assistant text and structured output in the result event
When the session manager processes the result event
Then it MUST use the structured output for events, memories, and escalation
And it MUST NOT parse text markers for events or memories

#### Scenario: Fallback to text markers when no structured output

Given a session's result event does not contain `structured_output`
When the session manager processes the result event
Then it MUST parse `[EVENT:...]` markers from the assistant text for events
And it MUST parse `[MEMORY:...]` markers from the assistant text for memories
And it MUST check for `$CLAUDEOPS_STATE_DIR/handoff.json` for escalation decisions

#### Scenario: Prompts updated to describe schema

Given the structured output rollout is complete
When a developer inspects `tier1-observe.md`
Then the prompt MUST contain a "Response Format" section describing the JSON schema fields
And the prompt MUST NOT contain `[EVENT:...]` or `[MEMORY:...]` marker format documentation

#### Scenario: Transition period supports both paths

Given the system is in the rollout transition period
When Tier 1 uses the updated prompt with `--json-schema` and Tier 2 uses the legacy prompt without `--json-schema`
Then Tier 1 events MUST be extracted from structured output
And Tier 2 events MUST be extracted from text markers
And both extraction paths MUST insert events into the same events table with the same schema

## References

- [ADR-0030: Structured Output via JSON Schema](../../adrs/ADR-0030-structured-output-json-schema.md)
- [ADR-0014: Real-Time Dashboard and Events](../../adrs/ADR-0014-realtime-dashboard-and-events.md)
- [ADR-0015: Persistent Agent Memory](../../adrs/ADR-0015-persistent-agent-memory.md)
- [ADR-0016: Session-Based Escalation with Structured Handoff](../../adrs/ADR-0016-session-based-escalation-handoff.md)
- [ADR-0011: Session Page CLI Output and Response](../../adrs/ADR-0011-session-page-cli-output-and-response.md)
- [SPEC-0013: Real-Time Dashboard and Events](../realtime-dashboard-events/spec.md)
- [SPEC-0015: Persistent Agent Memory](../persistent-agent-memory/spec.md)
- [SPEC-0016: Session-Based Escalation with Structured Handoff](../session-based-escalation/spec.md)
- [SPEC-0011: Session Page CLI Output](../session-cli-output/spec.md)
