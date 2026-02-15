# Claude Ops

AI-powered infrastructure monitoring and remediation. Claude Code runs on a schedule, discovers your services, health-checks everything, and fixes what it can — escalating to smarter (more expensive) models only when something is actually broken.

![Claude Ops Dashboard](docs/screenshot.png)

## How It Works

Claude Ops runs as a Docker container with a Go supervisor process. Every 60 minutes (configurable), it:

1. **Discovers** your infrastructure by scanning mounted repos for service definitions
2. **Checks** every service — HTTP endpoints, DNS, container state, databases, service-specific APIs
3. **Escalates** if issues are found, using progressively more capable (and costly) models
4. **Remediates** within safety guardrails — restarting containers, rotating API keys, redeploying services
5. **Notifies** you via [Apprise](https://github.com/caronc/apprise) (80+ services: email, ntfy, Slack, Discord, Telegram, etc.)
6. **Tracks** every session, health check, event, and remediation action in a real-time web dashboard

### Model Escalation

```mermaid
flowchart TD
    START([Every 60 min]) --> HAIKU

    subgraph TIER1["Tier 1 — Haiku (~$0.01-0.05/run)"]
        HAIKU[Discover services\nHealth check everything]
    end

    HAIKU --> HEALTHY{All healthy?}
    HEALTHY -->|Yes| LOG[Log results + exit]
    HEALTHY -->|No| SONNET

    subgraph TIER2["Tier 2 — Sonnet (on-demand)"]
        SONNET[Investigate logs\nRestart containers\nRotate API keys]
    end

    SONNET --> FIXED{Fixed?}
    FIXED -->|Yes| NOTIFY1[Notify: auto-remediated]
    FIXED -->|No| OPUS

    subgraph TIER3["Tier 3 — Opus (on-demand)"]
        OPUS[Run Ansible/Helm\nOrchestrate multi-service recovery\nDatabase repair]
    end

    OPUS --> RESOLVED{Resolved?}
    RESOLVED -->|Yes| NOTIFY2[Notify: fixed by Tier 3]
    RESOLVED -->|No| HUMAN[Notify: needs human attention]
```

On a healthy day, you spend ~$1-2 running 24 Haiku checks. Sonnet and Opus tokens are only spent when something is broken.

## Features

- **Web dashboard**: Real-time session viewer with live CLI output streaming via SSE. Browse session history, health check results, events, cooldown state, and configuration — all from a single UI.
- **Tiered model escalation**: Haiku observes, Sonnet investigates and applies safe fixes, Opus handles full redeployments. Each tier has strictly enforced permissions.
- **Automation-agnostic**: Works with Ansible, Docker Compose, Helm, or no automation at all. Mount your repos and Claude figures out the rest.
- **Repo discovery and extensions**: Mount any number of infrastructure repos under `/repos/`. Each can include a `CLAUDE-OPS.md` manifest and `.claude-ops/` directory with custom checks, playbooks, skills, and MCP server configs.
- **Cooldown safety**: Max 2 restarts per service per 4 hours. Max 1 redeployment per 24 hours. Exceeding limits triggers a "needs human attention" alert instead of retrying.
- **Built-in health checks**: HTTP endpoints, DNS resolution, Docker container state, PostgreSQL/Redis/MySQL connectivity, and service-specific APIs (Sonarr, Radarr, Jellyfin, etc.).
- **Built-in playbooks**: Container restart, full redeployment via Ansible/Helm, and API key rotation (including browser automation for web UIs without APIs).
- **Notifications via Apprise**: One env var, 80+ notification services. Email, ntfy, Slack, Discord, Telegram, PagerDuty, and more.
- **Browser automation**: Optional Chrome sidecar for interacting with web UIs that don't have APIs (e.g., rotating API keys from provider dashboards). Four security layers: credential injection (agent never sees raw values), URL allowlist, log redaction, and incognito context isolation. See [docs/browser-automation.md](docs/browser-automation.md) for the full setup guide.
- **MCP integration**: Docker, PostgreSQL, Chrome DevTools, and Fetch MCP servers included. Repos can bring their own MCP server configs.
- **12-factor config**: Everything configured via environment variables. No config files to template.
- **Manual triggers**: Kick off an ad-hoc run from the dashboard with the "Run Now" button — no need to wait for the next scheduled interval.

## Quick Start

### 1. Clone the repo

```bash
git clone https://github.com/joestump/claude-ops.git
cd claude-ops
```

### 2. Create a `.env` file

```bash
ANTHROPIC_API_KEY=sk-ant-...

# Optional: notifications via Apprise (comma-separated URLs)
# CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/my-topic,mailto://user:pass@smtp.example.com
```

### 3. Mount your infrastructure repos

Edit `docker-compose.yaml` and add volume mounts for your repos:

```yaml
services:
  watchdog:
    volumes:
      - ./state:/state
      - ./results:/results
      - /path/to/your/ansible-repo:/repos/infra-ansible:ro
      - /path/to/your/docker-images:/repos/docker-images:ro
      # SSH key for remote host access (optional)
      # - ~/.ssh/claude-ops_ed25519:/root/.ssh/id_ed25519:ro
      # - ~/.ssh/known_hosts:/root/.ssh/known_hosts:ro
```

### 4. (Optional) Add a manifest to your repos

Drop a `CLAUDE-OPS.md` in each mounted repo to tell Claude what it is:

```markdown
# Claude Ops Manifest

This repo manages home lab infrastructure via Ansible.

## Capabilities

- **service-discovery**: Inventory at `inventory/ie.yaml`
- **redeployment**: Playbooks in `playbooks/` (Tier 3 only)

## Rules

- Never modify any files in this repo
- Always use `--limit` when running playbooks
```

### 5. Run it

```bash
docker compose up -d
```

The dashboard is available at [http://localhost:8080](http://localhost:8080). Claude will start checking your infrastructure every 60 minutes. Session logs are stored in `./results/` and the SQLite database in `./state/`.

### With browser automation

If you need Claude to interact with web UIs (e.g., rotating API keys from provider dashboards):

```bash
docker compose --profile browser up -d
```

### Dry run mode

To observe without any remediation:

```bash
CLAUDEOPS_DRY_RUN=true docker compose up
```

## Dashboard

The web dashboard runs on port 8080 and provides:

- **Overview**: At-a-glance health status across all monitored services
- **Sessions**: Full history of scheduled and manual runs with tier, model, duration, and cost
- **Session detail**: Live CLI output streaming via SSE — watch Claude work in real-time
- **Events**: Service state changes, remediation actions, and escalation decisions
- **Cooldowns**: Current cooldown state and remediation action history per service
- **Config**: Active configuration and environment variable values

Sessions can be triggered manually from the dashboard using the "Run Now" button.

## Configuration

All configuration via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | *(required)* | Claude API key |
| `CLAUDEOPS_INTERVAL` | `3600` | Seconds between scheduled runs |
| `CLAUDEOPS_TIER1_MODEL` | `haiku` | Model for health checks (Tier 1) |
| `CLAUDEOPS_TIER2_MODEL` | `sonnet` | Model for investigation + safe remediation (Tier 2) |
| `CLAUDEOPS_TIER3_MODEL` | `opus` | Model for full remediation (Tier 3) |
| `CLAUDEOPS_DRY_RUN` | `false` | Observe only, no remediation |
| `CLAUDEOPS_REPOS_DIR` | `/repos` | Parent directory for mounted repos |
| `CLAUDEOPS_STATE_DIR` | `/state` | Persistent state directory (SQLite DB + cooldown JSON) |
| `CLAUDEOPS_RESULTS_DIR` | `/results` | Session log output directory |
| `CLAUDEOPS_APPRISE_URLS` | *(disabled)* | Comma-separated [Apprise URLs](https://github.com/caronc/apprise/wiki) for notifications |
| `CLAUDEOPS_DASHBOARD_PORT` | `8080` | HTTP port for the web dashboard |
| `CLAUDEOPS_ALLOWED_TOOLS` | `Bash,Read,Grep,Glob,Task,WebFetch` | Claude CLI tools to enable |
| `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS` | *(disabled)* | Comma-separated origins for browser automation (e.g., `https://sonarr.example.com`) |
| `BROWSER_CRED_{SERVICE}_{FIELD}` | *(none)* | Service credentials for browser login. `{SERVICE}` = uppercase name, `{FIELD}` = `USER`, `PASS`, `TOKEN`, or `API_KEY` |

## Architecture

Claude Ops has two layers: a **Go supervisor** that manages scheduling, the database, and the web dashboard, and **Claude Code agents** that do the actual infrastructure work.

### Go Supervisor (`cmd/claudeops`)

The Go binary handles everything outside of the AI:

- **Session scheduling**: Recurring timer + ad-hoc trigger channel for manual runs
- **SQLite database**: Stores sessions, health checks, events, and cooldown actions
- **Web dashboard**: HTML templates served over HTTP with SSE for real-time output streaming
- **MCP config merging**: Combines `.claude-ops/mcp.json` from all mounted repos before each run
- **Process management**: Spawns `claude` CLI as a subprocess with the appropriate tier prompt

### Claude Code Agents (Tiered)

The agents are defined entirely in markdown prompts and executed by the Claude Code CLI:

- **Tier 1** (`prompts/tier1-observe.md`): Discovers repos, reads manifests, runs health checks from `checks/`, evaluates results, escalates if needed
- **Tier 2** (`prompts/tier2-investigate.md`): Investigates failures, checks logs, applies safe remediations from `playbooks/`, re-verifies, escalates if needed
- **Tier 3** (`prompts/tier3-remediate.md`): Full remediation — Ansible playbooks, Helm upgrades, multi-service orchestration, database recovery

### Permission Tiers

| | Tier 1 (Haiku) | Tier 2 (Sonnet) | Tier 3 (Opus) |
|---|---|---|---|
| Read files, configs, logs | Yes | Yes | Yes |
| HTTP/DNS health checks | Yes | Yes | Yes |
| Query databases (read-only) | Yes | Yes | Yes |
| Restart containers | No | Yes | Yes |
| Fix file permissions | No | Yes | Yes |
| Browser automation | No | Yes | Yes |
| Send notifications | No | Yes | Yes |
| Run Ansible/Helm | No | No | Yes |
| Recreate containers | No | No | Yes |
| Multi-service orchestration | No | No | Yes |

**Never allowed (any tier):** Delete data volumes, modify repo files, change passwords/secrets, modify network config, push to git, `docker system prune`.

### Built-in Health Checks

Health checks are markdown files in `checks/` that Claude reads and executes:

| Check | File | What it does |
|-------|------|-------------|
| HTTP | `checks/http.md` | Curl endpoints, check status codes, track response times |
| DNS | `checks/dns.md` | Verify hostname resolution with dig |
| Containers | `checks/containers.md` | Check running/health status, detect crashloops |
| Databases | `checks/databases.md` | PostgreSQL, Redis, MySQL connectivity and stats |
| Services | `checks/services.md` | API-specific health (Sonarr, Radarr, Jellyfin, etc.) |

### Built-in Playbooks

Remediation procedures in `playbooks/` that Claude follows:

| Playbook | File | Tier | What it does |
|----------|------|------|-------------|
| Restart | `playbooks/restart-container.md` | 2+ | Docker restart with pre/post health verification |
| Redeploy | `playbooks/redeploy-service.md` | 3 | Full redeployment via Ansible/Helm |
| Rotate API Key | `playbooks/rotate-api-key.md` | 2+ | Browser automation for credential rotation |

## Extending Claude Ops

### Custom checks, playbooks, skills, and MCP servers

Any mounted repo can include a `.claude-ops/` directory with extensions:

```
your-repo/
├── CLAUDE-OPS.md                   # Manifest
├── .claude-ops/
│   ├── checks/                     # Additional health checks
│   │   └── verify-backups.md
│   ├── playbooks/                  # Repo-specific remediation
│   │   └── fix-media-perms.md
│   ├── skills/                     # Custom capabilities
│   │   └── refresh-ssl-certs.md
│   └── mcp.json                    # Additional MCP server configs
```

Extensions from all repos are combined at runtime. See [docs/repo-mounting.md](docs/repo-mounting.md) for the full spec.

### Custom MCP servers

The base image ships with MCP servers for Docker, PostgreSQL, Chrome DevTools, and Fetch. Repos can bring additional MCP configs via `.claude-ops/mcp.json` — these are merged with the baseline at startup.

## Project Structure

```
claude-ops/
├── cmd/claudeops/                  # Go supervisor binary
│   └── main.go
├── internal/                       # Go packages
│   ├── config/                     # Environment + flag config (Viper)
│   ├── session/                    # Session scheduler + ad-hoc triggers
│   ├── db/                         # SQLite (sessions, health checks, events, cooldowns)
│   ├── web/                        # HTTP dashboard + SSE streaming
│   │   ├── templates/              # HTML templates (layout, sessions, events, etc.)
│   │   └── static/                 # CSS, SVG assets
│   ├── hub/                        # SSE message hub with per-session circular buffers
│   └── mcp/                        # MCP config merging logic
├── prompts/                        # Tier prompt files (read by Claude CLI)
│   ├── tier1-observe.md
│   ├── tier2-investigate.md
│   └── tier3-remediate.md
├── checks/                         # Health check instructions (read by Claude)
│   ├── http.md
│   ├── dns.md
│   ├── containers.md
│   ├── databases.md
│   └── services.md
├── playbooks/                      # Remediation procedures (read by Claude)
│   ├── restart-container.md
│   ├── redeploy-service.md
│   └── rotate-api-key.md
├── docs/
│   ├── adrs/                       # 15 Architecture Decision Records
│   ├── openspec/                   # OpenSpec specifications
│   ├── repo-mounting.md            # Full guide to mounting repos
│   └── screenshot.png              # Dashboard screenshot
├── website/                        # Docusaurus documentation site
├── Dockerfile                      # Multi-stage: Go build + node:22-slim runtime
├── docker-compose.yaml             # Watchdog + optional Chrome sidecar
├── entrypoint.sh                   # Legacy shell entrypoint (replaced by Go binary)
├── CLAUDE.md                       # Safety runbook (permission tiers, cooldown rules)
├── Makefile                        # Build, test, run, Docker targets
├── go.mod / go.sum                 # Go dependencies
├── .github/workflows/
│   ├── ci.yaml                     # Lint + test + build + deploy docs
│   └── release.yaml                # Build + push Docker image to GHCR
├── state/                          # Persistent state (SQLite DB + cooldown JSON)
└── results/                        # Session logs
```

## Development

```bash
# Build the Go binary
make build

# Run tests
make test

# Run locally (uses /tmp for state/results/repos)
make run

# Run locally in dry-run mode
make run-dry

# Build Docker image
make docker-build

# Start with Docker Compose
make docker-up

# Stop
make docker-down
```

Requires Go 1.24+ for local development. The Docker build handles everything.

## CI/CD

GitHub Actions workflows:

- **ci.yaml**: Runs on push to `main` and PRs. Lints (`go vet` + `golangci-lint`), tests (`go test -race`), builds (Go binary + Docker image), and deploys the documentation site to GitHub Pages.
- **release.yaml**: Runs on push to `main` or version tags. Builds and pushes the Docker image to `ghcr.io/joestump/claude-ops` with semantic version tags.

## License

MIT
