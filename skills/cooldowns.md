<!-- Governing: SPEC-0007 REQ-1 (state file location), REQ-10 (agent tooling), REQ-11 (human readability) -->
# Skill: Cooldown Management

Read the cooldown state file at `$CLAUDEOPS_STATE_DIR/cooldown.json` (default: `/state/cooldown.json`) before taking any remediation action. The file is valid JSON, readable and writable with `cat`, `jq`, or `python3` -- no custom parsers needed.

## Limits

- **Max 2 container restarts** per service per 4-hour window
- **Max 1 full redeployment** (Ansible/Helm) per service per 24-hour window
- If the cooldown limit is exceeded: stop retrying, send a notification marked "needs human attention"
- Reset counters when a service is confirmed healthy for 2 consecutive checks
- Always update the state file after any remediation attempt or health check

<!-- Governing: SPEC-0007 REQ-13 — Cooldown State Data Model -->
## State File Schema

Top-level structure:
- `services`: object mapping service names to service state objects
- `last_run`: ISO 8601 UTC timestamp string, or `null`
- `last_daily_digest`: ISO 8601 UTC timestamp string, or `null`

Each service state object contains:
- `restarts`: array of action records (each with `timestamp` and `success`)
- `redeployments`: array of action records (each with `timestamp` and `success`)
- `consecutive_healthy`: integer counting consecutive healthy checks

Action records MAY include an `error` field for failed actions.

```json
{
  "services": {
    "nginx": {
      "restarts": [
        { "timestamp": "2026-02-16T08:15:00Z", "success": true },
        { "timestamp": "2026-02-16T10:30:00Z", "success": false, "error": "OOM killed after restart" }
      ],
      "redeployments": [],
      "consecutive_healthy": 0
    },
    "postgres": {
      "restarts": [],
      "redeployments": [
        { "timestamp": "2026-02-15T22:00:00Z", "success": true }
      ],
      "consecutive_healthy": 1
    }
  },
  "last_run": "2026-02-16T10:00:00Z",
  "last_daily_digest": "2026-02-16T08:00:00Z"
}
```

Cooldown windows are evaluated as sliding windows over the action record arrays: count entries with timestamps within the last 4 hours (restarts) or 24 hours (redeployments).

<!-- Governing: SPEC-0007 REQ-14 — Tier-Specific Access Patterns -->
## Per-Tier Rules

### Tier 1 (Observe) — Step 4 of tier1-observe.md
- Read cooldown state, note services in cooldown
- Do NOT modify cooldown counters (Tier 1 does not remediate)
- Update `last_run` timestamp after checks complete
- Update `last_daily_digest` when a digest is sent
- Increment/reset `consecutive_healthy` per service based on health check results

### Tier 2 (Investigate & Remediate) — Step 3 of tier2-investigate.md
- Read cooldown state before any remediation
- Check restart count (entries in `restarts` array within last 4 hours): if >= 2, skip and notify
- After each restart attempt, append a record to the service's `restarts` array
- Do NOT perform redeployments (Tier 2 limit)

### Tier 3 (Full Remediation) — Step 3 of tier3-remediate.md
- Read cooldown state before any remediation
- Check redeployment count (entries in `redeployments` array within last 24 hours): if >= 1, skip and notify
- After each redeployment, append a record to the service's `redeployments` array
- May also restart containers (same 2-per-4h limit applies)

<!-- Governing: SPEC-0007 REQ-12 — Single-Writer Execution Model -->
## Concurrency Model

Only one agent container writes to the cooldown state file at a time. The entrypoint runs a single-threaded loop, and subagents (Tier 2, Tier 3) are spawned synchronously -- the parent waits for the child to complete before continuing. No file locking is required.

Running multiple agent containers against the same state volume is NOT supported and may cause data corruption.

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
