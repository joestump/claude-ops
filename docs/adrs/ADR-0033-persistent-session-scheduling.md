---
status: proposed
date: 2026-03-21
decision-makers: Joe Stump
updates: ADR-0013, ADR-0023
---

# ADR-0033: Persistent Session Architecture with Intra-Session Scheduling

## Context and Problem Statement

Claude-Ops currently operates on a "shell loop + single-shot sessions" architecture (ADR-0010):

```bash
# entrypoint.sh (simplified)
while true; do
    claude -p "$(cat tier1-observe.md)" --allowedTools ... --disallowedTools ...
    sleep $CLAUDEOPS_INTERVAL
done
```

Each monitoring cycle spawns a fresh `claude -p` invocation. The session starts, runs health checks, potentially escalates to higher tiers, and exits. The entrypoint sleeps, then starts a new session. This has been the core architecture since inception.

This approach has fundamental limitations:

1. **No intra-session follow-up** — After a Tier 2 agent restarts a service, it cannot check back in 10 minutes to verify the fix. Verification only happens on the NEXT monitoring cycle, potentially 30+ minutes later.

2. **No event-driven triggering** — Monitoring is poll-based (fixed interval). External events (webhook alerts, operator messages) must go through separate HTTP endpoints and ad-hoc session triggering (ADR-0013, ADR-0024), which creates separate sessions disconnected from the monitoring context.

3. **Cold start every cycle** — Each session has no memory of the previous cycle. Context must be rebuilt from scratch (reading cooldown.json, querying recent events, etc.).

4. **Channel incompatibility** — Claude Code channels (ADR-0032) require a running session to receive events. Single-shot `-p` mode exits immediately, leaving no session for channels to push into.

Claude Code now supports **scheduled prompts** — session-scoped cron jobs via `CronCreate` that fire prompts while the session is running. Combined with channels (ADR-0032) and hooks (ADR-0029), this enables a fundamentally different architecture: a single persistent session that:

- Uses `CronCreate` for the monitoring heartbeat (replaces shell sleep loop)
- Receives external events via channels (replaces webhook endpoint)
- Schedules follow-up verification after remediation
- Maintains conversation context across monitoring cycles

**Important constraints of scheduled prompts:**

- Session-scoped: tasks die when the session exits
- 3-day automatic expiry on recurring tasks
- Tasks only fire when Claude is idle (between turns)
- No persistence across restarts
- 1-minute minimum granularity (cron-based)

## Decision Drivers

* **Enable post-remediation verification scheduling** — The primary motivating use case. After a Tier 2 agent restarts a container, it should be able to schedule a health check in 10 minutes to verify the fix took hold, without waiting for the next full monitoring cycle.
* **Enable event-driven monitoring via channels** — ADR-0032 defines a channel-based operator interface. Channels require a persistent running session to receive events. The session architecture must support this.
* **Maintain conversation context across monitoring cycles** — A persistent session retains the full monitoring state (check results, cooldown data, inventory) across heartbeats, eliminating cold-start overhead.
* **Reduce cold-start overhead** — Each single-shot session rebuilds context from scratch (reading cooldown.json, discovering repos, parsing inventory). A persistent session does this once.
* **The persistent session is also required by ADR-0032 (channels)** — Channels push events into a running session. Without a persistent session, channel events have nowhere to arrive.
* **Scheduled prompts are session-scoped with 3-day expiry** — The Go supervisor must handle session restarts and task renewal for any architecture that relies on CronCreate for long-running schedules.
* **Must preserve tiered escalation model and permission enforcement** — CronCreate must be subject to the same `--allowedTools`/`--disallowedTools` enforcement (ADR-0023) as all other tools.
* **Must preserve cost tracking and session recording** — The web dashboard (ADR-0008) and session lifecycle (ADR-0031) must continue to capture session data regardless of whether sessions are single-shot or persistent.

## Considered Options

1. **Persistent session with CronCreate** — Replace the entrypoint loop with a long-running interactive Claude session. The Go supervisor starts the session, and the session uses `CronCreate` to schedule its own monitoring heartbeats. The supervisor monitors the session process and restarts if it exits.

2. **Hybrid: shell loop + CronCreate within sessions** — Keep the entrypoint loop for the monitoring heartbeat, but within each session, allow agents to use `CronCreate` for follow-up verification. Sessions stay alive while scheduled tasks are pending, then exit.

3. **Keep current architecture** — No persistent session. No CronCreate. Accept the limitations of single-shot sessions.

4. **External scheduler (systemd timer / cron)** — Move scheduling out of Claude entirely, using OS-level cron to invoke `claude -p` on a schedule. Replace the shell loop with system-level scheduling.

