# SPEC-0026: CI Pipeline Failure Detection and Self-Correction

## Overview

Claude Ops monitors infrastructure services for runtime health but is currently blind to CI/CD pipeline failures on mounted repos. This specification defines how the agent detects CI failures, diagnoses their root cause, and — when the failure is mechanically fixable — proposes a correction via pull request using a git provider auto-discovered from the repo's remote URL.

See ADR-0026. This capability extends the PR-based workflow (SPEC-0018, ADR-0018) and the skills-based tool architecture (SPEC-0023, ADR-0022).

## Requirements

### Requirement: Provider Auto-Discovery

The agent MUST determine the git provider for each mounted repo by inspecting its remote URL at runtime. Provider configuration MUST NOT be required in `CLAUDE-OPS.md` or any repo-level file.

The agent MUST apply the following detection algorithm in order:

1. Run `git -C /repos/<name> remote get-url origin` to obtain the remote URL
2. Extract the hostname from the URL (handling both HTTPS and SSH formats)
3. Match against well-known hostnames: `github.com` → GitHub; `gitlab.com` → GitLab; `codeberg.org` → Forgejo
4. For unrecognized hostnames, probe `GET https://<host>/api/v1/version` (Gitea/Forgejo if JSON response) then `GET https://<host>/api/v4/version` (GitLab if JSON response)
5. If no probe succeeds, classify as unknown and fall back to git CLI operations

The detected provider and its token/tool mapping MUST be recorded in the session tool inventory and reused throughout the session without re-probing.

#### Scenario: Well-Known GitHub Host

- **WHEN** the remote URL hostname is `github.com`
- **THEN** the agent classifies the provider as GitHub, uses `GITHUB_TOKEN`, and prefers `mcp__github__*` MCP tools or `gh` CLI fallback

#### Scenario: Forgejo on Codeberg

- **WHEN** the remote URL hostname is `codeberg.org`
- **THEN** the agent classifies the provider as Forgejo, uses `CODEBERG_TOKEN`, and uses the Gitea-compatible API at `https://codeberg.org/api/v1`

#### Scenario: Self-Hosted Gitea

- **WHEN** the hostname is not in the well-known table and `GET https://<host>/api/v1/version` returns a JSON object
- **THEN** the agent classifies the provider as Gitea/Forgejo, sets `GITEA_URL=https://<host>`, and uses `GITEA_TOKEN`

#### Scenario: Unrecognized Provider

- **WHEN** no hostname match or API fingerprint succeeds
- **THEN** the agent logs `[ci-check] Skipped: <repo> — provider not detected` and skips all CI operations for that repo without raising an error

#### Scenario: Missing Token

- **WHEN** the provider is detected but the required token environment variable is unset or empty
- **THEN** the agent logs `[ci-check] Skipped: <repo> — token not configured for <provider>` and skips CI checks for that repo gracefully

---

### Requirement: CI Failure Detection in Tier 1

Tier 1 MUST include a CI status sweep as part of its observation pass for each mounted repo where a provider was successfully detected and a token is available.

The agent MUST query the most recent CI runs (minimum last 10) for each repo and record any runs that failed within the last 24 hours. If failures are found, they MUST be included in the observation summary and treated as service health failures that trigger the normal escalation path.

The agent MUST NOT query CI status for repos where provider detection or token configuration failed.

#### Scenario: Failing CI Run Detected

- **WHEN** a mounted repo has one or more failed CI runs within the last 24 hours
- **THEN** Tier 1 records the failure with the run ID, workflow name, failure timestamp, and repo name, and includes it in the failure summary passed to the escalation handoff

#### Scenario: No Recent Failures

- **WHEN** a mounted repo has no failed CI runs in the last 24 hours
- **THEN** Tier 1 records the repo's CI status as healthy and does not escalate on this basis

#### Scenario: CI Check Skip on Missing Provider

- **WHEN** provider detection or token lookup fails for a repo
- **THEN** Tier 1 omits CI status from that repo's health report and does not treat the skip as a failure

---

### Requirement: CI Job Log Retrieval

At Tier 3, when the handoff includes CI failures, the agent MUST retrieve the full job log output for each failed run before attempting diagnosis. Log retrieval MUST use the auto-discovered provider's API.

