---
sidebar_position: 1
---

# Usage Guide

Everything you need to run Claude Ops — from first `docker compose up` to full production deployment.

## Guides

- **[Quick Start](./quickstart)** — Get Claude Ops running in 5 minutes with Docker Compose
- **[Configuration](./configuration)** — All environment variables, model selection, and tuning options
- **[Connecting Repos](./repo-mounting)** — Mount infrastructure repos and write CLAUDE-OPS.md manifests
- **[Web Dashboard](./dashboard)** — Sessions, events, memories, cooldowns, and real-time streaming
- **[Notifications](./notifications)** — Set up Apprise for email, Slack, Discord, ntfy, and 80+ other services

## How It Works

Claude Ops runs as a Docker container with a Go supervisor process. On a configurable interval (default: 60 minutes), it:

1. **Discovers** your infrastructure by scanning mounted repos for service definitions
2. **Checks** every service — HTTP endpoints, DNS, container state, databases, service-specific APIs
3. **Escalates** if issues are found, using progressively more capable (and costly) models
4. **Remediates** within safety guardrails — restarting containers, rotating API keys, redeploying services
5. **Notifies** you via Apprise (80+ services)
6. **Tracks** every session, health check, event, and remediation action in a real-time web dashboard

On a healthy day, you spend ~$1-2 running 24 Haiku checks. Sonnet and Opus tokens are only spent when something is broken.
