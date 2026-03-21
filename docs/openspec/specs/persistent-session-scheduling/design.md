# Design: Intra-Session Scheduled Follow-Ups

## Overview

This design describes how Claude Ops extends the existing shell-loop monitoring architecture with intra-session scheduling via Claude Code's `CronCreate` tool. The hybrid approach preserves the entrypoint loop as the durable monitoring heartbeat while allowing Tier 2/3 agents to schedule one-shot verification tasks after remediation actions. Sessions stay alive while tasks are pending, bounded by a supervisor-enforced maximum duration.

See [SPEC-0034](./spec.md) and [ADR-0033](../../adrs/ADR-0033-persistent-session-scheduling.md).

## Architecture

### Hybrid Architecture: Entrypoint Loop + Intra-Session CronCreate

```
┌────────────────────────────────────────────────────────────────────┐
│  Go Supervisor (session manager)                                   │
│                                                                    │
│  ┌──────────────────────────────────────────────────────────────┐ │
│  │  while true:                                                  │ │
│  │    1. Spawn claude -p session (Tier 1)                        │ │
│  │    2. Monitor session process                                 │ │
│  │    3. Enforce CLAUDEOPS_MAX_SESSION_DURATION (default 30m)    │ │
│  │    4. Wait for session exit or timeout                        │ │
│  │    5. Record session result in DB                             │ │
│  │    6. Sleep CLAUDEOPS_INTERVAL                                │ │
│  │    7. Repeat                                                  │ │
│  └──────────────────────────────────────────────────────────────┘ │
│                            │                                       │
│                            ▼                                       │
│  ┌──────────────────────────────────────────────────────────────┐ │
│  │  Claude -p session                                            │ │
│  │                                                               │ │
│  │  Tier 1: Observe → discovers issue → escalates to Tier 2     │ │
│  │                                                               │ │
│  │  Tier 2 (subagent via Task):                                  │ │
│  │    1. Investigate root cause                                  │ │
│  │    2. Remediate (docker restart, compose up, etc.)            │ │
│  │    3. CronCreate("*/10 * * * *", "verify...", once=true)     │ │
│  │    4. Session stays alive (pending task)                      │ │
│  │    5. Task fires → agent verifies health                     │ │
│  │    6. If healthy → done. If not → further fix or escalate    │ │
│  │    7. Session exits when all tasks complete                   │ │
│  └──────────────────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────────────────┘
```

### What Changes vs. Current Architecture

| Component | Before (ADR-0010) | After (ADR-0033) |
|-----------|-------------------|-------------------|
| Monitoring heartbeat | Shell loop + `sleep $INTERVAL` | **Unchanged** — shell loop + `sleep $INTERVAL` |
| Session mode | `claude -p` (single-shot, exits after completion) | `claude -p` with extended lifetime when tasks are pending |
| Session duration | Typically 2-5 minutes | 2-5 minutes (no remediation) or up to 30 minutes (with verification) |
| Post-remediation verification | None — wait for next cycle | CronCreate one-shot task fires within the same session |
| Tier 1 tools | `Bash,Read,Grep,Glob,Task,WebFetch,WebSearch` | **Unchanged** |
| Tier 2 tools | `Bash,Read,Write,Edit,Grep,Glob,Task,WebFetch,WebSearch` | **Add**: `CronCreate,CronList,CronDelete` |
| Tier 3 tools | `Bash,Read,Write,Edit,Grep,Glob,Task,WebFetch,WebSearch` | **Add**: `CronCreate,CronList,CronDelete` |
| Supervisor timeout | None (sessions exit on their own) | `CLAUDEOPS_MAX_SESSION_DURATION` enforced (default 30m) |

## Session Lifecycle with Scheduled Tasks

### Normal Flow (No Remediation Needed)

```
Time  Event
─────────────────────────────────────────────
0:00  Supervisor spawns claude -p (Tier 1)
0:01  Tier 1 runs health checks
0:02  All services healthy
0:02  Session exits (no escalation, no tasks)
0:02  Supervisor sleeps CLAUDEOPS_INTERVAL
```

No change from current behavior. CronCreate is not used because no remediation occurred.

### Remediation + Verification Flow