Provider-specific log endpoints:
- **GitHub**: `GET /repos/<owner>/<repo>/actions/runs/<run_id>/jobs` then download the log archive
- **Gitea/Forgejo**: `GET /api/v1/repos/<owner>/<repo>/actions/runs/<run_id>/jobs`
- **GitLab**: `GET /api/v4/projects/<id>/jobs/<job_id>/trace`

If log retrieval fails (API error, permissions, missing run), the agent MUST NOT attempt diagnosis and MUST treat the failure as non-fixable, notifying the human with the available context.

#### Scenario: Logs Retrieved Successfully

- **WHEN** the agent calls the job log endpoint and receives a non-empty response
- **THEN** the agent parses the log content for error patterns and proceeds to failure classification

#### Scenario: Log Retrieval Failure

- **WHEN** the log retrieval API call returns an error or empty response
- **THEN** the agent records `[ci-fix] Log retrieval failed for run <id>: <error>` and routes the failure to human notification without attempting a fix

---

### Requirement: Failure Classification

The agent MUST classify each CI failure as either fixable or non-fixable before any remediation attempt. Only fixable failures MAY result in a PR. The agent MUST NOT guess or attempt fixes for non-fixable failures.

**Fixable failure patterns** (agent MAY attempt a PR):
- YAML syntax errors (indentation errors, missing quotes, mapping value errors)
- Ansible-lint deprecation warnings with known module replacements (e.g., `apt` → `ansible.builtin.apt`)
- Missing `loop:` key for a task that references `item`
- Task file or role referenced by name but not found (and the file can be located or created)

**Non-fixable failure patterns** (agent MUST NOT attempt a PR; notify human):
- Test assertion failures
- Authentication or credential errors
- Network timeouts or transient infrastructure errors
- Ambiguous logic errors with multiple plausible root causes
- Errors where the fix would require modifying inventory files, secrets, or network configuration

If the failure pattern matches both a fixable and a non-fixable pattern, the agent MUST treat it as non-fixable.

#### Scenario: YAML Syntax Error

- **WHEN** the log contains a message matching `mapping values are not allowed`, `found character '\t'`, or equivalent YAML parse errors with a clear file and line reference
- **THEN** the agent classifies the failure as fixable and proceeds to read the referenced file

#### Scenario: Ansible Deprecation Warning

- **WHEN** the log contains `[DEPRECATION WARNING]` with a module name and a known replacement listed in the warning
- **THEN** the agent classifies the failure as fixable, extracts the old module name and replacement, and identifies the files to update

#### Scenario: Logic or Assertion Failure

- **WHEN** the log shows a test assertion failure, a conditional that evaluated unexpectedly, or an error without a clear syntactic cause
- **THEN** the agent classifies the failure as non-fixable and routes to human notification

#### Scenario: Ambiguous Error

- **WHEN** the error matches more than one fixable pattern, or when the fix would require understanding the intent of the playbook
- **THEN** the agent classifies the failure as non-fixable

---

### Requirement: Fix Proposal via Pull Request

When a failure is classified as fixable, the agent MUST propose the fix by opening a pull request against the affected repo using the `git-pr` skill. The agent MUST NOT push directly to any branch or merge any PR.

The PR MUST:
- Be created on a branch named `claude-ops/fix/<short-description>` (max 60 characters)
- Contain only the files required to fix the identified error — no reformatting, no refactoring, no collateral changes
- Include in the PR body: the CI run URL, the exact error message from logs, the change made, and the rationale
- Be created at Tier 3 only — Tier 1 and Tier 2 MUST NOT create code-fix PRs

The agent MUST use the `git-pr` skill's scope rules, except that `checks/*.md` and `playbooks/*.md` within mounted repos ARE permitted for code-fix PRs at Tier 3. Inventory files, secrets, and network configuration remain on the Never Allowed list.

#### Scenario: Successful Fix PR

- **WHEN** the agent identifies a fixable error, reads the source file, constructs a targeted fix, and successfully opens a PR via the `git-pr` skill
- **THEN** the agent records the PR number and URL, sends an Apprise notification containing the PR URL, and cleans up the temporary clone directory

#### Scenario: Scope Violation During Fix

- **WHEN** the proposed fix would require modifying an inventory file, secrets file, or network configuration file
- **THEN** the agent MUST refuse the fix, log `[scope-violation] Refused: fix would modify <path>`, and route to human notification instead

#### Scenario: Git Clone Failure

- **WHEN** the agent cannot clone the repo to `/tmp/ci-fix-<repo>-<timestamp>` due to authentication or network errors
- **THEN** the agent MUST NOT proceed with the PR and MUST notify the human with the error details

