---
status: proposed
date: 2026-03-21
decision-makers: Joe Stump
supplements: ADR-0003, ADR-0007, ADR-0014, ADR-0004, ADR-0023
---

# ADR-0029: Hooks for Deterministic Lifecycle Guardrails

## Context and Problem Statement

Claude Ops enforces tier permissions through three layers: `--allowedTools` whitelist, `--disallowedTools` blocklist (ADR-0023), and prompt instructions. While the first two layers are hard CLI boundaries, certain enforcement needs remain soft and probabilistic:

1. **Cooldown enforcement** -- The cooldown system (ADR-0007, SPEC-0007) limits restarts to 2/service/4h and redeployments to 1/service/24h. Currently enforced by prompt instructions telling the agent to check `cooldown.json`. If the LLM ignores or misparses the cooldown file, it can exceed limits.

2. **Event emission** -- The real-time events system (ADR-0014, SPEC-0013) relies on the LLM outputting `[EVENT:level[:service]] message` markers in its response text. If the LLM forgets or formats markers incorrectly, events are silently lost.

3. **Post-remediation verification** -- After restarting a service, there is no deterministic check that the fix actually worked before the session ends.

4. **Dynamic context injection** -- Each session starts with static prompt files. Dynamic state (current cooldown counts, recent events, host status) must be manually embedded in prompts or queried by the agent at runtime, leading to stale state or wasted tokens on discovery.

5. **Notification reliability** -- Apprise notifications (ADR-0004) are invoked by the LLM via Bash commands in tier prompts. If the LLM forgets or the Apprise invocation is malformed, operator notifications are lost.

Claude Code now provides **hooks** -- deterministic shell commands that execute at specific lifecycle points. Hooks run as shell commands, receive event data as JSON on stdin, and can block actions (exit 2), allow actions (exit 0), or return structured JSON decisions. The available hook types are:

- **PreToolUse**: Fires before a tool executes. Can block with exit 2 or structured JSON `{"decision": "deny", "reason": "..."}`. Fires in `-p` (headless) mode.
- **PostToolUse**: Fires after a tool succeeds. Can inject feedback into the conversation.
- **Stop**: Fires when Claude finishes responding. Agent-based hooks can spawn a verification subagent to continue work.
- **SessionStart**: Fires when a session begins. Stdout is added to Claude's context.
- **Notification**: Fires when Claude sends a notification event.

Critically, hooks work in `-p` mode (headless/non-interactive), which is how Claude Ops invokes the CLI. `PermissionRequest` hooks do NOT fire in `-p` mode, but `PreToolUse` hooks DO -- making PreToolUse the correct choice for headless pre-execution enforcement.

The `--disallowedTools` layer (ADR-0023) blocks commands based on prefix matching. It cannot consult runtime state. A `--disallowedTools` pattern like `Bash(docker restart:*)` blocks ALL docker restarts for a tier, but it cannot say "block docker restart for this specific service because it has exhausted its cooldown budget." Hooks fill this gap: they can read `cooldown.json`, evaluate per-service limits, and block selectively based on runtime state.

## Decision Drivers

* **Defense in depth** -- Add a fourth deterministic enforcement layer for things currently only prompt-enforced, without replacing any existing layer.
* **Cooldown violations are the highest-risk soft enforcement gap** -- An LLM that ignores cooldown limits can restart a service indefinitely, causing cascading failures. This is the scenario ADR-0007 was designed to prevent.
* **Event emission reliability directly affects operator visibility** -- If the LLM fails to emit `[EVENT:...]` markers, the operator has no real-time view of what the agent is doing. The dashboard (ADR-0014) becomes a blind spot.
* **Hooks are the first mechanism that can enforce runtime state checks deterministically** -- Unlike `--disallowedTools` (which matches static prefixes), hooks can read files, query databases, and make decisions based on current state.
* **Zero application code** -- Hooks are shell scripts, fitting the markdown+bash architecture. No Go code, no compiled binaries, no new services.
* **Hooks work in headless `-p` mode** -- Claude Ops runs all sessions headlessly. PreToolUse hooks fire in this mode, unlike PermissionRequest hooks which are skipped.
* **Composable with existing layers** -- Hooks add a fourth layer without replacing `--allowedTools`, `--disallowedTools`, or prompt instructions. Each layer catches different things.

## Considered Options