## Decision Outcome

Chosen option: **"Hybrid: shell loop + CronCreate within sessions"**, because it addresses the most impactful limitation — no post-remediation verification — with the minimal architectural change. The shell loop remains the durable monitoring scheduler, while Tier 2/3 agents gain the ability to schedule intra-session follow-up tasks using CronCreate.

### How It Works

The entrypoint loop (`while true; sleep $INTERVAL`) remains unchanged. It continues to spawn `claude -p` sessions on a fixed interval, providing the reliable monitoring heartbeat.

The key change: Tier 2 and Tier 3 sessions can now use CronCreate to schedule verification tasks within their session. After a remediation action (container restart, compose up, ansible run), the agent schedules a one-shot verification check:

```
CronCreate("*/10 * * * *", "Verify that jellyfin is healthy at https://jellyfin.stump.rocks — expect HTTP 200. If unhealthy, attempt further remediation within tier permissions.", once=true)
```

The session stays alive while scheduled tasks are pending. After all tasks have fired (or after the maximum session duration is reached), the session exits, and the entrypoint loop resumes its normal sleep-and-restart cycle.

### Tier Tool Access

CronCreate, CronList, and CronDelete are added to `--allowedTools` for Tier 2 and Tier 3 only:

| Tier | CronCreate | CronList | CronDelete | Rationale |
|------|-----------|----------|------------|-----------|
| Tier 1 (Observe) | Not allowed | Not allowed | Not allowed | Observe-only; no remediation means no verification to schedule |
| Tier 2 (Safe Remediation) | Allowed | Allowed | Allowed | Can schedule verification after container restarts |
| Tier 3 (Full Remediation) | Allowed | Allowed | Allowed | Can schedule verification after Ansible/Helm operations |

This follows the same enforcement model as ADR-0023: tool access is controlled at the `--allowedTools` CLI boundary, not by prompt instructions.

### Session Lifecycle with Scheduled Tasks

```
Entrypoint loop iteration:
  1. Spawn claude -p session (Tier 1)
  2. Tier 1 discovers issue → escalates to Tier 2
  3. Tier 2 restarts container
  4. Tier 2 schedules verification: CronCreate("*/10 * * * *", "verify...", once=true)
  5. Session remains alive (pending task)
  6. 10 minutes later: verification fires
  7. If healthy → session completes, exits
  8. If unhealthy → Tier 2 attempts further fix or escalates to Tier 3
  9. Session exits after all tasks fire or max duration reached
  10. Entrypoint loop sleeps $INTERVAL, repeats
```

### Session Duration Enforcement

The Go supervisor enforces a maximum session duration (`CLAUDEOPS_MAX_SESSION_DURATION`, default 30 minutes). This prevents runaway sessions where an agent schedules far-future tasks or repeated verifications that never succeed:

- Sessions with pending scheduled tasks are allowed to run until all tasks fire OR the maximum duration is reached
- If the maximum duration is exceeded, the supervisor terminates the session
- The terminated session is recorded in the database with a `timeout` status
- The entrypoint loop continues normally after a timeout

### Why Not Option 1 (Full Persistent Session)?

The persistent session architecture (Option 1) is the long-term vision, especially once channels (ADR-0032) become available. However, adopting it now would be premature:

- **Channels are blocked on auth** (ADR-0032) — Channels require `claude.ai` login; Claude-Ops uses API key auth. Without channels, a persistent session loses its primary benefit (event-driven monitoring).
- **A persistent session changes the fundamental execution model** — The entire session lifecycle, logging, cost tracking, and dashboard integration would need to be redesigned for sessions that run for hours or days instead of minutes.
- **Session-scoped scheduling has a 3-day expiry** — Recurring CronCreate tasks expire after 3 days, requiring supervisor logic to renew them. This is unnecessary complexity for the hybrid approach, where tasks are one-shot verifications that fire within minutes.
- **The hybrid approach delivers the highest-value feature with minimal change** — Post-remediation verification scheduling addresses the most impactful limitation of the current architecture without requiring a fundamental architecture shift.
- **Option 1 can be adopted later as an evolution of Option 2** — When channels become available and API key auth is supported, the hybrid can evolve into a persistent session by replacing the shell loop with CronCreate-based heartbeats.

### Relationship to ADR-0032 (Channels)

When channels become available (auth blocker resolved), the full persistent session (Option 1) becomes viable and desirable. At that point:

- The shell loop is replaced by a persistent session with CronCreate for heartbeats
- Channels handle external events (replacing webhook/ad-hoc endpoints)
- The supervisor manages session lifecycle (restart on exit, 3-day task renewal)
- This ADR's hybrid approach is a natural stepping stone toward that architecture

