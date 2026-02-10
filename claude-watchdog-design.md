# Claude Watchdog: Autonomous Home Infrastructure Monitor

An AI-powered infrastructure monitoring and remediation agent that uses Claude Code CLI to periodically health-check services, investigate failures, and automatically fix common issues — with intelligent model escalation to control costs.

## Overview

Claude Watchdog runs as a Docker container on a home server. Every 60 minutes, it:

1. Discovers all deployed services from an Ansible inventory file
2. Health-checks every service (HTTP, DNS, database connectivity, container state)
3. If issues are found, escalates to smarter (more expensive) models to investigate and remediate
4. Sends email summaries and push notifications (ntfy) for urgent issues
5. Maintains a cooldown state file to avoid retry loops

## Architecture

```
┌──────────────────────────────────────────────────────┐
│                   Docker Compose                      │
│                                                       │
│  ┌─────────────────────────────────────────────────┐  │
│  │  claude-watchdog (main container)               │  │
│  │                                                 │  │
│  │  Entrypoint: loop every 60 min                  │  │
│  │    └─ claude --model haiku -p "Run checks"      │  │
│  │         ├─ Reads inventory (ie.yaml)            │  │
│  │         ├─ Health checks all services           │  │
│  │         ├─ Escalates via Task tool if needed    │  │
│  │         │    ├─ model: sonnet (investigation)   │  │
│  │         │    └─ model: opus (complex remediation)│  │
│  │         ├─ Remediates within permission tier    │  │
│  │         └─ Sends notifications                  │  │
│  │                                                 │  │
│  │  Includes: Node.js, Ansible, Python, SSH, curl  │  │
│  │  MCP servers: docker, postgres, mysql, redis,   │  │
│  │               fetch                             │  │
│  └──────────────┬──────────────────────────────────┘  │
│                 │ CDP WebSocket                        │
│  ┌──────────────▼──────────────────────────────────┐  │
│  │  claude-watchdog-chrome (sidecar)               │  │
│  │  Headless Chromium on port 9222                 │  │
│  │  Used for browser automation (e.g., rotating    │  │
│  │  API keys from web UIs without APIs)            │  │
│  └─────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────┘

Volumes:
  - Home infrastructure repo (read-write, for Ansible)
  - SSH private key (read-only, dedicated key pair)
  - Results/logs directory (persistent)
  - Cooldown state file (persistent)
```

## Model Escalation Strategy

Claude Code's `Task` tool accepts a `model` parameter, enabling cost-efficient escalation within a single session:

```
Tier 1 — Haiku (every run, ~$0.01-0.05)
  │  Parse inventory, HTTP checks, DNS verification, container status
  │
  │  All green? → Log results, exit
  │  Issues found? → Spawn Tier 2 subagent
  │
  ▼
Tier 2 — Sonnet (on-demand, only when issues exist)
  │  Task tool with model: "sonnet"
  │  Read container logs, check resource usage
  │  Attempt safe remediations (restart containers, fix permissions)
  │
  │  Fixed? → Email summary of actions taken
  │  Can't fix? → Spawn Tier 3 subagent
  │
  ▼
Tier 3 — Opus (on-demand, only for complex failures)
  │  Task tool with model: "opus"
  │  Run Ansible playbooks for full redeployment
  │  Multi-service cascading failure analysis
  │  Complex remediation (DB issues, multi-step recovery)
  │
  └─→ Always emails a detailed report
      "Fixed X by doing Y" or "Needs human attention because Z"
```

**Cost estimate on a healthy day:** ~$1-2/day (24 Haiku runs). Sonnet/Opus tokens are only spent when something is actually broken.

## Permission Model (Safety Tiers)

Encoded in the project's CLAUDE.md as hard rules:

### Haiku (Tier 1) — Observe Only
- Read files, configs, logs, inventory
- HTTP/DNS health checks
- Query databases (read-only)
- Inspect container state
- **Cannot** modify anything

### Sonnet (Tier 2) — Safe Remediation
- Everything Haiku can do, plus:
- `docker restart <container>` for unhealthy services
- `docker compose up -d` for stopped containers
- Fix file ownership/permissions (`chown`/`chmod` on known data paths)
- Clear tmp/cache directories
- Update API keys in services via their REST APIs
- Browser automation for credential rotation (via Chrome DevTools MCP)

### Opus (Tier 3) — Full Remediation
- Everything Sonnet can do, plus:
- Run Ansible playbooks for full service redeployment
- Investigate and fix database connectivity issues
- Recreate containers from scratch
- Multi-service orchestrated recovery (e.g., restart postgres, wait, then restart dependents)

