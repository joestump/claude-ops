# Skill: CI Pipeline Monitor

<!-- Governing: SPEC-0026 REQ "Provider Auto-Discovery", REQ "CI Failure Detection in Tier 1", REQ "CI Job Log Retrieval", REQ "Dry-Run Mode" -->

## Purpose

Detect CI pipeline failures on mounted repos and retrieve job logs for diagnosis. Use this skill during the Tier 1 observation pass to discover which git provider hosts each repo, query CI run status, and record failures for escalation. Tier 3 uses the log retrieval section to fetch full job output before attempting fixes.

## Tier Requirement

Tier 1 minimum for CI failure detection (read-only status queries). Tier 3 required for CI job log retrieval (may involve large log downloads and precedes fix attempts).

## Provider Auto-Discovery

For each mounted repo under `/repos/`, determine the git hosting provider at runtime. Provider configuration MUST NOT be required in `CLAUDE-OPS.md` or any repo-level file.

### Step 1: Get Remote URL

```bash
git -C /repos/<name> remote get-url origin
```

If the command fails (no git repo, no remote), skip this repo:
`[ci-monitor] Skipped: <name> — no git remote configured`

### Step 2: Extract Hostname

Parse the remote URL to extract the hostname. Handle both formats:

- **HTTPS**: `https://github.com/owner/repo.git` -> hostname: `github.com`
- **SSH**: `git@github.com:owner/repo.git` -> hostname: `github.com`
- **SSH with port**: `ssh://git@gitlab.example.com:2222/owner/repo.git` -> hostname: `gitlab.example.com`

Also extract the `<owner>/<repo>` path (strip `.git` suffix if present).

### Step 3: Match Well-Known Hosts

| Hostname | Provider | Token Env Var | MCP Tools | CLI Fallback | HTTP API Base |
|----------|----------|---------------|-----------|--------------|---------------|
| `github.com` | GitHub | `$GITHUB_TOKEN` | `mcp__github__*` | `gh` | `https://api.github.com` |
| `gitlab.com` | GitLab | `$GITLAB_TOKEN` | — | `glab` | `https://gitlab.com/api/v4` |
| `codeberg.org` | Forgejo | `$CODEBERG_TOKEN` | `mcp__gitea__*` (compatible) | `tea` | `https://codeberg.org/api/v1` |

If the hostname matches a well-known host, use the corresponding provider configuration. Skip to Step 5.

### Step 4: Fingerprint Unknown Hosts

For hostnames not in the well-known table, probe API version endpoints to identify the provider:

1. **Gitea/Forgejo probe**: `GET https://<host>/api/v1/version`
   - If the response is a JSON object (e.g., `{"version": "1.22.1"}`), classify as **Gitea/Forgejo**
   - Set: token `$GITEA_TOKEN`, MCP `mcp__gitea__*`, CLI `tea`, HTTP base `https://<host>/api/v1`

2. **GitLab probe**: `GET https://<host>/api/v4/version`
   - If the response is a JSON object (e.g., `{"version": "16.0"}`), classify as **GitLab (self-hosted)**
   - Set: token `$GITLAB_TOKEN`, CLI `glab`, HTTP base `https://<host>/api/v4`

3. **No match**: If neither probe returns a valid JSON response, classify as **unknown**:
   `[ci-monitor] Skipped: <repo> — provider not detected`
   Skip all CI operations for this repo without raising an error.

### Step 5: Verify Token

Check that the required token environment variable is set and non-empty:

```bash
echo "${GITHUB_TOKEN:+set}"   # for GitHub
echo "${GITLAB_TOKEN:+set}"   # for GitLab
echo "${GITEA_TOKEN:+set}"    # for Gitea/Forgejo
echo "${CODEBERG_TOKEN:+set}" # for Codeberg
```

If the token is unset or empty:
`[ci-monitor] Skipped: <repo> — token not configured for <provider>`

Skip CI checks for this repo gracefully. This is not an error.

### Step 6: Record in Tool Inventory

Record the detected provider in the session tool inventory:
`ci-provider: <name> (<host>)`

Example: `ci-provider: home-cluster (gitea.stump.wtf)`

