# Design: AllowedTools-Based Tier Enforcement

## Overview

This design describes how Claude Ops restores hard CLI-boundary enforcement for tier permissions after ADR-0022 removed the custom MCP server's programmatic `ValidateTier()` checks. The mechanism extends the existing `--allowedTools` flag with a complementary `--disallowedTools` flag, adding a third enforcement layer for command-prefix blocking without replacing the existing two layers (tool-type whitelisting and prompt instructions).

See [SPEC-0027](./spec.md) and [ADR-0023](../../adrs/ADR-0023-allowedtools-tier-enforcement.md).

## Architecture

### Enforcement Layer Stack

```
┌─────────────────────────────────────────────────────────┐
│           Claude Code CLI  (Hard Boundary)              │
│                                                         │
│  Layer 1: --allowedTools                                │
│  ┌──────────────────────────────────────────────────┐  │
│  │  Tool-type whitelist: Bash, Read, Grep, Glob...  │  │
│  │  Agent cannot invoke Write, Edit, etc. at Tier 1 │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
│  Layer 2: --disallowedTools  (NEW — ADR-0023)           │
│  ┌──────────────────────────────────────────────────┐  │
│  │  Command-prefix blocklist:                        │  │
│  │  Bash(docker restart:*), Bash(ansible-playbook:*) │  │
│  │  Blocks dangerous commands within allowed types   │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
                        │ passes
                        ▼
┌─────────────────────────────────────────────────────────┐
│           Prompt Instructions  (Soft Boundary)          │
│                                                         │
│  Layer 3: Tier prompt + skill scope rules + CLAUDE.md   │
│  ┌──────────────────────────────────────────────────┐  │
│  │  Handles what CLI patterns cannot express:        │  │
│  │  - SSH-tunneled remote commands                   │  │
│  │  - Argument-level scope (which files a PR touches)│  │
│  │  - "Never Allowed" ops without distinct prefixes  │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### Per-Tier Configuration

The entrypoint sets `ALLOWED_TOOLS` and `DISALLOWED_TOOLS` per tier before invoking the Claude CLI:

```
Tier 1 — Observe Only
  ALLOWED_TOOLS:     Bash,Read,Grep,Glob,Task,WebFetch
  DISALLOWED_TOOLS:  Bash(docker restart:*),Bash(docker stop:*),
                     Bash(docker start:*),Bash(docker rm:*),
                     Bash(docker compose:*),Bash(ansible:*),
                     Bash(ansible-playbook:*),Bash(helm:*),
                     Bash(gh pr create:*),Bash(gh pr merge:*),
                     Bash(tea pr create:*),Bash(git push:*),
                     Bash(git commit:*),Bash(systemctl restart:*),
                     Bash(systemctl stop:*),Bash(systemctl start:*),
                     Bash(apprise:*)

Tier 2 — Safe Remediation
  ALLOWED_TOOLS:     Bash,Read,Grep,Glob,Task,WebFetch,Write,Edit
  DISALLOWED_TOOLS:  Bash(ansible:*),Bash(ansible-playbook:*),
                     Bash(helm:*),Bash(docker compose down:*)

Tier 3 — Full Remediation
  ALLOWED_TOOLS:     Bash,Read,Grep,Glob,Task,WebFetch,Write,Edit
  DISALLOWED_TOOLS:  Bash(rm -rf /:*),Bash(docker system prune:*),
                     Bash(git push --force:*)
```

### Entrypoint Change

The `entrypoint.sh` invocation gains one flag:

```bash
claude \
    --model "${MODEL}" \
    -p "$(cat "${PROMPT_FILE}")" \
    --allowedTools "${ALLOWED_TOOLS}" \
    --disallowedTools "${DISALLOWED_TOOLS}" \
    --append-system-prompt "Environment: ${ENV_CONTEXT}" \
    2>&1 | tee -a "${LOG_FILE}" || true
```

`DISALLOWED_TOOLS` defaults to the tier-appropriate pattern list, overridable via `CLAUDEOPS_DISALLOWED_TOOLS`.

### Skill Fallback Chain Interaction

When `--disallowedTools` blocks a tool path in a skill's fallback chain, the skill exhausts its alternatives and reports failure. This is by design — the CLI boundary and the skill's adaptive discovery work together without the skill needing to know about tier restrictions:

```
Tier 1 agent executing git-pr skill:

  1. Skill tries mcp__gitea__create_pull_request
     → Blocked by --disallowedTools (if included in Tier 1 list)
     → Skill falls through

  2. Skill tries `gh pr create`
     → Blocked by Bash(gh pr create:*)
     → Skill falls through

  3. Skill tries `tea pr create`
     → Blocked by Bash(tea pr create:*)
     → Skill falls through

  4. No more tool paths
     → [skill:git-pr] ERROR: No suitable tool found for PR creation
     → Correct behavior: Tier 1 cannot create PRs
