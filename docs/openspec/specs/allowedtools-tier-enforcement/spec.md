---
status: accepted
date: 2026-03-02
---

# SPEC-0027: AllowedTools-Based Tier Enforcement

## Overview

Claude Ops enforces permission tier boundaries using a three-layer model: tool-level whitelisting (`--allowedTools`), command-prefix blocklisting (`--disallowedTools`), and prompt instructions. This specification formalizes the third layer — the `--disallowedTools` command-prefix blocklist — which was added by ADR-0023 to restore hard CLI-boundary enforcement for the highest-risk tier violations introduced when ADR-0022 removed the custom MCP server's programmatic `ValidateTier()` and `ValidateScope()` checks.

See [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md) for the decision rationale.

## Definitions

- **Tier**: A permission level assigned to a Claude Code CLI session — Tier 1 (observe-only), Tier 2 (safe remediation), or Tier 3 (full remediation). Set via `CLAUDEOPS_TIER` environment variable.
- **`--allowedTools` whitelist**: A CLI flag passed to `claude` that restricts which Claude Code tool types (e.g., `Bash`, `Read`, `Grep`) the agent may invoke at all. Enforced at the CLI binary boundary before tool execution.
- **`--disallowedTools` blocklist**: A CLI flag passed to `claude` that blocks specific tool invocation patterns, expressed as prefix-based glob patterns (e.g., `Bash(docker restart:*)`). Enforced at the CLI binary boundary before tool execution.
- **Hard boundary**: An enforcement layer implemented in the Claude Code CLI binary. The agent cannot bypass it through reasoning, prompt injection, or model output.
- **Soft boundary**: An enforcement layer implemented as prompt instructions. Compliance is probabilistic and depends on model behavior.
- **Command-prefix pattern**: A `--disallowedTools` expression matching the leading characters of a Bash invocation (e.g., `Bash(ansible-playbook:*)` blocks any Bash call whose command begins with `ansible-playbook`).
- **`DISALLOWED_TOOLS`**: A shell variable in `entrypoint.sh` containing the comma-separated `--disallowedTools` patterns for the current tier, configurable per deployment via `CLAUDEOPS_DISALLOWED_TOOLS`.

## Requirements

### REQ-1: Three-Layer Enforcement Model

The system MUST enforce tier permissions through exactly three independent layers applied in sequence:

1. **`--allowedTools` whitelist** (hard boundary): Restricts which tool types the agent may invoke.
2. **`--disallowedTools` blocklist** (hard boundary): Blocks specific command-prefix patterns within allowed tool types.
3. **Prompt instructions** (soft boundary): The tier prompt file and skill files describe additional restrictions.

All three layers MUST be active for every `claude` CLI invocation. The `--disallowedTools` layer MUST NOT replace or weaken the existing `--allowedTools` layer. The prompt instructions layer MUST NOT be considered sufficient alone for restrictions also covered by layers 1 or 2.

#### Scenario: All three layers active for Tier 1 invocation

Given the session manager starts a Tier 1 session
When it invokes the `claude` CLI
Then `--allowedTools Bash,Read,Grep,Glob,Task,WebFetch,WebSearch` is passed
And `--disallowedTools` contains the Tier 1 blocklist patterns
And `--append-system-prompt` includes the tier prompt with permission instructions

#### Scenario: Tool blocked by allowedTools before disallowedTools

Given `--allowedTools Bash,Read,Grep,Glob,Task,WebFetch,WebSearch` is set for Tier 1
When the agent attempts to invoke the `Write` tool
Then the CLI MUST reject the invocation at the `--allowedTools` boundary
And the `--disallowedTools` check is irrelevant (the tool is already blocked)

#### Scenario: Command blocked by disallowedTools within an allowed tool type

Given `Bash` is in the `--allowedTools` list for Tier 1
And `Bash(docker restart:*)` is in the `--disallowedTools` list
When the agent attempts `Bash("docker restart jellyfin")`
Then the CLI MUST reject the invocation before execution

### REQ-2: Tier 1 Disallowed Patterns

Tier 1 (`CLAUDEOPS_TIER=1`) MUST include the following patterns in its `--disallowedTools` blocklist:

```
Bash(docker restart:*)
Bash(docker stop:*)
Bash(docker start:*)
Bash(docker rm:*)
Bash(docker compose:*)
Bash(ansible:*)
Bash(ansible-playbook:*)
Bash(helm:*)
Bash(gh pr create:*)
Bash(gh pr merge:*)
Bash(tea pr create:*)
Bash(git push:*)
Bash(git commit:*)
Bash(systemctl restart:*)
Bash(systemctl stop:*)
Bash(systemctl start:*)
Bash(apprise:*)
```

These patterns MUST prevent Tier 1 from restarting containers, running configuration management tooling, creating or merging pull requests, committing or pushing to git, managing systemd services, or sending notifications.

#### Scenario: Tier 1 blocked from container restart

