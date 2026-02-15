# SPEC-0016: Session-Based Escalation with Structured Handoff

## Overview

Replace in-process `Task` tool escalation with a file-based handoff mechanism where each tier runs as a separate Claude CLI process with its own session record. The Go supervisor controls escalation decisions, reads structured handoff files written by exiting tiers, and spawns the next tier as a new CLI process with handoff context injected via `--append-system-prompt`. See ADR-0016.

## Requirements

### Requirement: Handoff File Format

The handoff file MUST be a JSON file written to `$CLAUDEOPS_STATE_DIR/handoff.json`. The file SHALL conform to a versioned schema so that future changes can be detected and handled. The supervisor MUST reject handoff files with an unrecognized schema version.

#### Scenario: Tier 1 writes handoff for Tier 2

- **WHEN** Tier 1 detects one or more unhealthy services during observation
- **THEN** it SHALL write a JSON file to `$CLAUDEOPS_STATE_DIR/handoff.json` containing:
  - `schema_version` set to `1`
  - `recommended_tier` set to `2`
  - `services_affected` as a non-empty array of service name strings
  - `check_results` as a non-empty array of check result objects
  - `cooldown_state` as an object with a snapshot of relevant cooldown data
- **THEN** it SHALL exit with code 0

#### Scenario: Tier 2 writes handoff for Tier 3

- **WHEN** Tier 2 cannot resolve the issue with safe remediation
- **THEN** it SHALL write a JSON file to `$CLAUDEOPS_STATE_DIR/handoff.json` containing:
  - `schema_version` set to `1`
  - `recommended_tier` set to `3`
  - `services_affected` as a non-empty array of service name strings
  - `check_results` from the original Tier 1 handoff
  - `investigation_findings` as a non-empty string describing root cause analysis
  - `remediation_attempted` as a non-empty string describing what was tried and why it failed
  - `cooldown_state` as an object with updated cooldown data
- **THEN** it SHALL exit with code 0

#### Scenario: Handoff file not written when all services healthy

- **WHEN** Tier 1 completes observation and all services are healthy
- **THEN** it MUST NOT write a handoff file
- **THEN** it SHALL exit with code 0

#### Scenario: Handoff file not written when Tier 2 resolves the issue

- **WHEN** Tier 2 successfully remediates all affected services
- **THEN** it MUST NOT write a handoff file
- **THEN** it SHALL exit with code 0

#### Scenario: Check result object structure

- **WHEN** a check result is included in the handoff file
- **THEN** it MUST contain the fields: `service` (string), `check_type` (string, one of `http`, `dns`, `container`, `database`, `service`), `status` (string, one of `healthy`, `degraded`, `down`), and `error` (string)
- **THEN** it MAY contain the OPTIONAL field `response_time_ms` (integer)

#### Scenario: Invalid handoff file rejected

- **WHEN** the supervisor reads a handoff file that is missing REQUIRED fields or has an unrecognized `schema_version`
- **THEN** it MUST log a warning, delete the file, and NOT spawn a next-tier process
- **THEN** it MUST record the parse failure as an event with level `critical`

### Requirement: Handoff File Lifecycle

The handoff file MUST have a well-defined lifecycle: written by one tier, read and deleted by the supervisor. The file MUST NOT persist across monitoring cycles.

#### Scenario: Supervisor reads and deletes handoff after Tier 1

- **WHEN** the Tier 1 CLI process exits with code 0
- **THEN** the supervisor SHALL check for the existence of `$CLAUDEOPS_STATE_DIR/handoff.json`
- **THEN** if the file exists, the supervisor SHALL read its contents, validate the schema, and delete the file before spawning the next tier

#### Scenario: Supervisor reads and deletes handoff after Tier 2

- **WHEN** the Tier 2 CLI process exits with code 0
- **THEN** the supervisor SHALL check for the existence of `$CLAUDEOPS_STATE_DIR/handoff.json`
- **THEN** if the file exists and `recommended_tier` is `3`, the supervisor SHALL read, validate, and delete the file before spawning Tier 3

#### Scenario: Stale handoff file from previous cycle

- **WHEN** the supervisor starts a new monitoring cycle and a handoff file already exists from a previous cycle (e.g., supervisor crashed between read and delete)
- **THEN** the Tier 1 process SHALL overwrite the stale file if it needs to escalate, or the supervisor MAY delete any pre-existing handoff file before starting Tier 1

#### Scenario: Handoff file deleted on supervisor crash recovery

- **WHEN** the supervisor restarts after an unexpected termination
- **THEN** it SHOULD delete any existing handoff file at `$CLAUDEOPS_STATE_DIR/handoff.json` before beginning its first monitoring cycle to avoid processing stale escalation data

#### Scenario: CLI process exits with non-zero code

- **WHEN** a tier's CLI process exits with a non-zero exit code
- **THEN** the supervisor MUST NOT read or act on any handoff file that may exist
- **THEN** the supervisor SHALL record the session as `failed` and proceed to the next scheduled cycle

