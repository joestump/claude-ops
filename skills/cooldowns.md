# Skill: Cooldown Management

Read the cooldown state file at `$CLAUDEOPS_STATE_DIR/cooldown.json` before taking any remediation action.

## Limits

- **Max 2 container restarts** per service per 4-hour window
- **Max 1 full redeployment** (Ansible/Helm) per service per 24-hour window
- If the cooldown limit is exceeded: stop retrying, send a notification marked "needs human attention"
- Reset counters when a service is confirmed healthy for 2 consecutive checks
- Always update the state file after any remediation attempt or health check

## State File Schema

```json
{
  "services": {
    "servicename": {
      "restart_count": 0,
      "last_restart": null,
      "redeploy_count": 0,
      "last_redeploy": null,
      "consecutive_healthy": 0,
      "last_check": null,
      "status": "healthy"
    }
  },
  "last_run": "2026-02-16T10:00:00Z",
  "last_daily_digest": "2026-02-16T08:00:00Z"
}
```

## Per-Tier Rules

### Tier 1 (Observe)
- Read cooldown state, note services in cooldown
- Do NOT modify cooldown counters (Tier 1 does not remediate)
- Update `last_run` timestamp after checks complete

### Tier 2 (Investigate & Remediate)
- Check restart count before restarting: if >= 2 in last 4 hours, skip and notify
- Increment restart count and update `last_restart` after each restart attempt
- Do NOT perform redeployments (Tier 2 limit)

### Tier 3 (Full Remediation)
- Check redeployment count before redeploying: if >= 1 in last 24 hours, skip and notify
- Increment redeploy count and update `last_redeploy` after each redeployment
- May also restart containers (same 2-per-4h limit applies)

## When Cooldown is Exceeded

- Do NOT retry the remediation
- Send an Apprise notification (if configured) indicating "needs human attention"
- Include: what service, what was tried, how many attempts, when cooldown resets
