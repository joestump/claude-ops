---
sidebar_position: 11
sidebar_label: Session CLI Output
---

# SPEC-0011: Session Page CLI Output and Formatted Response

## Overview

The session detail page in the Claude Ops dashboard MUST display both the full CLI activity trace (tool calls, tool results, assistant reasoning) and the final markdown response in a readable, separated layout. Currently, sessions only capture the plain-text final response from `claude -p`, providing no visibility into what tools Claude invoked, what commands it ran, or how it arrived at its conclusions.

This specification defines how the Claude CLI's `--output-format stream-json` flag is used to produce structured NDJSON events, how those events are parsed and formatted in the Go session manager, how session metadata (cost, turns, duration) and the final response are stored in SQLite, and how the session page renders both the response and activity log.

See ADR-0011 for the decision rationale and alternatives considered.

## Definitions

- **NDJSON (Newline-Delimited JSON)**: A text format where each line is a self-contained JSON object. The Claude CLI emits NDJSON when invoked with `--output-format stream-json`.
- **Stream event**: A single NDJSON line emitted by the CLI. Events have a `type` field: `system`, `assistant`, `user`, or `result`.
- **Activity log**: The chronological sequence of formatted event lines showing tool calls, results, and assistant reasoning -- what the operator would see running `claude` in a terminal.
- **Response**: The final markdown text from the `result` event's `result` field, containing the health report or remediation summary.
- **SSE (Server-Sent Events)**: HTTP protocol for real-time server-to-browser streaming, used to push activity log lines to the browser during running sessions.

## Requirements

### Requirement: CLI Invocation with stream-json

The session manager MUST invoke the Claude CLI with `--output-format stream-json` to receive structured NDJSON events on stdout. The CLI MUST NOT be invoked with plain `-p` text output mode.

#### Scenario: CLI args include stream-json flag

- **WHEN** the session manager builds the Claude CLI command args
- **THEN** the args MUST include `--output-format stream-json`

#### Scenario: CLI stdout produces NDJSON

- **WHEN** the Claude CLI process is running
- **THEN** each line on stdout MUST be a valid JSON object with a `type` field

### Requirement: Raw NDJSON Log Preservation

The session manager MUST write every raw NDJSON line from the CLI stdout to the session log file, unmodified. The log file serves as the forensic record of the complete event stream.

#### Scenario: Log file contains raw JSON

- **WHEN** a session completes and the log file is read
- **THEN** each line in the log file MUST be a valid JSON object matching the original CLI output

#### Scenario: Log file is written regardless of parse errors

- **WHEN** the CLI emits a line that cannot be parsed as JSON
- **THEN** the raw line MUST still be written to the log file

### Requirement: Event Parsing and Formatting

The session manager MUST parse each NDJSON line and format it into a human-readable string for display. The formatter MUST handle the following event types:

- `system` with `subtype: "init"` → `--- session started ---`
- `assistant` with `tool_use` content blocks → `[tool] {name}: {truncated input}`
- `assistant` with `text` content blocks → the text content
- `user` with `tool_result` content blocks → `[result] {truncated content}`
- `result` → `--- session complete (turns={n}, cost=${x}, duration={ms}ms) ---`

Unknown event types MUST be silently ignored (return empty string). Malformed JSON MUST be passed through as raw text.

#### Scenario: Tool use event formatting

- **WHEN** an `assistant` event contains a `tool_use` block with name `Bash` and input `{"command": "docker ps"}`
- **THEN** the formatted output MUST be `[tool] Bash: {"command": "docker ps"}`

#### Scenario: Tool result truncation

- **WHEN** a `user` event contains a `tool_result` with content exceeding 300 characters
- **THEN** the formatted output MUST truncate to 300 characters followed by `...`

#### Scenario: Unknown event type

- **WHEN** an NDJSON line has `type: "stream_event"` or any unrecognized type
- **THEN** the formatter MUST return an empty string (event is suppressed from display)

#### Scenario: Malformed JSON fallback

- **WHEN** a line from stdout is not valid JSON
- **THEN** the formatter MUST return the raw line text unchanged

### Requirement: Result Event Metadata Extraction

The session manager MUST extract the following fields from the `result` event and store them in the database:

- `result` (string): The final markdown response text
- `total_cost_usd` (float64): Total API cost in USD
- `num_turns` (int): Number of agent turns
- `duration_ms` (int64): Total API duration in milliseconds

