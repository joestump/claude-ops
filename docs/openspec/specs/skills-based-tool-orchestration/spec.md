---
status: proposed
date: 2026-02-21
supersedes:
  - SPEC-0006
  - SPEC-0019
---

# SPEC-0023: Skills-Based Tool Orchestration

## Overview

Claude Ops replaces custom MCP server development and mandatory npx-based MCP configurations with skills-based adaptive tool orchestration. Skills are markdown instruction files that the agent reads at runtime and executes by discovering and using whatever tools are available in the environment — MCP servers, CLIs, or raw HTTP as a last resort.

This specification formalizes the skill file format, tool discovery procedure, fallback behavior, observability requirements, and integration with the existing permission tier model. It supersedes SPEC-0006 (MCP Infrastructure Bridge) and SPEC-0019 (MCP Server for Git Provider Interface).

See [ADR-0022: Skills-Based Tool Orchestration](../../adrs/ADR-0022-skills-based-tool-orchestration.md) for the decision rationale.

## Definitions

- **Skill**: A markdown document that describes a single operational capability (e.g., "create a pull request", "check container health", "rotate credentials"). Skills include tool discovery procedures, execution steps, and validation criteria.
- **Baseline skill**: A skill file shipped with the Claude Ops container at `/app/.claude/skills/`. This path corresponds to the `.claude/skills/` directory relative to the container's working directory (`/app/`), which the Claude Code CLI loads natively (ADR-0010). Baseline skills cover core infrastructure operations.
- **Repo-provided skill**: A skill file contributed by a mounted repository at `.claude-ops/skills/`. Repo-provided skills extend or supplement baseline skills following the conventions in SPEC-0005.
- **Tool inventory**: A session-scoped mapping of capabilities to available tools, built once at session initialization and reused for the session's lifetime. Records which MCP tools and CLIs are available.
- **Tool path**: The specific tool (MCP tool, CLI command, or HTTP fallback) selected by a skill for a given invocation based on the tool inventory.
- **Fallback chain**: The ordered list of tool paths a skill attempts, from most preferred (MCP) to least preferred (raw HTTP/curl). The agent tries each in order until one is available.
- **Tool discovery**: The process of determining which tools are available in the current environment by enumerating MCP tools and checking for installed CLIs.
- **Fallback observability**: Logging that records which tool path was selected and whether the selection involved falling back from a preferred tool. This logging is a human-readable convention for operator review, not a machine-parsed format.
- **Skill domain**: A category of infrastructure operations that a skill addresses (e.g., git operations, container management, database queries, HTTP requests, issue tracking).

## Requirements

### REQ-1: Skill File Format

Every skill file MUST be a markdown document with the following sections:

1. **Title** — A heading describing the capability (e.g., `# Skill: Create Pull Request`).
2. **Purpose** — A brief description of what the skill does and when to use it.
3. **Tool Discovery** — An ordered list of tools the skill can use, from most preferred to least preferred, with instructions for detecting availability.
4. **Execution** — Step-by-step instructions for accomplishing the task using whichever tool was discovered, with separate subsections per tool path.
5. **Validation** — How to verify the action succeeded.

A skill file MAY also include:

- **Tier Requirement** — The minimum permission tier required to execute the skill.
- **Scope Rules** — File paths, hosts, or resources the skill MUST NOT modify.
- **Dry-Run Behavior** — How the skill behaves when `CLAUDEOPS_DRY_RUN` is `true`.

#### Scenario: Skill file contains all required sections

Given a baseline skill file at `/app/.claude/skills/git-pr.md`
When the agent reads the skill file
Then the file contains a title, purpose, tool discovery, execution, and validation section

#### Scenario: Skill file with optional tier requirement

Given a skill file includes a "Tier Requirement" section stating "Tier 2 minimum"
When a Tier 1 agent reads the skill
Then the agent MUST NOT execute the skill and MUST escalate to Tier 2

#### Scenario: Skill file with scope rules

Given the `git-pr.md` skill includes a "Scope Rules" section listing denied file paths (e.g., `ie.yaml`, `vms.yaml`, network configs)
When the agent prepares to create a PR modifying `ie.yaml`
Then the agent MUST refuse the operation and report the scope violation

### REQ-2: Skill Discovery and Loading

The system MUST discover skill files from two locations:

1. **Baseline skills** at `/app/.claude/skills/` — shipped with the Claude Ops container. The Claude Code CLI loads these natively from the `.claude/skills/` directory relative to the working directory.
2. **Repo-provided skills** at `.claude-ops/skills/` within each mounted repository under `$CLAUDEOPS_REPOS_DIR`.

Discovery MUST occur at the start of each monitoring cycle, following the same convention-based scanning defined in SPEC-0005 (REQ-4, REQ-7). Skill files MUST be markdown documents with a `.md` extension.

When a repo-provided skill has the same filename as a baseline skill, the repo-provided skill MUST take precedence for operations involving that repo's services.

#### Scenario: Baseline skills discovered at startup

Given the container starts with skill files in `/app/.claude/skills/`
When the agent begins a monitoring cycle
Then all `.md` files in `/app/.claude/skills/` are available as skills

#### Scenario: Repo-provided skills supplement baseline

Given a mounted repo at `/repos/infra-ansible` contains `.claude-ops/skills/deploy-service.md`
When the agent scans for extensions
Then `deploy-service.md` is available as a skill alongside baseline skills

#### Scenario: Repo skill overrides baseline for repo context

Given baseline skill `/app/.claude/skills/git-pr.md` exists
And repo `infra-ansible` provides `.claude-ops/skills/git-pr.md` with Gitea-specific instructions
When the agent needs to create a PR for a file in the `infra-ansible` repo
Then the agent MUST use the repo's `git-pr.md` skill for that repo's operations

#### Scenario: Skills re-discovered each cycle

Given a new skill file is added to `.claude-ops/skills/` between monitoring cycles
When the next monitoring cycle begins
Then the agent discovers and can use the new skill without a container restart

### REQ-3: Session-Level Tool Inventory

The agent MUST build a tool inventory at session initialization that maps capabilities to available tools. The inventory MUST be built once per session and reused for all skill invocations within that session.

The tool inventory MUST record:

1. **Available MCP tools** — enumerated from the Claude Code tool listing (these are known without probing).
2. **Available CLIs** — determined by checking for installed binaries (e.g., `which gh` or `command -v gh`).

The inventory MUST NOT be rebuilt mid-session. If the environment changes during a session (e.g., an MCP server becomes unavailable), the agent uses the inventory from session start.

#### Scenario: Tool inventory built at session start

Given the agent starts a new session
And the environment has `mcp__gitea__create_pull_request` available and `gh` installed
When the agent builds the tool inventory
Then the inventory records both `mcp__gitea__create_pull_request` (MCP) and `gh` (CLI) as available for git operations

#### Scenario: Tool inventory reused across skill invocations

Given the tool inventory was built at session start recording `docker` CLI as available
When the agent invokes a container health skill and later a container restart skill
Then both skills consult the same inventory without re-probing for `docker`

#### Scenario: CLI availability checked via which

Given the agent is building the tool inventory
When it checks for `tea` CLI availability
Then it runs `which tea` or `command -v tea` and records the result
And if `tea` is not found, it is recorded as unavailable

### REQ-4: Adaptive Tool Discovery with Preference Ordering

Each skill MUST define an ordered fallback chain of tool paths. The agent MUST attempt tools in the following preference order:

1. **MCP tools** (highest preference) — Pre-configured MCP servers provide the richest, most structured interface with typed schemas.
2. **CLI tools** — Installed command-line tools that are well-documented and widely available.
3. **Raw HTTP / curl** (lowest preference) — Universally available but requires constructing requests manually.

Within each tier, the skill MAY specify finer ordering (e.g., prefer `mcp__gitea__*` over `mcp__github__*` for a Gitea-hosted repo). The agent MUST select the first available tool from the fallback chain by consulting the tool inventory.

#### Scenario: MCP tool preferred when available

Given the tool inventory records `mcp__gitea__create_pull_request` as available
And `gh` CLI is also available
When the agent executes the `git-pr` skill for a Gitea repository
Then the agent MUST use `mcp__gitea__create_pull_request`

#### Scenario: CLI fallback when MCP unavailable

Given the tool inventory records no GitHub MCP tools available
And `gh` CLI is installed
When the agent executes the `git-pr` skill for a GitHub repository
Then the agent MUST use `gh` CLI

#### Scenario: HTTP fallback as last resort

Given the tool inventory records no MCP tools and no git CLIs available
And `curl` is installed
When the agent executes the `git-pr` skill
Then the agent SHOULD construct HTTP API requests using `curl` as the last available tool path

#### Scenario: No tool available

