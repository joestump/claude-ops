<!-- Governing: SPEC-0007 REQ-1 (state file location), REQ-10 (agent tooling), REQ-11 (human readability) -->
# Skill: Cooldown Management

Read the cooldown state file at `$CLAUDEOPS_STATE_DIR/cooldown.json` (default: `/state/cooldown.json`) before taking any remediation action. The file is valid JSON, readable and writable with `cat`, `jq`, or `python3` -- no custom parsers needed.

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

## Cooldown Markers

After every remediation attempt, emit a cooldown marker on its own line so the dashboard can track it. The marker is parsed automatically and stored in the database.

### Format

```
[COOLDOWN:restart:service-name] success — Restarted container, now healthy
[COOLDOWN:restart:service-name] failure — Restarted but still unhealthy
[COOLDOWN:redeployment:service-name] success — Ansible redeploy completed, service recovered
[COOLDOWN:redeployment:service-name] failure — Redeploy failed, OOM kill persists
```

- **Action type**: `restart` or `redeployment`
- **Service name**: alphanumeric, hyphens, underscores (e.g. `jellyfin`, `adguard-home`)
- **Result**: `success` or `failure`
- **Message**: free-text description after the dash separator (`—`, `–`, or `-`)

### When to Emit

- After every `docker restart` attempt
- After every `docker compose up -d` attempt
- After every Ansible playbook or Helm upgrade
- Always include the outcome and a brief explanation

## When Cooldown is Exceeded

- Do NOT retry the remediation
- Send an Apprise notification (if configured) indicating "needs human attention"
- Include: what service, what was tried, how many attempts, when cooldown resets
