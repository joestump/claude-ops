---
status: proposed
date: 2026-03-21
---

# SPEC-0034: Intra-Session Scheduled Follow-Ups

## Overview

Claude Ops uses a shell loop to spawn single-shot `claude -p` sessions on a fixed interval. After a Tier 2 or Tier 3 agent performs a remediation action (container restart, compose up, Ansible playbook), it currently cannot verify the fix until the next monitoring cycle — potentially 30+ minutes later. This specification formalizes the use of Claude Code's `CronCreate` tool for intra-session follow-up scheduling, allowing agents to schedule one-shot verification tasks that fire within the same session.

See [ADR-0033: Persistent Session Architecture with Intra-Session Scheduling](../../adrs/ADR-0033-persistent-session-scheduling.md) for the decision rationale.

## Definitions

- **CronCreate**: A Claude Code tool that creates a session-scoped scheduled task. The task fires a prompt at the next matching cron time while the session is running and Claude is idle. Supports `once=true` for one-shot tasks.
- **CronList**: A Claude Code tool that lists all scheduled tasks in the current session.
- **CronDelete**: A Claude Code tool that deletes a scheduled task by ID.
- **Verification task**: A one-shot CronCreate task scheduled after a remediation action to verify that the fix was effective. The task fires a prompt that checks the service's health.
- **Session duration**: The elapsed time from when a `claude -p` session starts to when it exits. In the hybrid architecture, sessions may run longer than the remediation itself due to pending verification tasks.
- **Maximum session duration**: A supervisor-enforced ceiling on session duration (`CLAUDEOPS_MAX_SESSION_DURATION`). Sessions exceeding this limit are terminated.
- **Remediation action**: Any action that modifies infrastructure state — container restart, `docker compose up -d`, Ansible playbook execution, Helm upgrade, configuration file edits. Does NOT include read-only operations like health checks, log inspection, or status queries.

## Requirements

### REQ-1: CronCreate Tool Access

`CronCreate`, `CronList`, and `CronDelete` MUST be included in `--allowedTools` for Tier 2 and Tier 3 sessions. These tools MUST NOT be included in `--allowedTools` for Tier 1 sessions. This MUST be enforced at the `--allowedTools` CLI boundary (ADR-0023), not by prompt instructions alone.

The tier tool access configuration MUST be:

| Tier | --allowedTools additions |
|------|--------------------------|
| Tier 1 | (none — CronCreate, CronList, CronDelete excluded) |
| Tier 2 | `CronCreate,CronList,CronDelete` |
| Tier 3 | `CronCreate,CronList,CronDelete` |

#### Scenario: Tier 2 agent can create scheduled tasks

Given a Tier 2 session is started with `--allowedTools` including `CronCreate,CronList,CronDelete`
When the Tier 2 agent invokes `CronCreate("*/10 * * * *", "Verify jellyfin health", once=true)`
Then the CLI MUST permit the invocation
And the scheduled task MUST be created in the session

#### Scenario: Tier 1 agent cannot create scheduled tasks

Given a Tier 1 session is started with `--allowedTools` that does NOT include `CronCreate`
When the Tier 1 agent attempts to invoke `CronCreate`
Then the CLI MUST reject the invocation at the `--allowedTools` boundary
And the agent MUST NOT be able to schedule any tasks

#### Scenario: Tier 3 agent can list and delete scheduled tasks

Given a Tier 3 session with `CronList` and `CronDelete` in `--allowedTools`
When the agent invokes `CronList` to see pending tasks
Then the CLI MUST permit the invocation and return the task list
When the agent invokes `CronDelete(taskId)` to cancel a pending task
Then the CLI MUST permit the invocation and delete the task

### REQ-2: Post-Remediation Verification

After performing a remediation action (container restart, `docker compose up -d`, Ansible playbook execution), Tier 2 and Tier 3 agents SHOULD schedule a one-shot verification task using `CronCreate`. The verification prompt MUST specify the service name, the expected health check method (HTTP request, DNS lookup, port check, or docker ps), and the success criteria (HTTP status code, expected response body substring, container state).

