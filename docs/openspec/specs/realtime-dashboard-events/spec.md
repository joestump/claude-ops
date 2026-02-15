# SPEC-0013: Real-Time Dashboard and Events System

## Overview

Replace the static dashboard and Services section with a real-time, polling-based UI and a first-class Events system where the LLM surfaces notable findings as operator-facing messages. See ADR-0014.

## Requirements

### Requirement: Real-Time Sessions List

The sessions page SHALL auto-refresh via HTMX polling so the operator sees session state changes without manual page reloads. The sessions table MUST display cost and token usage for each session.

#### Scenario: Session starts during page view

- **WHEN** the operator has the sessions page open and a new session starts (scheduled or ad-hoc)
- **THEN** the new session row appears in the table within 5 seconds without a page reload

#### Scenario: Session completes during page view

- **WHEN** a running session completes or fails while the sessions page is open
- **THEN** the session's status badge, duration, cost, and token count update within 5 seconds

#### Scenario: Sessions table shows cost and tokens

- **WHEN** the operator views the sessions list
- **THEN** each session row displays the cost (formatted as `$X.XXXX`) and token count alongside existing columns

### Requirement: Real-Time Overview

The overview page SHALL auto-refresh its sections so the operator always sees current state.

#### Scenario: Overview reflects latest session

- **WHEN** a session starts or completes while the overview page is open
- **THEN** the "Last Session" card updates within 10 seconds

#### Scenario: Overview shows recent events

- **WHEN** the overview page loads
- **THEN** it displays the 10 most recent events instead of a services grid

### Requirement: Remove Services UI

The dashboard MUST NOT include a Services page or Services navigation link. The `health_checks` table MAY remain for internal agent use but SHALL NOT be exposed in the dashboard UI.

#### Scenario: Services routes removed

- **WHEN** a user navigates to `/services` or `/services/{name}`
- **THEN** the server returns a 404 response

#### Scenario: Navigation has no Services link

- **WHEN** the dashboard sidebar renders
- **THEN** there is no "Services" entry in the navigation

### Requirement: Events Table

The system SHALL store events in a new `events` SQLite table. Each event MUST have an id, session_id, level, service (optional), message, and created_at timestamp.

#### Scenario: Event inserted during session

- **WHEN** the session manager parses an event marker from the LLM's assistant output
- **THEN** a row is inserted into the `events` table with the session ID, parsed level, optional service name, message text, and current UTC timestamp

#### Scenario: Event levels

- **WHEN** an event is created
- **THEN** its level MUST be one of: `info`, `warning`, `critical`

### Requirement: Event Marker Parsing

The session manager SHALL parse event markers from assistant text blocks in the stream-json output. The marker format MUST be `[EVENT:<level>] <message>` or `[EVENT:<level>:<service>] <message>`. Markers SHALL only be parsed from assistant text — never from tool results, user messages, or system events.

#### Scenario: Simple event marker

- **WHEN** the LLM outputs `[EVENT:warning] Jellyfin container restarted 3 times in 4 hours`
- **THEN** an event is created with level=`warning`, service=nil, message=`Jellyfin container restarted 3 times in 4 hours`

#### Scenario: Event marker with service tag

- **WHEN** the LLM outputs `[EVENT:critical:postgres] Connection refused on port 5432`
- **THEN** an event is created with level=`critical`, service=`postgres`, message=`Connection refused on port 5432`

#### Scenario: Marker in tool result ignored

- **WHEN** a tool result block contains text matching the `[EVENT:...]` pattern
- **THEN** it SHALL NOT be parsed as an event (only assistant text blocks produce events)

### Requirement: Events Page

The dashboard SHALL include an Events page accessible from the sidebar navigation. It MUST display events in reverse chronological order with severity-based styling.

#### Scenario: Events page renders

- **WHEN** the operator navigates to `/events`
- **THEN** a paginated list of events is displayed, newest first, with level badge, optional service tag, message, timestamp, and session link

#### Scenario: Events page auto-refreshes

- **WHEN** the operator has the events page open and new events are created
- **THEN** new events appear within 5 seconds via HTMX polling

### Requirement: Events on Overview Page

The overview page SHALL display the most recent events in place of the former services grid.

#### Scenario: Overview events feed

- **WHEN** the overview page loads
- **THEN** the top section shows the 10 most recent events with level badges, messages, and timestamps
- **THEN** a "View all" link navigates to the full events page

### Requirement: Prompt Integration

The tier-1 observe prompt MUST include an "Event Reporting" section that instructs the LLM to emit events using the `[EVENT:level] message` format for notable findings.

#### Scenario: LLM emits events per prompt instructions

- **WHEN** the LLM discovers a notable finding during a health check session
- **THEN** it outputs an event marker in its assistant text following the format specified in the prompt

### Requirement: Sessions Display Cost and Tokens

The sessions list and session detail pages MUST display cost and token/turn count for completed sessions.

#### Scenario: Sessions list shows cost column

- **WHEN** the operator views `/sessions`
- **THEN** each row includes a "Cost" column showing `$X.XXXX` (or `--` if null) and a "Turns" column

#### Scenario: Session detail shows cost

- **WHEN** the operator views a specific session at `/sessions/{id}`
- **THEN** the metadata card includes cost and turns (already present, confirmed working)
