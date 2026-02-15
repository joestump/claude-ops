---
status: accepted
date: 2025-06-01
---

# Persist Cooldown State in a JSON File on Mounted Volume

## Context and Problem Statement

Claude Ops enforces cooldown rules to prevent remediation retry loops: a maximum of 2 container restarts per service per 4-hour window, a maximum of 1 full redeployment per service per 24-hour window, and counter resets after 2 consecutive healthy checks. These limits require persistent state that survives container restarts, since the agent runs inside a Docker container that is rebuilt and restarted regularly.

The agent container itself must remain stateless -- all application logic lives in markdown prompts and shell scripts, with no compiled code or runtime dependencies beyond the Claude CLI. State must be persisted externally so that a fresh container picks up where the previous one left off. Without persistent cooldown tracking, the agent could restart a failing service indefinitely, causing cascading failures or masking underlying problems that require human intervention.

The state store must support the full agent lifecycle: initialization on first run (`entrypoint.sh` lines 25-27 create the file if missing), reads by the Tier 1 observer (Step 4 of `tier1-observe.md`), conditional writes by Tier 2 remediation (Step 3 of `tier2-investigate.md`), and periodic resets when services recover. The data model is simple -- per-service counters with timestamps -- but the access pattern is read-heavy with infrequent writes.

## Decision Drivers

* **Container statelessness** -- The Docker container must be disposable. State cannot live inside the container filesystem because it is lost on rebuild or restart.
* **Operational simplicity** -- The system is an AI agent runbook, not a traditional application. Adding infrastructure dependencies (databases, caches) increases operational burden and failure surface.
* **Zero additional dependencies** -- The container image includes the Claude CLI, bash, curl, jq, and standard Unix tools. The state mechanism should not require installing or running additional services.
* **Human readability** -- Operators should be able to inspect the cooldown state directly (e.g., `cat /state/cooldown.json | jq .`) without specialized tooling. This is critical for debugging why the agent did or did not take an action.
* **Agent compatibility** -- The Claude Code CLI operates via bash commands and file reads. The state store must be accessible through these primitives -- no client libraries, SDKs, or network protocols required.
* **Data model simplicity** -- The state is a flat map of service names to counters and timestamps. There are no relationships, joins, queries by range, or concurrent access patterns that would benefit from a database.

## Considered Options

1. **JSON file on persistent volume** -- A single `cooldown.json` file on a Docker volume mount, read and written with `jq` and standard file I/O.
2. **SQLite database on persistent volume** -- A SQLite database file on the same Docker volume, accessed via the `sqlite3` CLI.
3. **External Redis instance** -- A separate Redis container or managed service for state, accessed via `redis-cli`.
4. **PostgreSQL database (reuse existing MCP connection)** -- Store state in a PostgreSQL database that may already be available via MCP server configuration from a mounted repo.

## Decision Outcome

Chosen option: **"JSON file on persistent volume"**, because it requires no additional dependencies, is directly readable by both the agent (via `jq` and `cat`) and operators, and matches the simplicity of the data model. The file is initialized with `{"services":{},"last_run":null,"last_daily_digest":null}` on first run by `entrypoint.sh` and persists across container restarts via a Docker volume mounted at `$CLAUDEOPS_STATE_DIR`.

This approach keeps the container fully stateless -- the JSON file is external to the container image -- while maintaining state continuity across restarts with zero infrastructure dependencies beyond a volume mount. The agent reads and writes the file using `jq` inside bash commands, which are already available in the container and are the native interface for Claude Code tool use.

### Consequences

**Positive:**

* No additional services to deploy, monitor, or maintain. The volume mount is the only infrastructure requirement.
* The state file is human-readable. Operators can `cat`, `jq`, or edit the file directly to inspect or reset cooldown state.
* Initialization is trivial: a single `echo` command in `entrypoint.sh` creates the file if absent.
* The agent interacts with state using the same bash/file primitives it uses for everything else -- no client libraries, connection strings, or authentication.
* The file can be version-controlled, backed up, or copied between environments with standard file tools.
* Failure mode is simple and visible: if the file is missing or corrupt, the agent re-initializes it and logs the event.

**Negative:**

