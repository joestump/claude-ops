---
status: proposed
date: 2026-03-21
---

# SPEC-0032: Session Continuity via Resume

## Overview

Claude Ops escalates through permission tiers (Tier 1 observe -> Tier 2 safe remediation -> Tier 3 full remediation) by spawning separate `claude -p` CLI processes per tier. ADR-0016 defined the context-passing mechanism as structured JSON handoff files. ADR-0031 replaces that mechanism with `--resume <session_id>`, which continues the previous tier's conversation with full context preservation while changing the model, `--allowedTools`, and `--disallowedTools` for the new tier.

This specification formalizes the requirements for resume-based escalation: session ID capture, resume invocation, escalation prompt format, session chain linking, per-tier cost attribution, tool restriction override, handoff file deprecation, and context window management.

See [ADR-0031: Session Continuity via --resume for Escalation Chains](../../adrs/ADR-0031-session-continuity-resume.md) for the decision rationale.

## Definitions

- **Session ID**: A unique identifier assigned to each `claude -p` conversation by the Claude Code CLI. Available in the JSON output format as `session_id`.
- **Resume**: The `--resume <session_id>` CLI flag that continues an existing conversation, appending new prompts and tool calls to the prior context.
- **Escalation chain**: A sequence of linked sessions where each subsequent session resumes the previous one at a higher tier. Example: Session #18 (Tier 1) -> Session #19 (Tier 2) -> Session #20 (Tier 3).
- **Parent session**: The session that was resumed to create the current session. Tracked via `parent_session_id` in the sessions table.
- **Escalation prompt**: The `-p` prompt passed on the resumed invocation that describes the tier change and expanded permissions without repeating the investigation findings.
- **Handoff file**: The structured JSON file (`handoff.json`) used by ADR-0016 to pass context between tiers. Deprecated by this specification in favor of `--resume`.
- **Context window utilization**: The percentage of the model's context window consumed by the conversation history, including all prior tiers' tool outputs and reasoning.

## Requirements

### REQ-1: Session ID Capture

The session manager MUST capture the `session_id` from the JSON output of every `claude -p` invocation. The session ID MUST be stored in the sessions table alongside the existing session metadata (tier, model, cost, duration, status).

The session manager MUST use `--output-format json` (or equivalent) to obtain structured output that includes the `session_id` field. If the JSON output does not contain a `session_id` field, the session manager MUST log a warning and fall back to the handoff file mechanism for that session.

#### Scenario: Session ID captured from successful Tier 1 invocation

Given the session manager invokes `claude -p --output-format json` for a Tier 1 session
When the CLI process completes and returns JSON output
Then the session manager MUST parse the `session_id` field from the response
And store it in the sessions table record for that session

#### Scenario: Session ID stored in database

Given a Tier 1 session completes with `session_id = "sess_abc123"`
When the session manager updates the session record in the database
Then the sessions table row MUST contain `session_id = "sess_abc123"`
And the session ID MUST be queryable for future resume operations

#### Scenario: Missing session ID triggers fallback

Given the session manager parses JSON output from a CLI invocation
When the `session_id` field is absent or null
Then the session manager MUST log a warning with the session details
And the session manager MUST use the handoff file mechanism (ADR-0016) for any subsequent escalation from that session

### REQ-2: Resume-Based Escalation

When escalation is needed, the session manager MUST invoke the next tier using `claude -p --resume <session_id>` with the escalating tier's session ID. The resume invocation MUST include the new tier's `--model`, `--allowedTools`, and `--disallowedTools` flags.

The session manager MUST NOT pass `--append-system-prompt` with handoff file contents when using `--resume`. The conversation context from the prior tier is already present in the resumed session.

#### Scenario: Tier 1 escalates to Tier 2 via resume

Given Tier 1 session completes with `session_id = "sess_abc123"` and requests escalation
When the session manager spawns the Tier 2 process
Then the invocation MUST be `claude -p --resume "sess_abc123" --model sonnet --allowedTools "..." --disallowedTools "..." "<escalation_prompt>"`
And the Tier 2 process MUST have access to the full Tier 1 conversation context

#### Scenario: Tier 2 escalates to Tier 3 via resume

Given Tier 2 session completes with `session_id = "sess_def456"` and requests escalation to Tier 3
When the session manager spawns the Tier 3 process
Then the invocation MUST use `--resume "sess_def456"` with Tier 3's model, allowedTools, and disallowedTools
And the Tier 3 process MUST have access to the full Tier 1 and Tier 2 conversation context

#### Scenario: Resume invocation does not include handoff content in system prompt

Given the session manager is performing a resume-based escalation
When it constructs the `claude` CLI command
Then the command MUST NOT include `--append-system-prompt` with serialized handoff JSON
And the escalation prompt (`-p`) MUST be the sole new input to the resumed conversation

### REQ-3: Escalation Prompt

