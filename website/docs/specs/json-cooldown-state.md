---
sidebar_position: 7
sidebar_label: JSON Cooldown State
---

# SPEC-0007: JSON File Cooldown State

## Overview

Claude Ops enforces cooldown rules to prevent remediation retry loops that could cause cascading failures or mask problems requiring human intervention. This specification defines the persistent cooldown state mechanism: a JSON file stored on a Docker volume mount that tracks per-service remediation actions, enforces rate limits on restarts and redeployments, and resets counters when services recover.

The cooldown state file is the single source of truth for whether the agent is permitted to take a given remediation action. Every tier of the agent (observe, investigate, remediate) reads the state before acting, and every remediation action updates the state after completion.

## Definitions

- **Cooldown**: A time-bounded rate limit on remediation actions for a specific service, preventing the agent from retrying failed remediations indefinitely.
- **Restart**: A container-level restart (`docker restart <name>`) or equivalent operation that restarts a service without full redeployment.
- **Redeployment**: A full service redeployment via Ansible playbook, Helm upgrade, or container recreation (`docker compose down && up`).
- **Cooldown window**: The time period over which remediation action counts are tracked. 4 hours for restarts, 24 hours for redeployments.
- **Cooldown state file**: The JSON file at `$CLAUDEOPS_STATE_DIR/cooldown.json` that persists remediation tracking data.
- **Consecutive healthy checks**: Sequential health check runs in which a service passes all checks without any failures between them.
- **Agent loop iteration**: A single execution cycle of the entrypoint loop, from health check through remediation (if needed) to sleep.

## Requirements

### REQ-1: State File Location

The cooldown state file MUST be stored at the path `$CLAUDEOPS_STATE_DIR/cooldown.json`, where `$CLAUDEOPS_STATE_DIR` defaults to `/state` if not set.

#### Scenario: Default state directory
Given the environment variable CLAUDEOPS_STATE_DIR is not set
When the agent resolves the cooldown state file path
Then the path MUST be `/state/cooldown.json`

#### Scenario: Custom state directory
Given the environment variable CLAUDEOPS_STATE_DIR is set to `/custom/path`
When the agent resolves the cooldown state file path
Then the path MUST be `/custom/path/cooldown.json`

### REQ-2: State File Initialization

The entrypoint MUST create the cooldown state file with an empty initial structure if the file does not already exist. The initial structure MUST be `{"services":{},"last_run":null,"last_daily_digest":null}`.

#### Scenario: First run with no existing state file
Given the cooldown state file does not exist at the configured path
When the entrypoint script starts
Then the entrypoint MUST create the file with contents `{"services":{},"last_run":null,"last_daily_digest":null}`

#### Scenario: Subsequent run with existing state file
Given the cooldown state file already exists at the configured path
When the entrypoint script starts
Then the entrypoint MUST NOT overwrite or modify the existing file

#### Scenario: State file exists but is empty or corrupted
Given the cooldown state file exists but contains invalid JSON
When the agent attempts to read the file
Then the agent SHOULD re-initialize the file with the default structure
And the agent SHOULD log the re-initialization event

### REQ-3: Persistent Volume Mount

The state directory MUST be backed by a Docker volume or bind mount that persists across container restarts and rebuilds. The container filesystem MUST NOT be used for state storage.

#### Scenario: Container restart preserves state
Given a cooldown state file with recorded remediation actions
When the agent container is stopped and restarted
Then the cooldown state file MUST contain the same data as before the restart

#### Scenario: Container rebuild preserves state
Given a cooldown state file with recorded remediation actions
When the agent container image is rebuilt and a new container is started
Then the cooldown state file MUST contain the same data as before the rebuild

### REQ-4: Restart Cooldown Limit

The agent MUST NOT restart a service more than 2 times within any 4-hour sliding window. The agent MUST check the cooldown state file before every restart action.

#### Scenario: First restart within cooldown window
Given a service "nginx" has 0 restarts recorded in the last 4 hours
When the agent considers restarting "nginx"
Then the cooldown state MUST permit the restart

#### Scenario: Second restart within cooldown window
Given a service "nginx" has 1 restart recorded in the last 4 hours
When the agent considers restarting "nginx"
Then the cooldown state MUST permit the restart