* No atomic read-modify-write. If the agent is interrupted mid-write (e.g., container killed during a `jq` pipe to the file), the file could be left empty or truncated. In practice this is mitigated by the single-threaded execution model (one agent loop at a time) and by the low consequence of state loss (counters reset, worst case an extra remediation attempt occurs).
* No file locking. If multiple containers were pointed at the same volume (not a supported configuration), concurrent writes could corrupt the file. This is acceptable because the architecture explicitly runs a single agent container per deployment.
* No query capabilities beyond what `jq` provides. If future requirements demand querying across time ranges or aggregating across services, the flat file model would need to be replaced.
* The file grows linearly with the number of monitored services. For the expected scale (tens of services, not thousands), this is negligible.

## Pros and Cons of the Options

### JSON file on persistent volume

* Good, because it requires zero additional dependencies -- only a Docker volume mount and `jq`, which is already in the container.
* Good, because the file is directly human-readable and editable, aiding operator debugging and manual intervention.
* Good, because initialization is a single shell command, keeping `entrypoint.sh` simple.
* Good, because the agent reads and writes state using the same bash primitives it uses for all other operations.
* Good, because the data model (per-service counters and timestamps) maps naturally to a JSON object.
* Bad, because writes are not atomic -- a crash during write could truncate the file, requiring re-initialization on next run.
* Bad, because there is no built-in file locking, so concurrent access from multiple containers would risk corruption (though this is not a supported deployment pattern).
* Bad, because `jq` transformations on a file are less ergonomic than SQL or key-value get/set for complex state updates.

### SQLite database on persistent volume

* Good, because SQLite provides ACID transactions, eliminating the atomicity concern of raw file writes.
* Good, because SQL queries enable flexible state inspection (e.g., "show all services restarted in the last 4 hours").
* Good, because it is still a single file on a volume mount, maintaining the zero-external-dependency property.
* Bad, because it adds a dependency on the `sqlite3` CLI, which may not be in the container image and adds image size.
* Bad, because the agent would need to construct SQL queries in bash, which is more error-prone than `jq` for a JSON-native agent.
* Bad, because the database file is not human-readable -- operators need `sqlite3` to inspect state, reducing debuggability.
* Bad, because the data model is too simple to benefit from SQL -- there are no joins, indexes, or complex queries needed.
* Bad, because Claude models are less reliable at generating correct SQL in bash scripts than they are at generating `jq` expressions for JSON manipulation.

### External Redis instance

* Good, because Redis provides atomic operations (INCR, EXPIRE) that map well to counters with time windows.
* Good, because it handles concurrent access natively if the architecture ever scales to multiple agent containers.
* Good, because TTL-based key expiry could automate cooldown window resets without explicit counter management.
* Bad, because it introduces a network dependency -- the agent cannot function if Redis is unreachable, creating a circular problem (the monitoring agent depends on infrastructure it might need to monitor).
* Bad, because it requires deploying and maintaining a separate service (or managed instance), adding operational complexity to a system designed for simplicity.
* Bad, because `redis-cli` must be installed in the container, adding image size and a build dependency.
* Bad, because Redis state is not human-readable without connecting to the instance -- operators cannot simply `cat` a file.
* Bad, because the single-container, single-threaded execution model does not benefit from Redis's concurrency features.

### PostgreSQL database (reuse existing MCP connection)

* Good, because a PostgreSQL instance may already be accessible via MCP server configuration from mounted repos, avoiding a new service.
* Good, because ACID transactions and SQL provide robust state management with query flexibility.
* Good, because it could enable future features like historical trend analysis and cross-run reporting.
* Bad, because it couples the monitoring agent's core functionality to an external database that the agent itself may need to monitor -- a failure in PostgreSQL would prevent the agent from tracking its own remediation state.
* Bad, because MCP database connections are configured per-repo and may not be available in all deployments, making the state backend deployment-dependent.
* Bad, because it requires schema management (CREATE TABLE, migrations) for what is fundamentally a small key-value store.
* Bad, because the round-trip latency of network database queries is unnecessary for a single-threaded agent reading and writing a few kilobytes of state.
* Bad, because it is the heaviest solution for the simplest data model -- a service-to-counter map does not justify a relational database.