The `-p` prompt for a resumed escalation MUST describe the tier change and expanded permissions. It MUST NOT repeat the investigation findings, as those are present in the conversation context from the prior tier.

The escalation prompt SHOULD reference the tier role (e.g., "You are now operating as Tier 2 with safe remediation permissions"). The prompt MUST specify what actions the new tier is authorized to take. The prompt SHOULD be concise -- under 500 tokens -- since the prior context provides the investigation details.

#### Scenario: Escalation prompt describes tier change without repeating findings

Given the session manager constructs an escalation prompt for Tier 2
When the prompt is passed via `-p` on the resumed invocation
Then the prompt MUST state the new tier level and permissions
And the prompt MUST NOT contain serialized check results, error messages, or diagnostic output from Tier 1
And the prompt SHOULD reference that the prior investigation context is available in the conversation history

#### Scenario: Escalation prompt specifies authorized actions

Given the session manager constructs an escalation prompt for Tier 2
When the prompt is passed via `-p`
Then the prompt MUST list the categories of actions Tier 2 is authorized to perform (e.g., container restarts, PR creation, notifications)
And the prompt MUST reference the tier's cooldown and dry-run constraints

#### Scenario: Escalation prompt for Tier 3 references prior tiers

Given the session manager constructs an escalation prompt for Tier 3
When Tier 3 is resuming a Tier 2 session (which previously resumed a Tier 1 session)
Then the prompt MUST state that the agent now operates as Tier 3 with full remediation permissions
And the prompt MUST state that prior investigation and safe remediation attempts are in the conversation history

### REQ-4: Session Chain Linking

The sessions table MUST support linking escalation chains. Each resumed session MUST reference its parent session via the `parent_session_id` column. The session detail page in the dashboard MUST display the full escalation chain for any session that is part of a chain.

A session chain MUST be traversable in both directions: from parent to child (forward escalation) and from child to parent (root cause investigation).

#### Scenario: Resumed session references parent

Given Tier 1 session has `id = 18` and Tier 2 is spawned via `--resume`
When the session manager creates the Tier 2 session record
Then the record MUST have `parent_session_id = 18`
And the Tier 2 session's `session_id` (from CLI output) MUST also be stored

#### Scenario: Three-tier chain is fully linked

Given a monitoring cycle escalates through all three tiers
When sessions #18 (Tier 1), #19 (Tier 2), and #20 (Tier 3) are created
Then session #19 MUST have `parent_session_id = 18`
And session #20 MUST have `parent_session_id = 19`
And querying the chain from session #20 MUST yield the ordered sequence [#18, #19, #20]

#### Scenario: Dashboard displays escalation chain

Given a user views session #20 (Tier 3) in the dashboard
When the session detail page loads
Then the page MUST display the full chain: Session #18 (Tier 1) -> Session #19 (Tier 2) -> Session #20 (Tier 3)
And each session in the chain MUST show its tier, model, cost, duration, and status

### REQ-5: Per-Tier Cost Attribution

Each tier's API usage MUST be tracked separately even when sessions are resumed. The session manager MUST record the cost metrics from each CLI invocation against the tier that generated it. A resumed session's cost MUST NOT be attributed to the parent session.

The dashboard MUST display per-tier cost breakdowns for escalation chains, showing how much each tier contributed to the total chain cost.

#### Scenario: Tier 2 cost is independent of Tier 1 cost

Given Tier 1 session costs $0.03 and Tier 2 (resumed) costs $0.47
When the session manager records costs
Then session #18 (Tier 1) MUST show `cost_usd = 0.03`
And session #19 (Tier 2) MUST show `cost_usd = 0.47`
And the costs MUST NOT be aggregated into a single session record

#### Scenario: Dashboard shows per-tier cost breakdown

Given an escalation chain with sessions #18 ($0.03), #19 ($0.47), #20 ($2.00)
When the dashboard displays the chain
Then each session MUST show its individual cost
And the chain summary MUST show the total cost ($2.50) with a per-tier breakdown

#### Scenario: Resumed session metrics come from CLI JSON output

Given Tier 2 completes and returns JSON output with cost and token usage
When the session manager updates the Tier 2 session record
Then it MUST use the cost and token metrics from the Tier 2 JSON response
And it MUST NOT sum or merge these metrics with the Tier 1 session record

### REQ-6: Tool Restriction Override

The `--allowedTools` and `--disallowedTools` flags on the resume invocation MUST take effect for the resumed session, overriding the original session's tool restrictions. The session manager MUST pass the new tier's tool configuration on every resume invocation.

The receiving tier MUST be able to use tools that were blocked in the prior tier's session, provided those tools are in the new tier's `--allowedTools` and not in the new tier's `--disallowedTools`.

#### Scenario: Tier 2 gains Write and Edit tools on resume