```

The same skill at Tier 2, where none of these patterns appear in `DISALLOWED_TOOLS`, succeeds at step 1 or 2.

## Key Design Decisions

### Prefix-based matching is sufficient for the highest-risk operations

The `--disallowedTools` flag uses prefix matching: `Bash(docker restart:*)` blocks any Bash invocation whose text begins with `docker restart`. This is not full argument parsing, but it covers the critical cases:

- Container restart operations always begin with `docker restart` or `docker stop`
- Ansible runs always begin with `ansible-playbook` or `ansible`
- PR creation always begins with `gh pr create` or `tea pr create`

The trade-off is accepted: `Bash(docker compose:*)` over-blocks `docker compose ps` (read-only) at Tier 1, but Tier 1 should use `ssh host docker ps` for remote inspection rather than local Docker Compose commands.

### SSH is not blocked at any tier

`Bash(ssh:*)` is intentionally absent from all tier blocklists. SSH is the primary mechanism for Tier 1 to inspect remote hosts (`ssh root@ie01 docker ps`, `ssh root@ie01 docker logs jellyfin`). Blocking SSH would break observation.

The limitation: `ssh root@ie01 ansible-playbook` evades `Bash(ansible-playbook:*)`. This is the largest gap and is explicitly accepted in ADR-0023. Remote command restriction within SSH sessions remains prompt-enforced.

### Scope validation remains in skill scope rules

`--disallowedTools` cannot distinguish `gh pr create --title "fix" -- playbooks/fix.yml` (safe) from `gh pr create --title "update" -- ie.yaml` (forbidden). Full argument inspection is not available via prefix matching.

Scope enforcement — which files a PR may touch — lives in skill scope rules (SPEC-0023 REQ-8), where the agent can evaluate the full set of changed files before constructing the command. This is a soft boundary, but it is checked before the command is ever issued, making it more reliable than post-hoc argument parsing would be.

### The three layers are complementary, not redundant

Each layer catches different things:
- `--allowedTools`: Blocks entire tool categories (e.g., `Write` at Tier 1)
- `--disallowedTools`: Blocks dangerous commands within permitted tool categories (e.g., `docker restart` within `Bash`)
- Prompt instructions: Handle everything that cannot be expressed as a CLI flag (SSH tunneling, scope validation, semantic "never allowed" rules)

Removing any layer weakens the overall posture. ADR-0023 explicitly adds the second layer without removing the first or third.

## Enforcement Coverage Matrix

| Operation | Layer 1 (`--allowedTools`) | Layer 2 (`--disallowedTools`) | Layer 3 (prompt) |
|-----------|---------------------------|-------------------------------|------------------|
| Tier 1: `docker restart` | Bash is allowed | ✅ Blocked | Instructed against |
| Tier 1: `ansible-playbook` | Bash is allowed | ✅ Blocked | Instructed against |
| Tier 1: `gh pr create` | Bash is allowed | ✅ Blocked | Instructed against |
| Tier 1: `Write` file | ❌ Write not in allowedTools | N/A | Instructed against |
| Tier 1: SSH remote `ansible` | Bash is allowed | ❌ Not blocked (SSH prefix) | ✅ Prompt-only |
| Tier 2: `ansible-playbook` | Bash is allowed | ✅ Blocked | Instructed against |
| Tier 2: `gh pr create` | Bash is allowed | Not blocked | Permitted |
| Tier 2: PR modifies `ie.yaml` | Bash is allowed | Not blocked (no arg match) | ✅ Skill scope rules |
| Tier 3: `git push --force` | Bash is allowed | ✅ Blocked | Instructed against |

## References

- [ADR-0023: AllowedTools-Based Tier Enforcement](../../adrs/ADR-0023-allowedtools-tier-enforcement.md)
- [ADR-0003: Prompt-Based Permission Enforcement](../../adrs/ADR-0003-prompt-based-permission-enforcement.md)
- [ADR-0022: Skills-Based Tool Orchestration](../../adrs/ADR-0022-skills-based-tool-orchestration.md)
- [SPEC-0027: AllowedTools-Based Tier Enforcement](./spec.md)
- [SPEC-0003: Prompt-Based Permission Enforcement](../prompt-based-permissions/spec.md)
- [SPEC-0023: Skills-Based Tool Orchestration](../skills-based-tool-orchestration/spec.md)
