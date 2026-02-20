---
sidebar_position: 3
---

# Configuration

All configuration is via environment variables. No config files to template.

## Required

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Claude API key (or LiteLLM proxy key) |

## Core Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAUDEOPS_INTERVAL` | `3600` | Seconds between scheduled runs |
| `CLAUDEOPS_DRY_RUN` | `false` | Observe only, no remediation |
| `CLAUDEOPS_REPOS_DIR` | `/repos` | Parent directory for [mounted repos](./repo-mounting) |
| `CLAUDEOPS_STATE_DIR` | `/state` | Persistent state directory (SQLite DB + cooldown JSON) |
| `CLAUDEOPS_RESULTS_DIR` | `/results` | Session log output directory |
| `CLAUDEOPS_DASHBOARD_PORT` | `8080` | HTTP port for the [web dashboard](./dashboard) |
| `CLAUDEOPS_ALLOWED_TOOLS` | `Bash,Read,Grep,Glob,Task,WebFetch` | Claude CLI tools to enable |

## Model Selection

Each tier can use a different model. The defaults balance cost and capability:

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAUDEOPS_TIER1_MODEL` | `haiku` | Health checks (Tier 1) — cheapest, runs every interval |
| `CLAUDEOPS_TIER2_MODEL` | `sonnet` | Investigation + safe remediation (Tier 2) — on-demand |
| `CLAUDEOPS_TIER3_MODEL` | `opus` | Full remediation (Tier 3) — on-demand, most capable |
| `CLAUDEOPS_SUMMARY_MODEL` | `haiku` | Model for generating [TL;DR session summaries](./dashboard#tldr) |

### Cost implications

:::tip
On a healthy day, only Tier 1 runs — about $1-2 for 24 Haiku checks. Costs scale with how often things break, not with how many services you monitor.
:::

- **Tier 1 (Haiku)** runs every interval (~24x/day at default). At ~$0.01-0.05 per run, that's ~$1-2/day.
- **Tier 2 (Sonnet)** only runs when Tier 1 finds issues. Typical cost: $0.05-0.50 per escalation.
- **Tier 3 (Opus)** only runs when Tier 2 can't fix the problem. Typical cost: $0.50-5.00 per escalation.

## API Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | *(required)* | API key |
| `ANTHROPIC_BASE_URL` | *(Anthropic default)* | Base URL for the API — set to your LiteLLM/proxy URL |

### Using with LiteLLM or other proxies

Claude Ops works with [LiteLLM](https://github.com/BerriAI/litellm) or any Anthropic-compatible API proxy:

```env
ANTHROPIC_API_KEY=sk-your-litellm-key
ANTHROPIC_BASE_URL=https://litellm.example.com
```

:::warning Bedrock users
If your LiteLLM routes to AWS Bedrock, ensure your model deployments use inference profile ARNs (not raw model IDs) and that `drop_params: true` is set to strip unsupported beta headers.
:::

## Git Provider / PR Creation

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAUDEOPS_PR_ENABLED` | `false` | Enable PR creation via MCP and REST API |
| `CLAUDEOPS_MAX_TIER` | `3` | Maximum escalation tier (1-3) |
| `GITHUB_TOKEN` | *(none)* | GitHub personal access token for PR operations |
| `GITEA_URL` | *(none)* | Gitea instance URL |
| `GITEA_TOKEN` | *(none)* | Gitea API token |

PR creation is **disabled by default**. When disabled, the `create_pr` MCP tool is not registered and the REST API returns 403. Read-only operations (listing PRs, checking PR status) are always available. To enable:

```env
CLAUDEOPS_PR_ENABLED=true
GITHUB_TOKEN=ghp_...
```

## Notifications

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAUDEOPS_APPRISE_URLS` | *(disabled)* | Comma-separated [Apprise URLs](https://github.com/caronc/apprise/wiki) |

See [Notifications](./notifications) for setup details and common URL formats.

## Browser Automation

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS` | *(disabled)* | Comma-separated origins for browser automation |
| `BROWSER_CRED_{SERVICE}_{FIELD}` | *(none)* | Service credentials. `{SERVICE}` = uppercase name, `{FIELD}` = `USER`, `PASS`, `TOKEN`, or `API_KEY` |

Browser automation requires the Chrome sidecar (`docker compose --profile browser up -d`). When `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS` is empty, browser automation is disabled entirely.

:::info
Credentials are injected into the browser session at runtime — the Claude agent never sees raw passwords or tokens. See the [browser automation docs](https://github.com/joestump/claude-ops/blob/main/docs/browser-automation.md) for the full security model.
:::

## Example `.env`

```env
# Required
ANTHROPIC_API_KEY=sk-ant-...

# Monitoring interval (1 hour)
CLAUDEOPS_INTERVAL=3600

# Models (defaults shown)
CLAUDEOPS_TIER1_MODEL=haiku
CLAUDEOPS_TIER2_MODEL=sonnet
CLAUDEOPS_TIER3_MODEL=opus

# Notifications (ntfy example)
CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/my-claude-ops-topic

# PR creation (disabled by default)
# CLAUDEOPS_PR_ENABLED=true
# GITHUB_TOKEN=ghp_...

# Browser automation
CLAUDEOPS_BROWSER_ALLOWED_ORIGINS=https://sonarr.example.com,https://prowlarr.example.com
BROWSER_CRED_SONARR_USER=admin
BROWSER_CRED_SONARR_PASS=your-sonarr-password
```