Given `Bash(docker restart:*)` is in the Tier 1 `--disallowedTools` list
When a Tier 1 agent attempts `Bash("docker restart jellyfin")`
Then the CLI MUST reject the invocation
And the agent MUST report the tool was blocked

#### Scenario: Tier 1 blocked from creating a PR

Given `Bash(gh pr create:*)` and `Bash(tea pr create:*)` are in the Tier 1 blocklist
When a Tier 1 agent attempts to create a pull request via either CLI
Then both invocations MUST be rejected at the CLI boundary

#### Scenario: Tier 1 blocked from sending notifications

Given `Bash(apprise:*)` is in the Tier 1 blocklist
When a Tier 1 agent attempts to send a notification via `apprise`
Then the invocation MUST be rejected

#### Scenario: Tier 1 permitted to run SSH for remote observation

Given `Bash(ssh:*)` is NOT in the Tier 1 blocklist
When a Tier 1 agent runs `Bash("ssh root@ie01 docker ps")`
Then the invocation MUST be permitted
Because SSH is required for remote host observation

### REQ-3: Tier 2 Disallowed Patterns

Tier 2 (`CLAUDEOPS_TIER=2`) MUST include the following patterns in its `--disallowedTools` blocklist:

```
Bash(ansible:*)
Bash(ansible-playbook:*)
Bash(helm:*)
Bash(docker compose down:*)
```

Tier 2 MUST NOT include the Tier 1 patterns that restrict PR creation, container restart, notifications, or git operations — those capabilities are permitted at Tier 2.

#### Scenario: Tier 2 blocked from running Ansible

Given `Bash(ansible-playbook:*)` is in the Tier 2 blocklist
When a Tier 2 agent attempts `Bash("ansible-playbook playbooks/redeploy.yml")`
Then the CLI MUST reject the invocation

#### Scenario: Tier 2 permitted to create PRs

Given `Bash(gh pr create:*)` is NOT in the Tier 2 blocklist
When a Tier 2 agent creates a pull request via `gh pr create`
Then the invocation MUST be permitted

#### Scenario: Tier 2 blocked from docker compose down but not docker compose up

Given `Bash(docker compose down:*)` is in the Tier 2 blocklist
When a Tier 2 agent attempts `Bash("docker compose down")`
Then the CLI MUST reject the invocation
When a Tier 2 agent attempts `Bash("docker compose up -d jellyfin")`
Then the invocation MUST be permitted

### REQ-4: Tier 3 Disallowed Patterns

Tier 3 (`CLAUDEOPS_TIER=3`) MUST include the following catastrophic-operation patterns in its `--disallowedTools` blocklist:

```
Bash(rm -rf /:*)
Bash(docker system prune:*)
Bash(git push --force:*)
```

Tier 3 MUST NOT include patterns that restrict Ansible, Helm, or other full-remediation operations — those are permitted at Tier 3.

#### Scenario: Tier 3 permitted to run Ansible

Given `Bash(ansible-playbook:*)` is NOT in the Tier 3 blocklist
When a Tier 3 agent runs `Bash("ansible-playbook playbooks/redeploy-jellyfin.yml")`
Then the invocation MUST be permitted

#### Scenario: Tier 3 blocked from force push

Given `Bash(git push --force:*)` is in the Tier 3 blocklist
When a Tier 3 agent attempts `Bash("git push --force origin main")`
Then the CLI MUST reject the invocation

### REQ-5: Entrypoint Integration

The `entrypoint.sh` MUST pass `--disallowedTools "${DISALLOWED_TOOLS}"` to every `claude` CLI invocation, alongside the existing `--allowedTools` flag.

The `DISALLOWED_TOOLS` variable MUST be set per tier with the patterns specified in REQ-2, REQ-3, and REQ-4. `DISALLOWED_TOOLS` MUST default to the tier-appropriate pattern list when `CLAUDEOPS_DISALLOWED_TOOLS` is not set in the environment.

Operators MAY override `DISALLOWED_TOOLS` by setting `CLAUDEOPS_DISALLOWED_TOOLS` in their `.env` file. The override MUST replace (not extend) the default for the given tier.

#### Scenario: Entrypoint passes disallowedTools on every invocation

Given the entrypoint is starting a Tier 1 session
When it constructs the `claude` command
Then the command MUST include `--disallowedTools "Bash(docker restart:*),Bash(docker stop:*),..."`

#### Scenario: Operator overrides default Tier 1 blocklist

Given `CLAUDEOPS_DISALLOWED_TOOLS="Bash(ansible-playbook:*)"` is set in `.env`
When the entrypoint starts a Tier 1 session
Then `--disallowedTools "Bash(ansible-playbook:*)"` is passed instead of the full default list

#### Scenario: Default applied when CLAUDEOPS_DISALLOWED_TOOLS is unset

Given `CLAUDEOPS_DISALLOWED_TOOLS` is not set in the environment
When the entrypoint starts a Tier 2 session
Then `--disallowedTools` uses the default Tier 2 pattern list from REQ-3