#### Scenario: Result stored after successful session

- **WHEN** a session completes and the CLI emits a `result` event with `is_error: false`
- **THEN** the session manager MUST call `UpdateSessionResult` with the extracted response, cost, turns, and duration

#### Scenario: No result event emitted

- **WHEN** a session is killed (context cancelled) before emitting a `result` event
- **THEN** the session record MUST have NULL values for response, cost_usd, num_turns, and duration_ms

### Requirement: Database Schema for Session Metadata

The sessions table MUST include the following additional columns added via migration:

- `response TEXT` (nullable): The final markdown response
- `cost_usd REAL` (nullable): Total API cost in USD
- `num_turns INTEGER` (nullable): Number of agent turns
- `duration_ms INTEGER` (nullable): Total API duration in milliseconds

#### Scenario: Migration adds columns

- **WHEN** the database is opened and migration 002 runs
- **THEN** the sessions table MUST have `response`, `cost_usd`, `num_turns`, and `duration_ms` columns
- **THEN** existing session rows MUST have NULL values for the new columns

#### Scenario: Migration is idempotent

- **WHEN** migration 002 has already been applied
- **THEN** opening the database MUST NOT attempt to re-apply the migration

### Requirement: SSE Streaming of Formatted Events

For running sessions, the session manager MUST publish each formatted event line to the SSE hub. The web handler MUST stream these lines to the browser via Server-Sent Events.

#### Scenario: Browser receives tool call in real time

- **WHEN** a session is running and Claude invokes the `Bash` tool
- **THEN** the SSE stream MUST deliver a `data: [tool] Bash: {...}\n\n` event to subscribed browsers

#### Scenario: Session completion closes SSE stream

- **WHEN** a running session completes
- **THEN** the hub MUST be closed for that session ID
- **THEN** subscribed browsers MUST receive an `event: done` SSE event

### Requirement: Session Page Layout

The session detail page MUST display content in the following order from top to bottom:

1. **Back link**: Navigation to the sessions list
2. **Session header**: Session ID and status badge
3. **Metadata card**: Tier, model, started, duration, cost (if available), turns (if available), API time (if available)
4. **Response section**: The final markdown response rendered as formatted HTML (not raw text), wrapped in a card with prose styling. This section MUST only appear when a response exists.
5. **Activity log section**: The full CLI activity trace in a terminal-styled block. For running sessions, this MUST use SSE streaming. For completed sessions, this MUST show the formatted log file contents.
6. **Log file path**: The path to the raw NDJSON log file

#### Scenario: Completed session with response

- **WHEN** an operator views a completed session that has a response stored in the DB
- **THEN** the response MUST appear as rendered markdown in a card above the activity log
- **THEN** the activity log MUST show formatted tool calls and results from the log file

#### Scenario: Running session without response yet

- **WHEN** an operator views a running session
- **THEN** the response section MUST NOT be shown (no response yet)
- **THEN** the activity log MUST stream events via SSE in real time

#### Scenario: Completed session without response

- **WHEN** a session was killed before completion (no result event)
- **THEN** the response section MUST NOT be shown
- **THEN** the activity log MUST show whatever events were captured before termination

### Requirement: Markdown Response Rendering

The response section MUST render the markdown response as formatted HTML using a server-side markdown renderer (goldmark). The rendered HTML MUST support:

- Headings (h1-h4)
- Paragraphs
- Ordered and unordered lists
- Code blocks (inline and fenced)
- Tables
- Bold and italic text
- Links
- Blockquotes

#### Scenario: Markdown with code blocks renders correctly

- **WHEN** the response contains a fenced code block
- **THEN** the code block MUST render with terminal-style dark background and monospace font

#### Scenario: Markdown with tables renders correctly

- **WHEN** the response contains a markdown table
- **THEN** the table MUST render with borders and header styling

### Requirement: Log File Formatting on Read Path

When displaying a completed session, the web handler MUST read the NDJSON log file and format each line using the same `FormatStreamEvent` function used during live streaming. Lines that format to empty strings MUST be omitted.

#### Scenario: Completed session shows formatted activity

- **WHEN** an operator views a completed session
- **THEN** the activity log MUST NOT show raw JSON
- **THEN** the activity log MUST show formatted `[tool]`, `[result]`, and text lines

#### Scenario: Large log file handling

