# Skill: Issue Tracking

<!-- Governing: SPEC-0023 REQ-10, REQ-11, REQ-12; ADR-0022 -->

## Purpose

Create, list, view, and update issues on Git hosting providers (Gitea, GitHub). Use this skill to file issues for problems that require human attention, track remediation work, or check the status of existing issues. Listing and viewing issues is available at all tiers; creating and updating issues requires Tier 2.

## Tier Requirement

Tier 1 minimum for listing and viewing issues (read-only).
Tier 2 minimum for creating, commenting on, and updating issues.

## Tool Discovery

This skill uses the following tools in preference order:

### For Gitea repositories
1. **MCP**: `mcp__gitea__create_issue`, `mcp__gitea__list_repo_issues`, `mcp__gitea__get_issue_by_index`, `mcp__gitea__edit_issue`, `mcp__gitea__create_issue_comment` — check if available in tool listing
2. **CLI**: `tea` — check with `which tea`
3. **HTTP**: `curl` to Gitea API (`$GITEA_URL/api/v1/`) — requires `$GITEA_TOKEN`

### For GitHub repositories
1. **MCP**: `mcp__github__create_issue`, `mcp__github__list_issues`, `mcp__github__get_issue` — check if available in tool listing
2. **CLI**: `gh` — check with `which gh`
3. **HTTP**: `curl` to GitHub API (`https://api.github.com/`) — requires `$GITHUB_TOKEN`

## Execution

### Create Issue

#### Using MCP: mcp__gitea__create_issue (Gitea)

1. Call `mcp__gitea__create_issue` with:
   - `owner`: repository owner
   - `repo`: repository name
   - `title`: issue title
   - `body`: issue description with diagnostic context
2. Optionally add labels using `mcp__gitea__add_issue_labels`.
3. Log: `[skill:issue-tracking] Using: mcp__gitea__create_issue (MCP)`

#### Using MCP: mcp__github__create_issue (GitHub)

1. Call `mcp__github__create_issue` with title, body, and optional labels.
2. Log: `[skill:issue-tracking] Using: mcp__github__create_issue (MCP)`

#### Using CLI: gh (GitHub)

1. ```bash
   gh issue create --repo <owner>/<repo> --title "<title>" --body "<body>"
   ```
2. Optionally add labels: `--label "claude-ops,needs-attention"`
3. Log: `[skill:issue-tracking] Using: gh issue create (CLI)`
4. If MCP was preferred but unavailable, also log: `[skill:issue-tracking] WARNING: GitHub MCP not available, falling back to gh (CLI)`

#### Using CLI: tea (Gitea)

1. ```bash
   tea issue create --repo <owner>/<repo> --title "<title>" --description "<body>"
   ```
2. Log: `[skill:issue-tracking] Using: tea issue create (CLI)`
3. If MCP was preferred but unavailable, also log: `[skill:issue-tracking] WARNING: Gitea MCP not available, falling back to tea (CLI)`

#### Using HTTP: curl (Gitea)

1. ```bash
   curl -s -X POST \
     -H "Authorization: token $GITEA_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"title":"<title>","body":"<body>"}' \
     "$GITEA_URL/api/v1/repos/<owner>/<repo>/issues"
   ```
2. Log: `[skill:issue-tracking] Using: curl (HTTP)`
3. If MCP and CLI were preferred but unavailable, also log: `[skill:issue-tracking] WARNING: MCP and CLI not available, falling back to curl (HTTP)`

#### Using HTTP: curl (GitHub)

1. ```bash
   curl -s -X POST \
     -H "Authorization: token $GITHUB_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"title":"<title>","body":"<body>"}' \
     "https://api.github.com/repos/<owner>/<repo>/issues"
   ```
2. Log: `[skill:issue-tracking] Using: curl (HTTP)`

### List Issues

#### Using MCP: mcp__gitea__list_repo_issues / mcp__github__list_issues

1. Call the appropriate MCP tool with owner, repo, and optional state filter.
2. Log: `[skill:issue-tracking] Using: mcp__gitea__list_repo_issues (MCP)` or `[skill:issue-tracking] Using: mcp__github__list_issues (MCP)`

#### Using CLI: gh / tea

1. `gh issue list --repo <owner>/<repo>` or `tea issue list --repo <owner>/<repo>`.
2. Log: `[skill:issue-tracking] Using: gh issue list (CLI)` or `[skill:issue-tracking] Using: tea issue list (CLI)`

#### Using HTTP: curl

1. GET the issues endpoint with appropriate auth header.
2. Log: `[skill:issue-tracking] Using: curl (HTTP)`

### View Issue

#### Using MCP: mcp__gitea__get_issue_by_index / mcp__github__get_issue

1. Call with owner, repo, and issue number.
2. Log: `[skill:issue-tracking] Using: mcp__gitea__get_issue_by_index (MCP)`

#### Using CLI: gh / tea

1. `gh issue view <number> --repo <owner>/<repo>` or `tea issue view <number> --repo <owner>/<repo>`.
2. Log: `[skill:issue-tracking] Using: gh issue view (CLI)`

### Add Comment to Issue

#### Using MCP: mcp__gitea__create_issue_comment

1. Call with owner, repo, issue index, and comment body.
2. Log: `[skill:issue-tracking] Using: mcp__gitea__create_issue_comment (MCP)`

#### Using CLI: gh

1. ```bash
   gh issue comment <number> --repo <owner>/<repo> --body "<comment>"
   ```
2. Log: `[skill:issue-tracking] Using: gh issue comment (CLI)`

## Validation

After creating an issue:
1. Confirm the response contains an issue number/URL.
2. Report the issue URL in the monitoring cycle output.

After listing issues:
1. Confirm a valid list was returned (may be empty).

After viewing an issue:
1. Confirm the issue details were retrieved (title, state, body).

## Scope Rules

This skill MUST NOT:
- Create or modify issues on repositories not defined in repo inventories
- Close or delete issues — only humans may close issues
- Modify issue labels that control deployment or CI/CD workflows (e.g., `deploy`, `release`)
- Create issues with content that includes secrets, credentials, or internal IP addresses

If a scope violation is detected, the agent MUST:
1. Refuse the operation.
2. Report: `[skill:issue-tracking] SCOPE VIOLATION: <reason>`

## Dry-Run Behavior

When `CLAUDEOPS_DRY_RUN=true`:
- MUST NOT create, update, or comment on issues.
- MAY list and view existing issues (read-only).
- MUST still perform tool discovery and selection.
- Log: `[skill:issue-tracking] DRY RUN: Would create issue "<title>" on <owner>/<repo> using <tool>`
