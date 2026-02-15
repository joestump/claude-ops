# Design: Tiered Model Escalation

## Overview

This document describes the technical architecture for Claude Ops' tiered model escalation system. The system uses three Claude model tiers — Haiku, Sonnet, and Opus — to optimize cost while maintaining full remediation capability. Each monitoring cycle starts with the cheapest model for observation and only escalates to more expensive, more capable models when issues are detected and simpler remediations fail.

## Architecture

### Component Interaction

The tiered escalation system comprises four key components:

1. **Entrypoint loop** (`entrypoint.sh`): A bash script running an infinite loop on a configurable interval (default 60 minutes). It invokes the Claude CLI with the Tier 1 prompt and model on each cycle. The entrypoint is the only component that directly calls the Claude CLI; all subsequent tiers are spawned as subagents from within the running agent.

2. **Tier prompt files** (`prompts/tier1-observe.md`, `prompts/tier2-investigate.md`, `prompts/tier3-remediate.md`): Each file defines the complete behavioral contract for its tier — identity, permissions, procedures, and output format. These are markdown documents read by the Claude Code CLI as instructions.

3. **Task tool escalation**: When a lower tier detects issues it cannot handle, it uses the Claude Code Task tool to spawn a subagent at the next tier. The Task tool call specifies the model and includes the higher-tier prompt plus accumulated context.

4. **Cooldown state** (`$CLAUDEOPS_STATE_DIR/cooldown.json`): A JSON file tracking remediation actions per service. All tiers read it before acting; Tier 2 and Tier 3 update it after remediation attempts.

### Tier Invocation Chain

```
entrypoint.sh
  │
  ├── claude --model haiku --prompt-file tier1-observe.md
  │     │
  │     ├── [All healthy] → update state, exit
  │     │
  │     └── [Issues found] → Task(model: sonnet, prompt: tier2 + context)
  │           │
  │           ├── [Fixed] → update state, notify, exit
  │           │
  │           └── [Cannot fix] → Task(model: opus, prompt: tier3 + context)
  │                 │
  │                 ├── [Fixed] → update state, notify, exit
  │                 │
  │                 └── [Cannot fix] → notify "needs human", exit
  │
  └── sleep $CLAUDEOPS_INTERVAL → repeat
```

Each tier runs as a separate Claude model invocation. The entrypoint starts Tier 1 directly via the CLI. Tier 2 and Tier 3 are spawned as subagents from within the running agent using the Task tool.

### Permission Enforcement

Permissions are enforced through prompt instructions, not through technical access controls. Each tier's prompt file explicitly states what the agent may and may not do. This is a deliberate design choice documented in ADR-0003 (Prompt-Based Permission Tiers): the Claude model's instruction-following capability serves as the access control mechanism.

The entrypoint script also sets the `--allowedTools` flag for the Tier 1 invocation, which limits the CLI-level tools available. However, subagent tiers spawned via the Task tool inherit tool access from the parent agent's context, so prompt-level restrictions are the primary enforcement mechanism for Tier 2 and Tier 3.

## Data Flow

### Healthy Cycle (No Issues)

1. Entrypoint invokes Claude CLI with Haiku model and `tier1-observe.md`
2. Tier 1 agent discovers repos under `$CLAUDEOPS_REPOS_DIR`
3. Tier 1 reads check files from `/app/checks/` and any `.claude-ops/checks/` in mounted repos
4. Tier 1 executes health checks (HTTP, DNS, container state, database, service-specific)
5. Tier 1 reads cooldown state from `$CLAUDEOPS_STATE_DIR/cooldown.json`
6. All services are healthy → Tier 1 updates `last_run`, optionally sends daily digest
7. Tier 1 exits → entrypoint sleeps for `$CLAUDEOPS_INTERVAL`

### Escalation Cycle (Issues Found)

1. Steps 1-5 same as above
2. Tier 1 detects unhealthy services → builds failure summary
3. Tier 1 spawns Tier 2 via Task tool:
   - Model: `$CLAUDEOPS_TIER2_MODEL` (default: sonnet)
   - Prompt: contents of `tier2-investigate.md` + failure summary + cooldown state
4. Tier 2 investigates: reads container logs, checks dependencies, reviews playbooks
5. Tier 2 checks cooldown limits for affected services
6. Tier 2 attempts safe remediation (restart container, clear cache, etc.)
7. Tier 2 verifies fix by re-running the failed health check
8. If fixed: updates cooldown state, sends notification via Apprise, exits
9. If not fixed: spawns Tier 3 via Task tool:
   - Model: `$CLAUDEOPS_TIER3_MODEL` (default: opus)
   - Prompt: contents of `tier3-remediate.md` + original failure + investigation findings + attempted remediations
10. Tier 3 analyzes root cause (cascading failures, resource exhaustion, config drift)
11. Tier 3 performs full remediation (Ansible, Helm, container recreation, multi-service recovery)
12. Tier 3 verifies fix, updates cooldown state, sends detailed notification