### Consequences

**Positive:**

* Enables post-remediation verification — the single most impactful improvement to the monitoring loop. After restarting a container, the agent can verify it came back healthy in 10 minutes instead of waiting 30+ minutes for the next cycle.
* Minimal architecture change — the entrypoint loop, session lifecycle, logging, and dashboard integration remain fundamentally unchanged. CronCreate is just another tool in `--allowedTools`.
* Compatible with future persistent session migration — when channels (ADR-0032) become available, the hybrid approach evolves naturally into Option 1 by replacing the shell loop with CronCreate heartbeats.
* Follows the existing enforcement model — CronCreate access is controlled at the `--allowedTools` boundary (ADR-0023), consistent with how all other tools are gated per tier.
* Tier 1 cannot schedule tasks — observe-only agents have no remediation actions to verify, so CronCreate is appropriately excluded from their tool list.
* The supervisor's maximum session duration prevents runaway sessions where verification loops never converge.

**Negative:**

* Sessions may run significantly longer than today — a session with a 10-minute verification delay runs at least 10 minutes longer than it would without CronCreate. This affects the monitoring interval cadence.
* Supervisor needs new timeout logic — the session manager must track pending scheduled tasks and enforce a maximum session duration, adding complexity to the Go supervisor code.
* CronCreate tasks are lost if the session crashes — if the Claude CLI process exits unexpectedly before a scheduled task fires, the verification never happens. The next monitoring cycle will catch the issue, but the verification-after-remediation guarantee is best-effort.
* Tier prompts need updating — Tier 2 and Tier 3 prompt files must be updated to describe the CronCreate capability, when to use it (after remediation), and when NOT to use it (recurring monitoring, which is the supervisor's job).
* Adds complexity to session lifecycle management — the session detail page must display scheduled task status, and session completion logic must account for pending tasks.
* One-minute minimum granularity limits verification precision — CronCreate uses cron expressions, so the minimum delay is 1 minute. For most remediation scenarios (container restart, compose up), 5-10 minutes is appropriate, but sub-minute verification is not possible.

## Pros and Cons of the Options

### Persistent Session with CronCreate

Replace the entrypoint loop with a single long-running Claude session. The supervisor starts the session in interactive mode (not `-p`). The session uses `CronCreate` to schedule its own monitoring heartbeats. The supervisor monitors the session process and restarts if it exits. This becomes the session model for channels (ADR-0032) as well.

* Good, because it enables event-driven monitoring via channels — external events (Telegram messages, webhook alerts) arrive directly in the running session.
* Good, because it maintains full conversation context across monitoring cycles — the agent knows what it checked last time, what it fixed, and what is still degraded.
* Good, because it eliminates cold-start overhead — repo discovery, inventory parsing, and cooldown state reading happen once, not every cycle.
* Good, because it aligns with the channel architecture (ADR-0032), which requires a persistent session.
* Good, because CronCreate-based heartbeats can be dynamically adjusted — the agent can increase monitoring frequency when issues are detected and decrease it when everything is healthy.
* Good, because post-remediation verification is natural — the agent schedules follow-up tasks in the same session context where it performed the remediation.
* Bad, because channels are blocked on auth (ADR-0032) — adopting this architecture now gains the cold-start benefit but not the event-driven benefit, which is the primary motivator.
* Bad, because it changes the fundamental execution model from short-lived, isolated sessions to a long-running, stateful session, requiring significant changes to logging, cost tracking, dashboard integration, and session lifecycle management.
* Bad, because CronCreate tasks expire after 3 days — the supervisor must detect expiry and recreate tasks, adding complexity.
* Bad, because a single persistent session is a single point of failure — if the session enters a bad state (context window exhaustion, model confusion), there is no clean boundary to reset. The supervisor must detect degradation and force-restart the session.
* Bad, because cost tracking becomes harder — a single multi-hour session accumulates costs that are difficult to attribute to specific monitoring cycles.
* Bad, because the session's context window grows over time — after hours of monitoring, the conversation may approach the context limit, degrading model performance or requiring context management strategies.

### Hybrid: Shell Loop + CronCreate Within Sessions

Keep the entrypoint loop for the monitoring heartbeat. Within each session, Tier 2/3 agents can use CronCreate for intra-session follow-up verification. Sessions stay alive while scheduled tasks are pending, then exit normally. The supervisor enforces a maximum session duration.

* Good, because it delivers the highest-value feature (post-remediation verification) with the smallest architecture change.
* Good, because the entrypoint loop remains the durable monitoring scheduler — proven, simple, and reliable.
* Good, because CronCreate is just another tool in `--allowedTools`, following the existing enforcement model (ADR-0023).
* Good, because sessions remain short-lived and isolated — each cycle starts fresh, preventing context window accumulation and state drift.
* Good, because cost tracking, logging, and dashboard integration remain unchanged — each session is a discrete unit.
* Good, because the supervisor's maximum session duration prevents runaway sessions without requiring complex session health monitoring.
* Good, because it is a natural stepping stone to Option 1 — when channels become available, the shell loop can be replaced with CronCreate heartbeats.
* Bad, because sessions may run longer than today (waiting for scheduled tasks to fire), which delays the start of the next monitoring cycle.
* Bad, because CronCreate tasks are lost if the session crashes before they fire — verification is best-effort, not guaranteed.
* Bad, because the supervisor needs new timeout logic for session duration management.
* Bad, because it does not enable event-driven monitoring — channels still have no persistent session to push into.
* Bad, because cold-start overhead persists — each session still rebuilds context from scratch.
* Bad, because verification and the original remediation happen in the same session, so if the session times out during verification, the result may not be captured cleanly.

### Keep Current Architecture

No changes. Accept the limitations of single-shot sessions. Verification happens on the next scheduled monitoring cycle.

* Good, because there is zero implementation effort — the architecture is proven and working.
* Good, because sessions are short-lived, isolated, and predictable — each cycle is independent with clear boundaries.
* Good, because the supervisor has no session duration complexity — sessions run to completion and exit.
* Good, because there is no risk of CronCreate-related issues (task loss on crash, session runaway, timing edge cases).
* Bad, because post-remediation verification is delayed by the full monitoring interval (potentially 30+ minutes). A Tier 2 agent restarts a container and has no way to check if it came back healthy until the next cycle.
* Bad, because the agent cannot adapt its monitoring cadence — even during active incidents, the fixed interval determines when the next check happens.
* Bad, because it is incompatible with the channel architecture (ADR-0032), which requires a persistent session for event delivery.
* Bad, because cold-start overhead is paid on every cycle, rebuilding context that could be retained.
* Bad, because operators rely on the next cycle for verification, which may trigger unnecessary escalations if the service was temporarily slow to recover.

### External Scheduler (systemd timer / cron)

Replace the shell loop with OS-level scheduling. A systemd timer or cron job invokes `claude -p` on a fixed schedule. No CronCreate; no intra-session scheduling. The OS handles the heartbeat.

* Good, because OS-level schedulers (systemd, cron) are battle-tested, well-understood, and trivially configurable.
* Good, because the Docker entrypoint becomes simpler — no loop, just a single `claude` invocation.
* Good, because systemd provides built-in logging (journald), restart-on-failure, and dependency management.
* Good, because separation of concerns is clean — scheduling is the OS's job, monitoring is Claude's job.
* Bad, because it moves scheduling outside the container, breaking the self-contained Docker Compose deployment model (ADR-0009). Operators must configure the host scheduler in addition to the container.
* Bad, because it does not solve the core problem — there is still no intra-session verification. Each invocation is still a single-shot session with no follow-up capability.
* Bad, because it makes the deployment model platform-specific — systemd on Linux, launchd on macOS, Task Scheduler on Windows. Docker Compose works everywhere.
* Bad, because it eliminates the supervisor's ability to adjust the monitoring interval dynamically — the OS schedule is static.
* Bad, because concurrent execution prevention requires OS-level locking (flock, systemd's `RefuseManualStart`), adding configuration complexity compared to the current in-process mutex.
* Bad, because it is incompatible with both CronCreate-based verification and channels, gaining no benefit over the current shell loop while losing the containerization benefits.

## More Information

* **Scheduled prompts docs**: https://code.claude.com/docs/en/scheduled-tasks
* **CronCreate/CronList/CronDelete**: The underlying Claude Code tools for session-scoped scheduled prompts
* **Relates to ADR-0010**: The CLI subprocess invocation model (shell loop + `claude -p`) is preserved in the hybrid approach
* **Relates to ADR-0032**: Channels require a persistent session — the full persistent session (Option 1) is the future evolution when channels become available
* **Updates ADR-0013**: Ad-hoc sessions can coexist with intra-session scheduled tasks — the TriggerAdHoc channel wakes the supervisor, and the resulting session may use CronCreate for verification
* **Updates ADR-0023**: CronCreate, CronList, and CronDelete are added to the tier tool access model — allowed for Tier 2/3, not allowed for Tier 1
* **SPEC-0034**: The formal specification for intra-session scheduled follow-ups lives in `docs/openspec/specs/persistent-session-scheduling/spec.md`
