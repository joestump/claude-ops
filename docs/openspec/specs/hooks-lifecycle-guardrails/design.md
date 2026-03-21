# Design: Hooks-Based Lifecycle Guardrails

## Overview

This design describes how Claude Ops uses Claude Code's hook system to add a fourth enforcement layer for deterministic lifecycle guardrails. Hooks execute as shell commands (or agent prompts) at specific CLI lifecycle points -- PreToolUse, PostToolUse, Stop, SessionStart, and Notification -- to enforce cooldown limits, emit events, verify remediations, inject dynamic context, and bridge notifications to Apprise.

See [SPEC-0030](./spec.md) and [ADR-0029](../../adrs/ADR-0029-hooks-lifecycle-guardrails.md).

## Architecture

### Four-Layer Enforcement Stack

```
Agent attempts Bash("docker restart jellyfin")
                |
                v
+-----------------------------------------------+
|  Layer 1: --allowedTools                       |
|  Is "Bash" in the whitelist?                   |
|  YES -> continue   NO -> BLOCKED              |
+-----------------------------------------------+
                |
                v
+-----------------------------------------------+
|  Layer 2: --disallowedTools                    |
|  Does "docker restart" match any blocked       |
|  prefix for this tier?                         |
|  Tier 1: YES -> BLOCKED                        |
|  Tier 2: NO  -> continue                       |
+-----------------------------------------------+
                |
                v
+-----------------------------------------------+
|  Layer 3: Hooks (THIS DESIGN)                  |
|                                                |
|  PreToolUse: cooldown-check.sh                 |
|    Read cooldown.json                          |
|    jellyfin restarts: 2/2 in last 4h           |
|    -> {"decision":"deny","reason":"..."}       |
|    -> BLOCKED                                  |
|                                                |
|    OR                                          |
|                                                |
|    jellyfin restarts: 1/2 in last 4h           |
|    -> exit 0 (allow)                           |
|    -> continue                                 |
+-----------------------------------------------+
                |
                v
+-----------------------------------------------+
|  Layer 4: Prompt Instructions                  |
|  Tier prompt says "check cooldown first"       |
|  (redundant -- already enforced by hook)       |
|  Handles: SSH tunneling, scope validation,     |
|  "Never Allowed" ops without distinct prefixes |
+-----------------------------------------------+
                |
                v
        COMMAND EXECUTES
                |
                v
+-----------------------------------------------+
|  PostToolUse: event-emit.sh                    |
|  Detects "docker restart jellyfin"             |
|  Inserts event into SQLite events table        |
+-----------------------------------------------+
```

### Hook Lifecycle in a Claude Ops Session

```
entrypoint.sh starts claude -p "..." --allowedTools ... --disallowedTools ...
        |
        v
+-------------------+
| SessionStart Hook |-----> session-context.sh
|                   |       - Read cooldown.json -> summarize budgets
|                   |       - Query last 10 events from SQLite
|                   |       - Ping known hosts -> connectivity status
|                   |       - Output summary to stdout -> added to context
+-------------------+
        |
        v
+-------------------+
| Agent works...    |
| Attempts Bash()   |---+
+-------------------+   |
                        v
                +------------------+
                | PreToolUse Hook  |-----> cooldown-check.sh
                | (per Bash call)  |       - Parse command from stdin JSON
                |                  |       - Match against restart/deploy patterns
                |                  |       - If match: read cooldown.json, check limits
                |                  |       - Deny or allow
                +------------------+
                        |
                        v (if allowed)
                +------------------+
                | Bash executes    |
                +------------------+
                        |
                        v
                +------------------+
                | PostToolUse Hook |-----> event-emit.sh
                | (per Bash call)  |       - Parse command from stdin JSON
                |                  |       - Match against significant action patterns
                |                  |       - If match: sqlite3 INSERT into events
                +------------------+
        |
        v (repeats for each tool call)
+-------------------+
| Agent completes   |
+-------------------+
        |
        v
+-------------------+
| Stop Hook         |-----> verify-remediation.md (agent type)
|                   |       - Check if remediation occurred this session
|                   |       - If yes: verify service health
|                   |       - If unhealthy: return {continue: true}
|                   |       - If healthy: allow session to end
+-------------------+
        |
        v
+-------------------+
| Notification Hook |-----> notify-apprise.sh
| (if notification  |       - Check CLAUDEOPS_APPRISE_URLS
| event emitted)    |       - If set: forward to apprise CLI
|                   |       - If unset: exit 0 silently
+-------------------+
```

