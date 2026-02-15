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