This mapping is reused for all subsequent CI operations this session. Do NOT re-probe on each query.

## Tool Discovery

This skill uses the following tools in preference order per provider:

### GitHub
1. **MCP**: `mcp__github__list_repo_action_runs`, `mcp__github__get_repo_action_run` — check if available in tool listing
2. **CLI**: `gh` — check with `which gh`
3. **HTTP**: `curl` to `https://api.github.com` — universal fallback; requires `$GITHUB_TOKEN`

### Gitea/Forgejo (including Codeberg)
1. **MCP**: `mcp__gitea__list_repo_action_runs`, `mcp__gitea__list_repo_action_run_jobs` — check if available in tool listing
2. **CLI**: `tea` — check with `which tea`
3. **HTTP**: `curl` to `https://<host>/api/v1` — universal fallback; requires `$GITEA_TOKEN` or `$CODEBERG_TOKEN`

### GitLab
1. **CLI**: `glab` — check with `which glab`
2. **HTTP**: `curl` to `https://<host>/api/v4` — universal fallback; requires `$GITLAB_TOKEN`

Log tool selection: `[ci-monitor] Using: <tool> (<type>)`

## CI Failure Detection (Tier 1 — Read Only)

Query the last 10 CI runs filtered to failed status for each repo where a provider and token are available.

### GitHub

#### Using MCP: mcp__github__list_repo_action_runs

1. Call with owner, repo, and filter for failed status.
2. Log: `[ci-monitor] Using: mcp__github__list_repo_action_runs (MCP)`

#### Using CLI: gh

```bash
gh api "repos/<owner>/<repo>/actions/runs?status=failure&per_page=10" \
  --header "Authorization: token $GITHUB_TOKEN"
```

Log: `[ci-monitor] Using: gh (CLI)`

#### Using HTTP: curl

```bash
curl -s -H "Authorization: token $GITHUB_TOKEN" \
  "https://api.github.com/repos/<owner>/<repo>/actions/runs?status=failure&per_page=10"
```

Log: `[ci-monitor] Using: curl (HTTP)`

### Gitea/Forgejo

#### Using MCP: mcp__gitea__list_repo_action_runs

1. Call with owner, repo, and status filter for failure.
2. Log: `[ci-monitor] Using: mcp__gitea__list_repo_action_runs (MCP)`

#### Using CLI: tea

Note: `tea` may not support CI run listing. Fall through to HTTP if unavailable.

#### Using HTTP: curl

```bash
curl -s -H "Authorization: token $GITEA_TOKEN" \
  "https://<host>/api/v1/repos/<owner>/<repo>/actions/runs?status=failure&limit=10"
```

Log: `[ci-monitor] Using: curl (HTTP)`

For Codeberg, use `$CODEBERG_TOKEN` and `https://codeberg.org/api/v1`.

### GitLab

#### Using CLI: glab

```bash
glab api "projects/<url-encoded-path>/pipelines?status=failed&per_page=10"
```

Log: `[ci-monitor] Using: glab (CLI)`

#### Using HTTP: curl

```bash
curl -s -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<url-encoded-path>/pipelines?status=failed&per_page=10"
```

Log: `[ci-monitor] Using: curl (HTTP)`

### Recording Failures

From the response, extract runs that failed within the last 24 hours. For each, record:

```json
{
  "repo": "<repo-name>",
  "run_id": "<run-id>",
  "workflow": "<workflow-name>",
  "failed_at": "<ISO 8601 timestamp>",
  "provider": "<github|gitlab|gitea|forgejo>"
}
```

If no failures are found within the last 24 hours, the repo's CI status is healthy — do not escalate on this basis.

## CI Job Log Retrieval (Tier 3 Only)

When Tier 3 receives a handoff containing `ci_failures`, retrieve full job log output for each failed run before attempting diagnosis.

### GitHub

#### Using MCP: mcp__github__*

1. List jobs: call the appropriate MCP tool for listing run jobs.
2. Download logs for each failed job.
3. Log: `[ci-monitor] Using: mcp__github (MCP) for log retrieval`