### Never Allowed (Always Require Human)
- Delete persistent data volumes
- Modify the inventory file (ie.yaml) or playbooks
- Change passwords, secrets, or encryption keys
- Modify network configuration (VPN, Wireguard, Caddy, DNS records)
- `docker system prune` or any bulk cleanup
- Push to git repositories
- Any action on hosts other than the monitored cluster

## Health Check Routine

Each 60-minute run, Haiku performs these checks by reading the Ansible inventory to discover services dynamically:

### 1. Service Discovery
- Parse the inventory YAML file
- Extract all services with `enabled: true`
- Build a checklist of endpoints, ports, and expected behaviors

### 2. HTTP Health Checks
- For every service with Caddy enabled: `GET https://<dns>.<domain>/`
- Expect HTTP 2xx (or 3xx redirect to login)
- For services with explicit healthcheck definitions, extract and hit those endpoints
- For services with homepage widget URLs, verify those respond too

### 3. DNS Verification
- Resolve `<dns>.<domain>` for every service with `dns_cname` enabled
- Verify CNAME points to the expected host

### 4. Container State (via Docker MCP)
- Verify all expected containers are running
- Check restart counts (high restart count = crashloop)
- Check container uptime (recently restarted = potential issue)
- Inspect health status for containers with healthchecks

### 5. Database Health (via Postgres/MySQL/Redis MCPs)
- PostgreSQL: connection count, database sizes, dead tuple ratio
- MariaDB: process list, connection count, table status
- Redis: memory usage (`INFO memory`), `DBSIZE` per index, connected clients, eviction rate

### 6. Service-Specific Checks
- Prowlarr: indexer status via API (are indexers returning results?)
- Download stack: SABnzbd/qBittorrent queue health
- Jellyfin: active streams, library scan status
- Any service with `labels['homepage.widget.key']`: verify API key still works

## Remediation Cooldown

Claude maintains a JSON state file on the persistent volume:

```json
{
  "services": {
    "jellyfin": {
      "last_restart": "2026-02-08T12:00:00Z",
      "restart_count_4h": 1,
      "last_redeployment": null,
      "status": "healthy"
    },
    "prowlarr": {
      "last_restart": "2026-02-08T11:30:00Z",
      "restart_count_4h": 3,
      "last_redeployment": "2026-02-07T08:00:00Z",
      "status": "degraded"
    }
  },
  "last_run": "2026-02-08T12:30:00Z",
  "last_daily_digest": "2026-02-08T08:00:00Z"
}
```

**Rules:**
- Max 2 container restarts per service per 4-hour window
- Max 1 Ansible redeployment per service per 24-hour window
- If cooldown limit exceeded: stop retrying, send ntfy + email "needs human attention"
- Reset counters when a service is confirmed healthy for 2 consecutive checks
- Cooldown state persists across container restarts via mounted volume

## Notification Strategy

### Email (via SMTP)
- **Daily digest** (once per day, morning): summary of all checks, uptime stats
- **Auto-remediated** (immediately): "Restarted jellyfin because health check failed, verified it came back healthy"
- **Needs attention** (immediately): "Postgres is down, 6 dependent services affected, restart didn't help, here's what I found in the logs"

### ntfy (push notification)
- **Urgent only**: service down and remediation failed, or cooldown exceeded
- Allows immediate phone notification for things that need human intervention

## MCP Servers

Six MCP servers provide Claude with structured access to infrastructure:

| MCP | Purpose | Key Tools |
|-----|---------|-----------|
| **Docker** | Container lifecycle management | list, inspect, logs, restart, compose up/down |
| **PostgreSQL** | Direct database health queries | Query connection counts, DB sizes, dead tuples, running queries |
| **MySQL/MariaDB** | Direct database health queries | Process list, connection count, table status |
| **Redis** | Cache/broker inspection | INFO, DBSIZE, SLOWLOG, memory stats, per-DB-index inspection |
| **Chrome DevTools** | Browser automation for web UIs | Navigate, click, type, screenshot, extract text |
| **Fetch** | Structured HTTP health checks | GET/POST with full response metadata (status, headers, body) |

### Chrome DevTools Use Case: Usenet API Key Rotation

A concrete example of browser automation for remediation:

1. Haiku detects Prowlarr indexer errors via the Prowlarr API
2. Escalates to Sonnet: "Usenet indexer returning auth errors"
3. Sonnet uses Chrome DevTools MCP to:
   - Navigate to the Usenet indexer's web portal
   - Log in with stored credentials
   - Navigate to the API key page
   - Extract the current/new API key
4. Sonnet calls Prowlarr's REST API to update the indexer with the new key
5. Verifies downloads resume
6. Emails summary: "Rotated indexer API key in Prowlarr, stack healthy"

Credentials for services requiring browser automation are stored in the project configuration.

## Docker Image

