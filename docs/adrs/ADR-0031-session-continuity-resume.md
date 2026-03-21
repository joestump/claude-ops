---
status: proposed
date: 2026-03-21
decision-makers: Joe Stump
updates: ADR-0016 (escalation mechanism), ADR-0010 (new CLI flag usage)
---

# ADR-0031: Session Continuity via --resume for Escalation Chains

## Context and Problem Statement

Claude Ops uses a tiered escalation model (ADR-0001) where Tier 1 (Haiku) observes, escalates to Tier 2 (Sonnet) for safe remediation, and Tier 2 escalates to Tier 3 (Opus) for complex recovery. ADR-0016 defined the escalation mechanism: each tier runs as a separate `claude -p` session, and context is passed between tiers via structured JSON handoff files. The Go supervisor reads the handoff file after Tier 1 exits, injects its contents into the Tier 2 prompt via `--append-system-prompt`, and spawns a new CLI process.

The handoff file approach has served the project well but has four significant limitations:

1. **Context loss** -- Handoff files are summaries written by the escalating tier. The receiving tier gets a compressed version of findings, not the full investigation context (tool outputs, error messages, file contents read, intermediate reasoning). This forces the higher tier to re-run diagnostic commands the lower tier already executed.

2. **Serialization overhead** -- The escalating tier must format its findings into a structured JSON file. The Go session manager must read this file and inject it into the next tier's prompt. This adds complexity to both the prompt instructions (which tell the agent what to serialize) and the Go code (which parses and injects the JSON).

3. **Duplicate work** -- Tier 2 frequently re-runs the same `curl`, `dig`, `docker ps`, and `ssh` commands that Tier 1 already ran, because the full output wasn't in the handoff file -- only a summary was. This is observable in session logs where Tier 2's first several tool calls repeat Tier 1's diagnostics.

4. **Cost inefficiency** -- Re-running diagnostic commands wastes API tokens on tool calls whose results are already known. In a typical escalation from Tier 1 to Tier 2, 15-30% of Tier 2's tool calls duplicate work Tier 1 already completed.

Claude Code's CLI now supports `--resume <session_id>` for continuing a specific conversation, and `--continue` for continuing the most recent conversation. When used with `-p`, the new prompt is added to the existing conversation, preserving full context including all tool outputs, file reads, and intermediate reasoning. The `--model`, `--allowedTools`, and `--disallowedTools` flags can be changed on the resumed invocation, taking effect immediately.

This means Tier 2 could CONTINUE Tier 1's session instead of starting fresh, receiving the full investigation context automatically while operating with expanded permissions and a more capable model.

## Decision Drivers

* **Eliminate redundant diagnostic work across tiers** -- Tier 2 should not re-run commands that Tier 1 already executed. The full tool output history should be available.
* **Preserve full investigation context** -- Tool outputs, error messages, file contents, and intermediate reasoning from Tier 1 should flow to Tier 2 without lossy serialization.
* **Simplify Go session manager code** -- Handoff file parsing, JSON schema maintenance, and prompt injection add complexity that could be eliminated.
* **Maintain per-tier cost attribution** -- Each tier's API usage must remain independently trackable in the sessions table and dashboard.
* **Maintain per-tier tool restrictions** -- `--allowedTools` and `--disallowedTools` must change per tier (ADR-0023), and the resumed session must respect the new tier's permissions.
* **Session ID availability** -- The `session_id` is available in the JSON output format (`--output-format json`) of every `claude -p` invocation, making it capturable by the session manager.
* **Backward compatibility** -- The handoff file mechanism can be retained as a fallback for edge cases (session expiry, context window limits, CLI version mismatch).

## Considered Options

1. **Resume-based escalation** -- Tier 2 uses `claude -p --resume <tier1_session_id>` with a new escalation prompt, inheriting full Tier 1 context. Tool restrictions change via `--allowedTools` and `--disallowedTools` flags on the new invocation.
2. **Enhanced handoff files** -- Include full tool outputs in the handoff JSON, not just summaries. The existing mechanism is preserved but the serialization captures more context.
3. **Shared conversation log** -- Export Tier 1's conversation as a structured log file and inject it as context for Tier 2 via `--append-system-prompt`.
4. **No change** -- Keep the current handoff file approach from ADR-0016.

## Decision Outcome

Chosen option: **"Resume-based escalation"**, because it eliminates redundant diagnostic work, preserves full investigation context without lossy serialization, and simplifies the session manager by replacing handoff file I/O with a single `--resume` flag. The `--model`, `--allowedTools`, and `--disallowedTools` flags on the resumed invocation override the previous session's settings, enabling per-tier permission enforcement on the continued conversation.

### How It Works

When Tier 1 determines escalation is needed, the session manager captures Tier 1's `session_id` from the JSON output. Tier 2 is invoked with `claude -p --resume <session_id>` plus an escalation prompt, new `--allowedTools`, new `--disallowedTools`, and the Tier 2 `--model`. The Claude Code CLI loads the full Tier 1 conversation context and appends the escalation prompt.

Key architectural points:

- **Session ID capture**: The session manager already parses JSON output (for cost, token usage, session events). The `session_id` is a standard field in the JSON response, captured alongside existing fields.

