# Skill: Git Pull Request Operations

<!-- Governing: SPEC-0023 REQ-10, REQ-11, REQ-12; ADR-0022 -->

## Purpose

Create, list, and check the status of pull requests across Git hosting providers (Gitea, GitHub). Use this skill when a remediation or configuration change needs to be proposed via PR, or when checking the status of existing PRs.

## Tier Requirement

Tier 2 minimum. Creating PRs modifies remote repository state. Tier 1 agents MUST NOT execute this skill and MUST escalate to Tier 2. Listing and viewing PR status is read-only but grouped here because PR creation is the primary use case.

## Tool Discovery

This skill uses the following tools in preference order:

### For Gitea repositories
1. **MCP**: `mcp__gitea__create_pull_request`, `mcp__gitea__list_pull_requests`, `mcp__gitea__get_pull_request_by_index` — check if available in tool listing
2. **CLI**: `tea` — check with `which tea`
3. **HTTP**: `curl` to Gitea API (`$GITEA_URL/api/v1/`) — universal fallback; requires `$GITEA_TOKEN`

### For GitHub repositories
1. **MCP**: `mcp__github__create_pull_request`, `mcp__github__list_pull_requests` — check if available in tool listing
2. **CLI**: `gh` — check with `which gh`
3. **HTTP**: `curl` to GitHub API (`https://api.github.com/`) — universal fallback; requires `$GITHUB_TOKEN`

## Execution

### Create Pull Request

#### Using MCP: mcp__gitea__create_pull_request (Gitea)

1. Check for duplicate PRs first using `mcp__gitea__list_pull_requests` with the target repo owner and name.
2. If no duplicate exists, call `mcp__gitea__create_pull_request` with:
   - `owner`: repository owner
   - `repo`: repository name
   - `title`: PR title
   - `body`: PR description
   - `head`: source branch
   - `base`: target branch (usually `main`)
3. Log: `[skill:git-pr] Using: mcp__gitea__create_pull_request (MCP)`

#### Using MCP: mcp__github__create_pull_request (GitHub)

1. Check for duplicate PRs first using `mcp__github__list_pull_requests`.
2. If no duplicate exists, call `mcp__github__create_pull_request` with title, body, head, and base.
3. Log: `[skill:git-pr] Using: mcp__github__create_pull_request (MCP)`

#### Using CLI: gh (GitHub)

1. Check for duplicates: `gh pr list --repo <owner>/<repo> --head <branch> --state open`
2. If no duplicate, create the PR:
   ```bash
   gh pr create --repo <owner>/<repo> --title "<title>" --body "<body>" --head <branch> --base main
   ```
3. Log: `[skill:git-pr] Using: gh (CLI)`
4. If MCP was preferred but unavailable, also log: `[skill:git-pr] WARNING: MCP tools not found for GitHub, falling back to gh (CLI)`

#### Using CLI: tea (Gitea)

1. Check for duplicates: `tea pr list --repo <owner>/<repo> --state open`
2. If no duplicate, create the PR:
   ```bash
   tea pr create --repo <owner>/<repo> --title "<title>" --description "<body>" --head <branch> --base main
   ```
3. Log: `[skill:git-pr] Using: tea (CLI)`
4. If MCP was preferred but unavailable, also log: `[skill:git-pr] WARNING: MCP tools not found for Gitea, falling back to tea (CLI)`

#### Using HTTP: curl (Gitea)

1. Check for duplicates:
   ```bash
   curl -s -H "Authorization: token $GITEA_TOKEN" "$GITEA_URL/api/v1/repos/<owner>/<repo>/pulls?state=open&head=<branch>"
   ```
2. If no duplicate, create the PR:
   ```bash
   curl -s -X POST \
     -H "Authorization: token $GITEA_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"title":"<title>","body":"<body>","head":"<branch>","base":"main"}' \
     "$GITEA_URL/api/v1/repos/<owner>/<repo>/pulls"
   ```
3. Log: `[skill:git-pr] Using: curl (HTTP)`
4. If MCP and CLI were preferred but unavailable, also log: `[skill:git-pr] WARNING: MCP and CLI tools not found, falling back to curl (HTTP)`

