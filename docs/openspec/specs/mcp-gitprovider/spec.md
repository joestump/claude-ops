# SPEC-0019: MCP Server for Git Provider Interface

## Overview

Claude Ops agents at Tier 2 and Tier 3 can propose changes to operational procedures via pull requests through the git provider interface (SPEC-0018). Currently, the only access path is the REST API (`POST /api/v1/prs`, `GET /api/v1/prs`), which requires the agent to construct curl commands with JSON payloads. This specification defines an MCP server that exposes the same git provider operations as typed MCP tools, aligning PR workflows with the MCP-first architecture established in SPEC-0006.

The MCP server is implemented as a `claudeops mcp-server` subcommand of the existing binary, speaking JSON-RPC over stdio. It wraps the `internal/gitprovider` package, enforcing the same scope validation, tier gating, and dry-run behavior as the REST API. This is additive — the REST API remains for the dashboard UI and external integrations.

See [ADR-0019: MCP Server for Git Provider Interface](/docs/adrs/ADR-0019-mcp-gitprovider-interface.md).

## Definitions

- **MCP Server**: A subprocess that implements the Model Context Protocol, exposing typed tools over JSON-RPC stdio communication. See SPEC-0006 for the general MCP architecture.
- **`claudeops mcp-server`**: A Cobra subcommand of the `claudeops` binary that starts the MCP server process. The binary is already present in the container at `/app/claudeops`.
- **MCP Tool**: A named operation exposed by the MCP server with a JSON Schema defining its parameters and return type. The agent invokes tools through the MCP protocol rather than constructing CLI commands.
- **Token Gating**: The practice of routing API tokens (`GITHUB_TOKEN`, `GITEA_TOKEN`) to the MCP server subprocess via its `env` block in `.claude/mcp.json`, keeping them out of the agent's normal tool-calling workflow. This is defense-in-depth, not absolute isolation.
- **Server-Side Tier Enforcement**: The MCP server reads `CLAUDEOPS_TIER` from its own environment (set by the session manager) and validates tier restrictions before executing operations, rather than trusting the agent to self-report its tier.

## Requirements

### SPEC-0019-REQ-1: MCP Server Subcommand

The `claudeops` binary MUST provide a `mcp-server` subcommand that starts a JSON-RPC server communicating over stdin/stdout (stdio). The subcommand MUST NOT start the web dashboard, session manager, or any other supervisor components. The subcommand MUST initialize a `gitprovider.Registry` from environment variables and expose MCP tools that wrap the registry's providers.

#### Scenario: Subcommand starts stdio server

- **WHEN** the command `/app/claudeops mcp-server` is executed
- **THEN** the process MUST start a JSON-RPC server reading from stdin and writing to stdout
- **AND** the process MUST NOT listen on any TCP/UDP port

#### Scenario: Subcommand initializes provider registry

- **WHEN** the MCP server starts with `GITHUB_TOKEN` set in its environment
- **THEN** the server MUST initialize a `gitprovider.Registry` with the GitHub provider enabled
- **AND** the server MUST be ready to handle `create_pr` calls against GitHub repositories

#### Scenario: Subcommand without provider tokens

- **WHEN** the MCP server starts without any provider tokens set
- **THEN** the server MUST start successfully
- **AND** all providers MUST be registered in a disabled state
- **AND** tool calls that require a provider MUST return descriptive errors

### SPEC-0019-REQ-2: MCP Tool Discovery

The MCP server MUST respond to tool discovery requests (JSON-RPC `tools/list` method) by returning the definitions of three tools: `create_pr`, `list_prs`, and `get_pr_status`. Each tool definition MUST include a name, description, and JSON Schema defining its input parameters.

#### Scenario: Agent discovers available tools

- **WHEN** the Claude Code CLI sends a `tools/list` request to the MCP server
- **THEN** the response MUST include exactly three tool definitions: `create_pr`, `list_prs`, and `get_pr_status`
- **AND** each tool MUST have a human-readable description explaining its purpose

#### Scenario: Tool schemas include parameter types

- **WHEN** the agent inspects the `create_pr` tool schema
- **THEN** the schema MUST define parameters with JSON Schema types (string, integer, array, object)
- **AND** required parameters MUST be marked as required in the schema

