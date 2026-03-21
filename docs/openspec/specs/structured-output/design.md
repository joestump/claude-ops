# Design: Structured Output via JSON Schema

## Overview

This design describes how Claude Ops migrates from regex-based text marker parsing (`[EVENT:...]`, `[MEMORY:...]`) and filesystem-based handoff files (`handoff.json`) to Claude Code's `--json-schema` structured output for extracting events, memories, and escalation decisions from LLM agent responses.

See [SPEC-0031](./spec.md) and [ADR-0030](../../adrs/ADR-0030-structured-output-json-schema.md).

## Architecture

### Current Data Extraction Flow (Text Markers)

```
┌──────────────────────────────────────────────────────┐
│                  Claude CLI Session                   │
│                                                       │
│  Assistant text output:                               │
│  "Checking jellyfin... HTTP 502 detected.            │
│   [EVENT:critical:jellyfin] HTTP 502 for 5 checks    │
│   [MEMORY:timing:jellyfin] Takes 60s to restart      │
│   Writing handoff.json for escalation..."             │
│                                                       │
│  Filesystem:                                          │
│  $CLAUDEOPS_STATE_DIR/handoff.json                    │
└──────────────────┬───────────────────────────────────┘
                   │ stream-json NDJSON
                   ▼
┌──────────────────────────────────────────────────────┐
│              Session Manager (Go)                     │
│                                                       │
│  1. Regex scan assistant text for [EVENT:...] markers │
│     → INSERT into events table                        │
│  2. Regex scan assistant text for [MEMORY:...] markers│
│     → INSERT into memories table                      │
│  3. Check filesystem for handoff.json                 │
│     → Parse JSON, decide escalation                   │
│                                                       │
│  Problems: missed markers, malformed markers,         │
│  false positives, handoff file race conditions        │
└──────────────────────────────────────────────────────┘
```

### New Data Extraction Flow (Structured Output)

```
┌──────────────────────────────────────────────────────┐
│                  Claude CLI Session                   │
│                                                       │
│  --output-format stream-json                          │
│  --json-schema /app/schemas/agent-response.json       │
│                                                       │
│  Stream events (real-time):                           │
│    {"type":"assistant","content":[{"text":"..."}]}    │
│    {"type":"user","content":[{"tool_result":"..."}]}  │
│                                                       │
│  Final result event:                                  │
│    {"type":"result",                                  │
│     "total_cost_usd": 0.03,                           │
│     "structured_output": {                            │
│       "summary": "...",                               │
│       "events": [...],                                │
│       "memories": [...],                              │
│       "escalation": {...},                            │
│       "services_checked": [...]                       │
│     }}                                                │
└──────────────────┬───────────────────────────────────┘
                   │ stream-json NDJSON
                   ▼
┌──────────────────────────────────────────────────────┐
│              Session Manager (Go)                     │
│                                                       │
│  During stream:                                       │
│    Format assistant/user events → SSE hub (real-time) │
│    Write raw NDJSON → log file (forensic)             │
│                                                       │
│  On result event:                                     │
│    1. Parse structured_output JSON                    │
│    2. INSERT events[] → events table                  │
│    3. INSERT memories[] → memories table              │
│    4. Read escalation.needed → decide next tier       │
│    5. Extract cost, duration, turns → session record  │
│                                                       │
│  No regex. No handoff files. No false positives.      │
└──────────────────────────────────────────────────────┘
```

### CLI Invocation Change

The session manager's CLI command gains one flag:

```bash
claude \
    --model "${MODEL}" \
    -p "$(cat "${PROMPT_FILE}")" \
    --output-format stream-json \
    --json-schema "${CLAUDEOPS_SCHEMA_PATH:-/app/schemas/agent-response.json}" \
    --allowedTools "${ALLOWED_TOOLS}" \
    --disallowedTools "${DISALLOWED_TOOLS}" \
    --append-system-prompt "Environment: ${ENV_CONTEXT}" \
    2>&1
```

The `--json-schema` flag tells the CLI to constrain the LLM's final response to the provided schema. The structured output appears in the `structured_output` field of the final `result` event in the stream-json output.

## Response Schema Definition

