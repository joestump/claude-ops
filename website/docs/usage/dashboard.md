---
sidebar_position: 5
---

# Web Dashboard

The web dashboard runs on port 8080 (configurable via `CLAUDEOPS_DASHBOARD_PORT`) and provides real-time visibility into Claude Ops activity.

## TL;DR

The TL;DR page (formerly Overview) shows an LLM-generated summary of the latest session — key findings and actions at a glance. When a session completes, a fast model (Haiku by default) summarizes the full response into 2–4 sentences. If no summary is available, the page falls back to showing the full session response.

## Sessions

![Session detail showing a Tier 2 investigation report](/img/screenshots/claude-ops-sessions-01.png)

Full history of scheduled and manual runs. Each session shows:

- **Tier**: Which model tier handled the session (Haiku, Sonnet, Opus)
- **Model**: The specific model used
- **Duration**: How long the session ran
- **Cost**: Token cost for the session
- **Status**: Success, failure, or in-progress

### Session detail

Click any session to see its full output. The detail view streams Claude's CLI output in real-time via Server-Sent Events (SSE) — you can watch Claude work as it happens.

### Manual triggers

Click the **Run Now** button to kick off an ad-hoc session without waiting for the next scheduled interval.

## Events

Service state changes, remediation actions, and escalation decisions. Events are tagged by type:

- **Health check** results (pass/fail)
- **Remediation** actions taken (restart, redeploy, etc.)
- **Escalation** decisions (Tier 1 → 2, Tier 2 → 3)
- **Notifications** sent

## Cooldowns

Current cooldown state and remediation action history per service. Shows:

- How many restarts remain in the current 4-hour window
- Whether a redeployment has been used in the current 24-hour window
- When cooldowns reset

## Memories

![Memories page showing operational knowledge entries](/img/screenshots/claude-ops-memories-01.png)

Persistent operational knowledge that the agent learns across sessions. Memories are categorized by type (timing, dependency, behavior, remediation, maintenance) and scoped to specific services or global. Confidence scores decay over time if not reinforced.

## Config

Active configuration and environment variable values. Useful for verifying that your settings are applied correctly. Sensitive values (API keys) are redacted.
