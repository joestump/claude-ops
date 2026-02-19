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

### Requirement: Summarization Model

The system SHOULD use a fast, inexpensive model (Haiku) for summary generation to minimize cost and latency. The model identifier MUST be configurable and SHOULD default to `haiku`.

#### Scenario: Default summarization model

- **WHEN** no override is configured
- **THEN** the system uses the `haiku` model for summarization

#### Scenario: Custom summarization model

- **WHEN** the `CLAUDEOPS_SUMMARY_MODEL` environment variable is set
- **THEN** the system uses the specified model for summarization