### Requirement: Supervisor Escalation Logic

The Go supervisor MUST control all escalation decisions. The LLM tiers MUST NOT use the `Task` tool for escalation. The supervisor SHALL enforce policy checks before spawning each tier.

#### Scenario: Supervisor spawns Tier 2 from handoff

- **WHEN** the supervisor reads a valid handoff file with `recommended_tier` equal to `2` after Tier 1 exits
- **THEN** it SHALL create a new session record in the database with `tier=2`, the configured Tier 2 model, and `parent_session_id` set to the Tier 1 session ID
- **THEN** it SHALL spawn a new `claude` CLI process with `--model` set to the Tier 2 model, `-p` set to the Tier 2 prompt file content, and `--append-system-prompt` containing the serialized handoff context

#### Scenario: Supervisor spawns Tier 3 from handoff

- **WHEN** the supervisor reads a valid handoff file with `recommended_tier` equal to `3` after Tier 2 exits
- **THEN** it SHALL create a new session record in the database with `tier=3`, the configured Tier 3 model, and `parent_session_id` set to the Tier 2 session ID
- **THEN** it SHALL spawn a new `claude` CLI process with `--model` set to the Tier 3 model, `-p` set to the Tier 3 prompt file content, and `--append-system-prompt` containing the serialized handoff context

#### Scenario: Dry-run mode prevents escalation

- **WHEN** `CLAUDEOPS_DRY_RUN` is `true` and a handoff file requests escalation to Tier 2 or Tier 3
- **THEN** the supervisor MUST NOT spawn a next-tier process
- **THEN** the supervisor SHALL log that escalation was suppressed due to dry-run mode
- **THEN** the supervisor SHALL delete the handoff file

#### Scenario: Maximum tier limit enforced

- **WHEN** a handoff file requests escalation to a tier higher than the configured maximum tier (e.g., `recommended_tier=3` but max tier is `2`)
- **THEN** the supervisor MUST NOT spawn the requested tier
- **THEN** the supervisor SHALL log that escalation was blocked by the tier limit
- **THEN** the supervisor SHALL send a notification via Apprise indicating the issue requires human attention

#### Scenario: No handoff file after session exit

- **WHEN** a tier's CLI process exits with code 0 and no handoff file exists
- **THEN** the supervisor SHALL finalize the session record and return to the normal scheduling loop
- **THEN** no further escalation SHALL occur for this monitoring cycle

#### Scenario: Escalation chain terminates at Tier 3

- **WHEN** Tier 3 exits (regardless of whether it wrote a handoff file)
- **THEN** the supervisor MUST NOT spawn any further tiers
- **THEN** if Tier 3 wrote a handoff file, the supervisor SHALL log a warning, delete the file, and treat it as an unresolvable issue requiring human attention

### Requirement: Database Schema for Escalation Chains

The `sessions` table MUST include a `parent_session_id` column to link escalated sessions to their parent. This enables the dashboard to query and display escalation chains.

#### Scenario: Parent session ID column added

- **WHEN** the database migration for this feature runs
- **THEN** a new column `parent_session_id INTEGER REFERENCES sessions(id)` SHALL be added to the `sessions` table
- **THEN** the column MUST allow NULL values (Tier 1 sessions have no parent)

#### Scenario: Tier 1 session has no parent

- **WHEN** the supervisor creates a session record for a Tier 1 run
- **THEN** the `parent_session_id` column SHALL be NULL

#### Scenario: Tier 2 session links to Tier 1 parent

- **WHEN** the supervisor creates a session record for a Tier 2 escalation
- **THEN** the `parent_session_id` column SHALL be set to the ID of the Tier 1 session that produced the handoff file

#### Scenario: Tier 3 session links to Tier 2 parent

- **WHEN** the supervisor creates a session record for a Tier 3 escalation
- **THEN** the `parent_session_id` column SHALL be set to the ID of the Tier 2 session that produced the handoff file

#### Scenario: Full escalation chain queryable