The full schema is stored at `schemas/agent-response.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Claude Ops Agent Response",
  "description": "Structured output schema for Claude Ops agent tier sessions",
  "type": "object",
  "properties": {
    "summary": {
      "type": "string",
      "description": "Brief summary of findings and actions taken during this session"
    },
    "events": {
      "type": "array",
      "description": "Notable occurrences discovered or actions taken",
      "items": {
        "type": "object",
        "properties": {
          "level": {
            "type": "string",
            "enum": ["info", "warning", "critical"],
            "description": "Severity: info (routine), warning (degraded), critical (needs attention)"
          },
          "service": {
            "type": "string",
            "description": "Service name, if the event relates to a specific service"
          },
          "message": {
            "type": "string",
            "description": "Human-readable description of the event"
          }
        },
        "required": ["level", "message"]
      }
    },
    "memories": {
      "type": "array",
      "description": "Operational knowledge to persist across sessions",
      "items": {
        "type": "object",
        "properties": {
          "key": {
            "type": "string",
            "description": "Memory identifier in format 'category' or 'service:category'"
          },
          "value": {
            "type": "string",
            "description": "The operational knowledge to remember"
          }
        },
        "required": ["key", "value"]
      }
    },
    "escalation": {
      "type": "object",
      "description": "Whether this session recommends escalation to a higher tier",
      "properties": {
        "needed": {
          "type": "boolean",
          "description": "True if escalation to the next tier is recommended"
        },
        "reason": {
          "type": "string",
          "description": "Why escalation is needed (required when needed=true)"
        },
        "context": {
          "type": "string",
          "description": "Investigation findings and diagnostic context for the next tier"
        },
        "failed_checks": {
          "type": "array",
          "items": { "type": "string" },
          "description": "Identifiers of checks that failed"
        }
      },
      "required": ["needed"]
    },
    "services_checked": {
      "type": "array",
      "description": "Services inspected during this session with observed status",
      "items": {
        "type": "object",
        "properties": {
          "name": {
            "type": "string",
            "description": "Service name"
          },
          "status": {
            "type": "string",
            "enum": ["healthy", "degraded", "down", "unreachable"],
            "description": "Observed service status"
          },
          "detail": {
            "type": "string",
            "description": "Additional detail about the status observation"
          }
        },
        "required": ["name", "status"]
      }
    }
  },
  "required": ["summary", "events", "escalation", "services_checked"]
}
```

## Session Manager Changes

### Result Event Processing

The session manager already processes the `result` event to extract cost, duration, and turn count. The change adds `structured_output` parsing to the same code path.

```
result event received
  │
  ├─ Extract cost, duration, turns (existing)
  │
  ├─ Check for structured_output field
  │    │
  │    ├─ Present and non-null:
  │    │    ├─ Deserialize into AgentResponse struct
  │    │    ├─ Insert events[] → events table
  │    │    ├─ Insert memories[] → memories table
  │    │    ├─ Read escalation → set escalation state
  │    │    └─ Set extraction_method = "structured"
  │    │
  │    └─ Absent or null:
  │         ├─ Fall back to text marker parsing (legacy)
  │         ├─ Fall back to handoff file check (legacy)
  │         └─ Set extraction_method = "markers"
  │
  └─ Update session record in DB
```

### Go Struct for Response

```go
type AgentResponse struct {
    Summary         string           `json:"summary"`
    Events          []AgentEvent     `json:"events"`
    Memories        []AgentMemory    `json:"memories,omitempty"`
    Escalation      AgentEscalation  `json:"escalation"`
    ServicesChecked []ServiceCheck   `json:"services_checked"`
}

type AgentEvent struct {
    Level   string `json:"level"`
    Service string `json:"service,omitempty"`
    Message string `json:"message"`
}

type AgentMemory struct {
    Key   string `json:"key"`
    Value string `json:"value"`
}

type AgentEscalation struct {
    Needed       bool     `json:"needed"`
    Reason       string   `json:"reason,omitempty"`
    Context      string   `json:"context,omitempty"`
    FailedChecks []string `json:"failed_checks,omitempty"`
}

type ServiceCheck struct {
    Name   string `json:"name"`
    Status string `json:"status"`
    Detail string `json:"detail,omitempty"`
}
```

## Event Insertion Flow

When structured output is available, event insertion follows this path:

```
structured_output.events[]
  │
  for each event:
  │
  ├─ Validate level is in {"info", "warning", "critical"}
  │   (redundant — schema enforces this, but defense-in-depth)
  │
  ├─ INSERT INTO events (session_id, level, service, message, created_at)
  │   VALUES (?, event.Level, event.Service, event.Message, NOW())
  │
  └─ Publish to SSE hub for real-time event feed update
```