### REQ-6: Skill Fallback Chain Compatibility

When `--disallowedTools` blocks a tool that a skill would otherwise use, the skill's adaptive fallback chain (SPEC-0023 REQ-4) MUST handle the rejection naturally by falling through to the next available tool in the chain.

If all tool paths in a skill's fallback chain are blocked for the current tier, the skill MUST report an error following the observability format defined in SPEC-0023 REQ-5. The agent MUST NOT attempt to circumvent the block.

#### Scenario: Blocked MCP tool causes skill fallback to CLI

Given `--disallowedTools` for Tier 1 includes `mcp__gitea__create_pull_request`
And the `git-pr` skill prefers `mcp__gitea__create_pull_request` then `gh pr create`
When a Tier 1 agent executes the `git-pr` skill
Then `mcp__gitea__create_pull_request` is rejected by the CLI boundary
And the skill falls through to `gh pr create`
And `gh pr create` is rejected by `Bash(gh pr create:*)`
And the skill reports `[skill:git-pr] ERROR: No suitable tool found for PR creation`

#### Scenario: Tier 2 skill succeeds where Tier 1 fails

Given the same `git-pr` skill and tool inventory
When a Tier 2 agent executes the skill (no PR creation in Tier 2 blocklist)
Then the skill succeeds using the first available tool path

#### Scenario: Agent does not retry a blocked invocation

Given a Bash invocation is rejected by `--disallowedTools`
When the agent receives the rejection
Then the agent MUST NOT retry the same command
And the agent MUST either fall back to an alternative or report the failure

### REQ-7: MCP Tool Blocking

When MCP tools are configured in the environment (e.g., `mcp__gitea__create_pull_request`), the `--disallowedTools` list SHOULD include blocklist entries for MCP tools that correspond to restricted CLI operations at lower tiers.

At minimum, Tier 1 SHOULD block `mcp__gitea__create_pull_request` and `mcp__github__create_pull_request` if those tools are present in the environment.

Operators MUST configure MCP tool blocklist entries via `CLAUDEOPS_DISALLOWED_TOOLS` when user-configured MCP tools provide capabilities that the default CLI-based patterns do not cover.

#### Scenario: MCP PR creation blocked at Tier 1

Given `mcp__gitea__create_pull_request` is available in the environment
And it is listed in the Tier 1 `--disallowedTools`
When a Tier 1 agent attempts to use `mcp__gitea__create_pull_request`
Then the CLI MUST reject the invocation

### REQ-8: Acknowledged Limitations

The implementation MUST document and accept the following enforcement gaps, which remain prompt-enforced:

1. **SSH tunneling**: Commands executed via `ssh host <cmd>` evade command-prefix matching because the local command is `ssh`, not the remote command. `Bash(ssh:*)` MUST NOT be blocked for any tier.

2. **Scope validation**: `--disallowedTools` patterns block entire commands, not specific arguments. Whether a permitted `gh pr create` modifies a denied file (e.g., `ie.yaml`) is enforced by skill scope rules (SPEC-0023 REQ-8), not by `--disallowedTools`.

3. **Shell indirection**: Commands constructed as `bash -c "ansible-playbook ..."` or `eval "ansible-playbook ..."` evade prefix matching. If this pattern is observed in practice, `Bash(bash -c:*)` and `Bash(eval:*)` MAY be added to the Tier 1 and Tier 2 blocklists as a future tightening measure.

4. **Undocumented MCP tools**: MCP tools added to the environment after the default blocklist was authored are not automatically blocked. Operators MUST audit newly configured MCP tools against tier permission requirements.

#### Scenario: SSH tunneling is not blocked

Given `Bash(ansible-playbook:*)` is in the Tier 1 blocklist
When a Tier 1 agent runs `Bash("ssh root@ie01 ansible-playbook playbooks/redeploy.yml")`
Then the CLI permits the invocation (the local command is `ssh`, not `ansible-playbook`)
And enforcement of remote commands within SSH relies on prompt instructions

#### Scenario: Scope violation enforcement is prompt-based

Given `Bash(gh pr create:*)` is NOT in the Tier 2 blocklist
When a Tier 2 agent creates a PR modifying `ie.yaml`
Then the CLI permits the `gh pr create` invocation
And the `git-pr` skill's scope rules MUST catch and refuse the operation

## References

- [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md)
- [ADR-0003: Enforce Permission Tiers via Prompt Instructions and Allowed-Tool Lists](../../adrs/ADR-0003-prompt-based-permission-enforcement.md)
- [ADR-0022: Skills-Based Tool Orchestration](../../adrs/ADR-0022-skills-based-tool-orchestration.md)
- [ADR-0010: Invoke Claude via Claude Code CLI as Subprocess](../../adrs/ADR-0010-claude-code-cli-subprocess.md)
- [SPEC-0003: Prompt-Based Permission Enforcement](../prompt-based-permissions/spec.md)
- [SPEC-0023: Skills-Based Tool Orchestration](../skills-based-tool-orchestration/spec.md)
