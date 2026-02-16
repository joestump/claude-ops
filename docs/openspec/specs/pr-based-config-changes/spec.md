# SPEC-0018: PR-Based Configuration Changes with Pluggable Git Provider

## Overview

Claude Ops agents observe infrastructure and remediate issues, but currently cannot propose improvements to the operational procedures (checks, playbooks, manifests) that govern their behavior. This specification defines a PR-based workflow that allows agents to propose changes to configuration files through pull requests, ensuring all changes undergo human review before taking effect. The workflow supports multiple git hosting providers (GitHub and Gitea) through a pluggable provider interface.

This specification implements [ADR-0018: PR-Based Workflow for Runbook, Playbook, and Manifest Changes](/docs/adrs/ADR-0018-pr-based-config-changes.md).

## Definitions

- **Git Provider**: An implementation of the `GitProvider` interface for a specific git hosting platform (GitHub, Gitea, etc.) that handles branch creation, commits, and pull request operations via that platform's API.
- **Provider Registry**: A map of provider name to `GitProvider` implementation, initialized at startup from environment configuration. Used to look up the correct provider for a given repository.
- **RepoRef**: A struct identifying a repository by owner, name, and clone URL. Used by all provider interface methods to target operations at a specific repo.
- **Allowed Scope**: The set of file paths and patterns that the agent is permitted to propose changes to. Files outside this scope MUST NOT be modified by the agent.
- **Disallowed Scope**: The set of file paths and patterns that the agent is explicitly prohibited from modifying, even via PR. Includes prompt files, the runbook, entrypoint, inventory files, secrets, and infrastructure configuration.
- **Feature Branch**: A git branch created by the agent under the `claude-ops/` prefix for the purpose of submitting a PR. The agent MUST NOT push to any branch outside this namespace.
- **PR Body Context**: The structured information included in every PR description: what was observed, why the change is proposed, what the change does, and expected impact.

## Requirements

### SPEC-0018-REQ-1: GitProvider Interface

The system MUST define a `GitProvider` interface in Go with the following methods: `Name() string`, `CreateBranch(ctx, repo, branch, base)`, `CommitFiles(ctx, repo, branch, message, files)`, `CreatePR(ctx, repo, pr)`, `GetPRStatus(ctx, repo, prNumber)`, and `ListOpenPRs(ctx, repo, filter)`. All provider implementations MUST satisfy this interface. The interface MUST be defined in a dedicated package (`internal/gitprovider/`).

#### Scenario: Interface defines all required methods

- **WHEN** a new provider implementation is compiled
- **THEN** the Go compiler MUST enforce that it implements all six methods of the `GitProvider` interface

#### Scenario: Interface is importable from dedicated package

- **WHEN** the dashboard or agent code needs to interact with a git provider
- **THEN** it MUST import the `GitProvider` interface from `internal/gitprovider/`

### SPEC-0018-REQ-2: GitHub Provider Implementation

The system MUST include a GitHub provider implementation that uses the GitHub REST API for all operations. The provider MUST authenticate via a `GITHUB_TOKEN` environment variable. Branch creation MUST use the Git References API (`POST /repos/{owner}/{repo}/git/refs`). File commits MUST use the Contents API (`PUT /repos/{owner}/{repo}/contents/{path}`). PR creation MUST use the Pulls API (`POST /repos/{owner}/{repo}/pulls`). The provider MUST set the `claude-ops` and `automated` labels on created PRs when the repository has those labels available.

#### Scenario: Create PR on GitHub repository

- **WHEN** the agent calls `CreatePR` on the GitHub provider with a valid `PRRequest`
- **THEN** the provider MUST create a pull request via `POST /repos/{owner}/{repo}/pulls`
- **AND** the PR MUST target the specified base branch
- **AND** the response MUST include the PR number and URL

#### Scenario: GitHub token missing

- **WHEN** the GitHub provider is initialized without a `GITHUB_TOKEN` environment variable
- **THEN** the provider MUST return an error on any API call indicating that authentication is not configured

#### Scenario: Create branch on GitHub

- **WHEN** the agent calls `CreateBranch` with branch name `claude-ops/check/add-foo` and base `main`
- **THEN** the provider MUST create the branch via the Git References API
- **AND** the branch MUST be based on the current HEAD of the specified base branch

