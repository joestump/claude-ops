# SPEC-0012: Manual Ad-Hoc Session Runs from the Dashboard

## Overview

The Claude Ops dashboard MUST allow operators to trigger an immediate, ad-hoc session with a custom prompt text, without waiting for the next scheduled interval. Currently, `Manager.Run()` only executes sessions on a fixed timer. Operators responding to incidents (e.g., "Jellyfin is down") have no way to kick off an on-demand run from the browser.

This specification defines the channel-based trigger mechanism in the session manager, the `TriggerAdHoc()` public API, the `POST /sessions/trigger` HTTP endpoint, the database schema changes to record trigger type and prompt text, the HTMX form on the dashboard, and the display of trigger metadata in session list and detail views.

See ADR-0013 for the decision rationale and alternatives considered.

## Definitions

- **Scheduled session**: A session started automatically by the `Run()` loop's timer, using the configured prompt file.
- **Ad-hoc session**: A session started manually by an operator via the dashboard, using a custom prompt text.
- **Trigger type**: A string field (`"scheduled"` or `"manual"`) stored on each session record to distinguish how it was initiated.
- **Trigger channel**: A buffered Go channel used to deliver ad-hoc trigger requests from the web handler to the session manager's `Run()` loop.
- **Busy rejection**: An HTTP 409 Conflict response returned when an ad-hoc trigger is submitted while a session is already running.

## Requirements

### Requirement: Channel-Based Trigger in Session Manager

The session manager MUST expose a buffered Go channel for receiving ad-hoc trigger requests. The `Run()` loop MUST add a `select` case that reads from this channel, immediately waking from the sleep timer to start a session with the provided prompt text. The channel MUST have a buffer size of 1.

#### Scenario: Ad-hoc trigger wakes the sleep timer

- **WHEN** the `Run()` loop is sleeping between scheduled runs
- **AND** a trigger request is sent to the trigger channel
- **THEN** the `Run()` loop MUST wake immediately and start a session

#### Scenario: Only one pending trigger queued

- **WHEN** the trigger channel already contains a pending request (buffer full)
- **AND** another trigger request is attempted
- **THEN** `TriggerAdHoc()` MUST return an error indicating the manager is busy

#### Scenario: Trigger channel does not block the Run loop

- **WHEN** the trigger channel is empty
- **THEN** the `Run()` loop MUST continue to sleep on the timer as before (no busy-wait or polling)

### Requirement: TriggerAdHoc Public API

The session manager MUST expose a `TriggerAdHoc(prompt string) error` method. This method sends the prompt text over the trigger channel. It MUST be safe to call from any goroutine (the web handler goroutine).

#### Scenario: Successful trigger when idle

- **WHEN** `TriggerAdHoc("Jellyfin is down")` is called
- **AND** no session is currently running and the channel is empty
- **THEN** the method MUST return `nil`
- **AND** the prompt MUST be delivered to the `Run()` loop via the channel

#### Scenario: Trigger rejected when busy

- **WHEN** `TriggerAdHoc("Check DNS")` is called
- **AND** a session is currently running or a trigger is already pending
- **THEN** the method MUST return a non-nil error

#### Scenario: Empty prompt is rejected

- **WHEN** `TriggerAdHoc("")` is called with an empty string
- **THEN** the method MUST return a non-nil error indicating that a prompt is required

### Requirement: Ad-Hoc Session Uses runOnce with Custom Prompt

When a trigger is received from the channel, the `Run()` loop MUST execute `runOnce()` using the custom prompt text instead of reading from the configured prompt file. All other session lifecycle behavior (DB record, log file, SSE streaming, result capture, finalization) MUST be identical to scheduled sessions.

#### Scenario: Ad-hoc session reuses runOnce

- **WHEN** the `Run()` loop receives a trigger with prompt "Jellyfin is down"
- **THEN** the session MUST be started via `runOnce()` with the custom prompt passed to the Claude CLI via `-p`
- **AND** a session record MUST be inserted into the database
- **AND** the log file MUST be created in `$CLAUDEOPS_RESULTS_DIR`
- **AND** SSE events MUST be published to the hub

#### Scenario: System prompt is preserved for ad-hoc sessions

- **WHEN** an ad-hoc session is started
- **THEN** the `--append-system-prompt` flag MUST still include the environment context string
- **AND** only the `-p` prompt content MUST differ from a scheduled run

### Requirement: POST /sessions/trigger Endpoint

The web server MUST register a `POST /sessions/trigger` route. This endpoint accepts a form-encoded `prompt` field and calls `TriggerAdHoc()` on the session manager.

#### Scenario: Successful trigger returns redirect

- **WHEN** an operator submits `POST /sessions/trigger` with `prompt=Jellyfin is down`
- **AND** the session manager accepts the trigger
- **THEN** the endpoint MUST respond with HTTP 200
- **AND** the response body MUST contain an HTMX-compatible snippet indicating the session was triggered

#### Scenario: Busy trigger returns 409

- **WHEN** an operator submits `POST /sessions/trigger`
- **AND** the session manager rejects the trigger (busy)
- **THEN** the endpoint MUST respond with HTTP 409 Conflict
- **AND** the response body MUST contain a human-readable message: "A session is already running or queued"

