<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-8 (no build step), REQ-9 (self-documenting) -->

# Container State Checks

## When to Run

For every service expected to run as a Docker container.

## How to Check

Use Docker MCP tools or CLI:

```bash
# List running containers
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# Check a specific container's health
docker inspect --format '{{.State.Status}} {{.State.Health.Status}}' <container>

# Check restart count
docker inspect --format '{{.RestartCount}}' <container>

# Check when the container started
docker inspect --format '{{.State.StartedAt}}' <container>
```

## What's Healthy

- Container status: `running`
- Health status: `healthy` (if healthcheck defined)
- Restart count: 0 or low and stable
- Started at: not recently (unless expected)

## Warning Signs

- **High restart count**: container is crashlooping. Check logs.
- **Recently started**: may have just recovered from a crash. Note but don't flag unless repeated.
- **Status `restarting`**: actively crashlooping right now.
- **Status `exited`**: container has stopped. Check exit code.
- **Health status `unhealthy`**: container is running but failing its healthcheck.

## What to Record

For each expected container:
- Container name
- Running status
- Health status (if applicable)
- Restart count
- Uptime (time since last start)
- Whether it matches expectations