### SPEC-0018-REQ-3: Gitea Provider Implementation

The system MUST include a Gitea provider implementation that uses the Gitea REST API (v1) for all operations. The provider MUST authenticate via `GITEA_TOKEN` and connect to the instance specified by `GITEA_URL` environment variables. Branch creation MUST use the Gitea Branch API (`POST /api/v1/repos/{owner}/{repo}/branches`). File commits MUST use the Contents API (`POST /api/v1/repos/{owner}/{repo}/contents/{path}`). PR creation MUST use the Pulls API (`POST /api/v1/repos/{owner}/{repo}/pulls`).

#### Scenario: Create PR on Gitea repository

- **WHEN** the agent calls `CreatePR` on the Gitea provider with a valid `PRRequest`
- **THEN** the provider MUST create a pull request via `POST /api/v1/repos/{owner}/{repo}/pulls`
- **AND** the response MUST include the PR number and URL

#### Scenario: Gitea URL or token missing

- **WHEN** the Gitea provider is initialized without `GITEA_URL` or `GITEA_TOKEN`
- **THEN** the provider MUST return an error on any API call indicating that configuration is incomplete

#### Scenario: Gitea-specific response handling

- **WHEN** the Gitea API returns a response with Gitea-specific fields (e.g., different date formats, missing fields compared to GitHub)
- **THEN** the provider MUST handle these differences internally and return standard `PRResult`/`PRStatus` types

### SPEC-0018-REQ-4: Provider Registry and Discovery

The system MUST maintain a provider registry that maps provider names to `GitProvider` implementations. Providers MUST be registered at application startup. Repo-to-provider mapping MUST be resolved by one of two mechanisms in order of precedence: (1) explicit declaration in the repo's `CLAUDE-OPS.md` manifest under a `## Git Provider` section, or (2) inference from the git remote URL. URL-based inference MUST map `github.com` domains to the GitHub provider and configured Gitea domains to the Gitea provider. If no provider can be resolved for a repo, the system MUST skip PR creation for that repo and log a warning.

#### Scenario: Provider resolved from CLAUDE-OPS.md manifest

- **WHEN** a repo's `CLAUDE-OPS.md` contains a `## Git Provider` section declaring `provider: gitea`
- **THEN** the system MUST use the Gitea provider for that repo

#### Scenario: Provider inferred from remote URL

- **WHEN** a repo has no `## Git Provider` section in its manifest
- **AND** the repo's git remote URL contains `github.com`
- **THEN** the system MUST use the GitHub provider

#### Scenario: No provider available

- **WHEN** a repo's remote URL does not match any registered provider
- **AND** the repo has no explicit provider declaration
- **THEN** the system MUST NOT attempt to create a PR
- **AND** the system MUST log a warning identifying the repo and its remote URL

### SPEC-0018-REQ-5: Branch Naming Convention

All branches created by the agent MUST use the prefix `claude-ops/` followed by a type and short description: `claude-ops/{type}/{short-description}`. The type MUST be one of: `check`, `playbook`, `skill`, `manifest`, or `fix`. The short description MUST be kebab-case, lowercase, and no longer than 50 characters. The agent MUST NOT push to any branch that does not match this naming convention.

#### Scenario: Valid branch name for new check

- **WHEN** the agent proposes a new health check for service `jellyfin`
- **THEN** the branch name MUST be `claude-ops/check/add-jellyfin-health` or similar following the convention

#### Scenario: Branch name validation rejects invalid names

- **WHEN** the agent attempts to create a branch named `main` or `fix-something` (without the `claude-ops/` prefix)
- **THEN** the system MUST reject the operation and return an error

#### Scenario: Branch name length enforcement

- **WHEN** the agent generates a description longer than 50 characters
- **THEN** the system MUST truncate or reject the description to enforce the limit

### SPEC-0018-REQ-6: Allowed and Disallowed Change Scopes