## Hook Configuration

### settings.json Structure

The hooks are defined in `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": ".claude/hooks/cooldown-check.sh"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": ".claude/hooks/event-emit.sh"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "agent",
            "prompt_file": ".claude/hooks/verify-remediation.md"
          }
        ]
      }
    ],
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": ".claude/hooks/session-context.sh"
          }
        ]
      }
    ],
    "Notification": [
      {
        "hooks": [
          {
            "type": "command",
            "command": ".claude/hooks/notify-apprise.sh"
          }
        ]
      }
    ]
  }
}
```

### File Layout

```
.claude/
  settings.json          # Hook definitions (checked into repo)
  hooks/
    cooldown-check.sh    # PreToolUse: cooldown enforcement
    event-emit.sh        # PostToolUse: event emission
    verify-remediation.md # Stop: agent-based verification prompt
    session-context.sh   # SessionStart: dynamic context injection
    notify-apprise.sh    # Notification: Apprise bridge
```

## Script Implementations

### cooldown-check.sh (PreToolUse -- Cooldown Enforcement)

**Input**: JSON on stdin from Claude Code, containing `tool_input.command` with the Bash command.

**Logic flow**:

```
1. Read JSON from stdin, extract tool_input.command
2. Pattern match against restart/redeployment commands:
   - docker restart <service>       -> type=restart, extract service
   - docker stop <service>          -> type=restart, extract service
   - docker start <service>         -> type=restart, extract service
   - docker compose up              -> type=restart, extract service from args or dir
   - docker compose restart         -> type=restart, extract service
   - ansible-playbook *             -> type=redeployment, extract service from filename
   - helm upgrade *                 -> type=redeployment, extract service from args
3. If no pattern matches -> exit 0 (allow, no cooldown check needed)
4. Read $CLAUDEOPS_STATE_DIR/cooldown.json
5. Look up service in .services[service_name]
6. For type=restart:
   - Count restarts in last 4 hours (filter .restart_timestamps[] > now - 4h)
   - If count >= 2: output deny JSON with reason and next-allowed time
   - If count < 2: exit 0 (allow)
7. For type=redeployment:
   - Count redeployments in last 24 hours
   - If count >= 1: output deny JSON with reason and next-allowed time
   - If count < 1: exit 0 (allow)
```

**Deny output format**:

```json
{
  "decision": "deny",
  "reason": "Cooldown limit exceeded for jellyfin: 2/2 restarts in last 4h. Next allowed at 2026-03-21T18:30:00Z."
}
```

**Key design decisions**:
- The script uses `jq` for JSON parsing, which is already in the container image.
- Pattern matching uses shell `case` statements for simplicity and speed.
- Service name extraction is best-effort. If the script cannot determine the service from the command, it exits 0 (allow) -- fail-open is safer than blocking legitimate commands due to parsing failure.
- The script does NOT update cooldown.json. Cooldown state updates remain the responsibility of the agent (or a future PostToolUse hook). The PreToolUse hook is read-only.

### event-emit.sh (PostToolUse -- Event Emission)

**Input**: JSON on stdin from Claude Code, containing `tool_input.command`, `session_id`, and `tool_output`.

**Logic flow**:

```
1. Read JSON from stdin, extract tool_input.command and session_id
2. Pattern match against significant action categories:
   - docker restart <service>       -> level=warning, service=<service>
   - docker compose up              -> level=warning, service=<service>
   - docker compose restart         -> level=warning, service=<service>
   - ansible-playbook *             -> level=warning, service=<service>
   - helm upgrade *                 -> level=warning, service=<service>
   - gh pr create *                 -> level=info, service=NULL
   - tea pr create *                -> level=info, service=NULL
   - apprise *                      -> level=info, service=NULL
3. If no pattern matches -> exit 0 (no event to emit)
4. Construct message from the matched pattern and command
5. Insert into SQLite:
   sqlite3 "$CLAUDEOPS_DB_PATH" \
     "INSERT INTO events (session_id, level, service, message, created_at)
      VALUES ('$SESSION_ID', '$LEVEL', '$SERVICE', '$MESSAGE', datetime('now'));"
6. Exit 0 (PostToolUse hooks should not block)
```

