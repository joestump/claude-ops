# Playbook: Restart Container

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

1. **Record pre-restart state**
   Use the SSH command from the host access map (e.g., `ssh <user>@<host> [sudo] docker inspect ...`):
   ```bash
   docker inspect --format '{{.State.Status}} {{.State.Health.Status}} restarts={{.RestartCount}}' <container>
   docker logs --tail 20 <container>
   ```

2. **Restart the container**
   Use the SSH command from the host access map (requires write access — `method: root` or `method: sudo`):
   ```bash
   docker restart <container>
   ```

3. **Wait for startup**
   - Wait 15-30 seconds (adjust based on service — databases need longer)
   - Check container status: `docker inspect --format '{{.State.Status}}' <container>`

4. **Verify health**
   - Re-run the original health check that failed
   - If the container has a healthcheck, wait for it to report healthy
   - Check that dependent services can reach this one

5. **Update cooldown state**
   - Increment `restart_count_4h` for this service
   - Update `last_restart` timestamp
   - Set status to `healthy` if checks pass, `degraded` if partially recovered

## If It Doesn't Work

- Check container logs for crash reason (use SSH command from the host access map): `docker logs --tail 100 <container>`
- If crashlooping (exits immediately after restart), escalate to Tier 3
- If resource issue (OOM), note memory usage and escalate
- Do NOT retry the restart if it already failed — escalate instead