### SPEC-0019-REQ-3: `create_pr` Tool

The MCP server MUST expose a `create_pr` tool that creates a branch, commits files, and opens a pull request. The tool MUST accept the following parameters: `repo_owner` (string, REQUIRED), `repo_name` (string, REQUIRED), `title` (string, REQUIRED), `body` (string, REQUIRED), `files` (array of file change objects, REQUIRED), `clone_url` (string, OPTIONAL), `base_branch` (string, OPTIONAL, default "main"), and `change_type` (string, OPTIONAL, default "fix"). Each file change object MUST include `path` (string), `content` (string), and `action` (string: "create", "update", or "delete").

The tool MUST perform the following validations before any git operation:
1. Call `gitprovider.ValidateScope(files)` to enforce allowed/denied file patterns.
2. Call `gitprovider.ValidateTier(tier, files)` using the tier read from `CLAUDEOPS_TIER`.
3. Resolve the provider via `Registry.Resolve()`.

On success, the tool MUST return a JSON object with `number` (int), `url` (string), and `branch` (string).

#### Scenario: Successful PR creation

- **WHEN** a Tier 2 agent calls `create_pr` with valid parameters and 2 files in allowed scope
- **THEN** the MCP server MUST create a branch named `claude-ops/{change_type}/{slug}`
- **AND** MUST commit the files to the branch
- **AND** MUST open a PR with the specified title and body
- **AND** MUST return `{number, url, branch}`

#### Scenario: Scope validation failure

- **WHEN** the agent calls `create_pr` with a file path matching a denied pattern (e.g., `prompts/tier1-observe.md`)
- **THEN** the MCP server MUST return an error before any git operation is executed
- **AND** the error message MUST identify the denied path and pattern

#### Scenario: Tier validation failure

- **WHEN** a Tier 1 agent calls `create_pr`
- **THEN** the MCP server MUST return an error indicating that Tier 1 agents cannot create PRs

#### Scenario: Tier 2 file limit enforcement

- **WHEN** a Tier 2 agent calls `create_pr` with more than 3 files
- **THEN** the MCP server MUST return an error indicating the file limit
- **AND** the error SHOULD suggest escalation to Tier 3

#### Scenario: Provider resolution failure

- **WHEN** the agent calls `create_pr` with a `clone_url` that does not match any registered provider
- **AND** no manifest-based provider declaration exists
- **THEN** the MCP server MUST return an error identifying the unresolvable repository

### SPEC-0019-REQ-4: `list_prs` Tool

The MCP server MUST expose a `list_prs` tool that lists open pull requests filtered by the `claude-ops` label. The tool MUST accept: `repo_owner` (string, REQUIRED), `repo_name` (string, REQUIRED), and `clone_url` (string, OPTIONAL). The tool MUST return a JSON array of objects, each with `number` (int), `title` (string), and `files` (array of string paths).

#### Scenario: List open PRs

- **WHEN** the agent calls `list_prs` for a repository with 2 open claude-ops PRs
- **THEN** the MCP server MUST return an array of 2 PR summary objects
- **AND** each summary MUST include the PR number, title, and list of modified file paths

#### Scenario: No open PRs

- **WHEN** the agent calls `list_prs` for a repository with no open claude-ops PRs
- **THEN** the MCP server MUST return an empty array

#### Scenario: Provider not available

- **WHEN** the agent calls `list_prs` but the resolved provider is disabled
- **THEN** the MCP server MUST return an error explaining why the provider is unavailable

### SPEC-0019-REQ-5: `get_pr_status` Tool

The MCP server MUST expose a `get_pr_status` tool that returns the current status of a specific pull request. The tool MUST accept: `repo_owner` (string, REQUIRED), `repo_name` (string, REQUIRED), `pr_number` (integer, REQUIRED), and `clone_url` (string, OPTIONAL). The tool MUST return a JSON object with `number` (int), `state` (string: "open", "closed", or "merged"), `mergeable` (boolean), and `reviews` (array of review objects with `author`, `state`, and `body`).

#### Scenario: Get status of open PR

- **WHEN** the agent calls `get_pr_status` for an open PR with one approval review
- **THEN** the MCP server MUST return `{number, state: "open", mergeable: true, reviews: [{author, state: "approved", body}]}`