Given the tool inventory records no suitable tools for a skill's domain
When the agent attempts to execute the skill
Then the agent MUST report an error identifying the skill and the tools it searched for
And the agent MUST NOT silently skip the operation

### REQ-5: Fallback Observability

When a skill selects a tool path, the agent MUST log the selection. The logging MUST follow this convention:

- **Primary tool selected**: `[skill:<name>] Using: <tool> (<type>)` where type is "MCP", "CLI", or "HTTP".
- **Fallback occurred**: `[skill:<name>] WARNING: <preferred tool> not found, falling back to <actual tool> (<type>)`
- **No tool found**: `[skill:<name>] ERROR: No suitable tool found for <capability>`

This logging MUST occur for every skill invocation, not only when fallbacks occur. The format is a human-readable convention for operator review; it is not intended as a machine-parsed structured format.

#### Scenario: Primary tool selected — logged normally

Given the agent selects `mcp__gitea__create_pull_request` as the primary tool for the git-pr skill
When the selection is made
Then the agent logs `[skill:git-pr] Using: mcp__gitea__create_pull_request (MCP)`

#### Scenario: Fallback logged with warning

Given the tool inventory has no MCP tools for GitHub
And `gh` CLI is available
When the agent selects `gh` as a fallback for the git-pr skill
Then the agent logs `[skill:git-pr] WARNING: MCP tools not found for GitHub, falling back to gh (CLI)`

#### Scenario: Total failure logged as error

Given no suitable tools are available for the container-health skill
When the agent attempts to execute the skill
Then the agent logs `[skill:container-health] ERROR: No suitable tool found for container inspection`

### REQ-6: Tier Permission Integration

Skills MUST integrate with the existing permission tier model (ADR-0003, SPEC-0003). A skill that specifies a minimum tier MUST NOT be executed by an agent at a lower tier. Tier enforcement is prompt-based, consistent with the established permission model.

The agent MUST read `CLAUDEOPS_TIER` from the environment to determine the current tier. If `CLAUDEOPS_TIER` is not set, the agent MUST default to Tier 1 (most restrictive).

Skills MUST respect the tier boundaries defined in the runbook:
- **Tier 1**: Observe-only skills (health checks, read-only queries, HTTP/DNS checks).
- **Tier 2**: Safe remediation skills (container restart, file permissions, cache clearing, credential rotation, PR creation).
- **Tier 3**: Full remediation skills (Ansible playbooks, Helm upgrades, database recovery, multi-service orchestration).

#### Scenario: Tier 1 agent uses observe-only skill

Given a Tier 1 agent loads the `container-health` skill (Tier 1 minimum)
When the agent executes the skill
Then it performs read-only container inspection without restarting or modifying containers

#### Scenario: Tier 1 agent blocked from remediation skill

Given a Tier 1 agent encounters the `container-restart` skill (Tier 2 minimum)
When the agent reads the tier requirement
Then the agent MUST NOT execute the skill
And the agent MUST escalate to Tier 2

#### Scenario: Tier 3 agent may execute lower-tier skills

Given a Tier 3 agent loads the `container-restart` skill (Tier 2 minimum)
When the agent needs to restart a container as part of recovery
Then the agent MAY execute the skill because Tier 3 meets the Tier 2 minimum

#### Scenario: Missing CLAUDEOPS_TIER defaults to Tier 1

Given `CLAUDEOPS_TIER` is not set in the environment
When the agent evaluates tier permissions for a Tier 2 skill
Then the agent MUST treat itself as Tier 1 and refuse the skill

### REQ-7: Dry-Run Mode

Skills MUST respect `CLAUDEOPS_DRY_RUN`. When `CLAUDEOPS_DRY_RUN` is set to `"true"`, skills MUST NOT execute any mutating operations. The agent MUST log what it would do without executing, including:

- Which tool would be selected
- What command or API call would be made
- What parameters would be passed

Read-only operations (health checks, listing resources, querying status) MAY still execute in dry-run mode, as they do not modify state.

#### Scenario: Dry-run prevents mutating operations

Given `CLAUDEOPS_DRY_RUN=true` is set in the environment
When the agent executes the `git-pr` skill
Then the agent MUST NOT create a branch, commit files, or open a PR
And the agent MUST log: what tool would be used, what command would run, and what parameters would be passed

#### Scenario: Dry-run still validates inputs

