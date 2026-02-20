---
sidebar_position: 2
---

# Quick Start

Get Claude Ops running in 5 minutes with Docker Compose.

## Prerequisites

- Docker and Docker Compose
- An [Anthropic API key](https://console.anthropic.com/)
- One or more infrastructure repos to monitor (Ansible, Docker Compose, Helm, etc.)

## 1. Clone the repo

```bash
git clone https://github.com/joestump/claude-ops.git
cd claude-ops
```

## 2. Configure environment

```bash
cp .env.example .env
```

Edit `.env` and add your Anthropic API key:

```env
ANTHROPIC_API_KEY=sk-ant-...
```

See [Configuration](./configuration) for the full list of environment variables.

## 3. Mount your infrastructure repos

```bash
cp docker-compose.override.yaml.example docker-compose.override.yaml
```

Edit `docker-compose.override.yaml` and uncomment or add volume mounts for your repos:

```yaml
services:
  watchdog:
    volumes:
      - /path/to/your/ansible-repo:/repos/infra-ansible:ro
      - /path/to/your/docker-images:/repos/docker-images:ro
```

:::warning Always mount read-only
Use `:ro` (read-only) by default. Claude runs commands against remote hosts via SSH, not local files. Only mount read-write if Claude genuinely needs write access (rare).
:::

## 4. (Optional) Add a manifest to your repos

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

See [Connecting Repos](./repo-mounting) for the full manifest spec and extension directory structure.

## 5. Run it

```bash
docker compose up -d
```

The [web dashboard](./dashboard) is available at [http://localhost:8080](http://localhost:8080). Claude will start checking your infrastructure every 60 minutes.

### With browser automation

To enable the Chrome sidecar for web UI interactions (API key rotation, etc.):

```bash
docker compose --profile browser up -d
```

## What happens next

1. The Go supervisor starts and serves the web dashboard on port 8080
2. On the first interval tick, Claude discovers your mounted repos
3. Tier 1 (Haiku) runs health checks against all discovered services
4. If everything is healthy, it logs results and sleeps until the next interval
5. If something is broken, it escalates to Tier 2 (Sonnet) for investigation and safe fixes
6. If Tier 2 can't fix it, Tier 3 (Opus) runs full remediation (Ansible playbooks, Helm upgrades)
7. You get [notified via Apprise](./notifications) at each step (if configured)

## Development mode

For local development with hot-reload and dry-run mode:

```bash
make dev        # foreground with logs
make dev-up     # background
make dev-logs   # tail logs
make dev-down   # stop
```

:::tip
The development override sets `CLAUDEOPS_DRY_RUN=true` by default and starts the Chrome sidecar automatically — no need for the `--profile browser` flag.
:::