1. **Hooks for deterministic guardrails** -- Use PreToolUse for cooldown enforcement, PostToolUse for event emission, Stop (agent-based) for remediation verification, SessionStart for context injection, Notification for Apprise bridging.
2. **Custom middleware binary** -- Build a Go binary that wraps the Claude CLI, intercepts tool calls via stdin/stdout parsing, and enforces cooldown checks programmatically.
3. **Enhanced prompt engineering** -- Improve prompt instructions with more explicit cooldown-checking procedures, event emission reminders, and verification steps.
4. **No change** -- Accept current prompt-based enforcement gaps.

## Decision Outcome

Chosen option: **"Hooks for deterministic guardrails"**, because it adds a fourth enforcement layer using shell scripts that execute at CLI lifecycle points, without requiring application code changes. Hooks are the only mechanism that can enforce runtime state checks (cooldown.json) deterministically at the CLI boundary while composing cleanly with the existing three layers.

### Concrete Hooks to Implement

**PreToolUse hook -- Cooldown enforcement:**
- Matcher: `Bash`
- Script checks if the Bash command matches restart patterns (`docker restart`, `docker compose up`, `docker stop/start`) or redeployment patterns (`ansible-playbook`, `helm upgrade`)
- Reads `cooldown.json` from `$CLAUDEOPS_STATE_DIR`, extracts per-service counters
- If cooldown limits are exceeded (2 restarts/4h or 1 redeployment/24h), returns structured JSON: `{"decision": "deny", "reason": "Cooldown limit exceeded for <service>: <N>/<max> restarts in last 4h. Next allowed at <time>."}`
- If within limits, exits 0 (allow)

**PostToolUse hook -- Event emission:**
- Matcher: `Bash`
- After Bash commands matching significant infrastructure actions (`docker restart`, `docker compose up`, `ansible-playbook`, `gh pr create`, `apprise`), inserts an event into the events SQLite table
- Parses `tool_input.command` from stdin JSON to detect action type and extract service name
- Uses `sqlite3` CLI to INSERT into the `events` table with session_id, level, service, and descriptive message
- Does not block or modify the tool result -- purely side-effect

**Stop hook -- Remediation verification (agent-based):**
- Type: `agent`
- Fires when a Tier 2 or Tier 3 session completes
- The verification agent checks whether the remediated service is now healthy (HTTP endpoint, DNS resolution, or container status as appropriate)
- Returns `{"continue": true, "reason": "..."}` if verification fails, causing Claude to continue working on the problem
- Returns nothing (or `{"continue": false}`) if verification passes

**SessionStart hook -- Dynamic context injection:**
- Fires at session start
- Reads current `cooldown.json` and summarizes per-service cooldown state (remaining budget, time until reset)
- Queries the last 10 events from the SQLite database
- Checks host connectivity (`ping -c1` to known hosts)
- Outputs a structured context summary to stdout, which Claude Code adds to the LLM's context

**Notification hook -- Apprise bridge:**
- Fires when Claude Code emits a notification event
- Reads `$CLAUDEOPS_APPRISE_URLS`; if unset or empty, exits 0 silently
- Forwards the notification title and body to `apprise` CLI
- Replaces the need for the LLM to manually invoke `apprise` commands in tier prompts

### Four-Layer Enforcement Model

After this ADR, the enforcement stack becomes:

```
+---------------------------------------------------------+
|          Claude Code CLI  (Hard Boundary)                |
|                                                          |
|  Layer 1: --allowedTools                                 |
|  +----------------------------------------------------+ |
|  |  Tool-type whitelist: Bash, Read, Grep, Glob...    | |
|  |  Agent cannot invoke Write, Edit, etc. at Tier 1   | |
|  +----------------------------------------------------+ |
|                                                          |
|  Layer 2: --disallowedTools  (ADR-0023)                  |
|  +----------------------------------------------------+ |
|  |  Command-prefix blocklist:                          | |
|  |  Bash(docker restart:*), Bash(ansible-playbook:*)   | |
|  |  Blocks dangerous commands within allowed types     | |
|  +----------------------------------------------------+ |
|                                                          |
|  Layer 3: Hooks  (ADR-0029 -- THIS ADR)                  |
|  +----------------------------------------------------+ |
|  |  Runtime state-aware enforcement:                   | |
|  |  PreToolUse: Cooldown checks before Bash commands   | |
|  |  PostToolUse: Event emission after infra actions    | |
|  |  Stop: Remediation verification via subagent        | |
|  |  SessionStart: Dynamic context injection            | |
|  |  Notification: Apprise bridge                       | |
|  +----------------------------------------------------+ |
+---------------------------------------------------------+
                      | passes
                      v
+---------------------------------------------------------+
|          Prompt Instructions  (Soft Boundary)            |
|                                                          |
|  Layer 4: Tier prompt + skill scope rules + CLAUDE.md    |
|  +----------------------------------------------------+ |
|  |  Handles what CLI layers cannot express:            | |
|  |  - SSH-tunneled remote commands                     | |
|  |  - Argument-level scope (which files a PR touches)  | |
|  |  - "Never Allowed" ops without distinct prefixes    | |
|  +----------------------------------------------------+ |
+---------------------------------------------------------+
```

