---
status: proposed
date: 2026-03-21
---

# SPEC-0030: Hooks-Based Lifecycle Guardrails

## Overview

Claude Ops enforces permission tier boundaries using a four-layer model: tool-level whitelisting (`--allowedTools`), command-prefix blocklisting (`--disallowedTools`), hooks (runtime state-aware enforcement), and prompt instructions. This specification formalizes the third layer -- hooks -- which was added by ADR-0029 to provide deterministic enforcement for cooldown limits, event emission, remediation verification, dynamic context injection, and notification delivery.

The first two layers (ADR-0003, ADR-0023, SPEC-0027) provide static enforcement: they block tools or command prefixes regardless of runtime state. Hooks add runtime state awareness: a PreToolUse hook can read `cooldown.json` and block a `docker restart` only when a specific service has exceeded its budget, not for all services unconditionally.

See [ADR-0029: Hooks for Deterministic Lifecycle Guardrails](../../adrs/ADR-0029-hooks-lifecycle-guardrails.md) for the decision rationale.

## Definitions

- **Hook**: A deterministic shell command or agent that Claude Code executes at a specific lifecycle point. Hooks receive event data as JSON on stdin and can influence agent behavior through exit codes or structured JSON output.
- **PreToolUse hook**: A hook that fires before a tool is executed. Can deny the tool invocation by exiting with code 2 or returning `{"decision": "deny", "reason": "..."}`.
- **PostToolUse hook**: A hook that fires after a tool succeeds. Can inject feedback into the conversation but cannot block the already-completed action.
- **Stop hook**: A hook that fires when Claude finishes responding. Agent-based Stop hooks can spawn a verification subagent that may cause Claude to continue working.
- **SessionStart hook**: A hook that fires when a session begins. Stdout is added to Claude's context.
- **Notification hook**: A hook that fires when Claude sends a notification event.
- **Matcher**: A filter on a hook that restricts which tool invocations trigger it (e.g., matcher `Bash` means the hook only fires for Bash tool calls).
- **Cooldown budget**: The remaining number of restarts (max 2 per 4h) or redeployments (max 1 per 24h) for a specific service, as tracked in `cooldown.json`.
- **Headless mode**: Claude Code invoked with the `-p` flag, which is how Claude Ops runs all sessions. PermissionRequest hooks do not fire in this mode; PreToolUse hooks do.

## Requirements

### REQ-1: Four-Layer Enforcement Model

The system MUST enforce tier permissions through exactly four independent layers applied in sequence:

1. **`--allowedTools` whitelist** (hard boundary): Restricts which tool types the agent MAY invoke.
2. **`--disallowedTools` blocklist** (hard boundary): Blocks specific command-prefix patterns within allowed tool types.
3. **Hooks** (hard boundary): Executes deterministic shell commands at lifecycle points to enforce runtime state checks, emit events, verify remediations, inject context, and bridge notifications.
4. **Prompt instructions** (soft boundary): The tier prompt file and skill files describe additional restrictions.

All four layers MUST be active for every `claude` CLI invocation. The hooks layer MUST NOT replace or weaken the existing `--allowedTools` and `--disallowedTools` layers. Hooks MUST complement the prompt instructions layer by making deterministic what prompts can only request probabilistically.

#### Scenario: All four layers active for a Tier 2 invocation

Given the session manager starts a Tier 2 session
When it invokes the `claude` CLI
Then `--allowedTools Bash,Read,Write,Edit,Grep,Glob,Task,WebFetch,WebSearch` is passed
And `--disallowedTools` contains the Tier 2 blocklist patterns (SPEC-0027 REQ-3)
And `.claude/settings.json` contains hook definitions for PreToolUse, PostToolUse, Stop, SessionStart, and Notification
And `--append-system-prompt` includes the tier prompt with permission instructions

#### Scenario: Hooks do not override disallowedTools

Given `Bash(ansible-playbook:*)` is in the Tier 2 `--disallowedTools` list
When a Tier 2 agent attempts `Bash("ansible-playbook playbooks/redeploy.yml")`
Then the CLI MUST reject the invocation at the `--disallowedTools` boundary
And the PreToolUse hook MUST NOT fire (the command is already blocked)

#### Scenario: Hook fires after disallowedTools permits

Given `Bash(docker restart:*)` is NOT in the Tier 2 `--disallowedTools` list
And the PreToolUse cooldown hook is configured
When a Tier 2 agent attempts `Bash("docker restart jellyfin")`
Then the `--disallowedTools` check passes
And the PreToolUse hook fires and evaluates cooldown state for jellyfin

