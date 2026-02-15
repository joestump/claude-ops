---
status: accepted
date: 2025-06-01
---

# Enforce Permission Tiers via Prompt Instructions and Allowed-Tool Lists

## Context and Problem Statement

Claude Ops operates as an autonomous infrastructure monitoring and remediation agent with three escalating permission tiers: Tier 1 (observe-only via Haiku), Tier 2 (safe remediation via Sonnet), and Tier 3 (full remediation via Opus). Each tier has progressively broader access to dangerous operations like container restarts, Ansible playbook execution, and multi-service orchestrated recovery. A "Never Allowed" list further restricts all tiers from operations that always require human intervention (deleting volumes, modifying secrets, pushing to git, etc.).

The system needs a mechanism to enforce these permission boundaries so that a Tier 1 agent cannot restart a container and a Tier 2 agent cannot run Ansible playbooks. The enforcement approach must balance safety, operational simplicity, and the realities of running an AI agent in a Docker container on a schedule.

Currently, permissions are enforced through two complementary mechanisms:

1. **Prompt-level instructions** -- each tier's markdown prompt file (`prompts/tier1-observe.md`, `prompts/tier2-investigate.md`, `prompts/tier3-remediate.md`) contains explicit "Your Permissions" sections that tell the agent what it may and must not do.
2. **`--allowedTools` CLI flag** -- the `entrypoint.sh` script passes `--allowedTools` to the Claude CLI (default: `Bash,Read,Grep,Glob,Task,WebFetch` for Tier 1), which restricts which Claude Code tools the agent can invoke. Higher tiers are invoked as subagents via the `Task` tool, inheriting their own tool permissions.

There is no runtime ACL layer, no seccomp profile, no Linux capability restrictions beyond Docker defaults, and no separate container per tier.

## Decision Drivers

* **Operational simplicity** -- Claude Ops is a single Docker container running a bash loop; adding infrastructure complexity increases the maintenance burden and failure surface of the watchdog itself.
* **Defense in depth** -- multiple enforcement layers (prompt instructions + tool filtering) are preferable to relying on a single mechanism.
* **Transparency and auditability** -- permission rules written in natural language markdown are immediately readable by operators; binary policies or proxy configs require specialized knowledge to audit.
* **Speed of iteration** -- adding or modifying permission rules should be as fast as editing a markdown file, not rebuilding containers or redeploying proxy services.
* **Honest safety posture** -- the chosen approach must acknowledge its limitations rather than claim guarantees it cannot provide.
* **Cost and resource efficiency** -- the enforcement mechanism should not significantly increase compute, memory, or infrastructure costs.
* **Alignment with Claude Code's architecture** -- the solution should work within Claude Code's existing capabilities rather than fighting against them.

## Considered Options

1. **Prompt-based constraints + `--allowedTools` filtering** (current approach)
2. **Separate containers per tier with different Linux capabilities and seccomp profiles**
3. **Permission-checking proxy service that validates all actions before execution**
4. **Hybrid approach: prompt-based for decisions + technical sandboxing for dangerous operations**

## Decision Outcome

Chosen option: **Option 1 -- Prompt-based constraints + `--allowedTools` filtering**, because it provides a practical two-layer enforcement model that matches the operational reality of an AI agent runbook system, keeps the architecture simple enough to be reliable as a watchdog, and is honest about its trade-offs.

The `--allowedTools` flag provides a hard technical boundary at the tool level: Tier 1 cannot invoke `Write` or `Edit` tools because the CLI will reject those calls before the model ever sees the result. Prompt instructions provide the semantic layer: even though Tier 2 has access to `Bash` (and therefore `docker restart`), the prompt tells it not to run Ansible playbooks, and the model follows those instructions with high reliability.

This is not a security boundary in the traditional sense. A sufficiently creative or misaligned model could craft a `Bash` command that violates prompt-level restrictions. The decision acknowledges this and treats it as an acceptable risk for the following reasons:

- Claude Ops runs in a controlled environment (a Docker container with limited network access to known infrastructure)
- The cooldown state system provides a secondary safety net that limits the blast radius of repeated actions
- All actions are logged and can be audited after the fact
- The "Never Allowed" list items (delete volumes, push git, modify secrets) are the most dangerous operations, and an operator can further restrict these via Docker's own `--cap-drop`, read-only mounts, and network policies at the container level
- The alternative approaches all introduce significant complexity that itself becomes a reliability risk for a system whose primary job is to be a reliable watchdog

### Consequences

#### Positive