### Hook Configuration

Hooks are defined in `.claude/settings.json` under the `hooks` key. Hook scripts live in `.claude/hooks/` as executable shell scripts. The configuration is project-scoped and checked into the repository.

### Consequences

**Positive:**

* Cooldown enforcement becomes deterministic -- the LLM cannot exceed restart/redeployment limits regardless of its reasoning. The PreToolUse hook reads `cooldown.json` and blocks the Bash call before execution.
* Events are emitted reliably after significant infrastructure actions, not dependent on the LLM remembering to write `[EVENT:...]` markers. PostToolUse hooks insert events directly into SQLite.
* Post-remediation verification happens automatically via the Stop hook's verification subagent, reducing mean-time-to-detect for failed fixes.
* Dynamic context injection at SessionStart eliminates stale state issues -- every session begins with current cooldown budget, recent events, and host connectivity.
* The Notification hook bridges Claude Code notifications to Apprise without the LLM needing to invoke `apprise` commands, making notification delivery deterministic.
* Shell scripts fit the zero-application-code architecture -- no Go code, no compiled binaries.
* Composes with existing three layers (adds a fourth) without replacing any. Each layer catches different classes of violations.
* Hook scripts are checked into the repository and auditable alongside the rest of the runbook.

**Negative:**

* Hook scripts must be maintained alongside tier configurations and `--disallowedTools` patterns. Changes to cooldown limits or event categories require updating hook scripts.
* PreToolUse hooks add latency to every Bash tool call. The cooldown check reads a JSON file and runs `jq` -- typically <50ms, but it applies to every Bash invocation, not just infrastructure commands.
* Agent-based Stop hooks consume additional API tokens for the verification subagent. This cost scales with the number of Tier 2/3 sessions that perform remediation.
* Hook scripts need access to the SQLite database (for event insertion and querying) and the cooldown state file. Both paths must be configured correctly in the container.
* PreToolUse cooldown checks depend on pattern matching within the hook script to identify restart/redeployment commands, similar to `--disallowedTools` limitations. Shell indirection and SSH tunneling can evade these patterns.
* `PermissionRequest` hooks do not fire in `-p` mode. If a future Claude Code change alters hook behavior in headless mode, the enforcement layer could silently degrade.
* The PostToolUse event emission hook writes to the SQLite database outside the normal Go session manager codepath, requiring the database to be accessible from the hook's execution context.

## Pros and Cons of the Options

### Hooks for Deterministic Guardrails

Use Claude Code's hook system (PreToolUse, PostToolUse, Stop, SessionStart, Notification) to add deterministic enforcement for cooldown limits, event emission, remediation verification, context injection, and notification delivery.

* Good, because hooks execute deterministically as shell commands at CLI lifecycle points -- the LLM cannot bypass them.
* Good, because PreToolUse hooks can read runtime state (cooldown.json) and make per-service, per-invocation decisions that `--disallowedTools` cannot express.
* Good, because PostToolUse hooks reliably emit events after infrastructure actions without depending on LLM memory.
* Good, because the Stop hook's agent-based verification catches failed remediations before the session ends.
* Good, because SessionStart hooks inject current state into every session, eliminating stale context.
* Good, because the Notification hook makes Apprise delivery deterministic, replacing prompt-instructed `apprise` calls.
* Good, because hooks are shell scripts -- zero compiled code, fitting the runbook architecture.
* Good, because hooks compose with existing layers (allowedTools, disallowedTools, prompts) without replacing any.
* Good, because hooks work in `-p` (headless) mode, which is how Claude Ops invokes the CLI.
* Bad, because hook scripts add a maintenance surface alongside existing tier configurations.
* Bad, because PreToolUse hooks add latency to every Bash tool call (file read + jq parse on each invocation).
* Bad, because agent-based Stop hooks consume additional API tokens for verification.
* Bad, because pattern matching within hook scripts has the same evasion risks as `--disallowedTools` (SSH tunneling, shell indirection).
* Bad, because hook scripts executing outside the Go session manager must independently access SQLite and cooldown state.
* Neutral, because hooks do not replace any existing enforcement layer -- they add a fourth, increasing the total maintenance surface but also the total coverage.