```
Time   Event
─────────────────────────────────────────────
 0:00  Supervisor spawns claude -p (Tier 1)
 0:01  Tier 1 discovers jellyfin is down (HTTP 503)
 0:01  Tier 1 escalates → Task spawns Tier 2 subagent
 0:02  Tier 2 investigates: docker logs, container state
 0:03  Tier 2 remediates: docker restart jellyfin
 0:03  Tier 2 schedules verification:
         CronCreate("*/10 * * * *", "Verify jellyfin...", once=true)
 0:03  Tier 2 returns to Tier 1 with remediation report
 0:03  Session has pending task → stays alive
       (Claude is idle, waiting for next cron trigger)
 0:10  Verification task fires: "Verify jellyfin..."
 0:10  Agent checks https://jellyfin.stump.rocks → HTTP 200
 0:10  Agent reports: jellyfin recovered successfully
 0:10  No pending tasks → session exits
 0:10  Supervisor records session (completed, 10 min duration)
 0:10  Supervisor sleeps CLAUDEOPS_INTERVAL
```

### Verification Failure + Escalation Flow

```
Time   Event
─────────────────────────────────────────────
 0:00  Supervisor spawns session
 0:03  Tier 2 restarts jellyfin, schedules verification
 0:10  Verification fires → jellyfin still unhealthy (HTTP 503)
 0:10  Tier 2 attempts docker compose up -d --force-recreate
 0:11  Tier 2 schedules second verification:
         CronCreate("*/10 * * * *", "Re-verify jellyfin...", once=true)
 0:21  Second verification fires → still unhealthy
 0:21  Tier 2 exhausts options → escalates to Tier 3 via Task
 0:22  Tier 3 runs ansible-playbook redeploy-jellyfin.yml
 0:23  Tier 3 schedules verification:
         CronCreate("*/10 * * * *", "Verify jellyfin after redeploy...", once=true)
 0:30  Supervisor timeout reached (CLAUDEOPS_MAX_SESSION_DURATION=30m)
 0:30  Supervisor terminates session → records as "timeout"
 0:30  Pending verification task is lost
 0:30  Supervisor sleeps CLAUDEOPS_INTERVAL
       (Next cycle will catch jellyfin if still unhealthy)
```

## Supervisor Timeout Logic

### Session Duration Tracking

The session manager tracks the start time of each session and computes elapsed duration. The supervisor uses a timer set to `CLAUDEOPS_MAX_SESSION_DURATION`:

```
func (m *Manager) runOnce(ctx context.Context) {
    // Start session
    session := m.startSession(ctx, prompt, tier)
    startTime := time.Now()
    maxDuration := m.config.MaxSessionDuration  // default 30m

    // Create timeout context
    timeoutCtx, cancel := context.WithTimeout(ctx, maxDuration)
    defer cancel()

    // Run claude -p in subprocess
    err := m.runClaude(timeoutCtx, session)

    elapsed := time.Since(startTime)
    if timeoutCtx.Err() == context.DeadlineExceeded {
        session.Status = "timeout"
        session.Duration = elapsed
    } else if err != nil {
        session.Status = "error"
    } else {
        session.Status = "completed"
    }
    session.Duration = elapsed

    m.db.UpdateSession(session)
}
```

The `context.WithTimeout` approach ensures the Claude CLI process receives a signal when the deadline is exceeded. The supervisor does not need to know about CronCreate tasks specifically — it simply enforces a wall-clock limit on the session process.

### Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `CLAUDEOPS_MAX_SESSION_DURATION` | `30m` | Maximum wall-clock time a session may run before forced termination |
| `CLAUDEOPS_INTERVAL` | `3600` (seconds) | Sleep interval between monitoring cycles (unchanged) |

The maximum session duration MUST be less than the monitoring interval. If `CLAUDEOPS_MAX_SESSION_DURATION` exceeds `CLAUDEOPS_INTERVAL`, the supervisor SHOULD log a warning, as sessions could overlap with the next intended monitoring cycle.

## Tool Access Per Tier

### Updated --allowedTools Configuration

The entrypoint sets `ALLOWED_TOOLS` per tier. ADR-0033 adds CronCreate, CronList, and CronDelete to Tier 2 and Tier 3:

```
Tier 1 — Observe Only
  ALLOWED_TOOLS: Bash,Read,Grep,Glob,Task,WebFetch,WebSearch
  (no change)

Tier 2 — Safe Remediation
  ALLOWED_TOOLS: Bash,Read,Write,Edit,Grep,Glob,Task,WebFetch,WebSearch,
                 CronCreate,CronList,CronDelete
  (added: CronCreate,CronList,CronDelete)

Tier 3 — Full Remediation
  ALLOWED_TOOLS: Bash,Read,Write,Edit,Grep,Glob,Task,WebFetch,WebSearch,
                 CronCreate,CronList,CronDelete
  (added: CronCreate,CronList,CronDelete)
```