**Key design decisions**:
- The script writes directly to the SQLite database using the `sqlite3` CLI. This is the same database the Go session manager uses, accessed from a different process. SQLite supports concurrent readers and a single writer with WAL mode, which is sufficient for the append-only event insertion pattern.
- The `CLAUDEOPS_DB_PATH` environment variable must point to the SQLite database file. This is set in `entrypoint.sh` alongside other `CLAUDEOPS_*` variables.
- Event messages are descriptive but concise: "Container restarted: docker restart jellyfin" rather than the full command with all arguments.
- The hook does NOT replace the LLM's `[EVENT:...]` markers. Both mechanisms can coexist -- the hook provides reliability, while the LLM markers can add richer context that the hook cannot infer from the command alone.

### verify-remediation.md (Stop Hook -- Agent-Based Verification)

This is a prompt file for an agent-based Stop hook. When the session ends, Claude Code spawns a lightweight verification agent with this prompt.

**Prompt content summary**:

```
You are a remediation verification agent. Your job is to check whether
services that were remediated during this session are now healthy.

Check the session context for evidence of remediation actions (docker restart,
docker compose up, ansible-playbook). For each remediated service:

1. Check HTTP health endpoint (curl -s -o /dev/null -w "%{http_code}" https://<service>.stump.rocks/health)
2. Check container status (ssh root@<host> docker ps --filter name=<service> --format '{{.Status}}')
3. Check DNS resolution (dig +short <service>.stump.rocks)

If any remediated service is still unhealthy, respond with:
  {"continue": true, "reason": "Service <name> still unhealthy: <details>"}

If all remediated services are healthy (or no remediation occurred), respond with:
  Session complete. All remediated services verified healthy.
```

**Key design decisions**:
- The verification agent is lightweight -- it runs targeted health checks, not a full observation sweep. This limits API token consumption.
- The agent uses the same health check patterns as Tier 1 observation, maintaining consistency.
- If no remediation occurred (e.g., a Tier 1 session), the agent detects this from context and exits immediately.
- The `{"continue": true}` response causes Claude to resume working on the problem, which is the desired behavior for a failed verification.

### session-context.sh (SessionStart -- Dynamic Context Injection)

**Logic flow**:

```
1. Read cooldown state:
   - Parse $CLAUDEOPS_STATE_DIR/cooldown.json with jq
   - For each service: calculate remaining restart budget and redeployment budget
   - Format as human-readable summary

2. Read recent events:
   - Query SQLite: SELECT * FROM events ORDER BY created_at DESC LIMIT 10
   - Format as a table: timestamp | level | service | message

3. Check host connectivity:
   - For each known host (ie01, pie01, pie02, int01):
     ping -c1 -W2 <ip> > /dev/null 2>&1 && echo "reachable" || echo "unreachable"
   - Format as host status list

4. Output combined summary to stdout
```

**Output format**:

```
=== Claude Ops Session Context (auto-injected) ===

Cooldown State:
  jellyfin: 1/2 restarts (4h), 0/1 redeployments (24h) — next reset: 2026-03-21T18:30:00Z
  adguard: 0/2 restarts, 0/1 redeployments
  nginx: 0/2 restarts, 0/1 redeployments

Recent Events (last 10):
  2026-03-21T14:22:00Z | warning  | jellyfin | Container restarted: docker restart jellyfin
  2026-03-21T14:15:00Z | info     | -        | Pull request created: Fix jellyfin config
  2026-03-21T13:50:00Z | warning  | nginx    | Container restarted: docker restart nginx

Host Connectivity:
  ie01 (192.168.100.210): reachable
  pie01 (192.168.100.220): reachable
  pie02 (192.168.100.221): reachable
  int01 (192.168.5.30): reachable

=== End Session Context ===
```