The system MUST enforce allowed and disallowed file scopes for proposed changes. The agent MAY propose changes to: `checks/*.md`, `playbooks/*.md`, `.claude-ops/checks/*.md`, `.claude-ops/playbooks/*.md`, `.claude-ops/skills/*.md`, and `CLAUDE-OPS.md`. The agent MUST NOT propose changes to: `prompts/*.md`, `CLAUDE.md`, `entrypoint.sh`, Ansible inventory files (`ie.yaml`, `vms.yaml`), Docker Compose files, Dockerfiles, Helm charts, any file containing secrets or credentials, network configuration files (Caddy, WireGuard, DNS), or Go application source code. The scope check MUST be enforced before any git operation (branch creation, commit, push) is executed.

#### Scenario: Allowed change to check file

- **WHEN** the agent proposes a change to `checks/new-service.md`
- **THEN** the system MUST permit the change and proceed with the PR workflow

#### Scenario: Disallowed change to prompt file

- **WHEN** the agent proposes a change to `prompts/tier2-investigate.md`
- **THEN** the system MUST reject the change before any git operation occurs
- **AND** the system MUST log the rejection with the file path and reason

#### Scenario: Disallowed change to entrypoint

- **WHEN** the agent proposes a change to `entrypoint.sh`
- **THEN** the system MUST reject the change

#### Scenario: Allowed change to repo-specific playbook

- **WHEN** the agent proposes a change to `.claude-ops/playbooks/restart-postgres.md` in a mounted repo
- **THEN** the system MUST permit the change

### SPEC-0018-REQ-7: PR Body Context Requirements

Every PR created by the agent MUST include a structured body containing: (1) a summary of what was observed that triggered the change, (2) an explanation of why the change is needed, (3) a description of what the change does, and (4) the expected impact. The body MUST be formatted in markdown. The PR title MUST be concise (under 72 characters) and descriptive. All PRs MUST include the labels `claude-ops` and `automated` (if the repository supports labels).

#### Scenario: PR body includes observation context

- **WHEN** the agent creates a PR after detecting a missing health check
- **THEN** the PR body MUST include a section describing what monitoring cycle revealed the gap
- **AND** the body MUST explain why the proposed check addresses the gap

#### Scenario: PR title is concise

- **WHEN** the agent creates a PR
- **THEN** the PR title MUST be under 72 characters
- **AND** the title MUST describe the change (e.g., "Add health check for jellyfin service")

### SPEC-0018-REQ-8: Duplicate PR Prevention

Before creating a PR, the agent MUST check for existing open PRs with overlapping scope. The agent MUST call `ListOpenPRs` filtered by the `claude-ops` label. If an open PR already exists that modifies the same file(s), the agent MUST NOT create a duplicate PR. If a previously opened PR was closed without merging (rejected), the agent SHOULD NOT re-open the same change within 24 hours. The duplicate check MUST compare file paths, not PR titles.

#### Scenario: Duplicate PR prevented

- **WHEN** the agent proposes a change to `checks/http.md`
- **AND** an open PR from the agent already modifies `checks/http.md`
- **THEN** the agent MUST skip creating a new PR
- **AND** the system MUST log that a duplicate was prevented

#### Scenario: Previously rejected PR not re-submitted

- **WHEN** the agent proposes a change that matches a PR closed without merging within the last 24 hours
- **THEN** the agent SHOULD NOT create a new PR for the same change

#### Scenario: Different file allows new PR

- **WHEN** the agent proposes a change to `checks/dns.md`
- **AND** an existing open PR only modifies `checks/http.md`
- **THEN** the agent MUST proceed with creating the new PR

### SPEC-0018-REQ-9: Permission Tier Integration

PR creation MUST be restricted by the agent's current permission tier. Tier 1 (Haiku) agents MUST NOT create PRs; they MAY note in the escalation handoff that a PR might be warranted. Tier 2 (Sonnet) agents MAY create PRs for non-structural changes: new checks, threshold updates, documentation improvements, and manifest corrections. Tier 3 (Opus) agents MAY create PRs for structural changes: new playbooks, multi-file changes, and cross-repo changes. The tier restriction MUST be enforced at the provider interface level, not solely via prompt instructions.

#### Scenario: Tier 1 agent cannot create PR

- **WHEN** a Tier 1 agent attempts to call `CreatePR`
- **THEN** the system MUST reject the call and return an error indicating insufficient tier permissions

#### Scenario: Tier 2 agent creates check PR