### REQ-2: Cooldown Enforcement Hook

A PreToolUse hook MUST check `cooldown.json` before any Bash command matching restart or redeployment patterns. The hook MUST block the invocation with a structured denial if the service's cooldown limits are exceeded. The denial MUST include the remaining cooldown time.

The hook MUST match the following command patterns:
- `docker restart <service>`
- `docker stop <service>` / `docker start <service>`
- `docker compose up` (restart-equivalent)
- `docker compose restart`
- `ansible-playbook` (redeployment)
- `helm upgrade` (redeployment)

The hook MUST extract the service name from the command and look up its cooldown state. If the command does not match any pattern, the hook MUST exit 0 (allow) without reading cooldown state.

The hook MUST enforce these limits (as defined in ADR-0007):
- Maximum 2 container restarts per service per 4-hour window
- Maximum 1 full redeployment per service per 24-hour window

#### Scenario: Restart blocked when cooldown limit exceeded

Given service "jellyfin" has been restarted 2 times in the last 4 hours (per cooldown.json)
When the agent attempts `Bash("docker restart jellyfin")`
Then the PreToolUse hook MUST read `$CLAUDEOPS_STATE_DIR/cooldown.json`
And determine that jellyfin has reached its restart limit (2/2)
And return `{"decision": "deny", "reason": "Cooldown limit exceeded for jellyfin: 2/2 restarts in last 4h. Next allowed at <timestamp>."}`
And the Bash command MUST NOT execute

#### Scenario: Restart allowed when within cooldown budget

Given service "jellyfin" has been restarted 1 time in the last 4 hours
When the agent attempts `Bash("docker restart jellyfin")`
Then the PreToolUse hook MUST read cooldown.json
And determine that jellyfin has 1 restart remaining (1/2)
And exit with code 0 (allow)
And the Bash command MUST execute normally

#### Scenario: Redeployment blocked when daily limit exceeded

Given service "jellyfin" has been redeployed 1 time in the last 24 hours
When the agent attempts `Bash("ansible-playbook playbooks/redeploy-jellyfin.yml")`
Then the PreToolUse hook MUST determine that jellyfin has reached its redeployment limit (1/1)
And return a structured denial with reason and remaining cooldown time

#### Scenario: Non-infrastructure command passes without cooldown check

Given the PreToolUse hook is configured with matcher `Bash`
When the agent attempts `Bash("curl -s https://jellyfin.stump.rocks/health")`
Then the hook MUST determine the command does not match any restart or redeployment pattern
And exit with code 0 (allow) without reading cooldown.json

### REQ-3: Event Emission Hook

A PostToolUse hook MUST detect significant infrastructure actions after Bash commands succeed and insert events into the events SQLite table. Events MUST include session_id, level, service (when detectable), and a descriptive message.

The hook MUST detect the following action categories:
- **Container restart**: `docker restart`, `docker stop`, `docker start`, `docker compose restart`
- **Service deployment**: `docker compose up`, `ansible-playbook`, `helm upgrade`
- **PR creation**: `gh pr create`, `tea pr create`
- **Notification sent**: `apprise`

The hook MUST extract the service name from the command when possible. If the service name cannot be determined, the service field MUST be set to NULL.

Event levels MUST follow the existing convention (ADR-0014):
- `info`: Routine actions (PR created, notification sent)
- `warning`: Remediation actions (container restarted, service redeployed)
- `critical`: Failed remediations or actions requiring human attention

#### Scenario: Event emitted after container restart

Given the agent successfully executes `Bash("docker restart jellyfin")`
When the PostToolUse hook fires
Then the hook MUST parse the command and detect a container restart action
And insert an event: `level="warning", service="jellyfin", message="Container restarted: docker restart jellyfin"`
And include the current `session_id` from the hook's stdin JSON

#### Scenario: Event emitted after PR creation

Given the agent successfully executes `Bash("gh pr create --title 'Fix jellyfin config' --body '...' ")`
When the PostToolUse hook fires
Then the hook MUST detect a PR creation action
And insert an event: `level="info", service=NULL, message="Pull request created: Fix jellyfin config"`

#### Scenario: No event emitted for read-only commands

Given the agent successfully executes `Bash("docker ps --format '{{.Names}}'")`
When the PostToolUse hook fires
Then the hook MUST determine the command does not match any significant action pattern
And exit without inserting an event

#### Scenario: Event includes session_id from hook context