This replaces the `parseEventMarker()` regex pipeline from ADR-0014. The events table schema is unchanged -- the same columns are populated from different source fields.

## Memory Persistence Flow

Memory insertion maps the structured output's key-value format to the existing memories table schema:

```
structured_output.memories[]
  │
  for each memory:
  │
  ├─ Parse key: split on ":" to extract service and category
  │   "jellyfin:timing"  → service="jellyfin", category="timing"
  │   "remediation"      → service=NULL,        category="remediation"
  │
  ├─ Check for existing memory with same service + category
  │   ├─ Match found: reinforce confidence (existing ADR-0015 logic)
  │   └─ No match: INSERT with confidence=0.7 (default)
  │
  └─ INSERT INTO memories (service, category, observation, confidence,
  │                         active, created_at, updated_at, session_id, tier)
  │   VALUES (?, ?, memory.Value, 0.7, 1, NOW(), NOW(), ?, ?)
```

This replaces the `parseMemoryMarkers()` regex pipeline from ADR-0015. The memories table schema and lifecycle logic (confidence scoring, staleness pruning) are unchanged.

## Escalation Decision Flow

The escalation flow replaces the handoff file mechanism:

```
structured_output.escalation
  │
  ├─ escalation.needed == false
  │   └─ No escalation. Sleep and repeat.
  │
  └─ escalation.needed == true
      │
      ├─ Check supervisor policies:
      │   ├─ Cooldown limits (max restarts/service/4h)
      │   ├─ Dry-run mode (log but don't escalate)
      │   └─ Max-tier limit ($CLAUDEOPS_MAX_TIER)
      │
      ├─ Construct escalation context for next tier:
      │   ├─ escalation.reason → "Escalation reason: ..."
      │   ├─ escalation.context → "Investigation findings: ..."
      │   ├─ escalation.failed_checks → "Failed checks: ..."
      │   └─ services_checked[] → "Service status: ..."
      │
      ├─ Create new session record:
      │   ├─ tier = current_tier + 1
      │   ├─ model = tier_model_map[tier]
      │   └─ parent_session_id = current_session.id
      │
      └─ Spawn next tier CLI process:
          └─ --append-system-prompt includes escalation context
```

This replaces:
1. The LLM writing `$CLAUDEOPS_STATE_DIR/handoff.json` (filesystem artifact)
2. The supervisor reading and deleting the handoff file
3. The handoff JSON format contract between prompts and Go code

The escalation contract is now embedded in the response schema, which is validated at generation time.

## Stream-JSON Compatibility

The `--json-schema` flag works alongside `--output-format stream-json`. The interaction model:

```
Stream begins
  │
  ├─ {"type":"system", ...}        → Log, ignore for display
  │
  ├─ {"type":"assistant", ...}     → Format and publish to SSE hub
  │                                   (tool_use blocks, text blocks)
  │
  ├─ {"type":"user", ...}          → Format tool results, publish to hub
  │
  ├─ ... (more assistant/user events during tool use) ...
  │
  └─ {"type":"result",             → FINAL EVENT
       "total_cost_usd": 0.03,
       "duration_ms": 45000,
       "num_turns": 8,
       "structured_output": {       ← NEW FIELD (from --json-schema)
         "summary": "...",
         "events": [...],
         "memories": [...],
         "escalation": {...},
         "services_checked": [...]
       }
     }
```

Key points:
- Real-time activity display is **unaffected**. Assistant and user events stream to the browser as before.
- Structured output processing happens **once**, when the `result` event arrives.
- The `result` event is already the trigger for session completion (cost/duration extraction). Adding structured output parsing is an extension of the same handler.
- Text markers in the assistant text stream are **ignored** when structured output is available, preventing duplicate event/memory insertion.

## Schema File Location and Docker Mount

```
Project root:
  schemas/
    agent-response.json       ← The response schema

Dockerfile:
  COPY schemas/ /app/schemas/

Container runtime:
  /app/schemas/agent-response.json    ← Read by session manager
```

The path is configurable via `CLAUDEOPS_SCHEMA_PATH` environment variable:

```bash
# Default
CLAUDEOPS_SCHEMA_PATH=/app/schemas/agent-response.json

# Override (e.g., for local development)
CLAUDEOPS_SCHEMA_PATH=./schemas/agent-response.json
```

## Migration Path