### Main Container (claude-watchdog)

```dockerfile
FROM node:lts-slim

# System dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 python3-pip python3-venv pipenv \
    openssh-client \
    curl \
    dnsutils \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Working directory
WORKDIR /repo

# Entrypoint script
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

CMD ["/entrypoint.sh"]
```

### Chrome Sidecar (claude-watchdog-chrome)

Standard headless Chromium image (e.g., `browserless/chrome` or `chromium` with `--remote-debugging-port=9222`). No customization needed.

### Entrypoint Script

```bash
#!/bin/bash
set -euo pipefail

INTERVAL="${WATCHDOG_INTERVAL:-3600}"  # Default 60 minutes
PROMPT_FILE="${WATCHDOG_PROMPT:-/repo/PROMPT.md}"

echo "Claude Watchdog starting. Interval: ${INTERVAL}s"

while true; do
    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] Starting health check run..."

    claude \
        --model haiku \
        --print \
        --prompt-file "$PROMPT_FILE" \
        --allowedTools "Bash,Read,Grep,Glob,Task,WebFetch" \
        2>&1 | tee -a /results/watchdog-$(date +%Y%m%d).log

    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] Run complete. Sleeping ${INTERVAL}s..."
    sleep "$INTERVAL"
done
```

Note: The exact Claude Code CLI flags may need adjustment based on the version. Key requirements are non-interactive mode, model selection, and tool restrictions.

## Project Structure

```
claude-watchdog/
├── docker-compose.yaml          # Main container + Chrome sidecar
├── Dockerfile                   # Main watchdog image
├── entrypoint.sh                # Loop + invoke Claude
├── CLAUDE.md                    # Runbook: permission tiers, check routines, safety rules
├── PROMPT.md                    # The prompt given to Haiku each run
├── .claude/
│   └── mcp.json                 # MCP server configuration (docker, postgres, mysql, redis, fetch, chrome)
├── config/
│   ├── ssh_config               # SSH client configuration
│   └── credentials.yaml         # Service credentials for browser automation (encrypted)
├── state/
│   └── cooldown.json            # Remediation cooldown state (mounted volume)
├── results/
│   └── *.log                    # Run logs (mounted volume)
└── README.md
```

## Deployment

Claude Watchdog is designed to be deployed alongside the infrastructure it monitors. For an Ansible-managed cluster:

1. Add watchdog service configuration to the inventory
2. Create a playbook that deploys via the service role
3. Mount the infrastructure repo, SSH keys, and persistent volumes
4. Set environment variables: `ANTHROPIC_API_KEY`, SMTP credentials, ntfy URL

### Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Claude API key for Claude Code CLI |
| `WATCHDOG_INTERVAL` | Seconds between runs (default: 3600) |
| `WATCHDOG_SMTP_HOST` | SMTP server hostname |
| `WATCHDOG_SMTP_PORT` | SMTP server port |
| `WATCHDOG_SMTP_USER` | SMTP username |
| `WATCHDOG_SMTP_PASS` | SMTP password |
| `WATCHDOG_SMTP_FROM` | Sender email address |
| `WATCHDOG_SMTP_TO` | Recipient email address |
| `WATCHDOG_NTFY_URL` | ntfy server URL for push notifications |
| `WATCHDOG_NTFY_TOPIC` | ntfy topic for urgent alerts |

## CLAUDE.md Runbook (Summary)

The CLAUDE.md file is the core of the system. It tells Claude:

1. **What you are**: An infrastructure monitoring agent running on a schedule
2. **How to discover services**: Parse the Ansible inventory YAML, extract enabled services, build a check list
3. **What to check**: HTTP endpoints, DNS, container state, database health, service-specific APIs
4. **Permission tiers**: What each model tier is allowed to do (see Permission Model above)
5. **How to remediate**: Step-by-step playbooks for common issues (restart container, rotate API key, redeploy via Ansible)
6. **Cooldown rules**: Read/write the cooldown state file, respect limits
7. **How to notify**: When to email, when to ntfy, what to include in each
8. **What never to do**: Delete data, modify secrets, push to git, etc.

## Future Extensions

Once the core monitoring loop is stable, the same agent architecture supports:

- **Certificate expiration monitoring**: Check TLS cert expiry dates, alert before they lapse
- **Disk space management**: Monitor volume usage, clean up known safe paths (tmp, cache, old logs)
- **Automated backup verification**: Check that backup jobs completed, verify backup integrity
- **Performance trending**: Track response times over runs, alert on degradation trends
- **Multi-host support**: Monitor multiple servers (ie01, pie01, pie02) from a single agent
- **Custom check plugins**: User-defined check scripts that Claude can discover and execute
- **Scheduled maintenance windows**: Suppress alerts during known maintenance periods