Agents MUST NOT schedule verification for read-only operations (health checks, log inspection, status queries). Verification SHOULD only be scheduled when the agent has taken an action that changes infrastructure state.

Agents MUST NOT schedule recurring monitoring tasks — recurring monitoring is the supervisor's responsibility via the shell loop. All verification tasks MUST be one-shot (`once=true`).

#### Scenario: Verification scheduled after container restart

Given a Tier 2 agent has restarted the `jellyfin` container via `docker restart jellyfin`
When the restart command completes successfully
Then the agent SHOULD schedule a verification task:
  `CronCreate("*/10 * * * *", "Verify that jellyfin is healthy at https://jellyfin.stump.rocks — expect HTTP 200. If unhealthy, attempt docker restart within Tier 2 permissions.", once=true)`
And the task MUST be one-shot (non-recurring)

#### Scenario: No verification for read-only operations

Given a Tier 2 agent has checked the health of `jellyfin` and found it healthy
When the health check completes
Then the agent MUST NOT schedule a verification task
Because no remediation action was taken

#### Scenario: Verification scheduled after docker compose up

Given a Tier 2 agent has run `docker compose up -d jellyfin` on a remote host
When the compose command completes successfully
Then the agent SHOULD schedule a verification task specifying the service name, health check URL, and expected result
And the verification delay SHOULD be appropriate for the service's startup time

### REQ-3: Session Duration Management

The Go supervisor MUST enforce a maximum session duration via the `CLAUDEOPS_MAX_SESSION_DURATION` environment variable. The default value MUST be 30 minutes. The value MUST be specified as a duration string parseable by Go's `time.ParseDuration` (e.g., `30m`, `1h`, `45m`).

Sessions with pending scheduled tasks MUST be allowed to run until either all tasks have fired or the maximum session duration is reached, whichever comes first. The supervisor MUST terminate sessions that exceed the maximum duration regardless of pending task status.

When the supervisor terminates a session due to timeout, it MUST record the session in the database with a `timeout` completion status. The entrypoint loop MUST continue normally after a timeout — the next monitoring cycle starts after the standard sleep interval.

#### Scenario: Session completes after all tasks fire

Given a Tier 2 session has a pending verification task scheduled to fire in 10 minutes
And `CLAUDEOPS_MAX_SESSION_DURATION=30m`
When the verification task fires at the 10-minute mark
And the verification completes (healthy or further remediation attempted)
And no more tasks are pending
Then the session MUST exit normally
And the supervisor MUST record the session with a `completed` status

#### Scenario: Session terminated at maximum duration

Given a Tier 2 session has a pending verification task scheduled to fire in 25 minutes
And `CLAUDEOPS_MAX_SESSION_DURATION=30m`
When 30 minutes elapse since session start
Then the supervisor MUST terminate the session
And the supervisor MUST record the session with a `timeout` status
And the pending task is lost (acceptable — the next monitoring cycle will catch the issue)

#### Scenario: Default maximum duration applied

Given `CLAUDEOPS_MAX_SESSION_DURATION` is not set in the environment
When the supervisor starts a session
Then the maximum session duration MUST default to 30 minutes

### REQ-4: Verification Task Format

Verification tasks MUST be created as one-shot (non-recurring) cron tasks using `CronCreate` with `once=true`. The cron expression SHOULD default to a 10-minute delay (`*/10 * * * *`) but MAY be adjusted by the agent based on the service's expected recovery time.

The verification prompt MUST include:
1. The service name being verified
2. The health check method (HTTP request URL, DNS lookup hostname, `docker ps` filter, or port check)
3. The expected success criteria (HTTP status code, expected response substring, container status, or port open)
4. Instructions for what to do if the verification fails (attempt further remediation within tier permissions, or escalate)

#### Scenario: Well-formed verification prompt

