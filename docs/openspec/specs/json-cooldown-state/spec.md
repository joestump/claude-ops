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

- [ADR-0007: Persist Cooldown State in a JSON File on Mounted Volume](/docs/adrs/ADR-0007-json-file-cooldown-state.md)
- [ADR-0001: Tiered Model Escalation](/docs/adrs/ADR-0001-tiered-model-escalation.md)
- [ADR-0004: Apprise Notification Abstraction](/docs/adrs/ADR-0004-apprise-notification-abstraction.md)
- [CLAUDE.md Cooldown Rules](/CLAUDE.md) -- defines the cooldown limits and reset behavior
- [entrypoint.sh](/entrypoint.sh) -- initializes the state file on startup (lines 25-27)
- [tier1-observe.md](/prompts/tier1-observe.md) -- Step 4 reads cooldown state
- [tier2-investigate.md](/prompts/tier2-investigate.md) -- Step 3 checks cooldown before remediation
- [tier3-remediate.md](/prompts/tier3-remediate.md) -- Step 3 checks cooldown before full remediation
