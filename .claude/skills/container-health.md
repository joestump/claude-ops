# Skill: Container Health Inspection

<!-- Governing: SPEC-0023 REQ-10, REQ-11, REQ-12; ADR-0022 -->

## Purpose

Inspect container state, health status, and logs on remote hosts defined in repo inventories. Use this skill during health check cycles to determine whether containers are running, healthy, and producing expected output.

This is a read-only skill. It does NOT restart, start, or stop containers. For container lifecycle operations, see the `container-ops` skill.

## Tier Requirement

Tier 1 minimum. This skill performs only read-only inspection of container state and logs. All tiers may execute it.

## Tool Discovery

This skill uses the following tools in preference order:
1. **MCP**: `mcp__docker__list_containers`, `mcp__docker__inspect_container`, `mcp__docker__get_container_logs` — check if available in tool listing
2. **CLI**: `docker` (via SSH to remote host) — check with `which docker` or `which ssh`
3. **Browser**: `mcp__chrome-devtools__navigate_page` — for web-based Docker UIs (Portainer, Dozzle) defined in inventory

**Important**: Container inspection MUST target remote hosts defined in repo inventories. Do NOT use `docker ps` or `docker inspect` against the local Docker daemon — the local daemon is NOT the monitoring target.

## Execution

### Check Container State

#### Using MCP: mcp__docker__list_containers

1. Call `mcp__docker__list_containers` to get all containers and their states.
2. Filter results to containers matching the service name from the inventory.
3. Check the `State` field: `running`, `exited`, `restarting`, etc.
4. Check the `Health` field if present: `healthy`, `unhealthy`, `starting`.
5. Log: `[skill:container-health] Using: mcp__docker__list_containers (MCP)`

#### Using CLI: docker (via SSH)

1. Connect to the remote host defined in the inventory:
   ```bash
   ssh <user>@<host> "docker ps -a --filter name=<service> --format '{{.Names}}\t{{.Status}}\t{{.State}}'"
   ```
2. Parse the output to determine container state.
3. For detailed health info:
   ```bash
   ssh <user>@<host> "docker inspect --format '{{.State.Status}} {{.State.Health.Status}}' <container>"
   ```
4. Log: `[skill:container-health] Using: docker via ssh (CLI)`
5. If MCP was preferred but unavailable, also log: `[skill:container-health] WARNING: Docker MCP not available, falling back to docker via ssh (CLI)`

#### Using Browser: mcp__chrome-devtools__navigate_page (Dozzle/Portainer)

1. If the inventory defines a web UI URL for container management (e.g., Dozzle at `dozzle.stump.rocks`):
   - Navigate to the container management page using `mcp__chrome-devtools__navigate_page`.
   - Take a snapshot with `mcp__chrome-devtools__take_snapshot` to read container status.
2. Log: `[skill:container-health] Using: mcp__chrome-devtools__navigate_page (MCP/Browser)`
3. This path is a last resort when neither Docker MCP nor SSH is available.

### Inspect Container Logs

#### Using MCP: mcp__docker__get_container_logs

1. Call `mcp__docker__get_container_logs` with the container name and a tail limit (e.g., last 50 lines).
2. Scan logs for error patterns, crash loops, or OOM messages.
3. Log: `[skill:container-health] Using: mcp__docker__get_container_logs (MCP)`

#### Using CLI: docker logs (via SSH)

1. Fetch recent logs:
   ```bash
   ssh <user>@<host> "docker logs --tail 50 --timestamps <container> 2>&1"
   ```
2. Scan output for error patterns.
3. Log: `[skill:container-health] Using: docker logs via ssh (CLI)`

### Check Container Resource Usage

#### Using CLI: docker stats (via SSH)

1. Get a one-shot resource snapshot:
   ```bash
   ssh <user>@<host> "docker stats --no-stream --format '{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}' <container>"
   ```
2. Flag if memory usage exceeds 90% or CPU is sustained above 95%.
3. Log: `[skill:container-health] Using: docker stats via ssh (CLI)`

## Validation

After inspecting container state:
1. Confirm a clear status was obtained (running/stopped/unhealthy).
2. If the container has a health check, confirm the health status was retrieved.
3. Report results in the format: `<service>: <state> (<health>)` — e.g., `shamrock: running (healthy)`.

After inspecting logs:
1. Confirm logs were retrieved (may be empty for new containers).
2. Report any error patterns found, or "no errors in recent logs".

If inspection fails:
1. Report: `[skill:container-health] ERROR: No suitable tool found for container inspection`
2. Include which tools were attempted.
