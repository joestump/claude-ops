# SPEC-0021: TL;DR Overview Page with LLM-Generated Summaries

## Overview

Rename the dashboard's "Overview" page to "TL;DR" (with a 🤔 emoji in the sidebar) and replace the full markdown response with a concise LLM-generated summary. The summary is generated once when a session completes and persisted in the database so the overview page loads instantly without re-invoking the LLM.

See ADR-0001 (Go/HTMX Dashboard) for the underlying dashboard architecture.

## Requirements

### Requirement: Session Summary Generation

The system MUST generate a short plain-text summary of each session's response when the session completes. The summary MUST be produced by calling the Anthropic Messages API with the session's full response as input. The summary SHOULD be 2–4 sentences capturing the key findings and actions taken. The system MUST NOT block the session completion flow if summarization fails — a missing summary is acceptable.

#### Scenario: Successful summarization after session completion

- **WHEN** a session finishes with a non-empty response
- **THEN** the system calls the Anthropic API to generate a summary and stores it in the session's `summary` column

#### Scenario: Session completes with empty response

- **WHEN** a session finishes with an empty or nil response
- **THEN** the system MUST skip summarization and leave the summary column NULL

#### Scenario: Summarization API call fails

- **WHEN** the Anthropic API call fails (network error, rate limit, invalid response)
- **THEN** the system MUST log the error and leave the summary column NULL without affecting the session's final status

### Requirement: Summary Persistence

The system MUST store the generated summary in a new `summary` TEXT column on the `sessions` table. This column MUST be added via a new database migration. The column MUST be nullable (NULL when no summary has been generated).

#### Scenario: Database schema after migration

- **WHEN** migration 007 runs
- **THEN** the `sessions` table has a `summary` TEXT column that accepts NULL values

#### Scenario: Summary stored alongside session result

- **WHEN** a summary is successfully generated
- **THEN** the summary is written to the `summary` column of the corresponding session row

### Requirement: TL;DR Page Rendering

The dashboard's index page (route `GET /`) MUST display the session summary instead of the full markdown response. The page heading MUST read "TL;DR" instead of "Overview". The sidebar navigation label MUST read "🤔 TL;DR" with the thinking face emoji.

#### Scenario: Overview page with a summary available

- **WHEN** the latest session has a non-empty summary
- **THEN** the TL;DR page displays the summary text in place of the full response

#### Scenario: Overview page with no summary (fallback)

- **WHEN** the latest session has a NULL summary but a non-empty response
- **THEN** the TL;DR page MUST fall back to displaying the full rendered markdown response

#### Scenario: Page title and navigation

- **WHEN** the TL;DR page is rendered
- **THEN** the sidebar shows "🤔 TL;DR" as the nav label and the page heading reads "TL;DR"

### Requirement: Dashboard Stats HUD

The TL;DR page MUST display a two-row stats HUD of 8 tiles showing aggregate system metrics. The HUD MUST use the DaisyUI `stats` component. The tiles MUST be populated from a single `GetDashboardStats()` database call.

#### Scenario: HUD tile content — row 1

- **WHEN** the TL;DR page is rendered
- **THEN** the first row MUST contain 4 tiles: Total Runs (count of root sessions), Escalations (count of child sessions), Remediations (count of tier-3 sessions), and Success % (completed root sessions / total root sessions as a percentage)

#### Scenario: HUD tile content — row 2

- **WHEN** the TL;DR page is rendered
- **THEN** the second row MUST contain 4 tiles: Total Cost (sum of `cost_usd` across all sessions formatted as USD), Critical (count of `critical`-level events in the last 24 hours), Memories (count of active memories), and Avg Duration (average `duration_ms` across all sessions with non-null duration)

#### Scenario: HUD with no data

- **WHEN** no sessions, events, or memories exist
- **THEN** all tiles MUST show zero values without error

### Requirement: Last Run Status Bar

The TL;DR page MUST display a single-line status bar immediately below the HUD showing key metadata for the most recently started session.

#### Scenario: Last run bar content

- **WHEN** at least one session exists
- **THEN** the status bar MUST display the session ID (linked to the session detail page), status badge, tier level, cost (if available), and elapsed duration
- **THEN** the bar MUST auto-refresh via HTMX polling

#### Scenario: Last run bar when no sessions exist

- **WHEN** no sessions have been recorded
- **THEN** the status bar MUST show a placeholder message indicating no runs have occurred

### Requirement: Multiple Session Summaries

The TL;DR page MUST display up to 5 session cards, each showing a summary or response for sessions where that content is available.

#### Scenario: Session summary cards

- **WHEN** the TL;DR page is rendered
- **THEN** the page MUST show cards for up to 5 most recent sessions that have a non-null `summary` or `response`
- **THEN** each card MUST display the session ID (linked), status badge, started-at timestamp, and the summary text (or response if no summary)

#### Scenario: No sessions with summaries

- **WHEN** no sessions have summaries or responses
- **THEN** the section MUST show a placeholder message

### Requirement: Unified Activity Feed

The TL;DR page MUST display a chronologically merged activity feed combining events, memory upserts, and session lifecycle milestones. The feed MUST auto-refresh via HTMX polling.

#### Scenario: Activity feed items

- **WHEN** the activity feed is rendered
- **THEN** it MUST contain merged items from: database events (as-is), session start/complete/escalated/failed milestones, and memory records with an icon distinguishing the item type
- **THEN** items MUST be sorted by timestamp descending
- **THEN** the feed MUST be capped at 40 items

#### Scenario: Activity feed auto-refresh

- **WHEN** the TL;DR page is open in a browser
- **THEN** the activity feed section MUST poll `GET /` every 10 seconds via HTMX and update in-place without a full page reload

#### Scenario: Activity item visual differentiation

- **WHEN** the activity feed renders items of different types
- **THEN** events MUST display a level badge (info/warning/critical) with service tag if present
- **THEN** session milestones MUST display a status badge and session ID link
- **THEN** memory items MUST display a 🧠 icon with the memory category and service

### Requirement: Summarization Model

The system SHOULD use a fast, inexpensive model (Haiku) for summary generation to minimize cost and latency. The model identifier MUST be configurable and SHOULD default to `haiku`.

#### Scenario: Default summarization model

- **WHEN** no override is configured
- **THEN** the system uses the `haiku` model for summarization

#### Scenario: Custom summarization model

- **WHEN** the `CLAUDEOPS_SUMMARY_MODEL` environment variable is set
- **THEN** the system uses the specified model for summarization