#### Scenario: Missing prompt returns 400

- **WHEN** an operator submits `POST /sessions/trigger` with an empty or missing `prompt` field
- **THEN** the endpoint MUST respond with HTTP 400 Bad Request

#### Scenario: Web server has access to session manager

- **WHEN** the web server is constructed
- **THEN** it MUST receive a reference to an interface or concrete type that exposes `TriggerAdHoc(prompt string) error`

### Requirement: Session Database Record with Trigger Metadata

The sessions table MUST include two new columns added via migration:

- `trigger TEXT NOT NULL DEFAULT 'scheduled'`: Either `"scheduled"` or `"manual"`
- `prompt_text TEXT`: The custom prompt text for ad-hoc sessions (NULL for scheduled sessions)

#### Scenario: Migration adds trigger columns

- **WHEN** the database is opened and the new migration runs
- **THEN** the sessions table MUST have `trigger` and `prompt_text` columns
- **AND** existing session rows MUST have `trigger = 'scheduled'` and `prompt_text = NULL`

#### Scenario: Scheduled session record

- **WHEN** a scheduled session is created via `InsertSession()`
- **THEN** `trigger` MUST be `"scheduled"`
- **AND** `prompt_text` MUST be NULL

#### Scenario: Ad-hoc session record

- **WHEN** an ad-hoc session is created via `InsertSession()`
- **THEN** `trigger` MUST be `"manual"`
- **AND** `prompt_text` MUST contain the operator-provided prompt text

### Requirement: Busy Rejection When Session Already Running

The system MUST prevent concurrent sessions. If a session is already running when an ad-hoc trigger is submitted, the trigger MUST be rejected.

#### Scenario: Trigger during scheduled session

- **WHEN** a scheduled session is currently running
- **AND** an operator submits `POST /sessions/trigger`
- **THEN** the endpoint MUST return HTTP 409 Conflict
- **AND** the running session MUST NOT be interrupted or affected

#### Scenario: Trigger during another ad-hoc session

- **WHEN** an ad-hoc session is currently running
- **AND** an operator submits another `POST /sessions/trigger`
- **THEN** the endpoint MUST return HTTP 409 Conflict

### Requirement: HTMX Form on Dashboard

The dashboard index page MUST include a form for submitting ad-hoc session prompts. The form MUST use HTMX to submit asynchronously without a full page reload.

#### Scenario: Form is visible on index page

- **WHEN** an operator navigates to the dashboard index page
- **THEN** a form MUST be visible with a textarea for the prompt and a submit button

#### Scenario: Form submits via HTMX

- **WHEN** an operator fills in the prompt textarea and clicks submit
- **THEN** the form MUST send a `POST /sessions/trigger` request via HTMX (`hx-post`)
- **AND** the submit button MUST be disabled during the request to prevent double-submission

#### Scenario: Successful submission shows confirmation

- **WHEN** the trigger is accepted (HTTP 200)
- **THEN** the form area MUST display a confirmation message indicating the session was triggered

#### Scenario: Busy submission shows error

- **WHEN** the trigger is rejected (HTTP 409)
- **THEN** the form area MUST display an error message indicating a session is already running

### Requirement: Session List Shows Trigger Type

The session list page MUST display the trigger type for each session, distinguishing scheduled from manual sessions.

#### Scenario: Scheduled session in list

- **WHEN** an operator views the session list
- **AND** a session has `trigger = "scheduled"`
- **THEN** the session row MUST display a "scheduled" label or badge

#### Scenario: Manual session in list

- **WHEN** an operator views the session list
- **AND** a session has `trigger = "manual"`
- **THEN** the session row MUST display a "manual" label or badge that is visually distinct from "scheduled"

### Requirement: Session Detail Shows Trigger Metadata

The session detail page MUST display the trigger type and, for ad-hoc sessions, the custom prompt text.

#### Scenario: Ad-hoc session detail shows prompt

- **WHEN** an operator views the detail page for a manual session
- **THEN** the metadata card MUST show "Trigger: manual"
- **AND** the custom prompt text MUST be displayed below the metadata card

#### Scenario: Scheduled session detail

- **WHEN** an operator views the detail page for a scheduled session
- **THEN** the metadata card MUST show "Trigger: scheduled"
- **AND** no custom prompt section MUST be displayed

### Requirement: SSE Streaming Works Identically for Ad-Hoc Sessions

Ad-hoc sessions MUST stream CLI events to the browser via SSE using the same mechanism as scheduled sessions. No SSE-related code changes are required beyond ensuring `runOnce()` is called with the correct prompt.

#### Scenario: SSE stream for ad-hoc session

- **WHEN** an ad-hoc session is running
- **AND** an operator navigates to the session detail page
- **THEN** the activity log MUST stream events via SSE in real time
- **AND** the stream behavior MUST be identical to a scheduled session

#### Scenario: SSE done event for ad-hoc session

- **WHEN** an ad-hoc session completes
- **THEN** the SSE hub MUST close the session's channel
- **AND** subscribed browsers MUST receive an `event: done` SSE event