Given a Tier 2 agent needs to verify that `jellyfin` recovered after a restart
When the agent creates a verification task
Then the prompt MUST contain the service name (`jellyfin`)
And the prompt MUST specify the health check method (`HTTP GET https://jellyfin.stump.rocks`)
And the prompt MUST specify success criteria (`expect HTTP 200`)
And the prompt MUST specify failure action (`if unhealthy, attempt further remediation or escalate`)

#### Scenario: Adjusted delay for slow-starting service

Given a Tier 3 agent has redeployed a service known to take 5 minutes to start (e.g., a Java application)
When the agent creates a verification task
Then the cron expression SHOULD use a longer delay (e.g., `*/15 * * * *`) rather than the default 10 minutes
Because the service needs time to fully initialize before verification is meaningful

#### Scenario: Verification prompt includes escalation instructions

Given a Tier 2 agent creates a verification task after restarting a service
When the verification fires and finds the service still unhealthy
Then the prompt MUST instruct the agent to either attempt further remediation within Tier 2 permissions
Or escalate to Tier 3 if Tier 2 remediation options are exhausted

### REQ-5: Session Lifecycle Events

The session manager MUST record when a session has pending scheduled tasks. The session record in the database MUST include a field indicating whether scheduled tasks are pending.

The session detail page in the web dashboard SHOULD display the count and status of scheduled tasks (pending, fired, expired). Session completion MUST wait for pending tasks unless the maximum session duration (REQ-3) is exceeded.

When a session exits with unfired scheduled tasks (due to timeout or crash), the session record SHOULD indicate that tasks were abandoned.

#### Scenario: Session record includes pending task indicator

Given a Tier 2 session has scheduled a verification task via CronCreate
When the session record is updated in the database
Then the record MUST include a field indicating at least one scheduled task is pending
And the session status MUST be `running` (not `completed`) while tasks are pending

#### Scenario: Dashboard displays scheduled task status

Given an operator views the session detail page for a session with pending tasks
When the page renders
Then the page SHOULD display the number of pending scheduled tasks
And the page SHOULD display when the next task is expected to fire

#### Scenario: Abandoned tasks recorded on timeout

Given a session is terminated by the supervisor due to maximum duration exceeded (REQ-3)
And the session had 1 unfired scheduled task
When the session record is finalized
Then the record SHOULD indicate that 1 task was abandoned
And the session completion status MUST be `timeout`

### REQ-6: Tier Prompt Updates

Tier 2 and Tier 3 prompt files MUST be updated to describe the `CronCreate` capability and when to use it. The prompts MUST instruct the agent to schedule verification after remediation actions.

The prompts MUST NOT instruct the agent to schedule recurring monitoring tasks. The prompts MUST explicitly state that recurring monitoring is the supervisor's responsibility (via the shell loop), and that CronCreate is ONLY for one-shot post-remediation verification.

The prompts MUST include an example CronCreate invocation showing the correct format for a verification task.

#### Scenario: Tier 2 prompt describes CronCreate

Given the Tier 2 prompt file (`prompts/tier2-investigate.md`) is updated
When a Tier 2 agent reads the prompt
Then the prompt MUST describe CronCreate as a tool for scheduling post-remediation verification
And the prompt MUST include at least one example CronCreate invocation
And the prompt MUST instruct the agent to use `once=true` for all verification tasks

#### Scenario: Prompt explicitly prohibits recurring tasks

Given the Tier 2 or Tier 3 prompt file is updated
When the agent reads the scheduling instructions
Then the prompt MUST state that the agent MUST NOT schedule recurring monitoring tasks
And the prompt MUST explain that recurring monitoring is handled by the supervisor's shell loop

#### Scenario: Tier 1 prompt does not mention CronCreate

Given the Tier 1 prompt file (`prompts/tier1-observe.md`)
When a Tier 1 agent reads the prompt
Then the prompt MUST NOT reference CronCreate, CronList, or CronDelete
Because Tier 1 does not have access to these tools (REQ-1)