### Phase 1: Add Schema and Dual-Path Parsing

1. Create `schemas/agent-response.json` with the response schema.
2. Add `AgentResponse` Go structs to the session manager.
3. Modify the result event handler to check for `structured_output` and use it when available, falling back to marker parsing when absent.
4. Add `--json-schema` to the CLI invocation in the session manager.
5. The tier prompts are NOT yet updated -- the LLM may produce both text markers (from prompt instructions) and structured output (from schema constraint). The session manager uses structured output when available.

### Phase 2: Update Tier Prompts

1. Update `tier1-observe.md` to describe the response schema instead of marker formats.
2. Update `tier2-investigate.md` similarly.
3. Update `tier3-remediate.md` similarly.
4. Remove `[EVENT:...]` and `[MEMORY:...]` format documentation from all prompts.
5. Remove handoff file writing instructions from all prompts.

### Phase 3: Remove Legacy Parsing

1. Remove `parseEventMarker()` regex function.
2. Remove `parseMemoryMarkers()` regex function.
3. Remove handoff file check logic from the supervisor.
4. Remove the fallback path in the result event handler.
5. Clean up any `handoff.json` references in prompt files and documentation.

## Key Design Decisions

### Structured output extracts from the final result, not from the stream

The `structured_output` field appears only in the final `result` event, not in intermediate assistant events. This means events, memories, and escalation decisions are extracted **after** the session completes, not during streaming. This is acceptable because:

- Real-time activity display uses the raw assistant/user event stream, not extracted events.
- Event insertion latency of a few seconds (between the last tool call and the result event) is negligible for a monitoring dashboard with 5-second polling.
- Processing all extracted data at once (after the result event) simplifies the insertion logic and avoids partial-state issues.

### The memory key format uses "service:category" rather than separate fields

The schema uses a single `key` string for memories instead of separate `service` and `category` fields. This is because:

- The LLM has an easier time producing a single key string than populating two separate fields correctly.
- The key format matches the existing `[MEMORY:category:service]` marker convention, making the prompt migration simpler.
- Parsing "jellyfin:timing" into service + category in Go is trivial.

The trade-off is that the schema is less strict about the key format -- a key like "some random string" would pass schema validation but fail category mapping. This is an accepted limitation; the Go code validates the key format during insertion and logs a warning for unmappable keys.

### The memories field is optional in the schema

Unlike `events`, `escalation`, and `services_checked`, the `memories` array is not in the `required` list. This is because:

- Most Tier 1 observation sessions do not produce new memories. Requiring the field would force the LLM to emit `"memories": []` on every routine health check, which is wasted output tokens.
- Tier 2 and Tier 3 sessions are more likely to produce memories from investigation findings.
- The Go code treats absent `memories` the same as an empty array.

### Escalation replaces handoff files entirely

The `escalation` object in the structured output fully replaces the `handoff.json` file mechanism. There is no hybrid mode where some information comes from the file and some from structured output. The rationale:

- The handoff file's primary purpose is to communicate `escalation.needed`, `reason`, and `context` -- all of which are in the schema.
- The `services_checked` array provides the equivalent of the handoff file's `check_results` field.
- Eliminating the filesystem artifact removes race conditions (file not cleaned up, file from previous cycle, file written but process crashed before exiting).
- The supervisor reads the escalation decision from the same JSON response it already parses for cost and duration, simplifying the code path.

## References

- [ADR-0030: Structured Output via JSON Schema](../../adrs/ADR-0030-structured-output-json-schema.md)
- [ADR-0014: Real-Time Dashboard and Events](../../adrs/ADR-0014-realtime-dashboard-and-events.md)
- [ADR-0015: Persistent Agent Memory](../../adrs/ADR-0015-persistent-agent-memory.md)
- [ADR-0016: Session-Based Escalation with Structured Handoff](../../adrs/ADR-0016-session-based-escalation-handoff.md)
- [ADR-0011: Session Page CLI Output and Response](../../adrs/ADR-0011-session-page-cli-output-and-response.md)
- [SPEC-0031: Structured Output via JSON Schema](./spec.md)
- [SPEC-0013: Real-Time Dashboard and Events](../realtime-dashboard-events/spec.md)
- [SPEC-0015: Persistent Agent Memory](../persistent-agent-memory/spec.md)
- [SPEC-0016: Session-Based Escalation with Structured Handoff](../session-based-escalation/spec.md)