#### Using CLI: gh

```bash
gh api "repos/<owner>/<repo>/actions/runs/<run_id>/jobs" \
  --header "Authorization: token $GITHUB_TOKEN"
# Then download log for each failed job:
gh api "repos/<owner>/<repo>/actions/jobs/<job_id>/logs" \
  --header "Authorization: token $GITHUB_TOKEN"
```

Log: `[ci-monitor] Using: gh (CLI) for log retrieval`

#### Using HTTP: curl

```bash
# List jobs
curl -s -H "Authorization: token $GITHUB_TOKEN" \
  "https://api.github.com/repos/<owner>/<repo>/actions/runs/<run_id>/jobs"
# Download logs
curl -s -L -H "Authorization: token $GITHUB_TOKEN" \
  "https://api.github.com/repos/<owner>/<repo>/actions/jobs/<job_id>/logs"
```

Log: `[ci-monitor] Using: curl (HTTP) for log retrieval`

### Gitea/Forgejo

#### Using MCP: mcp__gitea__list_repo_action_run_jobs

1. Call with owner, repo, and run ID to list jobs.
2. Use `mcp__gitea__get_repo_action_job_log_preview` or `mcp__gitea__download_repo_action_job_log` for log content.
3. Log: `[ci-monitor] Using: mcp__gitea (MCP) for log retrieval`

#### Using HTTP: curl

```bash
# List jobs for a run
curl -s -H "Authorization: token $GITEA_TOKEN" \
  "https://<host>/api/v1/repos/<owner>/<repo>/actions/runs/<run_id>/jobs"
# Get job log
curl -s -H "Authorization: token $GITEA_TOKEN" \
  "https://<host>/api/v1/repos/<owner>/<repo>/actions/runs/<run_id>/jobs/<job_id>/logs"
```

Log: `[ci-monitor] Using: curl (HTTP) for log retrieval`

### GitLab

#### Using CLI: glab

```bash
glab api "projects/<url-encoded-path>/pipelines/<pipeline_id>/jobs"
glab api "projects/<url-encoded-path>/jobs/<job_id>/trace"
```

Log: `[ci-monitor] Using: glab (CLI) for log retrieval`

#### Using HTTP: curl

```bash
# List jobs for a pipeline
curl -s -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<url-encoded-path>/pipelines/<pipeline_id>/jobs"
# Get job trace (log)
curl -s -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<url-encoded-path>/jobs/<job_id>/trace"
```

Log: `[ci-monitor] Using: curl (HTTP) for log retrieval`

### Log Retrieval Failure

If log retrieval fails (API error, permissions, empty response):

```
[ci-monitor] Log retrieval failed for run <id>: <error>
```

The agent MUST NOT attempt diagnosis without logs. Route the failure to human notification with the available context (run ID, workflow name, failure timestamp).

## Scope Rules

This skill is **read-only**. It MUST NOT:
- Modify any files in repos or on hosts
- Create branches, commits, or pull requests (that is the `git-pr` skill's responsibility at Tier 3)
- Restart, stop, or start any CI runs or pipelines
- Delete or cancel any CI runs

If a mutating CI operation is attempted, refuse and report:
`[ci-monitor] SCOPE VIOLATION: <action> is not permitted — ci-monitor is read-only`

## Dry-Run Behavior

When `CLAUDEOPS_DRY_RUN=true`:
- MAY still perform CI status queries (read-only) — these do not modify state
- MAY still perform provider auto-discovery (read-only)
- MAY still retrieve CI job logs (read-only)
- MUST NOT create any branches, commits, or PRs (enforced by git-pr skill, not this skill)
- Log: `[ci-monitor] DRY RUN: CI status queries are read-only and permitted in dry-run mode`

## Validation

After querying CI status:
1. Confirm the API response is valid JSON (or the CLI returned exit code 0).
2. Verify the response contains run entries (may be an empty list if no failures).
3. Filter to failures within the last 24 hours.
4. Record failures in the format specified in "Recording Failures" above.

After retrieving logs:
1. Confirm the response is non-empty.
2. If empty or error, follow the "Log Retrieval Failure" procedure.