#### Scenario: Third restart blocked by cooldown
Given a service "nginx" has 2 restarts recorded in the last 4 hours
When the agent considers restarting "nginx"
Then the cooldown state MUST prevent the restart
And the agent MUST send a "needs human attention" notification (if Apprise is configured)
And the agent MUST NOT attempt the restart

#### Scenario: Restart permitted after window expires
Given a service "nginx" has 2 restarts recorded, but all are older than 4 hours
When the agent considers restarting "nginx"
Then the cooldown state MUST permit the restart because the actions are outside the window

### REQ-5: Redeployment Cooldown Limit

The agent MUST NOT perform a full redeployment of a service more than 1 time within any 24-hour sliding window. The agent MUST check the cooldown state file before every redeployment action.

#### Scenario: First redeployment within cooldown window
Given a service "postgres" has 0 redeployments recorded in the last 24 hours
When the agent considers redeploying "postgres"
Then the cooldown state MUST permit the redeployment

#### Scenario: Second redeployment blocked by cooldown
Given a service "postgres" has 1 redeployment recorded in the last 24 hours
When the agent considers redeploying "postgres"
Then the cooldown state MUST prevent the redeployment
And the agent MUST send a "needs human attention" notification (if Apprise is configured)
And the agent MUST NOT attempt the redeployment

#### Scenario: Redeployment permitted after window expires
Given a service "postgres" has 1 redeployment recorded, but it is older than 24 hours
When the agent considers redeploying "postgres"
Then the cooldown state MUST permit the redeployment

### REQ-6: Counter Reset on Recovery

The agent MUST reset a service's restart and redeployment counters when the service has been confirmed healthy for 2 consecutive health check runs. Counters MUST NOT be reset on a single healthy check.

#### Scenario: Service healthy for one consecutive check
Given a service "redis" has 2 restarts recorded and was healthy in the most recent check
When the agent evaluates the cooldown state
Then the counters MUST NOT be reset

#### Scenario: Service healthy for two consecutive checks
Given a service "redis" has 2 restarts recorded and was healthy in the 2 most recent consecutive checks
When the agent evaluates the cooldown state
Then the restart and redeployment counters for "redis" MUST be reset to zero
And the timestamps for previous actions MAY be cleared

#### Scenario: Healthy streak broken by failure
Given a service "redis" was healthy in the previous check
When the current check finds "redis" unhealthy
Then the consecutive healthy count MUST be reset to zero
And existing cooldown counters MUST NOT be modified

### REQ-7: State Update After Remediation

The agent MUST update the cooldown state file immediately after every remediation attempt, whether successful or not. The update MUST include the action type, timestamp, and outcome.

#### Scenario: Successful container restart
Given the agent restarts service "nginx" successfully
When the restart completes
Then the cooldown state MUST record the restart with the current UTC timestamp
And the restart count for "nginx" MUST be incremented

#### Scenario: Failed redeployment
Given the agent attempts to redeploy service "postgres" but the redeployment fails
When the failure is detected
Then the cooldown state MUST record the redeployment attempt with the current UTC timestamp
And the redeployment count for "postgres" MUST be incremented
And the failure SHOULD be noted in the state

### REQ-8: Last Run Tracking

The agent MUST update the `last_run` field in the cooldown state file at the end of every agent loop iteration with the current UTC timestamp in ISO 8601 format.

#### Scenario: Successful health check run
Given the agent completes a health check iteration at 2025-06-15T10:30:00Z
When the iteration ends
Then the `last_run` field MUST be updated to "2025-06-15T10:30:00Z"

#### Scenario: Run with escalation
Given the agent completes a health check that escalated to Tier 2
When the full iteration (including escalation) ends
Then the `last_run` field MUST be updated to the current UTC timestamp

### REQ-9: Daily Digest Tracking

The agent MUST track the timestamp of the last daily digest notification in the `last_daily_digest` field. The Tier 1 agent SHOULD send a daily digest when `last_daily_digest` is null or more than 24 hours ago.

#### Scenario: First run, no digest ever sent
Given the `last_daily_digest` field is null
When the Tier 1 agent evaluates whether to send a digest
Then a daily digest SHOULD be sent
And `last_daily_digest` MUST be updated to the current UTC timestamp