Given `CLAUDEOPS_DRY_RUN=true` is set
When the agent executes the `git-pr` skill with a file path matching a denied scope rule
Then the agent MUST still report the scope violation

#### Scenario: Read-only skills may execute in dry-run

Given `CLAUDEOPS_DRY_RUN=true` is set
When the agent executes the `container-health` skill (read-only)
Then the agent MAY execute the health check and return results

### REQ-8: Scope Enforcement via Skill Instructions

Skills that perform mutating operations MUST include scope rules that define what the skill is NOT allowed to modify. These scope rules replace the programmatic `ValidateScope()` enforcement from SPEC-0019.

Scope rules MUST be defined in the skill file itself, so they are available in the agent's context when the skill executes. The denied paths and resources from the previous `ValidateScope()` implementation MUST be preserved in the relevant skill files.

For the `git-pr` skill specifically, the scope rules MUST include at minimum:
- Inventory files (`ie.yaml`, `vms.yaml`)
- Network configuration (Caddy config, WireGuard config, DNS records)
- Secrets and credentials
- The Claude Ops runbook and prompt files

#### Scenario: Skill includes explicit scope rules

Given the `git-pr.md` skill file includes a "Scope Rules" section
When the agent reads the skill
Then the scope rules list denied file patterns and resources

#### Scenario: Scope violation detected and refused

Given the agent is executing the `git-pr` skill
And the agent prepares to create a PR modifying `ie.yaml`
When the agent checks the skill's scope rules
Then the agent MUST refuse the operation
And the agent MUST report which file and which scope rule was violated

#### Scenario: Scope rules preserved from ValidateScope

Given SPEC-0019 defined denied paths in `gitprovider.ValidateScope()`
When the `git-pr` skill is authored
Then all previously denied paths MUST appear in the skill's scope rules section

### REQ-9: No Custom MCP Server Requirement

The system MUST NOT require custom MCP server development (Go code, JSON-RPC protocol handling, or MCP tool schema definitions) to add new infrastructure capabilities. All new capabilities MUST be expressible as skill files (markdown documents).

Pre-configured MCP servers (e.g., user-installed `mcp__github__*`, `mcp__gitea__*`, `mcp__docker__*`) remain supported as preferred tool paths within skills, but the system MUST NOT ship or maintain its own MCP server implementations.

#### Scenario: New capability added via skill file

Given an operator wants to add a "rotate API key" capability
When they create `/app/.claude/skills/rotate-api-key.md` with tool discovery, execution, and validation sections
Then the agent can perform API key rotation on the next monitoring cycle
And no Go code, MCP server implementation, or container rebuild is required

#### Scenario: User-provided MCP servers used when available

Given a user has pre-configured `mcp__github__create_issue` in their Claude Code settings
When the agent executes an issue-tracking skill
Then the agent MUST prefer the MCP tool over CLI alternatives

#### Scenario: System functions without any MCP servers

Given the environment has no MCP servers configured
And CLI tools (`gh`, `docker`, `psql`, `curl`) are installed
When the agent executes skills
Then all skills function using CLI fallbacks

### REQ-10: Skill Domains

The system MUST provide baseline skills covering at minimum the following domains:

1. **Git operations** — PR creation, PR listing, PR status checking. Tool paths: `mcp__gitea__*` / `mcp__github__*`, `gh` CLI, `tea` CLI, `curl` to API.
2. **Container operations** — Container health checking, container restart, log inspection. Tool paths: `mcp__docker__*`, `docker` CLI.
3. **Database operations** — Connection checking, read-only queries. Tool paths: `mcp__postgres__*`, `psql` CLI, `mysql` CLI.
4. **HTTP operations** — Health checks, REST API interaction. Tool paths: Fetch MCP, `curl` CLI.
5. **Issue tracking** — Issue creation, listing, status updates. Tool paths: `mcp__github__*` / `mcp__gitea__*` issue tools, `gh issue` CLI, `tea issue` CLI.
6. **Browser automation** — Web UI interaction for credential rotation. Tool paths: Chrome DevTools MCP, Playwright CLI.

Each domain MUST have at least one baseline skill file in `/app/.claude/skills/`.

#### Scenario: Git operations skill available

Given the agent starts a monitoring cycle
When it discovers skills in `/app/.claude/skills/`
Then a skill for git PR operations is available with tool paths for MCP, gh, tea, and curl

#### Scenario: Container operations skill available