- **WHEN** a log file contains many lines
- **THEN** the handler MUST read and format the file line-by-line using a scanner (not read entire file into memory at once)

---

# Design: Session Page CLI Output and Formatted Response

## Context

Claude Ops runs Claude Code CLI sessions as subprocesses (ADR-0010, SPEC-0010) and serves a Go/HTMX dashboard (ADR-0008, SPEC-0008). The session detail page previously showed only the raw final response from `claude -p` -- unformatted markdown with no visibility into tool calls, commands, or reasoning.

Operators need to see what Claude did (the activity trace) and what Claude concluded (the response). These are fundamentally different kinds of output requiring different rendering: the activity trace looks like a terminal log, while the response is a structured document that benefits from markdown formatting.

The Claude CLI's `--output-format stream-json` flag provides the structured data source that makes this separation possible.

## Goals / Non-Goals

### Goals
- Show the full CLI activity trace (tool calls, results, reasoning) in the session page
- Render the final response as formatted markdown, visually separated from the activity log
- Stream activity in real time for running sessions via SSE
- Preserve raw NDJSON in log files for forensic analysis
- Store session metadata (cost, turns, duration) from the result event in the DB

### Non-Goals
- Syntax highlighting for code blocks in the activity log (plain monospace is sufficient)
- Collapsible/expandable tool results (show truncated inline)
- Filtering or searching within the activity log
- Token-level streaming via `--include-partial-messages` (complete events only)
- Client-side markdown rendering (server-side goldmark is used)

## Decisions

### Use `--output-format stream-json` Instead of Text Output

**Choice**: Invoke the Claude CLI with `--output-format stream-json` to get NDJSON events.

**Rationale**: Structured JSON events allow precise formatting, reliable response extraction, and metadata capture. Plain text output from `-p` mixes the response with any verbose output and provides no structured metadata.

**Alternatives considered**:
- `--verbose` text output: Unstructured, can't extract response separately, no metadata.
- Claude Agent SDK wrapper: Over-engineered, contradicts ADR-0010, introduces TypeScript dependency.

### Dual-Write: Raw JSON to Log, Formatted Text to Hub

**Choice**: Write raw NDJSON to the log file and formatted human-readable text to the SSE hub and stdout.

**Rationale**: Raw JSON preserves the complete event stream for forensic analysis and cost tracking. Formatted text is what operators need in the browser and terminal. Trying to serve both needs with one format compromises both.

**Alternatives considered**:
- Raw JSON everywhere, format on read: Adds latency to page loads and SSE delivery. The formatting is cheap enough to do inline.
- Formatted text everywhere, discard JSON: Loses structured metadata (cost, tokens, cache stats) that's valuable for analytics.

### Response at Top, Activity Log Below

**Choice**: Show the rendered markdown response card above the terminal-style activity log.

**Rationale**: The response is the primary deliverable -- the health report or remediation summary. Operators read the response first, then drill into the activity log if they need to verify or debug. This matches how people read reports (summary first, supporting evidence below).

**Alternatives considered**:
- Activity log at top, response at bottom: Buries the most important content below a potentially very long terminal output.
- Tabs (Response | Activity): Hides one view, forces clicking. Both views are useful simultaneously.
- Side-by-side: Doesn't work well on narrow viewports; both panels become too narrow.

### Server-Side Markdown Rendering with Goldmark

**Choice**: Render markdown to HTML on the server using the goldmark library, served as `template.HTML`.

**Rationale**: Server-side rendering means no JavaScript dependency for markdown parsing. Goldmark is the standard Go markdown library -- CommonMark compliant, extensible, well-maintained. The rendered HTML is wrapped in a `.prose` CSS class for styling.

**Alternatives considered**:
- Client-side marked.js: Adds a JS dependency and FOUC (Flash of Unstyled Content). The dashboard's philosophy is server-rendered HTML.
- Raw markdown in `<pre>`: Unreadable for operators. Defeats the purpose of separating response from activity log.

### SQLite Migration for New Columns

**Choice**: Add `response`, `cost_usd`, `num_turns`, `duration_ms` columns via migration 002 with ALTER TABLE.

**Rationale**: The existing migration system (versioned Go functions in transactions) handles this cleanly. ALTER TABLE ADD COLUMN is the simplest approach for nullable columns -- existing rows get NULL values, no data migration needed.

**Alternatives considered**:
- Separate results table: Over-normalized for a 1:1 relationship. Adds a JOIN to every session query.
- Store in the `context` JSON blob: Loses queryability. Can't ORDER BY cost_usd or filter by num_turns.

