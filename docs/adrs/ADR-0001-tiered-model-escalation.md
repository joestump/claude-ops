---
status: accepted
date: 2025-06-01
---

# Use Tiered Claude Model Escalation for Cost-Optimized Monitoring

## Context and Problem Statement

Claude Ops is an AI agent that monitors infrastructure health and remediates issues autonomously. It runs in a Docker container on a 60-minute loop, using the Claude Code CLI to execute markdown-based health checks and playbooks against mounted infrastructure repositories.

The system must decide which Claude model to invoke for each monitoring cycle. Most cycles discover no issues and require only observation (reading logs, running HTTP checks, inspecting container state). A smaller fraction of cycles uncover problems that need safe remediation (restarting a container, clearing a cache). An even smaller fraction encounter complex failures requiring multi-step recovery (Ansible redeployments, database repairs, orchestrated service restarts).

Using a single powerful model for every cycle wastes money on routine observation. Using only a cheap model for every cycle risks inadequate reasoning when complex remediation is needed. The system needs a strategy that balances cost efficiency with remediation capability.

## Decision Drivers

* **Cost proportionality** -- The vast majority of monitoring cycles find nothing wrong. Spending $0.50+ per cycle on a top-tier model when 90%+ of runs are observe-only is wasteful over thousands of runs.
* **Reasoning capability when it matters** -- Complex remediation (multi-service orchestration, database recovery, Helm upgrades) requires strong reasoning. Cheaper models may miss failure modes or execute recovery steps incorrectly.
* **Latency** -- Faster models reduce the wall-clock time of each monitoring cycle, leaving more headroom within the loop interval.
* **Simplicity of implementation** -- The escalation mechanism should be straightforward to implement and debug. The entrypoint script and prompt files should remain readable.
* **Safety** -- Higher-capability models should only be invoked when their additional permissions (remediation actions) are actually needed. Observation should not require a model with write access.

## Considered Options

1. **Tiered escalation (Haiku -> Sonnet -> Opus)** -- Start cheap, escalate only when issues are found.
2. **Single powerful model (always Opus)** -- Use the most capable model for every cycle regardless of whether issues exist.
3. **Rule-based escalation (non-LLM heuristics decide which model to use)** -- A script checks health endpoints first; only invoke an LLM if something looks wrong.
4. **Fixed model per check type** -- Assign specific models to specific check categories (e.g., Haiku for HTTP checks, Sonnet for database checks, Opus for deployment checks).

## Decision Outcome

Chosen option: **"Tiered escalation (Haiku -> Sonnet -> Opus)"**, because it delivers the best balance of cost, capability, and safety. Tier 1 (Haiku, ~$0.01-0.05/run) handles the common case of healthy infrastructure at minimal cost. Tier 2 (Sonnet) is spawned only when Tier 1 detects a problem, providing stronger reasoning for safe remediation. Tier 3 (Opus) is spawned only when Tier 2 cannot resolve the issue, bringing full reasoning power to complex recovery scenarios.

This maps directly to the permission model: Tier 1 is observe-only, Tier 2 has safe remediation permissions, and Tier 3 has full remediation permissions. The model capability scales with the permission scope, so the cheapest model never has access to dangerous operations, and the most expensive model is only invoked when dangerous operations may be necessary.

### Consequences

**Positive:**

* Routine monitoring costs are minimized (Haiku at ~$0.01-0.05 per cycle).
* The permission model aligns with model capability -- cheaper models cannot take destructive actions.
* Latency is low for the common case since Haiku is the fastest model.
* Context flows forward through escalation: each tier passes its findings to the next, so higher tiers do not re-run checks.
* The system is configurable via environment variables (`CLAUDEOPS_TIER1_MODEL`, `CLAUDEOPS_TIER2_MODEL`, `CLAUDEOPS_TIER3_MODEL`), allowing operators to swap models without code changes.

**Negative:**

* Adds complexity to the prompt architecture: three separate prompt files must be maintained (`tier1-observe.md`, `tier2-investigate.md`, `tier3-remediate.md`).
* Haiku may occasionally miss subtle issues that a more capable model would catch during observation.
* Escalation adds latency when issues are found (sequential model invocations rather than a single powerful pass).
* Context must be serialized and passed between tiers via the Task tool, which could lose nuance if the handoff prompt is poorly constructed.

## Pros and Cons of the Options

### Tiered escalation (Haiku -> Sonnet -> Opus)

* Good, because the common case (no issues) costs $0.01-0.05 per cycle instead of $0.50+.
* Good, because permission tiers map directly to model tiers, enforcing least-privilege by default.
* Good, because each tier's prompt can be specialized for its role (observation vs. investigation vs. remediation).
* Good, because model selection is configurable via environment variables without code changes.
* Bad, because three prompt files must be maintained and kept in sync.
* Bad, because Haiku may miss edge-case failures that require deeper reasoning to detect.
* Bad, because total latency increases when escalation occurs (three sequential model calls in the worst case).

### Single powerful model (always Opus)

* Good, because maximum reasoning capability is always available, reducing missed detections.
* Good, because the system is simpler -- one prompt file, one model, no escalation logic.
* Good, because no risk of context loss between tiers.
* Bad, because cost per cycle is ~10-50x higher than Haiku, and most cycles find nothing.
* Bad, because the most powerful model always has full remediation permissions, even during routine observation, violating least-privilege.
* Bad, because Opus is slower than Haiku, increasing cycle time even when no action is needed.
* Bad, because at 24 cycles/day, costs accumulate rapidly with no proportional benefit.

### Rule-based escalation (non-LLM heuristics decide which model to use)

* Good, because pre-screening with shell scripts (curl, docker inspect) is essentially free.
* Good, because it avoids invoking any LLM when everything is healthy.
* Bad, because writing and maintaining heuristic scripts for every check type defeats the purpose of an LLM-driven agent -- the checks become brittle code instead of flexible markdown.
* Bad, because heuristics cannot interpret ambiguous signals (e.g., elevated latency that may or may not indicate a problem).
* Bad, because it introduces a parallel system (scripts + LLM prompts) that must be kept in sync.
* Bad, because new checks require writing both a heuristic and a prompt, doubling the maintenance burden.

### Fixed model per check type

* Good, because predictable cost per check type allows straightforward budgeting.
* Good, because complex check types (database, deployment) always get a capable model.
* Bad, because check categories are not a reliable proxy for remediation complexity -- an HTTP check might reveal a problem that requires Opus-level reasoning to fix.
* Bad, because the model is chosen before the problem is understood, so a database check handled by Sonnet might encounter an issue that only Opus can resolve.
* Bad, because adding a new check requires deciding which model tier it belongs to, adding a classification burden.
* Bad, because it does not reduce cost for simple checks that happen to be in a "complex" category.
