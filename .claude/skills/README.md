# Skills Directory

<!-- Governing: SPEC-0023 REQ-1 (Skill File Format), REQ-2 (Skill Discovery and Loading), REQ-11 (Skill Composability), ADR-0022 -->

## What Are Skills?

Skills are markdown instruction files that describe **how to use available tools** for a specific operational capability. The Claude Ops agent reads these files at runtime and executes them by discovering and using whatever tools are available in the environment -- MCP servers, CLIs, or raw HTTP as a last resort.

Skills are distinct from checks and playbooks:

- **Checks** (`checks/`) determine **WHAT** to verify (which services, what thresholds, what health indicators).
- **Playbooks** (`playbooks/`) determine **WHAT** remediation to perform (restart sequence, recovery steps, escalation criteria).
- **Skills** (`.claude/skills/`) determine **HOW** to use available tools to carry out those operations (which tool to use, fallback chains, command syntax).

Checks and playbooks MAY reference skills for their tool execution. For example, `checks/http.md` describes what HTTP health checks to perform, while the `http-request` skill describes how to make HTTP requests using Fetch MCP, curl, or other available tools. Skills do NOT replace checks or playbooks -- they provide the tool abstraction layer.

## Discovery

Skills are discovered from two locations at the start of each monitoring cycle:

1. **Baseline skills** at `/app/.claude/skills/` -- shipped with the Claude Ops container. The Claude Code CLI loads these natively from the `.claude/skills/` directory relative to the working directory.
2. **Repo-provided skills** at `.claude-ops/skills/` within each mounted repository under `/repos/`.

When a repo-provided skill has the same filename as a baseline skill, the repo-provided skill takes precedence for operations involving that repo's services.

Discovery is re-run every cycle. No container restart is needed when skill files are added or modified.

## Required Skill File Format

Every skill file MUST be a markdown document (`.md` extension) with the following sections:

### 1. Title (required)

```markdown
# Skill: <Capability Name>
```

A heading describing the capability. Use the `# Skill:` prefix so agents can identify skill files by title.

### 2. Purpose (required)

A brief description of what the skill does and when to use it. One or two paragraphs.

### 3. Tool Discovery (required)

An ordered list of tools the skill can use, from most preferred to least preferred, with instructions for detecting availability:

1. **MCP tools** (highest preference) -- Pre-configured MCP servers provide the richest interface.
2. **CLI tools** -- Installed command-line tools, checked via `which <tool>`.
3. **HTTP / curl** (lowest preference) -- Universal fallback using raw HTTP requests.

### 4. Execution (required)

Step-by-step instructions for accomplishing the task using whichever tool was discovered. Include separate subsections per tool path (e.g., "Using MCP: mcp__gitea__create_pull_request", "Using CLI: gh").

### 5. Validation (required)

How to verify the action succeeded. What to check, what constitutes success, and what to do if validation fails.

### Optional Sections

- **Tier Requirement** -- The minimum permission tier required to execute the skill (Tier 1, 2, or 3). If omitted, the skill is available at all tiers.
- **Scope Rules** -- File paths, hosts, or resources the skill MUST NOT modify. If the skill performs mutating operations, scope rules SHOULD be included.
- **Dry-Run Behavior** -- How the skill behaves when `CLAUDEOPS_DRY_RUN=true`. Mutating skills SHOULD include this section.

## Template

See [SKILL-TEMPLATE.md](SKILL-TEMPLATE.md) for a ready-to-copy template with all required and optional sections.

## Baseline Skills

| Skill | Domain | Tier | Description |
|-------|--------|------|-------------|
| [git-pr.md](git-pr.md) | Git operations | Tier 2 | Create, list, and check PR status |
| [container-health.md](container-health.md) | Container operations | Tier 1 | Inspect container state and logs (read-only) |
| [container-ops.md](container-ops.md) | Container operations | Tier 2 | Restart, start, stop containers |
| [database-query.md](database-query.md) | Database operations | Tier 1 | Read-only database queries |
| [http-request.md](http-request.md) | HTTP operations | Tier 1 | HTTP health checks and API interactions |
| [issue-tracking.md](issue-tracking.md) | Issue tracking | Tier 1 (read) / Tier 2 (write) | Create, list, view, update issues |
| [browser-automation.md](browser-automation.md) | Browser automation | Tier 2 | Web UI interaction for credential rotation |

## Composability

<!-- Governing: SPEC-0023 REQ-11 (Skill Composability with Existing Extensions) -->

Skills compose with the existing extension types defined in SPEC-0005:

```
checks/*.md           -- WHAT to verify       (health indicators, thresholds)
playbooks/*.md        -- WHAT to remediate     (recovery steps, escalation criteria)
.claude/skills/*.md   -- HOW to use tools      (tool discovery, fallback chains, commands)
```

A check such as `checks/http.md` says "verify that each service responds with HTTP 200." The `http-request` skill says "to make an HTTP request, try WebFetch first, then curl." The check determines the target; the skill determines the mechanism.

A playbook such as `playbooks/restart-container.md` says "restart the container, wait 15 seconds, verify health." The `container-ops` skill says "to restart a container, try Docker MCP first, then `docker restart` via SSH." The playbook determines the procedure; the skill determines the tool.

This separation means:
- Adding a new check does not require knowing which tools are available.
- Adding a new tool path (e.g., a new MCP server) only requires updating the relevant skill, not every check and playbook.
- Repo owners can override skills for their environment without rewriting checks or playbooks.