### Context Handoff Format

When escalating, the current tier serializes its findings into a structured text block appended to the next tier's prompt. The handoff includes:

```
## Escalation Context

### Failed Services
- Service: <name>
  - Check: <which check failed>
  - Error: <error details>
  - Cooldown: <current cooldown state for this service>

### Investigation Findings (Tier 2 → Tier 3 only)
- Root cause hypothesis: <analysis>
- Container logs: <relevant excerpts>
- Dependency status: <which dependencies are healthy/unhealthy>

### Remediation Attempts (Tier 2 → Tier 3 only)
- Action: <what was tried>
- Result: <what happened>
- Why it failed: <analysis>
```

This is not a rigid schema but rather a guideline for how the AI agent structures its handoff. The receiving tier interprets it contextually.

## Key Decisions

### Why start with Haiku, not skip to a capable model

The overwhelming majority of monitoring cycles (estimated 90%+) find no issues. Running Opus for every cycle would cost roughly 10-50x more per cycle with no proportional benefit. Haiku can reliably execute the observation tasks (curl, dig, docker ps, reading files) at a fraction of the cost and with lower latency. See ADR-0001 for the full cost analysis.

### Why prompt-based permissions instead of technical ACLs

Claude's instruction-following capability is strong enough to serve as an access control mechanism for this use case. Technical enforcement (separate API keys, tool allow-lists per tier) would add complexity without proportional safety benefit, since all tiers run within the same Docker container with the same filesystem access. The prompt-based approach keeps the system simple and maintainable. See ADR-0003 for the detailed rationale.

### Why three tiers instead of two

Two tiers (observe + full remediation) would mean escalating directly from the cheapest model to the most expensive one. The mid-tier (Sonnet) handles the majority of real-world issues (container restarts, cache clears, permission fixes) at lower cost than Opus. Adding the mid-tier reduces the frequency of Opus invocations substantially, since most problems are simple enough for Sonnet to resolve.

### Why subagents instead of a single agent that upgrades itself

Using the Task tool to spawn separate subagents keeps each tier's prompt clean and focused. A single agent that "upgrades" its own permissions mid-run would need complex self-modification logic and would make the prompt structure harder to reason about. Separate prompts per tier also make it easy to audit what each tier can do.

### Why environment variables for model selection

Environment variables allow operators to swap models without modifying any files. This is especially useful for:
- Testing with different models (e.g., all tiers set to Haiku for low-cost testing)
- Upgrading to newer models as they become available
- Running in degraded mode (e.g., setting Tier 3 to Sonnet if Opus is unavailable)

## Trade-offs

### Gained

- **Cost efficiency**: Routine cycles cost $0.01-0.05 (Haiku) instead of $0.50+ (Opus). Over 24 cycles/day, this saves significant budget.
- **Least-privilege alignment**: The cheapest model has the narrowest permissions. An attacker who compromises the Tier 1 prompt gains only read access.
- **Low latency for the common case**: Haiku is the fastest model, so healthy cycles complete quickly, leaving headroom within the loop interval.
- **Specialized prompts**: Each tier's prompt is focused on its specific role, making behavior easier to predict and debug.
- **Operational flexibility**: Model selection via environment variables means no code changes for model swaps.

### Lost

- **Prompt maintenance burden**: Three prompt files must be maintained and kept in sync regarding system context (directory paths, cooldown rules, notification format). Changes to shared concepts must be replicated across all three.
- **Potential missed detections**: Haiku may miss subtle failure patterns that a more capable model would catch during observation. This is a deliberate trade-off: the cost savings from using Haiku for 90%+ of cycles outweigh the risk of occasional missed detections.
- **Sequential escalation latency**: When escalation occurs, total cycle time increases because tiers run sequentially (Tier 1 → wait → Tier 2 → wait → Tier 3). In the worst case, a single cycle involves three model invocations.
- **Context serialization fidelity**: The handoff between tiers relies on the lower-tier agent accurately summarizing its findings in text. If the summary is incomplete or poorly structured, the higher-tier agent may lack critical context.

## Future Considerations

- **Parallel health checks**: Tier 1 currently runs checks sequentially within a single agent invocation. As the number of monitored services grows, it may be worth spawning parallel subagents for independent check categories.
- **Model-specific tool restrictions**: The `--allowedTools` flag could be extended to enforce different tool sets per tier at the CLI level, providing defense-in-depth alongside prompt-based permissions.
- **Escalation telemetry**: Tracking escalation frequency, cost per cycle, and remediation success rates would enable data-driven tuning of the tier boundaries and model selection.
- **Dynamic tier selection**: Instead of fixed tiers, a future version could use a lightweight classifier to route directly to the appropriate tier based on the type of check failure, skipping intermediate tiers when the issue pattern is well-known.
- **Context window management**: As the number of services and checks grows, the context passed between tiers may exceed model limits. A future version may need to summarize or prioritize context rather than passing everything.