#### Scenario: Digest sent within 24 hours
Given the `last_daily_digest` field is "2025-06-15T08:00:00Z" and the current time is "2025-06-15T14:00:00Z"
When the Tier 1 agent evaluates whether to send a digest
Then a daily digest SHOULD NOT be sent

#### Scenario: Digest overdue
Given the `last_daily_digest` field is "2025-06-14T08:00:00Z" and the current time is "2025-06-15T10:00:00Z"
When the Tier 1 agent evaluates whether to send a digest
Then a daily digest SHOULD be sent
And `last_daily_digest` MUST be updated to the current UTC timestamp

### REQ-10: Agent Tooling Compatibility

The cooldown state file MUST be readable and writable using `jq` and standard POSIX file I/O commands (`cat`, `echo`, shell redirection). The state format MUST NOT require specialized client libraries, SDKs, or network protocols.

#### Scenario: Reading state with jq
Given a cooldown state file exists with service data
When the agent reads the restart count for service "nginx" using `jq '.services["nginx"].restarts | length' cooldown.json`
Then the command MUST return the correct count

#### Scenario: Writing state with jq
Given a cooldown state file exists
When the agent updates a field using `jq` piped to a temporary file and moved into place
Then the state file MUST contain the updated data

### REQ-11: Human Readability

The cooldown state file MUST be stored in formatted (pretty-printed) or at minimum valid JSON that operators can inspect directly using `cat`, `jq`, or a text editor without specialized tooling.

#### Scenario: Operator inspects cooldown state
Given an operator runs `cat /state/cooldown.json | jq .`
When the command executes
Then the output MUST display a human-readable JSON document showing all services, their counters, and timestamps

### REQ-12: Single-Writer Execution Model

The system MUST ensure that only one agent container writes to the cooldown state file at any time. Concurrent write access from multiple containers is NOT a supported configuration. The single-threaded agent loop provides implicit serialization of reads and writes.

#### Scenario: Single container accessing state
Given one agent container is running with the state volume mounted
When the agent reads and writes the cooldown state
Then no file locking mechanism is REQUIRED

#### Scenario: Multiple containers (unsupported)
Given two agent containers are configured to mount the same state volume
When both attempt to write the cooldown state
Then the behavior is undefined and data corruption MAY occur
And this configuration is explicitly unsupported

### REQ-13: Cooldown State Data Model

The cooldown state file MUST conform to the following top-level structure:

- `services`: An object mapping service names (strings) to service state objects
- `last_run`: A string containing an ISO 8601 UTC timestamp, or null
- `last_daily_digest`: A string containing an ISO 8601 UTC timestamp, or null

Each service state object MUST contain:
- `restarts`: An array of restart action records
- `redeployments`: An array of redeployment action records
- `consecutive_healthy`: An integer counting consecutive healthy checks

Each action record MUST contain:
- `timestamp`: An ISO 8601 UTC timestamp string
- `success`: A boolean indicating whether the action succeeded

Action records MAY contain additional fields such as `error` for failed actions.

#### Scenario: State file with multiple services
Given services "nginx", "postgres", and "redis" have been monitored
When the agent writes the state
Then the JSON MUST contain entries for all three services under the `services` key
And each entry MUST have `restarts`, `redeployments`, and `consecutive_healthy` fields

#### Scenario: Service with mixed action outcomes
Given service "nginx" has had 1 successful restart and 1 failed restart
When the agent writes the state
Then the `restarts` array for "nginx" MUST contain 2 records
And one record MUST have `success: true` and one MUST have `success: false`
And both records MUST have valid ISO 8601 timestamps

### REQ-14: Tier-Specific Access Patterns

The Tier 1 agent MUST read the cooldown state during health check evaluation (Step 4 of tier1-observe.md). The Tier 2 agent MUST read and write the cooldown state during remediation (Step 3 of tier2-investigate.md). The Tier 3 agent MUST read and write the cooldown state during full remediation (Step 3 of tier3-remediate.md).

#### Scenario: Tier 1 reads cooldown state
Given the Tier 1 agent is performing health checks
When the agent reaches Step 4 (Read Cooldown State)
Then the agent MUST read `$CLAUDEOPS_STATE_DIR/cooldown.json`
And MUST note any services in cooldown for the evaluation step