#### Scenario: Get status of merged PR

- **WHEN** the agent calls `get_pr_status` for a previously merged PR
- **THEN** the MCP server MUST return `{state: "merged"}`

#### Scenario: PR not found

- **WHEN** the agent calls `get_pr_status` with a non-existent PR number
- **THEN** the MCP server MUST return an error indicating the PR was not found

### SPEC-0019-REQ-6: Server-Side Tier Enforcement

The MCP server MUST read the `CLAUDEOPS_TIER` environment variable to determine the calling agent's permission tier. The tier MUST be an integer (1, 2, or 3). If `CLAUDEOPS_TIER` is not set or is not a valid integer, the MCP server MUST default to Tier 1 (most restrictive). The `create_pr` tool MUST call `gitprovider.ValidateTier(tier, files)` using the environment-derived tier, not any tier value from the tool parameters.

#### Scenario: Tier read from environment

- **WHEN** the MCP server starts with `CLAUDEOPS_TIER=2`
- **AND** the agent calls `create_pr`
- **THEN** the server MUST validate against Tier 2 restrictions (max 3 files)

#### Scenario: Missing tier defaults to Tier 1

- **WHEN** the MCP server starts without `CLAUDEOPS_TIER` set
- **AND** the agent calls `create_pr`
- **THEN** the server MUST reject the call with a Tier 1 restriction error

#### Scenario: Invalid tier defaults to Tier 1

- **WHEN** the MCP server starts with `CLAUDEOPS_TIER=abc`
- **AND** the agent calls `create_pr`
- **THEN** the server MUST treat the tier as 1 and reject the call

#### Scenario: Tier is not a tool parameter

- **WHEN** the agent inspects the `create_pr` tool schema
- **THEN** there MUST NOT be a `tier` parameter in the schema
- **AND** the tier MUST be determined solely from the environment

### SPEC-0019-REQ-7: Dry Run Mode

When `CLAUDEOPS_DRY_RUN` is set to `"true"` in the MCP server's environment, the `create_pr` tool MUST NOT execute any git provider API calls. The tool MUST return a response indicating dry-run mode with `dry_run: true`. The `list_prs` and `get_pr_status` tools MAY still execute read-only API calls in dry-run mode, as they do not modify state.

#### Scenario: Dry run prevents PR creation

- **WHEN** `CLAUDEOPS_DRY_RUN=true` is set in the MCP server's environment
- **AND** the agent calls `create_pr` with valid parameters
- **THEN** the server MUST NOT call `CreateBranch`, `CommitFiles`, or `CreatePR` on the provider
- **AND** the server MUST return a response with `dry_run: true`

#### Scenario: Dry run still validates

- **WHEN** `CLAUDEOPS_DRY_RUN=true` is set
- **AND** the agent calls `create_pr` with a file in denied scope
- **THEN** the server MUST still return a scope validation error

#### Scenario: Read-only tools in dry run

- **WHEN** `CLAUDEOPS_DRY_RUN=true` is set
- **AND** the agent calls `list_prs`
- **THEN** the server MAY execute the read-only API call and return results

### SPEC-0019-REQ-8: Token Gating via MCP Environment

The MCP server's entry in `.claude/mcp.json` MUST pass `GITHUB_TOKEN`, `GITEA_URL`, and `GITEA_TOKEN` to the server subprocess via the `env` block. These tokens MUST NOT be required in the agent's own process environment for PR operations. The agent MUST interact with the MCP tools without handling or knowing the values of these tokens.

This is defense-in-depth: the agent has Bash access and MAY be able to discover tokens through side channels (e.g., `env`, `printenv`, `/proc`). The token gating ensures tokens are absent from the agent's normal MCP tool-calling workflow, reducing the risk of accidental leakage in logs, PR descriptions, or memory entries.

#### Scenario: Token flows to MCP subprocess only

- **WHEN** the session manager spawns a Claude Code CLI process
- **AND** the CLI spawns the `claudeops mcp-server` subprocess per `.claude/mcp.json`
- **THEN** `GITHUB_TOKEN` and `GITEA_TOKEN` MUST be available in the MCP server's process environment
- **AND** the agent's tool calls to `create_pr` MUST NOT include any token parameters