---

### Requirement: Duplicate PR Prevention

Before creating any CI fix PR, the agent MUST check whether an open PR already exists on the same branch or addressing the same failure. If a duplicate is found, the agent MUST NOT create a new PR.

#### Scenario: Duplicate Found

- **WHEN** the agent queries open PRs and finds one with a branch matching `claude-ops/fix/<description>` or with the same CI run referenced in its body
- **THEN** the agent logs `[ci-fix] Skipped: duplicate PR #<number> already open` and sends a notification referencing the existing PR instead of creating a new one

#### Scenario: No Duplicate

- **WHEN** no open PR matches the proposed branch name or CI run reference
- **THEN** the agent proceeds with PR creation

---

### Requirement: Human Notification for Non-Fixable Failures

When a CI failure is classified as non-fixable, log retrieval fails, or any step in the fix process fails, the agent MUST send an Apprise notification. The notification MUST include:
- The repo name and CI run URL
- The exact failure message from logs (or the reason logs could not be retrieved)
- The classification decision and why no automated fix was attempted
- Recommended first steps for human investigation

The agent MUST NOT silently skip non-fixable failures.

#### Scenario: Non-Fixable Failure Notification

- **WHEN** a CI failure is classified as non-fixable
- **THEN** the agent sends an Apprise notification with the run URL, the error excerpt, and a "Recommended next steps" section

#### Scenario: Fix Attempt Failure Notification

- **WHEN** any step in the fix process fails (clone error, PR API error, scope violation)
- **THEN** the agent sends an Apprise notification explaining what was attempted, what failed, and what the human should do next

---

### Requirement: Cooldown State Integration

The agent MUST record CI fix PR attempts in the cooldown state. The agent MUST NOT open more than one CI fix PR per repo per 24-hour window, independent of the number of failing runs.

#### Scenario: Cooldown Active

- **WHEN** the cooldown state shows a CI fix PR was already attempted for a repo within the last 24 hours
- **THEN** the agent skips the PR creation step, logs the cooldown state, and sends a human notification indicating manual review is needed

#### Scenario: Cooldown Reset

- **WHEN** a CI fix PR is merged and subsequent CI runs on the repo pass
- **THEN** Tier 1 resets the CI fix cooldown counter for that repo on the next successful health check

---

### Requirement: Dry-Run Mode

When `CLAUDEOPS_DRY_RUN=true`, the agent MUST NOT create branches, commits, or pull requests. The agent MAY still perform all read operations (CI status queries, log retrieval, failure classification).

#### Scenario: Dry Run Fix Proposal

- **WHEN** `CLAUDEOPS_DRY_RUN=true` and the agent classifies a failure as fixable
- **THEN** the agent logs `[dry-run] Would: open PR "<title>" on <owner>/<repo> fixing <error>` and does not proceed with clone or PR creation

---

### Requirement: Never-Allowed Boundary Clarification

The Never Allowed list MUST be updated in the agent runbook and all tier prompts to distinguish between two types of file modifications:

1. **Direct file edits on running hosts** — modifying files via SSH, Ansible, or any runtime mechanism on a deployed system. This REMAINS never allowed at any tier.
2. **PR-based code proposals** — opening a pull request that proposes changes to files in a mounted repo. This IS allowed at Tier 3 for non-inventory, non-secret, non-network files.

The updated prohibition MUST read: "Directly modify inventory files, playbooks, Helm charts, or Dockerfiles on running hosts" rather than "Modify inventory files, playbooks, Helm charts, or Dockerfiles."

The `git-pr.md` skill's scope rules MUST be updated to remove `checks/*.md` and `playbooks/*.md` from the denied path list, as these are now permitted for PR-based code fixes at Tier 3.

#### Scenario: Playbook Fix PR Permitted

- **WHEN** the agent identifies a fixable error in an Ansible playbook in a mounted repo and proposes a fix via PR
- **THEN** the `git-pr` skill permits the PR without a scope violation, since the change is PR-based and the file is not an inventory file, secret, or network configuration

#### Scenario: Direct Edit Still Blocked

- **WHEN** the agent attempts to write to any file under `/repos/<name>/` directly (not via PR)
- **THEN** the operation is refused because mounted repos are read-only volumes — this constraint is enforced at the filesystem level and is unchanged by this spec