## Architecture

The data flows from the Claude CLI through three parallel paths: raw JSON to the log file, formatted text to the SSE hub (and browser), and result metadata to the database.

```
CLI: claude -p --output-format stream-json
    |
    v
Go Session Manager:
    bufio.Scanner (1MB buffer, line-by-line)
    |
    +-- raw line --> Log File (raw NDJSON)
    +-- raw line --> FormatStreamEvent()
    |                   |
    |                   +-- formatted text --> SSE Hub --> Browser SSE
    |                   +-- formatted text --> Container Stdout
    +-- raw line --> Result Event Capture (response, cost, turns, duration)
                        |
                        +-- UpdateSessionResult() --> SQLite sessions table
```

### Stream Event Types and Format Rules

| Event Type | Format Output |
|---|---|
| `system` with `subtype: "init"` | `--- session started ---` |
| `assistant` with `text` content | plain text content |
| `assistant` with `tool_use` content | `[tool] Name: truncated input` |
| `user` with `tool_result` content | `[result] truncated content` |
| `result` | `--- session complete (turns=N, cost=$X, duration=Yms) ---` |
| Unknown type | (empty string, suppressed) |
| Malformed JSON | raw line text |

### Database Schema Change

Migration 002 adds the following nullable columns to the `sessions` table:

- `response TEXT` -- The final markdown response (NEW)
- `cost_usd REAL` -- Total API cost in USD (NEW)
- `num_turns INTEGER` -- Number of agent turns (NEW)
- `duration_ms INTEGER` -- Total API duration in milliseconds (NEW)

## Files Affected

| File | Change |
|------|--------|
| `internal/session/manager.go` | Add `--output-format stream-json` to CLI args. Replace raw line fanout with NDJSON parser. Add `FormatStreamEvent()`, event types, truncation helpers. Capture result metadata. |
| `internal/db/db.go` | Add migration 002 (ALTER TABLE). Add `Response`, `CostUSD`, `NumTurns`, `DurationMs` to `Session` struct. Add `UpdateSessionResult()`. Update all session scan calls. |
| `internal/web/viewmodel.go` | Add `Response`, `CostUSD`, `NumTurns`, `DurationMs` to `SessionView`. Update `ToSessionView()`. |
| `internal/web/handlers.go` | Update `handleSession()` to format NDJSON log files using `FormatStreamEvent()` instead of raw display. |
| `internal/web/server.go` | Add `renderMarkdown` (goldmark), `fmtCost`, `fmtMs` template functions. |
| `internal/web/templates/session.html` | Add response card with `renderMarkdown`. Add cost/turns/duration to metadata. Rename "Output" to "Activity Log". |
| `internal/web/static/style.css` | Add `.prose` styles for rendered markdown (headings, lists, code blocks, tables). |
| `go.mod` | Add `github.com/yuin/goldmark` dependency. |

## Risks / Trade-offs

- **CLI output format stability**: The `--output-format stream-json` format is part of the Claude Code CLI contract and documented, but could change in major versions. Mitigation: the parser handles unknown event types gracefully (silent skip) and malformed JSON (passthrough).
- **Large tool results**: Some tool results (e.g., reading a large file) can be very large. Mitigation: truncate to 300 chars for display, raw JSON in log file preserves full content.
- **Race condition on result capture**: The scanner goroutine writes `resultResponse` while the main goroutine reads it after `cmd.Wait()`. This is safe because `cmd.Wait()` blocks until the pipe is closed (scanner goroutine finishes), establishing happens-before ordering.
- **Goldmark dependency**: Adds a new dependency. Mitigation: goldmark is the standard Go markdown library, actively maintained, no transitive dependencies.

## Migration Plan

1. Migration 002 adds nullable columns -- no data loss, backward compatible
2. Existing sessions get NULL for new columns, which templates handle with conditional rendering
3. New sessions populated automatically from result events
4. Log files transition from plain text to NDJSON -- old log files (if any) will show as raw text in the activity log since `FormatStreamEvent` passes through unparseable lines

## Open Questions

- Should the activity log support pagination or virtual scrolling for very long sessions? (Currently loads full formatted log.)
- Should we add `--include-partial-messages` for token-by-token streaming in the activity log? (Currently shows complete events only, which means tool calls appear all at once.)