The `--disallowedTools` lists (ADR-0023, SPEC-0027) are NOT changed. CronCreate, CronList, and CronDelete are not Bash commands — they are native Claude Code tools. They are gated entirely by `--allowedTools`, not by `--disallowedTools` patterns.

### Enforcement Boundary

```
┌─────────────────────────────────────────────────┐
│  --allowedTools (Hard Boundary)                  │
│                                                  │
│  Tier 1: CronCreate NOT listed → blocked         │
│  Tier 2: CronCreate listed → permitted           │
│  Tier 3: CronCreate listed → permitted           │
│                                                  │
│  Enforcement: Claude Code CLI binary rejects     │
│  the tool call before execution. The agent       │
│  cannot bypass this through reasoning.           │
└─────────────────────────────────────────────────┘
```

This follows the same enforcement model as all other tools (ADR-0023): `--allowedTools` is a hard boundary enforced by the CLI binary. The agent cannot use CronCreate at Tier 1 regardless of prompt instructions.

## Verification Task Creation

### Example: After Container Restart

After a Tier 2 agent restarts a container, it creates a verification task:

```
Agent action:
  Bash("ssh root@ie01 docker restart jellyfin")

Agent creates verification:
  CronCreate(
    schedule: "*/10 * * * *",
    prompt: "Verify that jellyfin is healthy after the restart performed earlier in this session. Check: curl -sL -o /dev/null -w '%{http_code}' https://jellyfin.stump.rocks — expect HTTP 200. If the service is unhealthy (non-200 response or connection refused), attempt docker compose up -d --force-recreate on ie01. If that also fails, escalate to Tier 3.",
    once: true
  )
```

### Example: After Ansible Playbook

After a Tier 3 agent runs an Ansible playbook:

```
Agent action:
  Bash("ansible-playbook -i /repos/home-cluster/ie.yaml /repos/home-cluster/playbooks/redeploy-jellyfin.yml")

Agent creates verification:
  CronCreate(
    schedule: "*/15 * * * *",
    prompt: "Verify that jellyfin is fully operational after the Ansible redeployment. Check 1: curl -sL -o /dev/null -w '%{http_code}' https://jellyfin.stump.rocks — expect HTTP 200. Check 2: ssh root@ie01 docker ps --filter name=jellyfin --format '{{.Status}}' — expect 'Up'. If either check fails, investigate docker logs and report the failure. Do not attempt further redeployment — the Ansible playbook has already been run.",
    once: true
  )
```

Note the 15-minute delay for Ansible redeployment vs. 10 minutes for a simple container restart — Ansible playbooks involve image pulls and full container recreation, which takes longer.

## Session Duration Tracking

### Database Schema Addition

The sessions table gains two columns to track scheduling-related state:

```sql
ALTER TABLE sessions ADD COLUMN has_pending_tasks BOOLEAN DEFAULT FALSE;
ALTER TABLE sessions ADD COLUMN abandoned_task_count INTEGER DEFAULT 0;
```

- `has_pending_tasks`: Set to `TRUE` when CronCreate is invoked during the session. Set to `FALSE` when all tasks have fired or the session exits.
- `abandoned_task_count`: Set to the number of unfired tasks when the session exits due to timeout or crash.

### Dashboard Display

The session detail page displays scheduling status when applicable:

```
Session #1847 — Tier 2
Status: completed (10m 23s)
Trigger: scheduled
Scheduled tasks: 1 created, 1 fired, 0 abandoned
```

For timed-out sessions:

```
Session #1848 — Tier 3
Status: timeout (30m 0s) ⚠
Trigger: scheduled
Scheduled tasks: 2 created, 1 fired, 1 abandoned
```

## Escalation Flow with Verification

### How Verification Interacts with Tier Escalation

The verification task fires within the same session that performed the remediation. The agent retains its tier context and permissions:

```
Session starts (Tier 1)
  └─ Tier 1 detects issue → escalates via Task
       └─ Tier 2 subagent (Task)
            ├─ Remediation action
            ├─ CronCreate verification
            └─ Returns to Tier 1 with report

Session stays alive (pending task from Tier 2's CronCreate)

Verification fires (runs at Tier 2 level — same subagent context)
  ├─ If healthy → done
  └─ If unhealthy → Tier 2 can:
       ├─ Attempt further remediation (within Tier 2 permissions)
       ├─ Schedule another verification
       └─ Escalate to Tier 3 via Task
```