- **Tool restriction changes**: `--allowedTools` and `--disallowedTools` are passed per invocation and override the previous session's settings. Tier 2's expanded permissions (e.g., `Write` and `Edit` added to `--allowedTools`; `docker restart` removed from `--disallowedTools`) take effect immediately on the resumed session.

- **Model upgrade**: `--model sonnet` or `--model opus` on the resume call upgrades the model for the continuation. The Tier 1 context (generated by Haiku) is readable by the higher-capability model -- this is the normal direction of capability upgrade.

- **Cost tracking**: The resumed session generates a new set of API usage metrics in the JSON response. The session manager records this as Tier 2 cost, attributed to the Tier 2 session record. The resumed session does not retroactively change Tier 1's cost.

- **Session chain linking**: The sessions table uses a `parent_session_id` column (already planned in ADR-0016) to link escalation chains. Each CLI invocation gets its own session record for per-tier cost tracking, but they are linked. The dashboard displays the chain: Session #18 (Tier 1) -> Session #19 (Tier 2) -> Session #20 (Tier 3).

- **Handoff file elimination**: The escalation prompt replaces the handoff file. Instead of "Investigate these findings: {handoff_json}", the prompt is: "The previous investigation found issues requiring escalation. You now have expanded permissions as Tier 2. Investigate further and remediate." The full context of what was found -- every `curl` output, every `docker ps` listing, every error message -- is already in the conversation history.

### What Changes from ADR-0016

- Handoff file creation by the agent (writing `handoff.json`) is eliminated.
- Handoff file parsing by the session manager (reading and injecting `handoff.json` content) is eliminated.
- The session manager passes `--resume <session_id>` instead of `--append-system-prompt` with handoff content.
- The escalation prompt is simplified: it describes the tier change and permissions, not the findings.
- The agent no longer needs instructions for serializing findings into a JSON schema.

### What Stays the Same from ADR-0016

- Each tier still has distinct `--allowedTools` and `--disallowedTools` (ADR-0023).
- Each tier still has its own session record with per-tier cost attribution.
- The Go session manager still orchestrates the escalation decision (the supervisor controls when to escalate, not the LLM).
- Tier prompts still define the role and permissions for each tier.
- The `parent_session_id` column still links escalation chains in the sessions table.
- The supervisor still enforces cooldowns, dry-run mode, and max-tier limits before spawning a higher tier.

### Consequences

**Positive:**

* Full context preservation -- Tier 2 receives every tool output, file read, error message, and intermediate reasoning from Tier 1. No information is lost in serialization.
* No duplicate diagnostics -- Tier 2 does not re-run `curl`, `dig`, `docker ps`, or `ssh` commands that Tier 1 already executed. The results are in the conversation history.
* Simpler Go code -- Handoff file I/O (write path in prompts, read/parse/delete path in supervisor, JSON schema definition) is replaced by passing `--resume <session_id>`.
* Lower API costs -- Eliminating 15-30% redundant tool calls per escalation saves tokens and API dollars.
* Faster escalation -- No re-investigation phase at the start of Tier 2. The agent can begin remediation immediately.
* Cleaner escalation prompts -- The Tier 2 prompt describes the role and permissions, not a serialized copy of findings. The prompt is shorter and less fragile.
* Backward-compatible -- The handoff file mechanism can be retained as a fallback if `--resume` fails (session expired, context window near capacity, CLI version mismatch).

**Negative:**

* Resumed sessions accumulate context from all prior tiers. A Tier 1 -> Tier 2 -> Tier 3 chain carries the full Tier 1 and Tier 2 context into Tier 3, which may approach context window limits for complex multi-service investigations.
* Session ID coupling -- The session manager depends on the `session_id` field in the CLI's JSON output. Changes to the output format or session storage in future CLI versions could break this.
* DB schema dependency -- The `parent_session_id` column (from ADR-0016) is still required. This ADR does not simplify the schema, only the supervisor code.
* Less explicit escalation context -- With handoff files, an operator could inspect exactly what was communicated between tiers. With `--resume`, the full conversation history is the context, which is harder to audit at a glance.
* Context window cost -- The resumed session pays for the input tokens of the full prior conversation on every API call, even though much of that context may not be relevant to the current tier's work.

## Pros and Cons of the Options

### Resume-Based Escalation

Tier 2 uses `claude -p --resume <tier1_session_id>` with a new escalation prompt, inheriting full Tier 1 context. Tool restrictions change via `--allowedTools` and `--disallowedTools` flags on the new invocation.