Given the PostToolUse hook receives JSON on stdin containing `"session_id": "abc-123"`
When the hook inserts an event into the events table
Then the event's `session_id` column MUST be set to `"abc-123"`

### REQ-4: Remediation Verification Hook

A Stop hook of type `agent` MUST verify service health after Tier 2 or Tier 3 sessions that performed remediation actions. The verification MUST check the appropriate health indicator (HTTP endpoint, DNS resolution, or container status) for the remediated service. The hook MUST return `{"continue": true, "reason": "..."}` if verification fails, causing Claude to continue working on the problem.

The verification hook SHOULD only fire when the session actually performed a remediation action. The hook MAY inspect session context or a state flag set by the PostToolUse event emission hook to determine whether remediation occurred.

#### Scenario: Verification passes after successful restart

Given a Tier 2 session restarted jellyfin and the session is completing
When the Stop hook fires
Then the verification agent MUST check jellyfin's health endpoint (e.g., `curl -s https://jellyfin.stump.rocks/health`)
And the health check returns HTTP 200
And the hook MUST allow the session to end normally

#### Scenario: Verification fails after restart -- session continues

Given a Tier 2 session restarted jellyfin and the session is completing
When the Stop hook fires
Then the verification agent MUST check jellyfin's health endpoint
And the health check returns HTTP 502
And the hook MUST return `{"continue": true, "reason": "Service jellyfin still unhealthy after restart: HTTP 502"}`
And Claude MUST continue working on the problem

#### Scenario: Verification skipped when no remediation occurred

Given a Tier 1 session performed only observation (no restarts, no deployments)
When the session completes
Then the Stop hook SHOULD either not fire or exit without spawning a verification agent
Because Tier 1 sessions do not perform remediation

### REQ-5: Dynamic Context Injection Hook

A SessionStart hook MUST read current runtime state and output a structured summary to stdout. The output MUST be added to Claude's context at the start of every session.

The context summary MUST include:
- Current cooldown state: per-service restart count, redeployment count, and time until budget reset
- Last 10 events from the events SQLite table (timestamp, level, service, message)
- Host connectivity status: results of basic reachability checks for known hosts

The output SHOULD be formatted as a human-readable summary that the LLM can reference during the session.

#### Scenario: Context includes cooldown state

Given cooldown.json shows jellyfin has 1/2 restarts used and adguard has 0/2
When a new session starts and the SessionStart hook fires
Then the hook's stdout MUST include a cooldown summary like:
```
Cooldown State:
  jellyfin: 1/2 restarts used (4h window), 0/1 redeployments used (24h window)
  adguard: 0/2 restarts used, 0/1 redeployments used
```

#### Scenario: Context includes recent events

Given the events table contains 3 recent events
When the SessionStart hook fires
Then the hook's stdout MUST include the last 10 events (or fewer if less than 10 exist) with timestamp, level, service, and message

#### Scenario: Context includes host connectivity

Given ie01 (192.168.100.210) is reachable and pi04 is not
When the SessionStart hook fires
Then the hook's stdout MUST include:
```
Host Connectivity:
  ie01 (192.168.100.210): reachable
  pi04: unreachable
```

### REQ-6: Notification Bridge Hook

A Notification hook MUST forward Claude Code notification events to the Apprise CLI when `CLAUDEOPS_APPRISE_URLS` is configured. The hook MUST gracefully skip notification delivery if `CLAUDEOPS_APPRISE_URLS` is not set or is empty.

The hook MUST pass the notification title and body to `apprise` using the URLs from `CLAUDEOPS_APPRISE_URLS`. The hook MUST NOT cause the session to fail if Apprise delivery fails -- notification delivery failure MUST be logged but not propagated as a session error.

#### Scenario: Notification forwarded to Apprise

Given `CLAUDEOPS_APPRISE_URLS="ntfy://ntfy.stump.rocks/claudeops"` is configured
When Claude Code emits a notification event with title "Remediation Complete" and body "Restarted jellyfin"
Then the Notification hook MUST invoke `apprise -t "Remediation Complete" -b "Restarted jellyfin" "ntfy://ntfy.stump.rocks/claudeops"`

#### Scenario: Notification skipped when Apprise not configured

Given `CLAUDEOPS_APPRISE_URLS` is not set or is empty
When Claude Code emits a notification event
Then the Notification hook MUST exit 0 without invoking apprise
And no error MUST be logged

#### Scenario: Apprise delivery failure does not break the session