* Single container, single entrypoint, minimal moving parts -- the watchdog is less likely to fail due to its own infrastructure.
* Permission rules are readable markdown that any operator can audit in seconds without specialized tooling.
* Adding a new tier or modifying permissions requires editing a prompt file and optionally the `CLAUDEOPS_ALLOWED_TOOLS` environment variable -- no container rebuilds, no proxy redeployments.
* The `--allowedTools` flag provides a genuine technical boundary for tool-level restrictions (e.g., Tier 1 truly cannot write files via Claude Code's `Write` tool).
* Subagent escalation via the `Task` tool provides natural tier isolation: each tier runs as a separate model invocation with its own prompt context.
* Compatible with future hardening -- nothing about this approach prevents adding seccomp profiles or capability restrictions later.

#### Negative

* **Prompt instructions are not a security boundary.** A model that ignores or misinterprets prompt instructions could execute forbidden operations via `Bash`. This is the fundamental trade-off: we rely on model compliance for semantic restrictions within a tool category (e.g., "you have Bash but must not run Ansible").
* **`--allowedTools` is coarse-grained.** It operates at the tool level (Bash/Read/Write/etc.), not at the command level. There is no way to say "allow `docker restart` but deny `docker rm`" through this mechanism alone.
* **No runtime interception of Bash commands.** If the model decides to run `rm -rf /` via Bash, the only things stopping it are: (a) the prompt instruction, (b) Docker's own filesystem protections, and (c) the user running the container with appropriate restrictions (read-only mounts, non-root user, etc.).
* **Auditability is post-hoc.** Violations are caught by reviewing logs after the fact, not by a runtime enforcement layer that blocks the action before it executes.
* **Depends on model instruction-following quality.** As models change or are upgraded, the reliability of prompt-based enforcement may vary. This requires periodic validation.

## Pros and Cons of the Options

### Option 1: Prompt-based constraints + `--allowedTools` filtering

This is the current implementation. Each tier's prompt file contains explicit permission boundaries, and the `--allowedTools` CLI flag restricts which Claude Code tools are available at each tier.

* Good, because it is operationally simple: one container, one bash script, markdown files for configuration.
* Good, because the two-layer approach (tool filtering + prompt instructions) provides meaningful defense in depth for the most common cases.
* Good, because permission rules are human-readable and trivially auditable.
* Good, because iteration speed is maximized: changing permissions is a markdown edit.
* Good, because it introduces zero additional infrastructure that could itself fail.
* Good, because `--allowedTools` provides a genuine hard boundary at the tool level.
* Bad, because prompt-level restrictions within a tool (e.g., which Bash commands are allowed) rely on model compliance, not technical enforcement.
* Bad, because there is no runtime interception layer to block a forbidden Bash command before execution.
* Bad, because coarse tool-level filtering cannot express fine-grained command restrictions.
* Neutral, because this approach is compatible with future hardening via Docker capabilities, seccomp, or network policies.

### Option 2: Separate containers per tier with different Linux capabilities and seccomp profiles

Each permission tier would run in its own container with precisely scoped Linux capabilities. Tier 1 would drop all capabilities and run with a restrictive seccomp profile that blocks `exec`, networking to non-allowed hosts, and filesystem writes. Tier 2 would add back specific capabilities. Tier 3 would have the broadest access.

* Good, because it provides genuine OS-level security boundaries that cannot be bypassed by model behavior.
* Good, because seccomp profiles can block specific syscalls (e.g., preventing `exec` of certain binaries).
* Good, because it follows the principle of least privilege at the infrastructure level.
* Bad, because it significantly increases operational complexity: three container definitions, three sets of capability configs, three seccomp profiles to maintain.
* Bad, because Claude Code's `Task` tool (used for tier escalation) spawns subagents within the same process -- it does not launch a new container. Implementing cross-container escalation would require a custom orchestrator.
* Bad, because seccomp and capability restrictions are blunt instruments: it is difficult to allow `docker restart` while blocking `docker rm` at the syscall level, since both use the same Docker socket.
* Bad, because debugging capability/seccomp issues is notoriously difficult and could make the watchdog itself unreliable.
* Bad, because the agent needs access to the Docker socket for container management, and restricting Docker socket access granularly is an unsolved problem (you either have it or you don't).

### Option 3: Permission-checking proxy service

A sidecar service would intercept all outbound actions (Bash commands, API calls, Docker commands) and validate them against a permission policy before allowing execution. The Claude agent would talk to the proxy instead of executing commands directly.

* Good, because it provides runtime enforcement: forbidden commands are blocked before execution, not just discouraged.
* Good, because the policy can be as fine-grained as needed (allow `docker restart` but block `docker rm`).
* Good, because it creates a complete audit trail with allow/deny decisions.
* Bad, because it is a significant engineering effort to build and maintain a proxy that can parse and validate the full range of possible Bash commands, Docker API calls, and other actions.
* Bad, because the proxy itself becomes a critical dependency: if it fails, the watchdog cannot operate at all.
* Bad, because Claude Code does not natively support routing Bash commands through a proxy -- this would require wrapping the shell or replacing the Bash tool.
* Bad, because command parsing is inherently fragile: shell commands can be constructed in countless ways (pipes, subshells, variable expansion, encoding tricks) that a proxy would need to handle.
* Bad, because it contradicts the "runbook, not application" philosophy of Claude Ops -- it would introduce a real application component that needs its own testing, deployment, and monitoring.

### Option 4: Hybrid approach (prompt-based for decisions + technical sandboxing for dangerous operations)

Maintain prompt-based permission tiers for the decision-making layer but add targeted technical restrictions for the most dangerous operations. For example: mount repos as read-only for Tier 1, restrict Docker socket access via a filtered proxy (like Tecnativa's docker-socket-proxy), and use network policies to limit which hosts each tier can reach.

* Good, because it hardens the highest-risk operations (filesystem writes, Docker commands) with real technical controls.
* Good, because it keeps prompt-based enforcement for the semantic layer where it works well (deciding which remediation strategy to use).
* Good, because docker-socket-proxy is a proven, maintained project that can restrict Docker API endpoints.
* Good, because read-only filesystem mounts are trivial to implement in Docker Compose.
* Bad, because it adds 2-3 new infrastructure components (docker-socket-proxy, network policies, mount configuration per tier) that increase the failure surface.
* Bad, because the `Task` tool's in-process subagent model means different tiers cannot easily have different Docker socket access or filesystem mount configurations.
* Bad, because partial hardening can create a false sense of security: operators might assume all restrictions are technically enforced when only some are.
* Neutral, because individual elements of this approach (read-only mounts, docker-socket-proxy) could be adopted incrementally without committing to the full hybrid model. This is the recommended evolution path if prompt-based enforcement proves insufficient in practice.