#### Scenario: Agent creates PR without token knowledge

- **WHEN** the agent calls `create_pr` with repo_owner, repo_name, title, body, and files
- **THEN** the MCP server MUST authenticate with the git provider using tokens from its own environment
- **AND** the agent MUST NOT have passed any authentication credentials in the tool call

### SPEC-0019-REQ-9: MCP Configuration Registration

The MCP server MUST be registered in the baseline `.claude/mcp.json` alongside the existing Docker, PostgreSQL, Chrome DevTools, and Fetch MCP servers. The registration MUST use `"type": "stdio"`, `"command": "/app/claudeops"`, and `"args": ["mcp-server"]`. The `env` block MUST include `GITHUB_TOKEN`, `GITEA_URL`, `GITEA_TOKEN`, `CLAUDEOPS_TIER`, and `CLAUDEOPS_DRY_RUN`.

#### Scenario: MCP config includes claudeops server

- **WHEN** the agent reads `.claude/mcp.json`
- **THEN** the config MUST contain a `claudeops` entry with command `/app/claudeops` and args `["mcp-server"]`

#### Scenario: Environment variables passed through

- **WHEN** the Claude CLI spawns the `claudeops` MCP server
- **THEN** the subprocess MUST receive `GITHUB_TOKEN`, `GITEA_URL`, `GITEA_TOKEN`, `CLAUDEOPS_TIER`, and `CLAUDEOPS_DRY_RUN` from the `env` block

#### Scenario: MCP server coexists with baseline servers

- **WHEN** the merged MCP config is loaded
- **THEN** the `claudeops` server MUST coexist with `docker`, `postgres`, `chrome-devtools`, and `fetch` servers without conflict

### SPEC-0019-REQ-10: REST API Remains Functional

The addition of the MCP server MUST NOT remove, modify, or degrade the existing REST API endpoints for PR operations (`POST /api/v1/prs`, `GET /api/v1/prs`). The REST API MUST continue to serve the dashboard UI and external integrations. Both the MCP server and the REST API MUST use the same `internal/gitprovider` package, ensuring consistent behavior.

#### Scenario: REST API unaffected by MCP server

- **WHEN** the MCP server is registered and running
- **THEN** `POST /api/v1/prs` and `GET /api/v1/prs` MUST continue to function as specified in SPEC-0018

#### Scenario: Same validation in both paths

- **WHEN** the agent calls `create_pr` via MCP with a denied file path
- **AND** the dashboard calls `POST /api/v1/prs` with the same denied file path
- **THEN** both MUST return equivalent scope validation errors

### SPEC-0019-REQ-11: Branch Name Generation

The `create_pr` tool MUST generate branch names using the same algorithm as the REST API: `claude-ops/{change_type}/{slug}`. The slug MUST be derived from the title, lowercased, with non-alphanumeric characters replaced by hyphens, consecutive hyphens collapsed, and truncated to 50 characters. This MUST match the behavior specified in SPEC-0018-REQ-5.

#### Scenario: Branch name matches REST API behavior

- **WHEN** the agent calls `create_pr` with title "Add health check for jellyfin" and change_type "check"
- **THEN** the branch name MUST be `claude-ops/check/add-health-check-for-jellyfin`

#### Scenario: Default change type

- **WHEN** the agent calls `create_pr` without specifying `change_type`
- **THEN** the change_type MUST default to "fix"

## References

- [ADR-0019: MCP Server for Git Provider Interface](/docs/adrs/ADR-0019-mcp-gitprovider-interface.md)
- [ADR-0018: PR-Based Workflow for Runbook, Playbook, and Manifest Changes](/docs/adrs/ADR-0018-pr-based-config-changes.md)
- [ADR-0006: Use MCP Servers as Primary Infrastructure Access Layer](/docs/adrs/ADR-0006-mcp-infrastructure-bridge.md)
- [SPEC-0018: PR-Based Configuration Changes](/docs/openspec/specs/pr-based-config-changes/spec.md)
- [SPEC-0006: MCP Infrastructure Bridge](/docs/openspec/specs/mcp-infrastructure-bridge/spec.md)
- [Model Context Protocol specification](https://modelcontextprotocol.io/)