Given `CLAUDEOPS_APPRISE_URLS` is configured but the notification target is unreachable
When the Notification hook invokes apprise and it fails
Then the hook MUST log the failure to stderr
And exit 0 (the session MUST continue normally)

### REQ-7: Hook Configuration Location

All hooks MUST be defined in `.claude/settings.json` within the project repository under the `hooks` key. Hook scripts MUST be stored in the `.claude/hooks/` directory. All hook scripts MUST be executable (`chmod +x`).

The `.claude/hooks/` directory MUST contain at minimum:
- `cooldown-check.sh` -- PreToolUse cooldown enforcement (REQ-2)
- `event-emit.sh` -- PostToolUse event emission (REQ-3)
- `verify-remediation.md` -- Stop hook verification agent prompt (REQ-4)
- `session-context.sh` -- SessionStart context injection (REQ-5)
- `notify-apprise.sh` -- Notification bridge (REQ-6)

The `settings.json` hook configuration MUST specify matchers where applicable (e.g., `Bash` matcher for PreToolUse and PostToolUse hooks).

#### Scenario: Settings.json contains all hook definitions

Given the project repository contains `.claude/settings.json`
When an operator inspects the hooks configuration
Then it MUST contain entries for PreToolUse (cooldown-check.sh), PostToolUse (event-emit.sh), Stop (verify-remediation.md), SessionStart (session-context.sh), and Notification (notify-apprise.sh)

#### Scenario: Hook scripts are executable

Given the `.claude/hooks/` directory contains hook scripts
When the CI pipeline or a pre-commit check inspects file permissions
Then all `.sh` files in `.claude/hooks/` MUST have the executable bit set

#### Scenario: Hook script missing causes graceful degradation

Given `.claude/hooks/cooldown-check.sh` is missing or not executable
When a session starts and the PreToolUse hook is triggered
Then Claude Code MUST report the hook failure
And the Bash command SHOULD still execute (fail-open for missing hooks)
Because a missing hook is a configuration error, not a reason to block all agent operations

### REQ-8: Headless Compatibility

All hooks MUST function in `-p` (headless) mode, which is how Claude Ops invokes the CLI via `entrypoint.sh`. The implementation MUST NOT use `PermissionRequest` hooks, which do not fire in `-p` mode. PreToolUse hooks MUST be used for all pre-execution enforcement.

All hooks MUST receive event data as JSON on stdin and MUST NOT require interactive terminal input. Hook scripts MUST complete within a reasonable timeout (SHOULD complete within 5 seconds for command-type hooks, MAY take longer for agent-type Stop hooks).

#### Scenario: PreToolUse hook fires in headless mode

Given Claude Ops invokes `claude -p "$(cat tier2-investigate.md)" --allowedTools ...`
When the agent attempts a Bash tool call
Then the PreToolUse cooldown-check.sh hook MUST fire and receive tool_input JSON on stdin
And the hook MUST execute and return a decision

#### Scenario: PermissionRequest hook is not used

Given the `.claude/settings.json` hook configuration
When an operator inspects the configuration
Then there MUST be no `PermissionRequest` hook entries
Because PermissionRequest hooks do not fire in `-p` mode

#### Scenario: Hooks complete within timeout

Given the PreToolUse cooldown-check.sh hook is executing
When it reads cooldown.json and evaluates limits
Then the hook MUST complete within 5 seconds
Because hook latency directly impacts agent response time

## References

- [ADR-0029: Hooks for Deterministic Lifecycle Guardrails](../../adrs/ADR-0029-hooks-lifecycle-guardrails.md)
- [ADR-0003: Enforce Permission Tiers via Prompt Instructions and Allowed-Tool Lists](../../adrs/ADR-0003-prompt-based-permission-enforcement.md)
- [ADR-0007: Persist Cooldown State in a JSON File on Mounted Volume](../../adrs/ADR-0007-json-file-cooldown-state.md)
- [ADR-0014: Real-Time Dashboard and Events System](../../adrs/ADR-0014-realtime-dashboard-and-events.md)
- [ADR-0004: Use Apprise CLI for Universal Notification Abstraction](../../adrs/ADR-0004-apprise-notification-abstraction.md)
- [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md)
- [SPEC-0027: AllowedTools-Based Tier Enforcement](../allowedtools-tier-enforcement/spec.md)
- [SPEC-0007: JSON File Cooldown State](../json-cooldown-state/spec.md)
- [SPEC-0013: Real-Time Dashboard and Events](../realtime-dashboard-events/spec.md)