#### Using HTTP: curl (GitHub)

1. Check for duplicates:
   ```bash
   curl -s -H "Authorization: token $GITHUB_TOKEN" "https://api.github.com/repos/<owner>/<repo>/pulls?state=open&head=<owner>:<branch>"
   ```
2. If no duplicate, create the PR:
   ```bash
   curl -s -X POST \
     -H "Authorization: token $GITHUB_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"title":"<title>","body":"<body>","head":"<branch>","base":"main"}' \
     "https://api.github.com/repos/<owner>/<repo>/pulls"
   ```
3. Log: `[skill:git-pr] Using: curl (HTTP)`

### List Pull Requests

#### Using MCP: mcp__gitea__list_pull_requests / mcp__github__list_pull_requests

1. Call the appropriate MCP tool with owner, repo, and optional state filter.
2. Log: `[skill:git-pr] Using: mcp__gitea__list_pull_requests (MCP)`

#### Using CLI: gh / tea

1. Run `gh pr list --repo <owner>/<repo>` or `tea pr list --repo <owner>/<repo>`.
2. Log: `[skill:git-pr] Using: gh pr list (CLI)` or `[skill:git-pr] Using: tea pr list (CLI)`

#### Using HTTP: curl

1. GET the pulls endpoint with appropriate auth header.
2. Log: `[skill:git-pr] Using: curl (HTTP)`

### Get PR Status

#### Using MCP: mcp__gitea__get_pull_request_by_index / mcp__github__get_pull_request

1. Call with owner, repo, and PR number/index.
2. Log: `[skill:git-pr] Using: mcp__gitea__get_pull_request_by_index (MCP)`

#### Using CLI: gh / tea

1. Run `gh pr view <number> --repo <owner>/<repo>` or `tea pr view <number> --repo <owner>/<repo>`.
2. Log: `[skill:git-pr] Using: gh pr view (CLI)`

#### Using HTTP: curl

1. GET the specific pull endpoint.
2. Log: `[skill:git-pr] Using: curl (HTTP)`

## Validation

After creating a PR:
1. Confirm the response contains a PR number/URL.
2. Verify the PR exists by listing or fetching its status.
3. Report the PR URL in the monitoring cycle output.

After listing PRs:
1. Confirm the response is a valid list (may be empty).

After getting PR status:
1. Confirm the response contains the PR state (open, closed, merged).

## Scope Rules

This skill MUST NOT create PRs that modify any of the following files or resources:

- **Inventory files**: `ie.yaml`, `vms.yaml`, or any file matching `**/inventory/*.yaml`
- **Network configuration**: Caddy configs (`Caddyfile`, `caddy/*.json`), WireGuard configs (`wg*.conf`, `wireguard/`), DNS records
- **Secrets and credentials**: `.env` files, `**/secrets/**`, `**/credentials/**`, `*.key`, `*.pem`, `*.crt`, password files
- **Claude Ops runbook and prompts**: `CLAUDE.md`, `prompts/*.md`, `entrypoint.sh`, `checks/*.md`, `playbooks/*.md`
- **Persistent data volumes**: anything under `/volumes/`

If the agent detects that a proposed PR would modify any denied path, it MUST:
1. Refuse the operation.
2. Report: `[skill:git-pr] SCOPE VIOLATION: Refusing to create PR modifying <path> — matches denied pattern <rule>`
3. Do NOT attempt to work around the restriction.

### Branch Naming Convention

PRs created by Claude Ops SHOULD use the branch naming pattern: `claude-ops/<type>/<name>` (e.g., `claude-ops/fix/shamrock-config`, `claude-ops/chore/update-api-key`).

## Dry-Run Behavior

When `CLAUDEOPS_DRY_RUN=true`:
- MUST NOT create branches, commits, or pull requests.
- MUST still perform tool discovery and selection.
- MUST still check scope rules and report violations.
- MAY list existing PRs (read-only).
- Log: `[skill:git-pr] DRY RUN: Would create PR "<title>" on <owner>/<repo> from <head> to <base> using <tool>`