#### Scenario: Tier 2 checks and updates cooldown state
Given the Tier 2 agent is investigating a failing service
When the agent considers a restart action
Then the agent MUST read the cooldown state to check limits
And after performing the action MUST update the cooldown state with the result

#### Scenario: Tier 3 checks and updates cooldown state
Given the Tier 3 agent is performing full remediation
When the agent considers a redeployment action
Then the agent MUST read the cooldown state to check the 24-hour redeployment limit
And after performing the action MUST update the cooldown state with the result

## References

- [ADR-0007: Persist Cooldown State in a JSON File on Mounted Volume](../adrs/adr-0007)
- [ADR-0001: Tiered Model Escalation](../adrs/adr-0001)
- [ADR-0004: Apprise Notification Abstraction](../adrs/adr-0004)

---

# Design: JSON File Cooldown State

## Overview

The cooldown state system prevents Claude Ops from retrying failed remediations indefinitely by tracking per-service action counts within sliding time windows. The state is persisted as a single JSON file on a Docker volume mount, keeping the system aligned with the project's zero-dependency, container-stateless architecture.

This design covers the data model, file lifecycle, access patterns across all three agent tiers, and the write-safety guarantees achievable without file locking.

## Architecture

### Component Interactions

The cooldown state file sits at the intersection of three system components:

```
entrypoint.sh ---- creates file if missing ----> cooldown.json
                                                      ^
                                                      |
                                                  read/write
                                                      |
                                              +-------+-------+
                                              |               |
                                         Tier 1 Agent    Tier 2/3 Agents
                                         (read-only)     (read + write)
```

1. **entrypoint.sh** -- Initializes the state file before the first agent invocation. Checks for the file's existence and creates it with the default structure if absent. This runs once per container startup, before the agent loop begins.

2. **Tier 1 Agent (Haiku)** -- Reads the state file during Step 4 of `tier1-observe.md` to identify services in cooldown. Categorizes these services as `in_cooldown` in the evaluation step. Updates `last_run` and `last_daily_digest` timestamps. Does not perform remediations, so does not modify service counters.

3. **Tier 2 Agent (Sonnet)** -- Reads the state file in Step 3 of `tier2-investigate.md` before any remediation action. Checks restart counts against the 2-per-4-hour limit. Writes the state file after each remediation attempt (successful or failed) to record the action.

4. **Tier 3 Agent (Opus)** -- Reads the state file in Step 3 of `tier3-remediate.md` before full remediation actions. Checks redeployment counts against the 1-per-24-hour limit. Writes the state file after each remediation attempt.

### Volume Mount Architecture

```
Docker Host Filesystem
+-- /var/lib/docker/volumes/claudeops_state/_data/
|   +-- cooldown.json            <-- persistent across container lifecycle
|
Container Filesystem
+-- /state/                      <-- volume mount point ($CLAUDEOPS_STATE_DIR)
|   +-- cooldown.json            <-- same file, accessed inside the container
```

The state directory is configured as a named Docker volume in `docker-compose.yaml`. This ensures the data survives container stops, restarts, and image rebuilds. The volume is not part of the container image and is never included in `docker compose down -v` operations (which require explicit `--volumes` flag).

## Data Flow

### Initialization Flow

```
Container Start
    |
    v
entrypoint.sh
    |
    +-- Check: does $CLAUDEOPS_STATE_DIR/cooldown.json exist?
    |       |
    |       +-- Yes -> proceed (do not modify)
    |       |
    |       +-- No -> write default: {"services":{}, "last_run":null, "last_daily_digest":null}
    |
    v
Enter agent loop
```

### Health Check Read Flow (Tier 1)

```
Tier 1 Agent (Step 4)
    |
    +-- Read cooldown.json
    |
    +-- For each service in "services":
    |       |
    |       +-- Count restarts within last 4 hours
    |       +-- Count redeployments within last 24 hours
    |       |
    |       +-- If at restart limit (2) -> mark "in_cooldown"
    |       +-- If at redeployment limit (1) -> mark "in_cooldown"
    |
    +-- Update "last_run" to current UTC timestamp
    |
    +-- Check if daily digest is due:
    |       |
    |       +-- last_daily_digest is null -> send digest, update timestamp
    |       +-- last_daily_digest > 24h ago -> send digest, update timestamp
    |
    v
Pass cooldown context to evaluation (Step 5)
```