* Good, because full investigation context (tool outputs, error messages, file contents, intermediate reasoning) flows automatically without serialization.
* Good, because Tier 2 does not re-run diagnostic commands that Tier 1 already executed, saving 15-30% of tool calls per escalation.
* Good, because the session manager code is simplified -- no handoff file I/O, no JSON schema parsing, no prompt injection of serialized findings.
* Good, because the escalation prompt is shorter and more robust -- it describes the tier change, not the findings.
* Good, because `--model`, `--allowedTools`, and `--disallowedTools` can be changed on the resumed invocation, preserving per-tier enforcement (ADR-0023).
* Good, because the mechanism uses a stable, documented CLI feature (`--resume`) rather than a custom file-based protocol.
* Good, because the handoff file mechanism can be retained as a fallback, providing graceful degradation.
* Bad, because resumed sessions accumulate context from all prior tiers, increasing context window utilization and input token costs.
* Bad, because the session manager is coupled to the `session_id` field in the CLI's JSON output format.
* Bad, because the full conversation history is harder to audit at a glance compared to a structured handoff JSON file.
* Bad, because context window limits may be reached in complex multi-service, multi-tier escalation chains.
* Bad, because this is a newer CLI feature with less production mileage than the established handoff file pattern.

### Enhanced Handoff Files

Include full tool outputs in the handoff JSON, not just summaries. The existing mechanism is preserved but the serialization captures more context.

* Good, because it preserves the existing architecture -- no new CLI flags, no session ID tracking, no dependency on `--resume`.
* Good, because the handoff file remains human-readable and auditable -- operators can inspect exactly what was communicated.
* Good, because the handoff JSON schema is under the project's control and can be extended without dependency on CLI changes.
* Good, because there is no context window accumulation -- each tier starts fresh with only the relevant handoff context.
* Bad, because full tool outputs are verbose. A typical Tier 1 investigation might produce 50KB+ of tool output, which would make the handoff file large and the Tier 2 prompt bloated.
* Bad, because the serialization logic in the Tier 1 prompt becomes more complex -- the agent must capture and format every tool output, not just a summary.
* Bad, because `--append-system-prompt` has a practical size limit. Injecting 50KB of tool output may degrade model performance or hit token limits on the system prompt.
* Bad, because the handoff JSON schema grows in complexity and must be maintained in both the prompt (agent writes it) and the supervisor (Go code parses it).
* Bad, because it does not eliminate duplicate work entirely -- the receiving tier may still need to re-run some commands if the handoff was incomplete.

### Shared Conversation Log

Export Tier 1's conversation as a structured log file and inject it as context for Tier 2 via `--append-system-prompt`.

* Good, because it provides full conversation history without depending on the `--resume` flag.
* Good, because the log file can be filtered or truncated before injection, giving the supervisor control over what context Tier 2 receives.
* Good, because each tier still starts a fresh session, avoiding context window accumulation from prior tiers.
* Bad, because conversation export is not a standard CLI feature -- the supervisor would need to parse the CLI's internal conversation storage or capture stdout in a structured format.
* Bad, because injecting a full conversation log via `--append-system-prompt` would be extremely large, likely exceeding practical system prompt size limits.
* Bad, because the format of the conversation log is not standardized and could change between CLI versions.
* Bad, because it combines the worst aspects of both approaches: the complexity of serialization (like handoff files) with the size of full context (like `--resume`), but without the native support of either.
* Bad, because parsing and reformatting conversation output is fragile and introduces a new maintenance surface.

### No Change (Keep Current Handoff File Approach)

Retain the ADR-0016 handoff file mechanism without modification.

* Good, because no implementation work is required.
* Good, because the existing mechanism is understood, tested, and has production mileage.
* Good, because handoff files are simple to debug -- inspect the JSON to see what was communicated.
* Good, because each tier starts with a clean context window, avoiding accumulation.
* Bad, because context loss from summary-based handoff files forces Tier 2 to re-run 15-30% of Tier 1's diagnostic commands.
* Bad, because the handoff JSON schema is a maintenance burden that must be kept in sync between prompts and Go code.
* Bad, because the serialization instructions in tier prompts are complex and error-prone -- the agent must remember to capture all relevant findings in the correct JSON format.
* Bad, because duplicate diagnostic work increases API costs and escalation latency unnecessarily.
* Bad, because the `--resume` feature is now available and purpose-built for this use case, making the handoff file approach an unnecessary workaround.

## More Information

* **Updates ADR-0016**: This ADR replaces the handoff file escalation mechanism with `--resume`-based escalation. ADR-0016's session-based architecture (separate CLI processes per tier, `parent_session_id` linking, supervisor-controlled escalation) is preserved; only the context-passing mechanism changes.
* **Updates ADR-0010**: ADR-0010 established the CLI subprocess invocation pattern. This ADR adds `--resume <session_id>` and `--output-format json` to the set of flags used by the session manager, alongside the existing `--model`, `-p`, `--allowedTools`, `--disallowedTools`, and `--append-system-prompt`.
* **Interacts with ADR-0023**: The `--allowedTools` and `--disallowedTools` flags (ADR-0023) are passed on the resumed invocation and override the prior session's tool restrictions. This ensures per-tier enforcement is maintained even when resuming a lower tier's session.
* **The `--resume` flag**: Documented at https://code.claude.com/docs/en/headless. When used with `-p`, the prompt is appended to the existing conversation. The session ID is available in the JSON output format.
* **Fallback strategy**: If `--resume` fails (e.g., session expired, context window near capacity, CLI version incompatibility), the session manager falls back to the ADR-0016 handoff file mechanism. This provides graceful degradation without a hard dependency on `--resume` availability.
