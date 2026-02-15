# Design: Prompt-Based Permission Enforcement

## Overview

Claude Ops enforces permission tiers through a two-layer model: hard tool-level boundaries via the `--allowedTools` CLI flag, and semantic restrictions via natural-language instructions embedded in tier-specific markdown prompt files. This design prioritizes operational simplicity, auditability, and honest acknowledgment of trade-offs over traditional security boundary enforcement.

## Architecture

### Enforcement Layers

The permission system consists of two distinct enforcement layers operating at different levels of abstraction:

```
+-----------------------------------------------------------+
|                    Claude Code CLI                         |
|                                                           |
|   Layer 1: --allowedTools (Hard Technical Boundary)       |
|   +-----------------------------------------------------+ |
|   | Tool whitelist: Bash, Read, Grep, Glob, Task, ...   | |
|   | Rejected tool calls never reach the model            | |
|   +-----------------------------------------------------+ |
|                                                           |
|   Layer 2: Prompt Instructions (Semantic Boundary)        |
|   +-----------------------------------------------------+ |
|   | "Your Permissions" section in tier prompt file       | |
|   | Constrains WHAT to do within allowed tools           | |
|   | Relies on model instruction-following compliance     | |
|   +-----------------------------------------------------+ |
|                                                           |
|   Layer 3: Cooldown State (Blast Radius Limiter)          |
|   +-----------------------------------------------------+ |
|   | Max 2 restarts / service / 4 hours                  | |
|   | Max 1 redeployment / service / 24 hours             | |
|   | Agent reads state before any remediation             | |
|   +-----------------------------------------------------+ |
+-----------------------------------------------------------+
```

**Layer 1 (`--allowedTools`)** is enforced by the Claude Code CLI binary. When the model attempts to invoke a tool not in the allowed list, the CLI rejects the call before execution. This is a genuine hard boundary -- the model cannot bypass it regardless of prompt content or intent.

**Layer 2 (Prompt Instructions)** is enforced by the model's instruction-following behavior. Each tier's prompt file contains explicit positive and negative permission lists. The model reads these instructions and constrains its behavior accordingly. This layer is not a security boundary -- it depends on model compliance.

**Layer 3 (Cooldown State)** is a data-driven safety net. The agent reads `cooldown.json` before any remediation action. Even if the model ignores a prompt restriction and attempts a remediation, the cooldown check provides an additional point where the agent is instructed to stop and notify instead of acting.

### Tier-Prompt Mapping

Each tier maps to a specific prompt file, model, and tool configuration:

| Tier | Prompt File | Default Model | Default Allowed Tools |
|------|-------------|---------------|-----------------------|
| Tier 1 | `prompts/tier1-observe.md` | Haiku | `Bash,Read,Grep,Glob,Task,WebFetch` |
| Tier 2 | `prompts/tier2-investigate.md` | Sonnet | Inherited from Task tool context |
| Tier 3 | `prompts/tier3-remediate.md` | Opus | Inherited from Task tool context |

Tier 1 is invoked directly by the entrypoint script with explicit `--allowedTools`. Tiers 2 and 3 are invoked as subagents via the `Task` tool from the preceding tier, receiving their prompt content as part of the Task invocation.

### Subagent Escalation Model

Permission isolation between tiers relies on the `Task` tool's subagent architecture:

```
entrypoint.sh
  |
  +-- claude --model haiku --allowedTools "Bash,Read,..." --prompt-file tier1-observe.md
        |
        +-- (issues found) --> Task(model: sonnet, prompt: tier2-investigate.md + context)
              |
              +-- (cannot fix) --> Task(model: opus, prompt: tier3-remediate.md + context)
```

Each `Task` invocation creates a new model session with its own prompt context. The higher-tier agent receives its permissions from its own prompt file, not from the lower tier. The lower-tier agent's `--allowedTools` restriction does not propagate to the subagent -- the subagent's capabilities are defined by the `Task` tool's inherent permissions and its prompt instructions.

This means Tier 2 and Tier 3 subagents have access to all Claude Code tools (including `Write`, `Edit`, and unrestricted `Bash`). Their constraints are entirely prompt-based within the subagent context.

### Permission Structure Within Prompts

Each tier prompt follows a consistent structure for its permission section:

```markdown
## Your Permissions

You may:
- [Explicit list of permitted operations]

You must NOT:
- [Explicit list of prohibited operations]
- Anything in the "Never Allowed" list in CLAUDE.md
```

The "Never Allowed" list is defined in the runbook (`CLAUDE.md`) and referenced by all tier prompts. This avoids duplication and ensures consistency -- any change to the Never Allowed list applies to all tiers.

## Data Flow

### Permission Enforcement During a Run Cycle

```
1. entrypoint.sh starts
   |
2. Sets ALLOWED_TOOLS from env var (or default)
   |
3. Invokes: claude --allowedTools $ALLOWED_TOOLS --prompt-file tier1-observe.md
   |
4. Tier 1 agent reads its prompt (including permission section)
   |
5. Agent performs health checks using allowed tools
   |
   +-- Tool call "Write" --> CLI REJECTS (not in allowed list)
   +-- Tool call "Bash: curl ..." --> CLI ALLOWS --> executed
   +-- Tool call "Bash: docker restart ..." --> CLI ALLOWS Bash...
       ...but prompt says "DO NOT remediate" --> agent self-constrains
   |
6. Agent finds issues --> spawns Task(tier2-investigate.md + context)
   |
7. Tier 2 subagent reads ITS prompt (Tier 2 permission section)
   |
8. Tier 2 agent checks cooldown state before any remediation
   |
9. Agent performs remediation within Tier 2 boundaries
   |
   +-- "docker restart X" --> permitted by Tier 2 prompt --> executed
   +-- "ansible-playbook ..." --> prohibited by Tier 2 prompt --> agent self-constrains
   |
10. If unresolved --> spawns Task(tier3-remediate.md + context)
```

