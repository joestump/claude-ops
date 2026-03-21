---
status: proposed
date: 2026-03-21
decision-makers: Joe Stump
updates: ADR-0014, ADR-0015, ADR-0016
---

# ADR-0030: Structured Output via JSON Schema for Reliable Data Extraction

## Context and Problem Statement

Claude Ops extracts structured data from LLM responses using text markers parsed via regex:

- **Events**: `[EVENT:level[:service]] message` markers (ADR-0014, SPEC-0013) -- parsed during stream-json processing to populate the events table.
- **Memories**: `[MEMORY:category[:service]] observation` markers (ADR-0015, SPEC-0015) -- parsed to persist operational knowledge in the memories table.
- **Escalation decisions**: Tier 1 writes a JSON handoff file that Tier 2/3 reads (ADR-0016, SPEC-0016) -- a separate mechanism but still relies on the LLM correctly formatting JSON.

This text-marker approach has known fragility:

1. **Omission under complexity.** The LLM can forget to emit markers, especially under complex reasoning or long tool-use chains. A session that runs 20+ tool calls may complete without emitting any `[EVENT:...]` markers, even though it discovered notable conditions.
2. **Malformed markers.** Markers can be malformed -- wrong level names, missing service tags, unclosed brackets. `[EVENT:warnig:jellyfin]` (typo in "warning") silently drops the event because the regex requires exact level matches.
3. **False positives.** Markers embedded in code blocks, tool results, or quoted text can be parsed as real events. The parser only matches markers in assistant text blocks, but the LLM sometimes reproduces the marker format when explaining what it will emit.
4. **Placement errors.** The regex parser only matches markers in assistant text blocks. If the LLM emits an event inside a tool input or reasoning block, it is missed entirely.
5. **No schema validation.** There is no structured validation of marker content. A typo in the level field, a missing message, or an excessively long observation text all pass through without validation.

Claude Code's `-p` (programmatic) mode now supports `--json-schema` for structured output. When provided, the LLM's final response is constrained to match a JSON Schema definition. The response is returned in the `structured_output` field of the JSON output. This provides:

- **Schema-enforced typing**: level must be one of "info", "warning", "critical" -- not "warnig" or "CRITICAL"
- **Required fields**: every event must have a message; every escalation must have a "needed" boolean
- **Array fields**: multiple events and memories in one response, cleanly separated
- **No regex parsing**: standard JSON deserialization replaces fragile regex matching
- **API-level validation**: errors are caught at generation time, not in post-processing

## Decision Drivers

* **Event emission reliability directly affects operator visibility.** Missed events create blind spots in the dashboard. The operator does not know what they do not see.
* **Memory persistence reliability affects cross-session knowledge continuity.** A memory lost to a malformed marker means the agent re-learns the same lesson in a future session, wasting tokens and time.
* **Escalation handoff reliability affects tiered remediation correctness.** If the handoff file is malformed or the escalation decision is ambiguous, the supervisor may fail to escalate or escalate incorrectly.
* **JSON Schema validation catches errors at generation time, not parsing time.** The LLM is constrained to produce valid output rather than the parser hoping the output is valid.
* **The Go web server already parses JSON from stream-json output.** Adding `structured_output` parsing is incremental -- the session manager already deserializes JSON from the `result` event.
* **Maintains compatibility with `--output-format stream-json` for real-time activity display.** The structured output is extracted from the final `result` message in the stream, preserving the real-time activity log (ADR-0011).
* **Zero new dependencies.** `--json-schema` is a CLI flag that takes a file path. The schema is a static JSON file shipped in the container image.

## Considered Options

1. **JSON Schema structured output** -- Use `--json-schema` to define a response schema with typed fields for events, memories, escalation decisions, and findings. The session manager extracts `structured_output` from the final result message.
2. **Improved regex parsing** -- Make text marker parsing more robust with better regex patterns, fallback matching, typo tolerance, and post-parse validation.
3. **Post-processing LLM call** -- After each session, run a second Haiku call to extract structured data from the raw response text.
4. **No change** -- Accept current marker reliability and address edge cases individually.

## Decision Outcome

Chosen option: **"JSON Schema structured output"**, because it eliminates the entire class of regex parsing fragility by constraining the LLM's output to a validated schema at generation time. Define a standard response schema that all tiers use. The schema includes typed arrays for events and memories, a structured escalation decision object, a services-checked array, and a summary field. The Go session manager passes `--json-schema <path>` alongside existing flags and reads `structured_output` from the final JSON response.

### Response Schema

The schema file is stored at `schemas/agent-response.json` in the project root and copied to `/app/schemas/agent-response.json` in the Docker image:

```json
{
  "type": "object",
  "properties": {
    "summary": {
      "type": "string",
      "description": "Brief summary of findings and actions taken"
    },
    "events": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "level": { "type": "string", "enum": ["info", "warning", "critical"] },
          "service": { "type": "string" },
          "message": { "type": "string" }
        },
        "required": ["level", "message"]
      }
    },
    "memories": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "key": { "type": "string" },
          "value": { "type": "string" }
        },
        "required": ["key", "value"]
      }
    },
    "escalation": {
      "type": "object",
      "properties": {
        "needed": { "type": "boolean" },
        "reason": { "type": "string" },
        "context": { "type": "string" },
        "failed_checks": {
          "type": "array",
          "items": { "type": "string" }
        }
      },
      "required": ["needed"]
    },
    "services_checked": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": { "type": "string" },
          "status": { "type": "string", "enum": ["healthy", "degraded", "down", "unreachable"] },
          "detail": { "type": "string" }
        },
        "required": ["name", "status"]
      }
    }
  },
  "required": ["summary", "events", "escalation", "services_checked"]
}
```

### Interaction with stream-json

The `--json-schema` flag works with `--output-format stream-json`. The structured output appears in the final `result` message's `structured_output` field. This preserves the real-time activity display from ADR-0011: tool calls, results, and assistant text continue to stream to the browser via SSE, while the structured data extraction happens once when the final `result` event arrives.

The session manager continues to use `--output-format stream-json` and processes events in real time. When it receives the `result` event, it parses `structured_output` in addition to the existing metadata (cost, duration, turns). Events, memories, and escalation decisions are extracted from this structured output rather than from regex-parsed text markers.

### Escalation Decision Replaces Handoff Files

The `escalation` object in the structured output replaces the handoff file mechanism from ADR-0016 for the escalation *decision*. When `escalation.needed` is `true`, the session manager reads `escalation.reason`, `escalation.context`, and `escalation.failed_checks` to construct the context for the next tier's `--append-system-prompt`. The supervisor no longer needs to check for a filesystem-based `handoff.json` -- the escalation decision is embedded in the response.

The `services_checked` array provides the equivalent of the handoff file's `check_results` field, giving the supervisor structured visibility into what was checked and what failed.

### Consequences

**Positive:**

* Type-safe event extraction. Level values are constrained to the enum; service and message are typed strings. No regex parsing, no typo-induced silent drops.
* Schema-validated at generation time. The LLM cannot produce a response that does not match the schema. Malformed markers are impossible because the output is not free-form text.
* Structured escalation decisions replace handoff files. The supervisor reads `escalation.needed` from the JSON response instead of checking for a filesystem artifact. This simplifies the escalation flow and eliminates a class of bugs (file not written, file written but malformed, file from a previous cycle not cleaned up).
* Memories are reliably captured. Every memory the LLM intends to persist is in the `memories` array. No marker syntax to forget or malform.
* Zero new dependencies. The `--json-schema` flag is built into the Claude Code CLI. The schema is a static JSON file.
* Incremental change to the session manager. The `result` event is already parsed for cost and duration. Adding `structured_output` parsing is a small addition to the same code path.
* Real-time activity display is preserved. Stream-json continues to work for the activity log. Structured output is extracted from the final event, not from the stream.

**Negative:**

* Schema constrains response format. The LLM must fill all required fields even when they are trivially empty (e.g., an empty `events` array when nothing notable was found). This adds a small amount of output overhead per session.
* Requires Go code changes to parse `structured_output`. The session manager's result-parsing code must be extended to deserialize the structured output and route events, memories, and escalation decisions to their respective handlers.
* Tier prompt updates required. The existing `[EVENT:...]` and `[MEMORY:...]` marker format documentation in tier prompts must be replaced with instructions describing the response schema. All three tier prompts need updating.
* Backward compatibility during rollout. While prompts are being updated, the system must handle sessions that still use text markers alongside sessions that use structured output. A transition period is needed.
* Handoff file deprecation. ADR-0016's handoff file mechanism must be deprecated and eventually removed. During transition, the supervisor should check both `structured_output.escalation` and `handoff.json`.

## Pros and Cons of the Options

### JSON Schema Structured Output

Use `--json-schema` to define a response schema with typed fields for events, memories, escalation decisions, and service check results. The session manager extracts `structured_output` from the final result message.

* Good, because it eliminates regex parsing entirely -- structured JSON deserialization replaces fragile pattern matching.
* Good, because schema validation at generation time catches errors that text markers would silently propagate (typos, missing fields, wrong types).
* Good, because it unifies three separate data extraction mechanisms (event markers, memory markers, handoff files) into a single structured response.
* Good, because the `--json-schema` flag is a CLI-level feature that requires no new infrastructure, services, or dependencies.
* Good, because the schema is declarative and version-controllable -- changes to the response format are visible as diffs to a JSON file.
* Good, because it preserves real-time streaming by extracting structured data from the final `result` event rather than blocking the stream.
* Bad, because the LLM must produce valid JSON matching the schema, which consumes output tokens for structure (braces, keys, empty arrays) even when there is nothing to report.
* Bad, because all tier prompts must be updated to describe the schema instead of the marker format, requiring coordinated changes across multiple files.
* Bad, because backward compatibility during rollout requires dual-path parsing (markers + structured output) temporarily.
* Bad, because the schema becomes a contract between the CLI, the prompts, and the session manager -- schema changes require updating all three.
* Bad, because `memories` in the schema uses a flat key-value structure that is less expressive than the current category/service/observation model. The memory insertion code must map `key` to the appropriate category and service fields.