- **WHEN** a monitoring cycle involves all three tiers (Session #18 Tier 1, Session #19 Tier 2, Session #20 Tier 3)
- **THEN** Session #19 `parent_session_id` SHALL equal Session #18 `id`
- **THEN** Session #20 `parent_session_id` SHALL equal Session #19 `id`
- **THEN** querying the chain from any session in the chain SHALL return all linked sessions

#### Scenario: Index on parent_session_id

- **WHEN** the migration runs
- **THEN** an index SHOULD be created on `parent_session_id` to support efficient chain lookups

### Requirement: Tier Prompt Changes

The Tier 1 and Tier 2 prompt files MUST be updated to instruct the agent to write handoff files instead of using the `Task` tool for escalation. The `Task` tool MUST be removed from the allowed tools list for Tier 1 and Tier 2.

#### Scenario: Tier 1 prompt writes handoff instead of spawning Task

- **WHEN** the Tier 1 agent detects unhealthy services
- **THEN** the prompt SHALL instruct it to write a handoff JSON file to `$CLAUDEOPS_STATE_DIR/handoff.json` with the required fields and exit
- **THEN** the prompt MUST NOT contain instructions to use the `Task` tool for escalation

#### Scenario: Tier 2 prompt writes handoff instead of spawning Task

- **WHEN** the Tier 2 agent determines it cannot resolve the issue
- **THEN** the prompt SHALL instruct it to write a handoff JSON file to `$CLAUDEOPS_STATE_DIR/handoff.json` with investigation findings and exit
- **THEN** the prompt MUST NOT contain instructions to use the `Task` tool for escalation

#### Scenario: Tier 2 receives handoff context via system prompt

- **WHEN** the supervisor spawns a Tier 2 CLI process
- **THEN** the handoff context (check results, affected services, cooldown state) SHALL be injected via the `--append-system-prompt` flag
- **THEN** the Tier 2 agent SHALL be able to parse and act on the handoff context without re-running health checks

#### Scenario: Tier 3 receives handoff context via system prompt

- **WHEN** the supervisor spawns a Tier 3 CLI process
- **THEN** the handoff context (check results, investigation findings, remediation attempted, cooldown state) SHALL be injected via the `--append-system-prompt` flag

#### Scenario: Task tool removed from allowed tools

- **WHEN** the supervisor builds the `--allowedTools` flag for Tier 1 or Tier 2
- **THEN** the `Task` tool MUST NOT be included in the allowed tools list

### Requirement: Dashboard Escalation Chain Display

The dashboard MUST display escalation chains so the operator can see the relationship between sessions in a monitoring cycle that involved multiple tiers.

#### Scenario: Session detail shows parent link

- **WHEN** the operator views a Tier 2 or Tier 3 session at `/sessions/{id}`
- **THEN** the session detail page SHALL display a link to the parent session labeled "Escalated from Session #{parent_id} (Tier {parent_tier})"

#### Scenario: Session detail shows child link

- **WHEN** the operator views a Tier 1 or Tier 2 session that triggered escalation
- **THEN** the session detail page SHALL display a link to the child session labeled "Escalated to Session #{child_id} (Tier {child_tier})"

#### Scenario: Sessions list shows escalation indicator

- **WHEN** the operator views the sessions list at `/sessions`
- **THEN** sessions that are part of an escalation chain SHALL display a visual indicator (e.g., chain icon or indentation) showing their relationship

#### Scenario: Escalation chain cost rollup

- **WHEN** the operator views a session that is the root of an escalation chain
- **THEN** the session detail page SHOULD display both the individual session cost and the total chain cost (sum of all sessions in the chain)

### Requirement: Per-Tier Cost Attribution

Each tier MUST have its own session record with accurate cost, duration, and turn count metrics. The dashboard MUST display per-tier costs both individually and as part of the chain.

#### Scenario: Tier 1 cost recorded independently

- **WHEN** a Tier 1 session completes
- **THEN** the session record SHALL contain the `cost_usd`, `num_turns`, and `duration_ms` values from the Tier 1 CLI process result event only

#### Scenario: Tier 2 cost recorded independently

- **WHEN** a Tier 2 session completes
- **THEN** the session record SHALL contain the `cost_usd`, `num_turns`, and `duration_ms` values from the Tier 2 CLI process result event only, not including Tier 1 costs

#### Scenario: Chain cost breakdown visible

- **WHEN** the operator views an escalation chain in the dashboard
- **THEN** each tier's cost, duration, and turn count SHALL be displayed individually
- **THEN** the total chain cost (sum of all tiers) SHALL be displayed as a summary

### Requirement: Handoff Context Serialization

The handoff context injected into `--append-system-prompt` MUST preserve all information needed for the receiving tier to operate without re-running prior checks or investigations.

#### Scenario: Tier 2 receives complete failure context

- **WHEN** the supervisor injects handoff context into the Tier 2 system prompt
- **THEN** the injected text MUST include the full `check_results` array, `services_affected` list, and `cooldown_state` from the handoff file
- **THEN** the text MUST be clearly labeled (e.g., wrapped in a `## Escalation Context` section) so the agent can parse it

#### Scenario: Tier 3 receives complete investigation context

- **WHEN** the supervisor injects handoff context into the Tier 3 system prompt
- **THEN** the injected text MUST include all fields from the Tier 2 handoff: `check_results`, `services_affected`, `investigation_findings`, `remediation_attempted`, and `cooldown_state`

#### Scenario: Handoff context size limit

- **WHEN** the serialized handoff context exceeds 50,000 characters
- **THEN** the supervisor SHOULD truncate the `check_results` array to include only results with `status` not equal to `healthy`, preserving the most critical information
- **THEN** the supervisor MUST log a warning that handoff context was truncated