- **WHEN** a Tier 2 agent proposes a new health check file
- **THEN** the system MUST permit the PR creation

#### Scenario: Tier 2 agent cannot create multi-file PR

- **WHEN** a Tier 2 agent attempts to create a PR modifying more than 3 files
- **THEN** the system SHOULD reject the PR and suggest escalation to Tier 3

#### Scenario: Tier 3 agent creates cross-repo PR

- **WHEN** a Tier 3 agent proposes changes spanning multiple repositories
- **THEN** the system MUST create separate PRs for each repository

### SPEC-0018-REQ-10: Agent MUST NOT Merge Own PRs

The agent MUST NOT merge, approve, or auto-merge any pull request it creates. The `GitProvider` interface MUST NOT expose a merge method. PR merging MUST be performed exclusively by human reviewers through the git hosting platform's UI. The agent MAY check PR status on subsequent monitoring cycles using `GetPRStatus` to track whether proposed changes have been accepted or rejected.

#### Scenario: No merge capability exposed

- **WHEN** a developer inspects the `GitProvider` interface
- **THEN** the interface MUST NOT include a `MergePR` or equivalent method

#### Scenario: Agent tracks PR status

- **WHEN** the agent runs a monitoring cycle after previously creating a PR
- **THEN** the agent MAY call `GetPRStatus` to check if the PR was merged, closed, or is still open

### SPEC-0018-REQ-11: Notification on PR Creation

When the agent successfully creates a PR, it MUST send a notification via Apprise (per SPEC-0004) containing the PR title, URL, target repository, and a brief summary of the proposed change. If `CLAUDEOPS_APPRISE_URLS` is not configured, the notification MUST be skipped silently.

#### Scenario: Notification sent on PR creation

- **WHEN** the agent creates a PR successfully
- **AND** `CLAUDEOPS_APPRISE_URLS` is configured
- **THEN** the agent MUST send an Apprise notification with the PR URL and title

#### Scenario: Notification skipped when Apprise not configured

- **WHEN** the agent creates a PR successfully
- **AND** `CLAUDEOPS_APPRISE_URLS` is empty or unset
- **THEN** the agent MUST NOT attempt to send a notification
- **AND** the agent MUST NOT log an error about missing Apprise configuration

### SPEC-0018-REQ-12: Dry Run Mode

When `CLAUDEOPS_DRY_RUN` is `true`, the PR workflow MUST log the proposed branch name, commit message, file changes, and PR body without executing any git operations. The dry run output MUST be detailed enough for an operator to understand exactly what the agent would have done. No branches MUST be created, no commits MUST be made, and no PRs MUST be opened in dry run mode.

#### Scenario: Dry run logs proposed changes

- **WHEN** `CLAUDEOPS_DRY_RUN` is `true`
- **AND** the agent identifies a change to propose
- **THEN** the system MUST log the full PR details (branch, files, title, body) to the results directory
- **AND** the system MUST NOT execute any git API calls

#### Scenario: Dry run does not create branches

- **WHEN** `CLAUDEOPS_DRY_RUN` is `true`
- **THEN** no calls to `CreateBranch`, `CommitFiles`, or `CreatePR` MUST be made on any provider

### SPEC-0018-REQ-13: Environment Variable Configuration

The GitHub provider MUST be configured via the `GITHUB_TOKEN` environment variable. The Gitea provider MUST be configured via `GITEA_URL` (the base URL of the Gitea instance) and `GITEA_TOKEN` environment variables. If a provider's required environment variables are not set, the provider MUST be registered in a disabled state and return descriptive errors when called. The system MUST NOT fail to start due to missing provider configuration -- providers with missing config are simply unavailable.

#### Scenario: Partial provider configuration

- **WHEN** `GITHUB_TOKEN` is set but `GITEA_URL` and `GITEA_TOKEN` are not
- **THEN** the GitHub provider MUST be available and functional
- **AND** the Gitea provider MUST be registered but return errors when called
- **AND** the system MUST start successfully

#### Scenario: No provider configuration

- **WHEN** neither `GITHUB_TOKEN` nor `GITEA_TOKEN` are set
- **THEN** the system MUST start successfully
- **AND** any attempt to create a PR MUST return an error indicating no providers are configured
