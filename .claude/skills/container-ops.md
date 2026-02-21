# Skill: Container Lifecycle Operations

<!-- Governing: SPEC-0023 REQ-10, REQ-11, REQ-12; ADR-0022 -->

## Purpose

Restart, start, and stop containers on remote hosts as part of remediation workflows. Use this skill when a container is unhealthy or stopped and needs to be brought back to a healthy state. Always check the cooldown state before performing any operation.

## Tier Requirement

Tier 2 minimum. Container lifecycle operations modify infrastructure state. Tier 1 agents MUST NOT execute this skill and MUST escalate to Tier 2. For read-only container inspection, use the `container-health` skill instead.

## Tool Discovery

This skill uses the following tools in preference order:
1. **MCP**: `mcp__docker__restart_container`, `mcp__docker__start_container`, `mcp__docker__stop_container` — check if available in tool listing
2. **CLI**: `docker` (via SSH to remote host) — check with `which ssh`

**Important**: All operations MUST target remote hosts defined in repo inventories. Do NOT operate on the local Docker daemon.

## Execution

### Restart Container

#### Using MCP: mcp__docker__restart_container

1. Read cooldown state from `$CLAUDEOPS_STATE_DIR/cooldown.json`.
2. Check that the service has not exceeded the restart limit (max 2 restarts per 4-hour window).
3. If within limits, call `mcp__docker__restart_container` with the container name.
4. Update cooldown state with the restart timestamp.
5. Log: `[skill:container-ops] Using: mcp__docker__restart_container (MCP)`

#### Using CLI: docker restart (via SSH)

1. Read cooldown state from `$CLAUDEOPS_STATE_DIR/cooldown.json`.
2. Check restart limits (max 2 per 4-hour window).
3. If within limits:
   ```bash
   ssh <user>@<host> "docker restart <container>"
   ```
4. Update cooldown state.
5. Log: `[skill:container-ops] Using: docker restart via ssh (CLI)`
6. If MCP was preferred but unavailable, also log: `[skill:container-ops] WARNING: Docker MCP not available, falling back to docker via ssh (CLI)`

### Start Stopped Container

#### Using MCP: mcp__docker__start_container

1. Call `mcp__docker__start_container` with the container name.
2. Log: `[skill:container-ops] Using: mcp__docker__start_container (MCP)`

#### Using CLI: docker compose up -d (via SSH)

1. If a compose file is available for the service:
   ```bash
   ssh <user>@<host> "cd /path/to/service && docker compose up -d <service>"
   ```
2. If no compose file, use direct start:
   ```bash
   ssh <user>@<host> "docker start <container>"
   ```
3. Log: `[skill:container-ops] Using: docker compose up -d via ssh (CLI)` or `[skill:container-ops] Using: docker start via ssh (CLI)`

### Stop Container

#### Using MCP: mcp__docker__stop_container

1. Call `mcp__docker__stop_container` with the container name.
2. Log: `[skill:container-ops] Using: mcp__docker__stop_container (MCP)`

#### Using CLI: docker stop (via SSH)

1. ```bash
   ssh <user>@<host> "docker stop <container>"
   ```
2. Log: `[skill:container-ops] Using: docker stop via ssh (CLI)`

## Validation

After restarting a container:
1. Wait 10-15 seconds for the container to stabilize.
2. Use the `container-health` skill to verify the container is running and healthy.
3. Report: `<service>: restarted successfully, now <state> (<health>)` or `<service>: restart failed, container is <state>`.

After starting a container:
1. Verify the container transitioned from stopped to running.
2. Use the `container-health` skill to confirm health status.

After stopping a container:
1. Verify the container is in the stopped/exited state.

## Scope Rules

This skill MUST NOT:
- Use `docker compose down` — this removes containers and may lose state
- Use `docker rm` or `docker rm -f` — container removal requires human approval
- Use `docker system prune` — bulk cleanup is never allowed
- Use `docker volume rm` — persistent data deletion is never allowed
- Operate on the local Docker daemon — only remote hosts from inventory
- Exceed cooldown limits (max 2 restarts per service per 4-hour window)

If any of these are attempted, refuse the operation and report:
`[skill:container-ops] SCOPE VIOLATION: <action> is not permitted`

## Dry-Run Behavior

When `CLAUDEOPS_DRY_RUN=true`:
- MUST NOT restart, start, or stop any containers.
- MUST still check cooldown state and report whether the operation would be allowed.
- MUST still perform tool discovery and selection.
- Log: `[skill:container-ops] DRY RUN: Would restart <container> on <host> using <tool> (cooldown: <N> of 2 restarts used)`