### REQ-7: Escalation Compatibility

Scheduled follow-up tasks MUST be compatible with the tiered escalation model. When a verification task fires and discovers the service is still unhealthy, the agent SHOULD attempt further remediation within its current tier's permissions.

If the agent cannot resolve the issue within its tier permissions (e.g., Tier 2 has already attempted a container restart and the service is still unhealthy), the agent MUST escalate to the next tier using the standard escalation mechanism (Task tool for subagent spawning).

Escalated subagent sessions (Tier 3 spawned from a verification task) SHOULD also have access to CronCreate for their own follow-up verification.

#### Scenario: Verification finds service healthy

Given a verification task fires 10 minutes after a container restart
When the health check finds the service responding with HTTP 200
Then the agent MUST report the service as healthy
And the agent MUST NOT schedule additional verification tasks
And the session MAY exit if no other tasks are pending

#### Scenario: Verification finds service unhealthy — further remediation

Given a verification task fires 10 minutes after a Tier 2 container restart
When the health check finds the service still unhealthy (HTTP 503)
Then the agent SHOULD attempt further remediation within Tier 2 permissions (e.g., `docker compose up -d --force-recreate`)
And the agent MAY schedule another verification task for the new remediation action

#### Scenario: Verification finds service unhealthy — escalation

Given a verification task fires and the service is still unhealthy after a second remediation attempt by Tier 2
When the agent determines it has exhausted Tier 2 remediation options
Then the agent MUST escalate to Tier 3 via the Task tool
And the Tier 3 subagent SHOULD have CronCreate in its `--allowedTools` for its own verification needs

### REQ-8: Graceful Degradation

If `CronCreate` is unavailable (not in `--allowedTools`, tool call fails, or Claude Code version does not support it), the agent MUST continue normally without scheduling follow-ups. The absence of scheduled verification MUST NOT prevent the session from completing.

If a scheduled task fails to fire (session crash, CLI error), the session MUST still exit cleanly. The next monitoring cycle will detect any unresolved issues through normal health checks.

Agents MUST NOT retry a failed CronCreate invocation more than once. If the retry also fails, the agent MUST log the failure and continue without scheduling.

#### Scenario: CronCreate not in allowedTools

Given a session is started without `CronCreate` in `--allowedTools` (e.g., Tier 1)
When the agent performs remediation (if it could — hypothetically via a prompt override)
Then the agent MUST complete the session without scheduling verification
And the session MUST exit normally

#### Scenario: CronCreate invocation fails

Given a Tier 2 agent attempts to schedule a verification task
When the `CronCreate` invocation returns an error
Then the agent SHOULD retry once
If the retry also fails, the agent MUST log the error and continue without scheduling
And the session MUST NOT be blocked by the scheduling failure

#### Scenario: Session crashes with pending tasks

Given a Tier 2 session has a pending verification task scheduled to fire in 8 minutes
When the Claude CLI process exits unexpectedly at the 5-minute mark
Then the pending task is lost (session-scoped — tasks do not survive session exit)
And the supervisor MUST record the session with an `error` status
And the next monitoring cycle MUST proceed normally and will detect any unresolved issues

## References

- [ADR-0033: Persistent Session Architecture with Intra-Session Scheduling](../../adrs/ADR-0033-persistent-session-scheduling.md)
- [ADR-0010: Invoke Claude via Claude Code CLI as Subprocess](../../adrs/ADR-0010-claude-code-cli-subprocess.md)
- [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md)
- [ADR-0032: Channel-Based Operator Interface](../../adrs/ADR-0032-channel-operator-interface.md)
- [ADR-0013: Manual Ad-Hoc Session Runs from the Dashboard](../../adrs/ADR-0013-manual-ad-hoc-session-runs.md)
- [SPEC-0027: AllowedTools-Based Tier Enforcement](../allowedtools-tier-enforcement/spec.md)