### Remediation Write Flow (Tier 2/3)

```
Tier 2/3 Agent (before action)
    |
    +-- Read cooldown.json
    |
    +-- Check limit for intended action:
    |       |
    |       +-- Restart: count restarts in last 4h < 2? -> proceed
    |       +-- Redeployment: count redeployments in last 24h < 1? -> proceed
    |       |
    |       +-- Limit exceeded -> skip action, notify "needs human attention"
    |
    v
Perform remediation action
    |
    v
After action (success or failure)
    |
    +-- Read current cooldown.json
    |
    +-- Append action record to appropriate array:
    |       {
    |         "timestamp": "2025-06-15T10:30:00Z",
    |         "success": true/false,
    |         "error": "..." (if failed)
    |       }
    |
    +-- Write updated cooldown.json
    |
    v
Verify service health, continue
```

### Counter Reset Flow

```
Tier 1 Agent (Step 5, during evaluation)
    |
    +-- For each service marked "healthy":
    |       |
    |       +-- Read consecutive_healthy from state
    |       |
    |       +-- Increment consecutive_healthy
    |       |
    |       +-- If consecutive_healthy >= 2:
    |       |       |
    |       |       +-- Clear restarts array
    |       |       +-- Clear redeployments array
    |       |       +-- Reset consecutive_healthy to 0
    |       |
    |       +-- Write updated state
    |
    +-- For each service marked unhealthy:
    |       |
    |       +-- Reset consecutive_healthy to 0
    |
    v
Continue to escalation (if needed)
```

## Data Model

### Complete State File Structure

```json
{
  "services": {
    "nginx": {
      "restarts": [
        {
          "timestamp": "2025-06-15T08:15:00Z",
          "success": true
        },
        {
          "timestamp": "2025-06-15T10:30:00Z",
          "success": false,
          "error": "container exited with code 137 after restart"
        }
      ],
      "redeployments": [],
      "consecutive_healthy": 0
    },
    "postgres": {
      "restarts": [],
      "redeployments": [
        {
          "timestamp": "2025-06-14T22:00:00Z",
          "success": true
        }
      ],
      "consecutive_healthy": 1
    }
  },
  "last_run": "2025-06-15T11:00:00Z",
  "last_daily_digest": "2025-06-15T08:00:00Z"
}
```

### Design Rationale for Array-Based Action Records

Action records are stored as arrays of timestamped events rather than simple counters. This was chosen because:

1. **Sliding window evaluation**: The 4-hour and 24-hour limits are sliding windows, not fixed intervals. The agent needs timestamps to determine which actions fall within the current window. A simple counter would not support this without a separate "window start" field and reset logic.

2. **Audit trail**: Storing individual records with success/failure status provides operators with a history of what was attempted and when, aiding root-cause analysis when human intervention is needed.

3. **Self-cleaning**: Expired records (outside the sliding window) can be pruned during state reads, preventing unbounded growth. The agent can filter to only recent-window records when evaluating limits and periodically remove old entries.

### Why Not a Simple Counter

A counter-based model (`restarts_count: 2, restarts_window_start: "..."`) was considered but rejected because:

- Window resets are complex: when does the window start? The first action? A fixed time? Sliding windows are more intuitive and match the specification.
- Counters lose the ability to answer "when did each restart happen?" which operators need for debugging.
- Counters cannot distinguish between "2 restarts in the last 10 minutes" and "2 restarts spread over 3.5 hours" -- both hit the limit but have very different diagnostic implications.

## Key Decisions

### JSON Over Alternatives (from ADR-0007)

The ADR evaluated JSON, SQLite, Redis, and PostgreSQL. JSON was chosen because:

- The agent operates via bash commands. `jq` is already in the container image and is the most natural JSON manipulation tool for a bash-based agent.
- The data model is a flat map of service names to counters/timestamps -- no relationships, joins, or range queries needed.
- Operators can inspect state with `cat` and `jq` without any additional tooling or client connections.
- No additional services to deploy, monitor, or maintain. The only infrastructure requirement is a Docker volume mount.

### Write-Through Pattern Without Locking

