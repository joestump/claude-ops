# Claude Ops

AI-powered infrastructure monitoring and remediation. Claude Code runs on a schedule, discovers your services, health-checks everything, and fixes what it can — escalating to smarter (more expensive) models only when something is actually broken.

## How It Works

Claude Ops runs as a Docker container. Every 60 minutes (configurable), it:

1. **Discovers** your infrastructure by scanning mounted repos for service definitions
2. **Checks** every service — HTTP endpoints, DNS, container state, database health, service-specific APIs
3. **Escalates** if issues are found, using progressively more capable (and costly) models
4. **Remediates** within safety guardrails — restarting containers, rotating API keys, redeploying services
5. **Notifies** you via [Apprise](https://github.com/caronc/apprise) (80+ services: email, ntfy, Slack, Discord, Telegram, etc.)

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

- **Automation-agnostic**: Works with Ansible, Docker Compose, Helm, or no automation at all. Mount your repos and Claude figures out the rest.
- **Repo discovery**: Mount any number of infrastructure repos under `/repos/`. Each can include a `CLAUDE-OPS.md` manifest and `.claude-ops/` directory with custom checks, playbooks, skills, and MCP server configs.
- **Tiered permissions**: Haiku can only observe. Sonnet can restart containers and rotate keys. Opus can run full redeployments. Destructive actions are never allowed.
- **Cooldown safety**: Max 2 restarts per service per 4 hours. Max 1 redeployment per 24 hours. Exceeding limits triggers a "needs human attention" alert instead of retrying.
- **12-factor config**: Everything configured via environment variables. No config files to template.
- **Browser automation**: Optional Chrome sidecar for interacting with web UIs that don't have APIs (e.g., rotating API keys from provider dashboards).
- **Extensible via MCP**: Docker, PostgreSQL, Chrome DevTools, and Fetch MCP servers included. Repos can bring their own MCP server configs.
- **Notifications via Apprise**: One env var, 80+ notification services. Email, ntfy, Slack, Discord, Telegram, PagerDuty, and more.

## Quick Start

### 1. Clone the repo

```bash
git clone https://github.com/your-org/claude-ops.git
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

That's it. Claude will start checking your infrastructure every 60 minutes. Logs go to `./results/`.

### Dry run mode

To observe without any remediation:

```bash
CLAUDEOPS_DRY_RUN=true docker compose up
```

### With browser automation

If you need Claude to interact with web UIs (e.g., rotating API keys):

```bash
docker compose --profile browser up -d
```

## Configuration

All configuration via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | *(required)* | Claude API key |
| `CLAUDEOPS_INTERVAL` | `3600` | Seconds between runs |
| `CLAUDEOPS_TIER1_MODEL` | `haiku` | Model for health checks (Tier 1) |
| `CLAUDEOPS_TIER2_MODEL` | `sonnet` | Model for investigation + safe remediation (Tier 2) |
| `CLAUDEOPS_TIER3_MODEL` | `opus` | Model for full remediation (Tier 3) |
| `CLAUDEOPS_DRY_RUN` | `false` | Observe only, no remediation |
| `CLAUDEOPS_REPOS_DIR` | `/repos` | Parent directory for mounted repos |
| `CLAUDEOPS_STATE_DIR` | `/state` | Cooldown state directory |
| `CLAUDEOPS_RESULTS_DIR` | `/results` | Log output directory |
| `CLAUDEOPS_APPRISE_URLS` | *(disabled)* | Comma-separated [Apprise URLs](https://github.com/caronc/apprise/wiki) for notifications |

## Project Structure

```
claude-ops/
├── Dockerfile                      # node:22-slim + Claude Code CLI + Apprise
├── docker-compose.yaml             # Watchdog + optional Chrome sidecar
├── entrypoint.sh                   # Scheduling loop
├── CLAUDE.md                       # Safety runbook (permission tiers, cooldown rules)
├── CLAUDE-OPS.md                   # This repo's own manifest
├── prompts/
│   ├── tier1-observe.md            # Haiku: discover + check
│   ├── tier2-investigate.md        # Sonnet: investigate + safe remediation
│   └── tier3-remediate.md          # Opus: full remediation
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
│   └── repo-mounting.md            # Full guide to mounting repos
├── state/                          # Cooldown state (persistent volume)
└── results/                        # Run logs (persistent volume)
```

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

The base image ships with MCP servers for Docker, PostgreSQL, Chrome DevTools, and Fetch. Repos can bring additional MCP configs via `.claude-ops/mcp.json` — these are merged with the baseline at startup. You can also edit `.claude/mcp.json` directly to add or remove servers globally.

## License

MIT
