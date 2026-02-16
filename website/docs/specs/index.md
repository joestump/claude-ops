---
sidebar_position: 1
sidebar_label: Overview
---

# Specifications

OpenSpec specifications define the detailed requirements and design for each component of Claude Ops. Each spec includes an RFC 2119 requirements document and an architecture design document.

| Spec | Component |
|------|-----------|
| [Tiered Model Escalation](tiered-model-escalation) | Cost-optimized model selection |
| [Markdown Executable Instructions](markdown-executable-instructions) | Checks and playbooks as markdown |
| [Prompt-Based Permissions](prompt-based-permissions) | Safety enforcement via prompts |
| [Apprise Notifications](apprise-notifications) | Notification abstraction layer |
| [Mounted Repo Extensions](mounted-repo-extensions) | `.claude-ops/` extension directories |
| [MCP Infrastructure Bridge](mcp-infrastructure-bridge) | MCP servers for system access |
| [JSON Cooldown State](json-cooldown-state) | Rate limiting via JSON state |
| [Go HTMX Dashboard](go-htmx-dashboard) | Real-time web dashboard |
| [Docker Compose Deployment](docker-compose-deployment) | Container orchestration |
| [Claude Code CLI Subprocess](claude-code-cli-subprocess) | CLI subprocess management |
| [Session CLI Output](session-cli-output) | CLI output streaming to dashboard |