Given the agent starts a monitoring cycle
When it discovers skills in `/app/.claude/skills/`
Then a skill for container health checking is available with tool paths for Docker MCP and docker CLI

#### Scenario: All domains covered by baseline skills

Given the Claude Ops container starts with its default skill set
When the agent lists available baseline skills
Then at least one skill exists for each of the six defined domains

### REQ-11: Skill Composability with Existing Extensions

Skills MUST compose with the existing extension types defined in SPEC-0005 (checks and playbooks in `.claude-ops/`) and SPEC-0002 (markdown instruction format). Checks and playbooks MAY reference skills for their tool execution. Skills MUST NOT replace checks or playbooks — they provide the "how to use tools" layer while checks provide the "what to verify" layer and playbooks provide the "how to remediate" layer.

#### Scenario: Check references a skill for tool execution

Given `checks/http.md` describes performing HTTP health checks
And the `http-request` skill describes how to make HTTP requests using available tools
When the Tier 1 agent runs the HTTP health check
Then the agent uses the `http-request` skill to determine whether to use Fetch MCP or `curl`

#### Scenario: Playbook references a skill for container operations

Given `playbooks/restart-container.md` describes restarting a container
And the `container-ops` skill describes how to interact with containers using available tools
When the Tier 2 agent executes the restart playbook
Then the agent uses the `container-ops` skill to determine whether to use Docker MCP or `docker` CLI

#### Scenario: Skill does not replace check

Given a `container-health` skill exists and a `checks/containers.md` check exists
When the agent runs health checks
Then the check file determines WHAT to verify (which containers, what thresholds)
And the skill determines HOW to interact with containers (which tool to use)

### REQ-12: Skill Testability

Skills MUST support testing via dry-run mode (`CLAUDEOPS_DRY_RUN=true`), which exercises tool discovery, selection, and validation logic without executing mutating operations.

Each skill SHOULD be tested in at least three environment configurations:

1. **MCP-only** — Relevant MCP servers configured, CLIs absent.
2. **CLI-only** — CLIs installed, no MCP servers configured.
3. **Mixed** — Both MCP servers and CLIs available, verifying the preference order.

Each tool path defined in a skill SHOULD have an acceptance test that validates:
- Tool discovery correctly identifies the available tool
- The skill executes the correct commands or calls for that tool path
- The validation step confirms success

Fallback paths SHOULD be specifically tested by removing preferred tools and verifying the skill falls through with appropriate warning logs (REQ-5).

#### Scenario: Dry-run exercises discovery without side effects

Given `CLAUDEOPS_DRY_RUN=true` is set
When the agent executes the `git-pr` skill
Then the agent performs tool discovery, selects a tool, and logs the selection
And no branches, commits, or PRs are created

#### Scenario: Skill tested in CLI-only environment

Given the environment has `gh` CLI installed but no GitHub MCP tools
When the `git-pr` skill is tested
Then the skill selects `gh` CLI as the tool path
And the acceptance test verifies the correct `gh` commands are constructed

#### Scenario: Fallback path tested by removing preferred tool

Given the environment normally has `mcp__docker__list_containers` available
When that MCP tool is removed from the environment for testing
Then the `container-health` skill falls through to `docker` CLI
And the agent logs a WARNING about the fallback (per REQ-5)

## References

- [ADR-0022: Skills-Based Tool Orchestration](../../adrs/ADR-0022-skills-based-tool-orchestration.md)
- [ADR-0002: Use Markdown Documents as Executable Instructions](../../adrs/ADR-0002-markdown-as-executable-instructions.md)
- [ADR-0003: Enforce Permission Tiers via Prompt Instructions](../../adrs/ADR-0003-prompt-based-permission-enforcement.md)
- [ADR-0005: Allow Mounted Repos to Extend the Agent via Convention-Based Discovery](../../adrs/ADR-0005-mounted-repo-extension-model.md)
- [ADR-0010: Claude Code CLI Subprocess](../../adrs/ADR-0010-claude-code-cli-subprocess.md)
- [SPEC-0002: Markdown as Executable Instructions](../markdown-executable-instructions/spec.md)
- [SPEC-0005: Mounted Repo Extension Model](../mounted-repo-extensions/spec.md)
- [SPEC-0006: MCP Infrastructure Bridge](../mcp-infrastructure-bridge/spec.md) (superseded)
- [SPEC-0019: MCP Server for Git Provider Interface](../mcp-gitprovider/spec.md) (superseded)