### Cooldown State Check Flow

```
Agent decides to remediate service X
  |
  +-- Read $STATE_DIR/cooldown.json
  |
  +-- Check: restarts for X in last 4 hours >= 2?
  |     YES --> Skip remediation, send "needs human attention" notification
  |     NO  --> Continue
  |
  +-- Check: redeployments for X in last 24 hours >= 1? (Tier 3 only)
  |     YES --> Skip remediation, send "needs human attention" notification
  |     NO  --> Continue
  |
  +-- Perform remediation
  |
  +-- Update cooldown.json with action timestamp
```

## Key Decisions

### Why prompt instructions over technical sandboxing

The ADR evaluated four options: prompt-based constraints, separate containers per tier, a permission-checking proxy, and a hybrid approach. Prompt-based constraints were chosen because:

1. **Architectural compatibility**: Claude Code's `Task` tool spawns subagents within the same process, not in separate containers. Cross-container escalation would require building a custom orchestrator.

2. **Docker socket is binary**: Container management requires Docker socket access, which is inherently all-or-nothing. A Docker socket proxy (like Tecnativa's) can restrict API endpoints but cannot distinguish between `docker restart` and `docker rm` at the syscall level in a way that maps cleanly to the tier model.

3. **Watchdog reliability**: Claude Ops is itself a reliability tool. Adding infrastructure complexity (proxy services, multi-container orchestration, seccomp profiles) increases the chance that the watchdog itself fails, defeating its purpose.

4. **Bash command parsing is unsolvable**: A proxy that validates arbitrary Bash commands before execution would need to parse shell syntax including pipes, subshells, variable expansion, and encoding -- a problem with no complete solution.

### Why --allowedTools supplements prompts

While prompt instructions alone could theoretically enforce all restrictions, `--allowedTools` provides a defense-in-depth layer that is technically enforced. For Tier 1, this means the agent genuinely cannot write files via Claude Code's `Write` tool, regardless of prompt compliance. This is particularly valuable for the most common boundary: ensuring Tier 1 is truly read-only at the tool level.

### Why the "Never Allowed" list lives in CLAUDE.md

The Never Allowed list is defined once in the runbook rather than duplicated in each tier prompt. Each tier prompt references it with "Anything in the 'Never Allowed' list in CLAUDE.md." This ensures:

- Single source of truth for the most critical restrictions
- No risk of tier prompts diverging on what is universally prohibited
- The list is visible in the project's primary documentation file

## Trade-offs

### Gained

- **Operational simplicity**: Single container, single entrypoint script, markdown-only configuration. The entire permission system can be audited by reading three prompt files and one environment variable.
- **Iteration speed**: Adding or modifying permissions requires editing a markdown file. No container rebuilds, no proxy redeployments, no seccomp profile updates.
- **Transparency**: Permission rules are human-readable natural language. Any operator can understand what each tier is allowed to do by reading the prompt file.
- **Reliability**: No additional infrastructure that could fail. The permission system cannot crash, lose connectivity, or become a bottleneck.
- **Compatibility with future hardening**: Nothing prevents adding Docker capabilities, read-only mounts, or network policies later. The prompt-based approach is a starting point, not a constraint.

### Lost

- **Guaranteed enforcement within tools**: There is no mechanism to prevent a Bash command that violates prompt restrictions before it executes. The model must comply voluntarily.
- **Runtime interception**: Violations are detectable only through log review after the fact, not blocked in real time.
- **Fine-grained command control**: The `--allowedTools` flag operates at the tool level (Bash vs. Write), not at the command level (`docker restart` vs. `docker rm`).
- **Model-independent guarantees**: The effectiveness of prompt-based enforcement depends on the specific model's instruction-following quality. Model upgrades or changes could alter enforcement reliability.

## Future Considerations

### Incremental hardening path

The ADR identifies a recommended evolution path if prompt-based enforcement proves insufficient:

1. **Read-only filesystem mounts**: Mount repos as read-only (`:ro`) for Tier 1 invocations. This is trivial to implement in Docker Compose and provides a hard boundary for filesystem writes.
2. **Docker socket proxy**: Deploy Tecnativa's `docker-socket-proxy` to restrict which Docker API endpoints are accessible. This can enforce container-level restrictions (e.g., allow `GET` but deny `DELETE`) without prompt reliance.
3. **Network policies**: Restrict which hosts each tier can reach, preventing accidental or malicious access to infrastructure outside the known inventory.

These can be adopted individually without committing to the full hybrid model.

### Model compliance monitoring

As models are upgraded, the reliability of prompt-based enforcement should be periodically validated through:

- Test scenarios that verify each tier respects its boundaries
- Log analysis to detect any historical boundary violations
- Comparison of enforcement reliability across model versions

### Potential for structured permissions

A future evolution could replace free-form prompt instructions with a structured permission manifest (YAML or JSON) that is parsed by both the prompt generation system and a lightweight runtime validator. This would allow:

- Machine-readable permission definitions
- Automated generation of prompt permission sections from the manifest
- Basic runtime validation of commands against the manifest before execution

This remains speculative and should only be pursued if prompt compliance proves unreliable in practice.