The agent uses a simple write-through pattern: read the current file, modify in memory (via `jq`), write the result back. There is no file locking because:

1. The entrypoint runs a single-threaded loop. Only one agent iteration runs at a time.
2. Subagents (Tier 2, Tier 3) are spawned synchronously via the Task tool -- the parent agent waits for the child to complete before continuing.
3. The risk of write interruption (container killed mid-write) is mitigated by the low consequence of state loss: counters reset, and at worst the agent performs one extra remediation attempt before the state is rebuilt.

To further mitigate partial-write risk, implementations SHOULD write to a temporary file and rename (atomic on most filesystems):

```bash
jq '.services["nginx"].restarts += [{"timestamp":"2025-06-15T10:30:00Z","success":true}]' \
  "$STATE_DIR/cooldown.json" > "$STATE_DIR/cooldown.json.tmp" && \
  mv "$STATE_DIR/cooldown.json.tmp" "$STATE_DIR/cooldown.json"
```

### Implicit Service Registration

Services are added to the state file on first encounter -- there is no explicit registration step. When the Tier 1 agent reads the state and finds a service that has no entry, the service is treated as having zero restarts, zero redeployments, and zero consecutive healthy checks. The entry is created when the first state update occurs for that service (e.g., first health check marks it healthy, or first remediation is attempted).

This avoids the need for a service inventory synchronization step and naturally handles dynamic environments where services appear and disappear.

## Trade-offs

### Gained

- **Zero operational overhead**: No database to provision, no connection strings to configure, no schema migrations to manage. The state file is just a file on a volume.
- **Full transparency**: Any operator can inspect, understand, and manually edit the state. This is critical for an AI-driven system where operators need to verify and override agent decisions.
- **Crash recovery simplicity**: If the state file is corrupted or lost, the agent re-initializes it. The worst case is one extra remediation attempt per service before the state is rebuilt -- this is acceptable given the safety margins in the cooldown limits.
- **Agent-native interface**: The agent reads and writes state using the same `jq`/bash tools it uses for everything else. No client libraries, no API calls, no connection management.

### Lost

- **Atomicity**: No ACID transactions. A crash during write can corrupt the file. Mitigated by write-to-temp-and-rename pattern and the single-writer execution model.
- **Query flexibility**: Cannot efficiently query across time ranges, aggregate across services, or perform complex joins. `jq` expressions for complex queries become unwieldy. If future requirements demand trend analysis or reporting, the flat file model will need to be supplemented or replaced.
- **Concurrent access**: The file cannot safely support multiple writers. This is explicitly not a supported configuration, but it means horizontal scaling of the agent is not possible without rearchitecting state management.
- **Bounded growth**: The file grows with the number of services and action records. For the expected scale (tens of services), this is negligible. At hundreds of services with frequent actions, periodic pruning of expired records becomes important.

## Future Considerations

### Migration to SQLite (ADR-0008)

ADR-0008 proposes a Go/HTMX web dashboard that would use SQLite for state storage. If accepted and implemented, the cooldown state would migrate from the JSON file to SQLite tables. The migration path is straightforward:

1. Read the existing `cooldown.json` on first run of the new Go binary
2. Insert records into SQLite tables
3. Use SQLite as the single source of truth going forward
4. The JSON file format documented in this spec would become the import format

### State Pruning

As the system runs continuously, action record arrays will accumulate entries beyond the cooldown window. Implementations SHOULD periodically prune records older than the longest cooldown window (24 hours for redeployments) plus a buffer (e.g., 48 hours total) to prevent unbounded growth. Pruning can occur during any state read-modify-write cycle.

### Multi-Instance State Sharing

If the architecture ever evolves to support multiple agent containers monitoring different service subsets, the state backend would need to be replaced with something supporting concurrent access (SQLite with WAL mode, Redis, or PostgreSQL). The JSON file model explicitly does not support this and the spec documents it as an unsupported configuration.

### Extended Metadata

Future versions may add fields to action records such as:
- `tier`: Which agent tier performed the action (2 or 3)
- `action_detail`: Specific command run (e.g., `docker restart nginx`)
- `duration_ms`: How long the action took

These can be added as optional fields without breaking existing implementations, since the core schema (timestamp + success) is stable.