Given Tier 1 was invoked with `--allowedTools "Bash,Read,Grep,Glob,Task,WebFetch,WebSearch"`
When Tier 2 resumes with `--allowedTools "Bash,Read,Write,Edit,Grep,Glob,Task,WebFetch,WebSearch"`
Then the resumed session MUST permit `Write` and `Edit` tool invocations
Even though those tools were not available in the Tier 1 portion of the conversation

#### Scenario: Tier 2 disallowedTools replaces Tier 1 disallowedTools

Given Tier 1 had `--disallowedTools` including `Bash(docker restart:*)` and `Bash(gh pr create:*)`
When Tier 2 resumes with `--disallowedTools "Bash(ansible:*),Bash(ansible-playbook:*),Bash(helm:*),Bash(docker compose down:*)"`
Then `Bash(docker restart:*)` MUST be permitted in the resumed session (not in Tier 2's blocklist)
And `Bash(gh pr create:*)` MUST be permitted in the resumed session
And `Bash(ansible-playbook:*)` MUST be blocked in the resumed session

#### Scenario: Session manager always passes tier-appropriate tool flags

Given any resume-based escalation
When the session manager constructs the `claude` command
Then the command MUST include `--allowedTools` with the new tier's whitelist
And the command MUST include `--disallowedTools` with the new tier's blocklist
And the command MUST NOT omit these flags and rely on the prior session's settings

### REQ-7: Handoff File Deprecation

The system MUST NOT create handoff files for escalation when using resume-based escalation. The tier prompts MUST NOT include instructions to write `handoff.json` when `--resume` is the active escalation mechanism.

The handoff file mechanism (ADR-0016) MAY be retained as a fallback if `--resume` fails. The fallback MUST be triggered automatically by the session manager when:
- The `session_id` could not be captured (REQ-1 fallback condition)
- The `--resume` invocation fails with a non-zero exit code indicating session not found or expired
- The context window management check (REQ-8) determines the resumed session would exceed safe context limits

#### Scenario: No handoff file created during resume-based escalation

Given the session manager uses `--resume` for Tier 1 -> Tier 2 escalation
When Tier 1 completes and the session manager processes its output
Then no `handoff.json` file MUST exist in `$CLAUDEOPS_STATE_DIR`
And the Tier 1 prompt MUST NOT have instructed the agent to write a handoff file

#### Scenario: Fallback to handoff file when session ID is missing

Given Tier 1 completes but the JSON output does not contain `session_id`
When the session manager determines escalation is needed
Then it MUST fall back to the handoff file mechanism
And the session manager MUST instruct the Tier 1 agent (on next cycle) to write a handoff file
Or re-invoke Tier 1 with handoff-file-enabled prompts

#### Scenario: Fallback to handoff file when resume fails

Given the session manager attempts `claude -p --resume "sess_expired123"`
When the CLI returns a non-zero exit code indicating session not found
Then the session manager MUST log the resume failure
And the session manager MUST fall back to re-running the escalation via handoff file mechanism

### REQ-8: Context Window Management

The system SHOULD monitor context utilization during escalation chains. If the resumed session's context is near capacity, the system SHOULD fall back to handoff-file escalation rather than risk truncation or degraded model performance.

The session manager SHOULD track cumulative token usage across the escalation chain. A configurable threshold (default: 80% of the model's context window) SHOULD trigger the fallback.

#### Scenario: Context utilization below threshold proceeds with resume

Given Tier 1 used 15,000 tokens of a 200,000-token context window (7.5%)
When the session manager evaluates whether to use `--resume` for Tier 2
Then it MUST proceed with resume-based escalation
Because context utilization is well below the 80% threshold

#### Scenario: Context utilization above threshold triggers fallback

Given Tier 1 used 170,000 tokens of a 200,000-token context window (85%)
When the session manager evaluates whether to use `--resume` for Tier 2
Then it SHOULD fall back to the handoff file mechanism
And it SHOULD log that context window pressure triggered the fallback

#### Scenario: Configurable context threshold

Given the operator sets `CLAUDEOPS_RESUME_CONTEXT_THRESHOLD=0.70` in the environment
When the session manager evaluates context utilization
Then it MUST use 70% as the threshold instead of the default 80%
And it MUST document the effective threshold in the session log

## References

- [ADR-0031: Session Continuity via --resume for Escalation Chains](../../adrs/ADR-0031-session-continuity-resume.md)
- [ADR-0016: Session-Based Escalation with Structured Handoff Files](../../adrs/ADR-0016-session-based-escalation-handoff.md)
- [ADR-0010: Invoke Claude via Claude Code CLI as Subprocess](../../adrs/ADR-0010-claude-code-cli-subprocess.md)
- [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md)
- [SPEC-0027: AllowedTools-Based Tier Enforcement](../allowedtools-tier-enforcement/spec.md)
- [SPEC-0016: Session-Based Escalation](../session-based-escalation/spec.md)