**Key design decisions**:
- The output is plain text, not JSON, because it will be injected directly into the LLM's context where readability matters more than parseability.
- Host IPs are hardcoded in the script. This is acceptable because the host list changes infrequently and is already hardcoded in other places (playbooks, inventory). A future improvement could read hosts from a configuration file.
- The `ping` timeout is set to 2 seconds per host. With 4 hosts, the worst-case total is 8 seconds if all hosts are unreachable. This is acceptable for a SessionStart hook.
- If the SQLite database or cooldown.json is missing, the script outputs "No data available" for that section rather than failing. Fail-open is the correct behavior for context injection -- missing context is better than a blocked session.

### notify-apprise.sh (Notification -- Apprise Bridge)

**Logic flow**:

```
1. Check if CLAUDEOPS_APPRISE_URLS is set and non-empty
   - If unset/empty: exit 0 silently
2. Read JSON from stdin, extract notification title and body
3. Invoke apprise:
   apprise -t "$TITLE" -b "$BODY" "$CLAUDEOPS_APPRISE_URLS"
4. If apprise fails: log error to stderr, exit 0
   (notification failure must not break the session)
5. Exit 0
```

**Key design decisions**:
- The hook checks `CLAUDEOPS_APPRISE_URLS` at runtime, not at configuration time. This means the hook is always configured in `settings.json` but is a no-op when the environment variable is unset. This matches the existing graceful degradation pattern from ADR-0004.
- Apprise failures are logged to stderr but do not cause the hook to exit non-zero. A non-zero exit from a Notification hook could interfere with session behavior, and notification delivery is never worth blocking agent operations.
- The hook replaces the need for tier prompts to instruct the LLM to invoke `apprise` commands. Tier 2 and Tier 3 prompts can remove their "Step 5: Send Notification" sections once this hook is in place. However, the `[EVENT:...]` marker format should be retained in prompts for rich event context that the PostToolUse hook cannot infer.

## Cooldown Check Flow (Detailed)

```
PreToolUse fires for Bash("docker restart jellyfin")
    |
    v
cooldown-check.sh receives on stdin:
{
  "tool_name": "Bash",
  "tool_input": {
    "command": "docker restart jellyfin"
  }
}
    |
    v
Extract command: "docker restart jellyfin"
    |
    v
Pattern match:
  case "docker restart *" -> type=restart, service=jellyfin
    |
    v
Read $CLAUDEOPS_STATE_DIR/cooldown.json:
{
  "services": {
    "jellyfin": {
      "restart_timestamps": [
        "2026-03-21T14:00:00Z",
        "2026-03-21T14:22:00Z"
      ],
      "redeployment_timestamps": []
    }
  }
}
    |
    v
Count restarts in last 4h: 2
Max restarts per 4h: 2
    |
    v
2 >= 2 -> DENY
    |
    v
Calculate next allowed: earliest_timestamp + 4h = 2026-03-21T18:00:00Z
    |
    v
Output to stdout:
{
  "decision": "deny",
  "reason": "Cooldown limit exceeded for jellyfin: 2/2 restarts in last 4h. Next allowed at 2026-03-21T18:00:00Z."
}
    |
    v
Bash command is NOT executed
Claude receives denial reason in context
```

## Event Emission Flow (Detailed)

```
PostToolUse fires after Bash("docker restart jellyfin") succeeds
    |
    v
event-emit.sh receives on stdin:
{
  "tool_name": "Bash",
  "tool_input": {
    "command": "docker restart jellyfin"
  },
  "session_id": "sess-abc-123"
}
    |
    v
Extract command: "docker restart jellyfin"
Extract session_id: "sess-abc-123"
    |
    v
Pattern match:
  case "docker restart *" -> level=warning, service=jellyfin
    |
    v
Construct message: "Container restarted: docker restart jellyfin"
    |
    v
sqlite3 "$CLAUDEOPS_DB_PATH" \
  "INSERT INTO events (session_id, level, service, message, created_at)
   VALUES ('sess-abc-123', 'warning', 'jellyfin',
           'Container restarted: docker restart jellyfin',
           datetime('now'));"
    |
    v
Exit 0 (success, no output needed)
```

## Interaction with Existing Layers

### Layer 2 (--disallowedTools) vs. Layer 3 (Hooks)

These layers are complementary and ordered:

| Scenario | Layer 2 (disallowedTools) | Layer 3 (Hooks) | Result |
|----------|--------------------------|-----------------|--------|
| Tier 1: `docker restart jellyfin` | BLOCKED (prefix match) | Hook never fires | Blocked by Layer 2 |
| Tier 2: `docker restart jellyfin` (within cooldown) | Not blocked | Hook allows | Executed |
| Tier 2: `docker restart jellyfin` (cooldown exceeded) | Not blocked | Hook denies | Blocked by Layer 3 |
| Tier 2: `ansible-playbook ...` | BLOCKED (prefix match) | Hook never fires | Blocked by Layer 2 |
| Tier 3: `ansible-playbook ...` (within cooldown) | Not blocked | Hook allows | Executed |
| Any tier: `curl -s https://...` | Not blocked | Hook allows (no pattern match) | Executed |

The key insight: `--disallowedTools` provides tier-level access control (can this tier ever run this command?), while hooks provide instance-level access control (can this service tolerate this action right now?).

### PostToolUse Events vs. LLM `[EVENT:...]` Markers

Both mechanisms coexist:

| Aspect | PostToolUse Hook | LLM [EVENT:...] Marker |
|--------|-----------------|----------------------|
| Reliability | Deterministic -- always fires after tool use | Probabilistic -- LLM may forget |
| Context richness | Limited to command parsing | Rich -- LLM can describe WHY it acted |
| Event types | Only infrastructure actions (commands) | Any observation (config drift, degraded state) |
| Deduplication | Hook-emitted events include `source=hook` tag | LLM-emitted events include `source=llm` tag |

The PostToolUse hook guarantees that every `docker restart`, `ansible-playbook`, and `gh pr create` is logged. The LLM's `[EVENT:...]` markers add qualitative context: "Jellyfin database was corrupted, restarted after clearing lock file." Both are stored in the same events table.

## Failure Modes and Mitigations

| Failure | Impact | Mitigation |
|---------|--------|------------|
| cooldown.json missing | Hook cannot check limits | Fail-open: exit 0, allow the command. Log warning to stderr. |
| SQLite database missing | Events cannot be inserted | Fail-open: event-emit.sh logs to stderr, exits 0. Events are lost but agent continues. |
| Hook script not executable | Hook fails to run | Claude Code reports hook failure. Agent operation continues (fail-open). |
| jq not installed | JSON parsing fails | jq is already in the container image (required by cooldown.json operations). |
| sqlite3 not installed | Event insertion fails | sqlite3 must be added to the container image if not already present. |
| Hook timeout (>30s) | Agent blocked | Keep hook scripts fast. cooldown-check.sh should complete in <100ms. session-context.sh in <10s. |
| Malformed stdin JSON | Hook cannot parse input | Fail-open: if jq parse fails, exit 0. Log error. |

## Performance Considerations

The PreToolUse hook fires on every Bash tool call, including read-only commands like `curl`, `docker ps`, `cat`, etc. Most invocations will not match any restart/redeployment pattern, so the hot path is:

```
1. Read stdin JSON (pipe, ~1ms)
2. Extract command with jq (~5ms)
3. Case statement pattern match (~0ms)
4. No match -> exit 0
```

Total overhead for non-matching commands: ~6ms per Bash call. For matching commands, add:

```
5. Read cooldown.json with jq (~5ms)
6. Filter timestamps and count (~5ms)
7. Decision and output (~1ms)
```

Total overhead for matching commands: ~17ms. This is negligible relative to the Bash command execution time and LLM inference latency.

## References

- [ADR-0029: Hooks for Deterministic Lifecycle Guardrails](../../adrs/ADR-0029-hooks-lifecycle-guardrails.md)
- [ADR-0003: Prompt-Based Permission Enforcement](../../adrs/ADR-0003-prompt-based-permission-enforcement.md)
- [ADR-0007: JSON File Cooldown State](../../adrs/ADR-0007-json-file-cooldown-state.md)
- [ADR-0014: Real-Time Dashboard and Events System](../../adrs/ADR-0014-realtime-dashboard-and-events.md)
- [ADR-0004: Use Apprise CLI for Universal Notification Abstraction](../../adrs/ADR-0004-apprise-notification-abstraction.md)
- [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md)
- [SPEC-0030: Hooks-Based Lifecycle Guardrails](./spec.md)
- [SPEC-0027: AllowedTools-Based Tier Enforcement](../allowedtools-tier-enforcement/spec.md)
