<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), SPEC-0007 REQ-4 (max 2 restarts per service per 4-hour window), REQ-3 (Playbook Document Structure), REQ-8 (no build step), REQ-9 (self-documenting) -->
# Playbook: Restart Container

<!-- Governing: SPEC-0002 REQ-11 — Playbook Tier Gating, REQ-3 — Playbook Document Structure -->

**Tier**: 2 (Sonnet) minimum

## When to Use

- Container health check is failing
- Container is in `unhealthy` state
- Service is returning 502/503 errors
- Container is running but unresponsive

## Prerequisites

- Check cooldown state: max 2 restarts per service per 4-hour window
- Verify the container actually exists and is in a bad state (don't restart healthy containers)

## Host Access

Before executing this playbook, consult the host access map (from the handoff file) and `/app/skills/ssh-discovery.md` to determine the correct SSH user and command prefix for the target host. All commands below run on the remote host via SSH.

**If the host has `method: limited`, this playbook CANNOT be executed.** Follow the limited-access fallback procedure in the tier prompt instead.

## Steps

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->

1. **Record pre-restart state**
   Use the SSH command from the host access map (e.g., `ssh <user>@<host> [sudo] docker inspect ...`):
   ```bash
   ssh <user>@<host> docker inspect --format '{{.State.Status}} {{.State.Health.Status}} restarts={{.RestartCount}}' <container>
   ssh <user>@<host> docker logs --tail 20 <container>
   ```

   Replace `<user>` and `<host>` with the SSH credentials from the host access map. Replace `<container>` with the actual container name from the inventory.

2. **Restart the container**
   Use the SSH command from the host access map (requires write access — `method: root` or `method: sudo`):
   ```bash
   ssh <user>@<host> docker restart <container>
   ```

3. **Wait for startup**
   <!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->
   - Wait 15-30 seconds (adjust based on service — databases need longer, lightweight services like static file servers may be ready in 5 seconds)
   - Check container status: `ssh <user>@<host> docker inspect --format '{{.State.Status}}' <container>`

4. **Verify health**
   - Re-run the original health check that failed
   - If the container has a healthcheck, wait for it to report healthy
   - Check that dependent services can reach this one

5. **Update cooldown state**
   - Increment `restart_count_4h` for this service
   - Update `last_restart` timestamp
   - Set status to `healthy` if checks pass, `degraded` if partially recovered

## Verification

- Re-run the original health check that triggered this playbook
- If the container defines a Docker healthcheck, wait until it reports `healthy`
- Confirm dependent services can reach the restarted container
- Verify no new errors in container logs after restart

## If It Doesn't Work

- Check container logs for crash reason (use SSH command from the host access map): `ssh <user>@<host> docker logs --tail 100 <container>`
- If crashlooping (exits immediately after restart), escalate to Tier 3
- If resource issue (OOM), note memory usage and escalate
- Do NOT retry the restart if it already failed — escalate instead
