---
sidebar_position: 5
---

# Web Dashboard

The web dashboard runs on port 8080 (configurable via `CLAUDEOPS_DASHBOARD_PORT`) and provides real-time visibility into Claude Ops activity.

## Overview

The overview page shows at-a-glance health status across all monitored services, including recent events, memory entries, and cooldown state.

## Sessions

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

## Config

Active configuration and environment variable values. Useful for verifying that your settings are applied correctly. Sensitive values (API keys) are redacted.
