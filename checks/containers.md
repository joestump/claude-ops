<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-2 (Check Document Structure), REQ-8 (no build step), REQ-9 (self-documenting) -->
# Container State Checks

## When to Run

For every service expected to run as a Docker container.

## How to Check

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->

Use Docker MCP tools or CLI. All commands run on the remote host via SSH — use the host access map to determine the correct SSH user and prefix.

```bash
# List running containers on a remote host
ssh <user>@<host> docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# Check a specific container's health
ssh <user>@<host> docker inspect --format '{{.State.Status}} {{.State.Health.Status}}' <container>

# Check restart count
ssh <user>@<host> docker inspect --format '{{.RestartCount}}' <container>

# Check when the container started
ssh <user>@<host> docker inspect --format '{{.State.StartedAt}}' <container>
```

Replace `<user>` and `<host>` with values from the SSH access map. Replace `<container>` with the actual container name from the inventory.

## What's Healthy

- Container status: `running`
- Health status: `healthy` (if healthcheck defined)
- Restart count: 0 or low and stable
- Started at: not recently (unless expected)

## Warning Signs

<!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->

- **High restart count**: container is crashlooping. Check logs. A restart count of 1-2 after a recent deployment is normal — only flag if the count keeps climbing.
- **Recently started**: may have just recovered from a crash. Note but don't flag unless repeated.
- **Status `restarting`**: actively crashlooping right now.
- **Status `exited`**: container has stopped. Check exit code. Some containers (backup jobs, one-shot tasks) are expected to exit — check the service type before flagging.
- **Health status `unhealthy`**: container is running but failing its healthcheck.

## What to Record

For each expected container:
- Container name
- Running status
- Health status (if applicable)
- Restart count
- Uptime (time since last start)
- Whether it matches expectations

## Special Cases

- Init containers or one-shot containers (e.g., migration runners) may have status `exited` with exit code 0 — this is expected and healthy
- Containers with `restart: unless-stopped` policy may have a non-zero restart count from a previous incident — check if the count is increasing, not just non-zero
- Some containers do not define a Docker healthcheck — absence of health status does not mean unhealthy; fall back to checking if the container is running and the service responds to HTTP/TCP checks
- Sidecar containers (e.g., log shippers, metrics exporters) may not have web endpoints — verify they are running and not crashlooping