### Improved Regex Parsing

Make text marker parsing more robust with better regex patterns, typo-tolerant matching, validation of extracted fields, and fallback patterns for common malformations.

* Good, because it requires no changes to the CLI invocation -- the `--json-schema` flag is not needed.
* Good, because it preserves the existing marker-based workflow that the prompts already describe.
* Good, because it can be implemented incrementally without a coordinated prompt/parser/supervisor change.
* Good, because the LLM's free-form text output is not constrained, avoiding the overhead of producing JSON structure.
* Bad, because regex parsing is fundamentally fragile -- adding more patterns and fallbacks increases complexity without eliminating the root cause.
* Bad, because typo tolerance introduces ambiguity: is `[EVENT:warn:jellyfin]` a typo for "warning" or an intentional new level?
* Bad, because false positives from markers in code blocks, tool results, and quoted text are difficult to eliminate without context-aware parsing.
* Bad, because it does not address the omission problem -- the LLM can still forget to emit markers entirely.
* Bad, because three separate parsing paths (events, memories, handoff files) must each be independently hardened.
* Bad, because validation added after parsing is post-hoc -- the malformed data was already generated; the parser is just discarding it.

### Post-Processing LLM Call

After each session, run a second Haiku call with the raw response text and ask it to extract structured events, memories, and escalation decisions into JSON.

* Good, because it cleanly separates the agent's operational reasoning from the data extraction task.
* Good, because the extraction prompt can be tuned independently without affecting the operational prompts.
* Good, because it can handle free-form text that does not follow any marker format -- the extraction LLM interprets intent, not syntax.
* Bad, because it doubles the LLM cost for every session -- a Haiku extraction call for every Tier 1, 2, and 3 session.
* Bad, because it adds latency between session completion and data availability. Events and escalation decisions are delayed by the extraction call, which can take 5-30 seconds.
* Bad, because the extraction LLM can itself make errors (hallucinate events, miss memories, misinterpret escalation intent), introducing a second source of unreliability.
* Bad, because escalation decisions must be extracted before the supervisor can spawn the next tier, making the extraction call blocking on the critical path.
* Bad, because it creates a dependency on a second LLM invocation that could fail (rate limits, network errors, API downtime), adding a failure mode to the extraction pipeline.

### No Change

Accept current marker reliability and address edge cases individually as they arise.

* Good, because it requires zero implementation effort.
* Good, because the current system works for the common case -- most sessions emit markers correctly.
* Good, because it avoids the complexity of schema management, backward compatibility, and coordinated prompt updates.
* Bad, because known reliability gaps persist -- missed events, malformed markers, and false positives continue to create blind spots.
* Bad, because the escalation handoff file mechanism has a separate set of fragility issues (file not written, malformed JSON, stale files) that are not addressed.
* Bad, because as Claude Ops monitors more services and runs more complex investigations, the probability of marker omission increases with session length and tool-call count.
* Bad, because each marker format (events, memories, handoff) must be independently maintained and debugged, multiplying the surface area for parsing bugs.
* Bad, because the operator's trust in the dashboard depends on event completeness -- silent event drops undermine the system's value proposition.

## More Information

* **Updates ADR-0014** (Real-Time Dashboard and Events): The `[EVENT:...]` text marker parsing mechanism is replaced by the `events` array in the structured output. Event insertion into the events table continues, but the source changes from regex-parsed markers to JSON-deserialized objects. The events table schema is unchanged.
* **Updates ADR-0015** (Persistent Agent Memory): The `[MEMORY:...]` text marker parsing mechanism is replaced by the `memories` array in the structured output. The memory insertion and lifecycle management (confidence scoring, staleness pruning) continue as specified in ADR-0015, but the ingestion path changes from marker parsing to JSON deserialization.
* **Updates ADR-0016** (Session-Based Escalation): The filesystem-based `handoff.json` mechanism is replaced by the `escalation` object in the structured output for the escalation decision. The supervisor reads `escalation.needed` from the parsed response instead of checking for a handoff file. The handoff context (reason, investigation findings, failed checks) is embedded in the response JSON.
* **Interacts with ADR-0011** (Session Page CLI Output): The `--output-format stream-json` approach is preserved. Structured output is extracted from the final `result` event, not from the stream. The real-time activity display is unaffected.
* **Schema versioning**: The schema file should include a `$schema` reference and a version comment. When the schema changes, the session manager must handle responses from both the old and new schema versions during rolling updates.
* **Prompt migration**: All three tier prompt files (`tier1-observe.md`, `tier2-investigate.md`, `tier3-remediate.md`) must be updated to describe the response schema. The existing marker format documentation should be removed and replaced with a "Response Format" section that shows the expected JSON structure and field descriptions.