The key detail: when the CronCreate task fires, it runs in the session context where it was created. If the task was created by a Tier 2 subagent (spawned via Task), the verification prompt runs with the same tool permissions. The `--allowedTools` for the session determine what the verification prompt can do.

## Future Evolution: Persistent Session (ADR-0032)

When channels (ADR-0032) become available (auth blocker resolved), the hybrid architecture evolves into a full persistent session:

```
Current (ADR-0033 Hybrid):
  Entrypoint loop → spawns session → session uses CronCreate for verification → session exits → loop repeats

Future (Persistent Session):
  Supervisor starts persistent session
    └─ Session uses CronCreate for monitoring heartbeat (replaces shell loop)
    └─ Channels receive external events (replaces webhook/ad-hoc endpoints)
    └─ CronCreate used for both heartbeats AND verification
    └─ Supervisor monitors session health, restarts on exit
    └─ Supervisor renews CronCreate tasks before 3-day expiry
```

The migration path:

1. **Today (ADR-0033)**: Shell loop heartbeat + CronCreate for verification only
2. **Channels available**: Shell loop replaced by persistent session + CronCreate heartbeat + channel events
3. **Mature state**: Persistent session is the primary execution model, with supervisor handling lifecycle (restart, task renewal, context window management)

ADR-0033's hybrid approach is designed to be a stepping stone. The CronCreate integration, session timeout logic, and tier tool access added now are all directly reusable in the persistent session architecture.

## Key Design Decisions

### CronCreate for verification only, not monitoring heartbeats

In the hybrid architecture, the shell loop remains the monitoring heartbeat. CronCreate is used ONLY for intra-session follow-up verification. This decision is critical because:

- The shell loop is proven, simple, and reliable — it has been the core scheduler since inception
- CronCreate tasks are session-scoped and lost on crash — unsuitable as the primary monitoring scheduler
- The 3-day expiry on recurring tasks would require supervisor renewal logic that is unnecessary in the hybrid approach
- Separating heartbeat (supervisor) from verification (agent) creates a clean responsibility boundary

### One-shot tasks only (once=true)

All verification tasks MUST be one-shot. Recurring CronCreate tasks are explicitly prohibited for agents because:

- Recurring monitoring is the supervisor's job, not the agent's
- A recurring task that fires every 10 minutes could cause the session to run indefinitely (until the max duration timeout)
- One-shot tasks have a clear completion criterion: the task fires, verification runs, the session can exit

### Supervisor timeout is a wall-clock limit, not task-aware

The supervisor does not inspect CronCreate tasks or attempt to detect pending tasks. It enforces a simple wall-clock limit on the session process. This is intentional:

- The supervisor has no API to query CronCreate tasks inside the Claude session
- A wall-clock limit is simple, predictable, and impossible to circumvent
- If tasks are abandoned due to timeout, the next monitoring cycle catches any remaining issues
- Adding task-awareness would couple the supervisor to CronCreate's internal state, which is opaque

### CronCreate gated by --allowedTools, not --disallowedTools

CronCreate is a native Claude Code tool, not a Bash command. It does not match `--disallowedTools` patterns (which are Bash command prefixes). Tool access is controlled entirely by `--allowedTools`:

- Tier 1: CronCreate not in `--allowedTools` → hard-blocked
- Tier 2: CronCreate in `--allowedTools` → permitted
- Tier 3: CronCreate in `--allowedTools` → permitted

This is consistent with how `Write`, `Edit`, and `Task` are gated — they appear in `--allowedTools` for permitted tiers and are absent for restricted tiers.

## References

- [ADR-0033: Persistent Session Architecture with Intra-Session Scheduling](../../adrs/ADR-0033-persistent-session-scheduling.md)
- [ADR-0010: Invoke Claude via Claude Code CLI as Subprocess](../../adrs/ADR-0010-claude-code-cli-subprocess.md)
- [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md)
- [ADR-0032: Channel-Based Operator Interface](../../adrs/ADR-0032-channel-operator-interface.md)
- [ADR-0013: Manual Ad-Hoc Session Runs from the Dashboard](../../adrs/ADR-0013-manual-ad-hoc-session-runs.md)
- [SPEC-0034: Intra-Session Scheduled Follow-Ups](./spec.md)
- [SPEC-0027: AllowedTools-Based Tier Enforcement](../allowedtools-tier-enforcement/spec.md)