### Custom Middleware Binary

Build a Go binary that wraps the `claude` CLI invocation, parses stdout/stdin to intercept tool calls, and enforces cooldown checks and event emission programmatically before/after tool execution.

* Good, because a compiled binary can perform arbitrary logic (cooldown checks, event insertion, verification) with full programmatic control.
* Good, because a single binary consolidates all enforcement logic in one place.
* Good, because Go is already used in the project (session manager, dashboard), so the toolchain is available.
* Bad, because it introduces application code into the agent execution path, contradicting the "no application code in the agent loop" principle.
* Bad, because parsing Claude Code's stdin/stdout to intercept tool calls is fragile and depends on undocumented wire formats.
* Bad, because it creates a tight coupling between the middleware and Claude Code's internal tool execution protocol.
* Bad, because the middleware becomes a critical path dependency -- any bug in the wrapper blocks all agent execution.
* Bad, because it requires building, testing, and maintaining a new binary alongside the existing session manager.
* Bad, because hooks are an officially supported, documented mechanism for this exact purpose, making a custom wrapper redundant.

### Enhanced Prompt Engineering

Improve prompt instructions to reduce LLM compliance failures: add explicit step-by-step cooldown checking procedures, event emission reminders at key decision points, and mandatory verification steps before session completion.

* Good, because it requires no infrastructure changes -- only markdown edits.
* Good, because it is immediately deployable without new dependencies or code.
* Good, because it improves the quality of the soft enforcement layer regardless of whether other layers are added.
* Bad, because prompt-based enforcement remains fundamentally probabilistic -- no amount of prompt engineering makes it deterministic.
* Bad, because longer, more detailed prompts consume more tokens and may actually reduce compliance as the context window fills.
* Bad, because the LLM may still skip cooldown checks under time pressure, context window limits, or when focused on a complex remediation chain.
* Bad, because event emission reliability depends on the LLM formatting markers correctly every time, which prompt engineering cannot guarantee.
* Bad, because ADR-0003 already acknowledged that prompt instructions are "not a security boundary" -- doubling down on prompts does not change this fundamental limitation.
* Neutral, because prompt improvements are complementary to hooks and should be done regardless. This option is not mutually exclusive with Option 1.

### No Change

Accept current prompt-based enforcement gaps for cooldown limits, event emission, and remediation verification.

* Good, because it requires zero implementation effort.
* Good, because it avoids adding maintenance surface for hook scripts.
* Good, because the current system works adequately when the LLM follows instructions, which it does in the majority of cases.
* Bad, because cooldown violations can cause cascading service failures -- the highest-risk scenario in the entire system.
* Bad, because silent event loss degrades operator visibility, undermining the dashboard's value (ADR-0014).
* Bad, because failed remediations go undetected until the next monitoring cycle, increasing mean-time-to-detect.
* Bad, because it contradicts the defense-in-depth principle established in ADR-0003 and extended in ADR-0023.
* Bad, because Claude Code hooks are now available and purpose-built for this use case -- choosing not to use them leaves a known gap with an available solution.

## More Information

* **Supplements ADR-0003**: Adds a fourth enforcement layer (hooks) to the existing model of allowedTools + disallowedTools + prompt instructions.
* **Supplements ADR-0023**: Hooks fill the gap that `--disallowedTools` cannot: runtime state-aware enforcement. Where `--disallowedTools` blocks all `docker restart` at Tier 1, hooks can selectively block `docker restart jellyfin` at Tier 2 because jellyfin has exhausted its cooldown budget.
* **Supplements ADR-0007**: Cooldown enforcement moves from prompt-only to deterministic PreToolUse hook enforcement. The `cooldown.json` file format and limits are unchanged.
* **Supplements ADR-0014**: Event emission moves from LLM-dependent `[EVENT:...]` markers to deterministic PostToolUse hook insertion into the events table. The LLM marker format is retained as a complementary mechanism.
* **Supplements ADR-0004**: The Notification hook bridges Claude Code notification events to Apprise, replacing the need for tier prompts to instruct the LLM to invoke `apprise` commands.
* **See SPEC-0030** for formal requirements and scenarios.
* **Claude Code hooks documentation**: https://docs.anthropic.com/en/docs/claude-code/hooks
* **Hook types**: `command` (shell script), `agent` (spawns a subagent). PreToolUse and PostToolUse support matchers to filter by tool name.
