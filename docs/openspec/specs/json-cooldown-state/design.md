# Design: JSON File Cooldown State

## Overview

The cooldown state system prevents Claude Ops from retrying failed remediations indefinitely by tracking per-service action counts within sliding time windows. The state is persisted as a single JSON file on a Docker volume mount, keeping the system aligned with the project's zero-dependency, container-stateless architecture.

This design covers the data model, file lifecycle, access patterns across all three agent tiers, and the write-safety guarantees achievable without file locking.

## Architecture

### Component Interactions

The cooldown state file sits at the intersection of three system components:

```
entrypoint.sh ──── creates file if missing ────► cooldown.json
                                                      ▲
                                                      │
                                                  read/write
                                                      │
                                              ┌───────┴───────┐
                                              │               │
                                         Tier 1 Agent    Tier 2/3 Agents
                                         (read-only)     (read + write)
```

1. **entrypoint.sh** -- Initializes the state file before the first agent invocation. Lines 25-27 of the current script check for the file's existence and create it with the default structure if absent. This runs once per container startup, before the agent loop begins.

2. **Tier 1 Agent (Haiku)** -- Reads the state file during Step 4 of `tier1-observe.md` to identify services in cooldown. Categorizes these services as `in_cooldown` in the evaluation step. Updates `last_run` and `last_daily_digest` timestamps. Does not perform remediations, so does not modify service counters.

3. **Tier 2 Agent (Sonnet)** -- Reads the state file in Step 3 of `tier2-investigate.md` before any remediation action. Checks restart counts against the 2-per-4-hour limit. Writes the state file after each remediation attempt (successful or failed) to record the action.

4. **Tier 3 Agent (Opus)** -- Reads the state file in Step 3 of `tier3-remediate.md` before full remediation actions. Checks redeployment counts against the 1-per-24-hour limit. Writes the state file after each remediation attempt.

### Volume Mount Architecture

```
Docker Host Filesystem
├── /var/lib/docker/volumes/claudeops_state/_data/
│   └── cooldown.json            ◄── persistent across container lifecycle
│
Container Filesystem
├── /state/                      ◄── volume mount point ($CLAUDEOPS_STATE_DIR)
│   └── cooldown.json            ◄── same file, accessed inside the container
```

The state directory is configured as a named Docker volume in `docker-compose.yaml`. This ensures the data survives container stops, restarts, and image rebuilds. The volume is not part of the container image and is never included in `docker compose down -v` operations (which require explicit `--volumes` flag).

## Data Flow

### Initialization Flow

```
Container Start
    │
    ▼
entrypoint.sh
    │
    ├── Check: does $CLAUDEOPS_STATE_DIR/cooldown.json exist?
    │       │
    │       ├── Yes → proceed (do not modify)
    │       │
    │       └── No → write default: {"services":{}, "last_run":null, "last_daily_digest":null}
    │
    ▼
Enter agent loop
```

### Health Check Read Flow (Tier 1)

```
Tier 1 Agent (Step 4)
    │
    ├── Read cooldown.json
    │
    ├── For each service in "services":
    │       │
    │       ├── Count restarts within last 4 hours
    │       ├── Count redeployments within last 24 hours
    │       │
    │       ├── If at restart limit (2) → mark "in_cooldown"
    │       └── If at redeployment limit (1) → mark "in_cooldown"
    │
    ├── Update "last_run" to current UTC timestamp
    │
    ├── Check if daily digest is due:
    │       │
    │       ├── last_daily_digest is null → send digest, update timestamp
    │       └── last_daily_digest > 24h ago → send digest, update timestamp
    │
    ▼
Pass cooldown context to evaluation (Step 5)
```

### Remediation Write Flow (Tier 2/3)

```
Tier 2/3 Agent (before action)
    │
    ├── Read cooldown.json
    │
    ├── Check limit for intended action:
    │       │
    │       ├── Restart: count restarts in last 4h < 2? → proceed
    │       ├── Redeployment: count redeployments in last 24h < 1? → proceed
    │       │
    │       └── Limit exceeded → skip action, notify "needs human attention"
    │
    ▼
Perform remediation action
    │
    ▼
After action (success or failure)
    │
    ├── Read current cooldown.json
    │
    ├── Append action record to appropriate array:
    │       {
    │         "timestamp": "2025-06-15T10:30:00Z",
    │         "success": true/false,
    │         "error": "..." (if failed)
    │       }
    │
    ├── Write updated cooldown.json
    │
    ▼
Verify service health, continue
```

### Counter Reset Flow

```
Tier 1 Agent (Step 5, during evaluation)
    │
    ├── For each service marked "healthy":
    │       │
    │       ├── Read consecutive_healthy from state
    │       │
    │       ├── Increment consecutive_healthy
    │       │
    │       ├── If consecutive_healthy >= 2:
    │       │       │
    │       │       ├── Clear restarts array
    │       │       ├── Clear redeployments array
    │       │       └── Reset consecutive_healthy to 0
    │       │
    │       └── Write updated state
    │
    ├── For each service marked unhealthy:
    │       │
    │       └── Reset consecutive_healthy to 0
    │
    ▼
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
