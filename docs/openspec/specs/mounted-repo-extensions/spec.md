# SPEC-0005: Mounted Repo Extension Model

## Overview

Claude Ops monitors infrastructure health by running checks and remediating issues autonomously inside a Docker container. The infrastructure it monitors is defined across multiple repositories owned by different teams -- Ansible inventories, Docker image repos, Helm chart repos, and others. These repos are mounted as Docker volumes under a configurable parent directory (default: `/repos/`).

This specification defines a convention-based extension model that allows mounted repositories to extend the agent's behavior at runtime. Repos can declare their identity and rules via a manifest file (`CLAUDE-OPS.md`), provide custom health checks, remediation playbooks, operational skills, and MCP server configurations through a well-known directory structure (`.claude-ops/`). The agent discovers these extensions by scanning the repos directory at the start of each monitoring cycle, requiring zero configuration changes to the Claude Ops platform itself.

## Definitions

- **Mounted repo**: An infrastructure repository mounted as a Docker volume subdirectory under `$CLAUDEOPS_REPOS_DIR` (default: `/repos/`).
- **Manifest**: A `CLAUDE-OPS.md` markdown file at a repo's root that declares the repo's identity, capabilities, and rules for the agent to follow.
- **Extension directory**: A `.claude-ops/` directory at a repo's root containing custom checks, playbooks, skills, and MCP configurations.
- **Check**: A markdown file describing a health check procedure that the agent reads and executes by running the described commands.
- **Playbook**: A markdown file describing a remediation procedure for a specific failure scenario.
- **Skill**: A markdown file describing a freeform operational capability (maintenance, reporting, cleanup).
- **MCP config**: A JSON file (`.claude-ops/mcp.json`) defining additional MCP servers to merge into the agent's baseline configuration.
- **Monitoring cycle**: One complete execution of the agent's observe-investigate-remediate loop, initiated by `entrypoint.sh`.
- **Tier**: A permission level (1=observe, 2=safe remediation, 3=full remediation) that constrains what actions the agent may take.

## Requirements

### REQ-1: Repo Discovery via Directory Scanning

The system MUST discover mounted repos by scanning all immediate subdirectories of `$CLAUDEOPS_REPOS_DIR` at the start of each monitoring cycle. The scan MUST be performed every cycle so that newly mounted or removed repos are detected without requiring a container restart.

#### Scenario: Discover repos at startup
Given the agent starts a new monitoring cycle
And `$CLAUDEOPS_REPOS_DIR` is set to `/repos`
And `/repos/` contains subdirectories `infra-ansible/`, `docker-images/`, and `helm-charts/`
When the agent scans for mounted repos
Then it identifies three repos: `infra-ansible`, `docker-images`, and `helm-charts`

#### Scenario: New repo detected on subsequent cycle
Given the agent completed a monitoring cycle with repos `infra-ansible/` and `docker-images/`
And a new volume `helm-charts/` is mounted under `/repos/` before the next cycle
When the next monitoring cycle begins
Then the agent discovers and processes all three repos including the newly added `helm-charts/`

#### Scenario: Custom repos directory
Given `$CLAUDEOPS_REPOS_DIR` is set to `/my/custom/repos`
And `/my/custom/repos/` contains subdirectory `my-infra/`
When the agent scans for mounted repos
Then it discovers the repo at `/my/custom/repos/my-infra/`

### REQ-2: Manifest Discovery and Reading

The system MUST look for a `CLAUDE-OPS.md` file at the root of each discovered repo. If found, the agent MUST read the manifest and incorporate its declared capabilities and rules into the current monitoring cycle's context.

#### Scenario: Repo with CLAUDE-OPS.md manifest
Given a repo is mounted at `/repos/infra-ansible`
And it contains a `CLAUDE-OPS.md` at the root
When the agent scans for repo extensions
Then it reads the manifest and incorporates the repo's declared capabilities and rules

#### Scenario: Repo without CLAUDE-OPS.md manifest
Given a repo is mounted at `/repos/docker-images`
And it does not contain a `CLAUDE-OPS.md` file
When the agent scans for repo extensions
Then it proceeds to check for a `.claude-ops/` directory and falls back to file inference (REQ-8)

### REQ-3: Manifest Content Structure

The manifest (`CLAUDE-OPS.md`) SHOULD include the following sections to describe the repo to the agent:

- **Kind**: The type of infrastructure repo (e.g., "Ansible infrastructure", "Docker images", "Helm charts").
- **Capabilities**: What the repo provides to the agent (e.g., `service-discovery`, `redeployment`, `image-inspection`), including which tiers are required.
- **Rules**: Constraints the agent MUST follow when interacting with this repo (e.g., "never modify files", "playbooks require Tier 3", "always use `--limit`").

The agent MUST respect all rules declared in a manifest throughout the monitoring cycle across all tiers.

#### Scenario: Manifest declares tier-restricted capabilities
Given a repo's `CLAUDE-OPS.md` declares the capability `redeployment` with the note "Only Opus (Tier 3) should run playbooks"
When a Tier 2 agent encounters a failure that could be resolved by redeployment
Then the Tier 2 agent MUST NOT execute redeployment playbooks from this repo and MUST escalate to Tier 3

#### Scenario: Manifest declares operational rules
Given a repo's `CLAUDE-OPS.md` includes the rule "Always use `--limit` when running playbooks to target specific hosts"
When a Tier 3 agent runs a playbook from this repo
Then the agent includes the `--limit` flag targeting specific hosts

#### Scenario: Manifest declares read-only rule
Given a repo's `CLAUDE-OPS.md` includes the rule "Never modify any files in this repo"
When the agent processes this repo at any tier
Then the agent MUST NOT write to, delete, or modify any files within the repo's directory tree

### REQ-4: Extension Directory Discovery

The system MUST check for a `.claude-ops/` directory at the root of each discovered repo. If present, the system MUST scan its contents for the following subdirectories: `checks/`, `playbooks/`, `skills/`, and an `mcp.json` file.

#### Scenario: Repo with full extension directory
Given a repo at `/repos/infra-ansible` contains a `.claude-ops/` directory
And the directory contains `checks/`, `playbooks/`, `skills/`, and `mcp.json`
When the agent scans for extensions
Then it discovers and processes all four extension types

#### Scenario: Repo with partial extension directory
Given a repo at `/repos/docker-images` contains a `.claude-ops/` directory
And the directory contains only `checks/`
When the agent scans for extensions
Then it discovers the custom checks and does not error on missing `playbooks/`, `skills/`, or `mcp.json`

#### Scenario: Repo without extension directory
Given a repo at `/repos/simple-repo` does not contain a `.claude-ops/` directory
When the agent scans for extensions
Then it skips extension scanning for this repo without error

### REQ-5: Custom Health Checks

Custom health check files placed in `.claude-ops/checks/` MUST be discovered and executed alongside the built-in health checks during Tier 1 observation. Each check file MUST be a markdown document describing what to check and how. The agent reads the check description and executes the appropriate commands.

Custom checks from all mounted repos MUST be combined into a single health check routine. If two repos define checks for the same service or concern, both MUST run.

#### Scenario: Custom check discovered and executed
Given a repo at `/repos/infra-ansible` has `.claude-ops/checks/verify-backups.md`
And the file describes checking for backup freshness in `/data/backups/`
When the Tier 1 agent runs health checks
Then it executes the backup freshness check described in the file alongside the built-in checks

#### Scenario: Multiple repos contribute checks
Given repo `infra-ansible` has `.claude-ops/checks/verify-backups.md`
And repo `docker-images` has `.claude-ops/checks/check-base-images.md`
When the Tier 1 agent runs health checks
Then both custom checks run alongside built-in checks from `checks/`

#### Scenario: Custom check format
Given a check file at `.claude-ops/checks/verify-backups.md`
Then the file SHOULD contain: a title, a description of what to check and how, criteria for healthy state, and criteria for unhealthy state

### REQ-6: Custom Playbooks

Playbook files placed in `.claude-ops/playbooks/` MUST be discovered and made available for remediation. Custom playbooks MUST follow the same tier permission model as built-in playbooks: a playbook that requires actions beyond the current tier's permissions MUST NOT be executed and MUST trigger escalation.

#### Scenario: Custom playbook available to correct tier
Given a repo has `.claude-ops/playbooks/fix-media-perms.md` that describes fixing file ownership (a Tier 2 action)
When a Tier 2 agent encounters a media permissions issue
Then the agent MAY use the custom playbook to remediate

#### Scenario: Custom playbook requires higher tier
Given a repo has `.claude-ops/playbooks/redeploy-service.md` that describes running Ansible playbooks (a Tier 3 action)
When a Tier 2 agent encounters a failure that matches this playbook
Then the Tier 2 agent MUST NOT execute the playbook and MUST escalate to Tier 3

#### Scenario: Custom playbook supplements built-in playbooks
Given the built-in playbooks include `restart-container.md`
And a repo provides `.claude-ops/playbooks/restart-with-custom-config.md` for its specific service
When the agent needs to remediate a failure for that service
Then both the built-in and custom playbooks are available for the agent to choose from

### REQ-7: Custom Skills

Skill files placed in `.claude-ops/skills/` MUST be discovered and made available to the agent. Skills are freeform capabilities (maintenance tasks, reporting, cleanup operations) and MUST follow the same tier permission model as checks and playbooks.

#### Scenario: Custom skill discovered
Given a repo has `.claude-ops/skills/prune-old-logs.md` describing a log cleanup procedure
When the agent scans for extensions
Then the skill is available for use when appropriate during the monitoring cycle

#### Scenario: Skill respects tier permissions
Given a custom skill describes an operation requiring `docker restart` (a Tier 2 action)
When a Tier 1 agent processes this skill
Then the Tier 1 agent MUST NOT execute the skill

### REQ-8: Fallback Discovery for Repos Without Extensions

If a mounted repo has neither a `CLAUDE-OPS.md` manifest nor a `.claude-ops/` directory, the agent SHOULD attempt to infer the repo's purpose by reading top-level files such as `README.md`, examining the directory structure, and inspecting configuration files.

#### Scenario: Repo without any Claude Ops files
Given a repo at `/repos/legacy-scripts` has no `CLAUDE-OPS.md` or `.claude-ops/` directory
And it contains a `README.md` and several shell scripts
When the agent scans this repo
Then it reads the `README.md` and directory listing to infer the repo's purpose and notes what it found

#### Scenario: Graceful degradation
Given a repo cannot be fully understood through file inference
When the agent completes scanning
Then it records that the repo was discovered but has limited operational context, without treating this as an error

### REQ-9: MCP Configuration Merging

Repos MAY provide additional MCP server definitions in `.claude-ops/mcp.json`. The entrypoint script MUST merge these into the baseline MCP configuration (`/app/.claude/mcp.json`) before each monitoring cycle. The merge MUST use additive semantics: repo-defined MCP servers are added to the baseline set.

If a repo defines an MCP server with the same name as a baseline server, the repo version MUST override the baseline version.

If multiple repos define an MCP server with the same name, the merge order MUST be deterministic. Repos MUST be processed in alphabetical order by directory name, with later repos overriding earlier ones on name collision.

#### Scenario: Repo adds a new MCP server
Given the baseline config defines servers `docker`, `postgres`, `chrome-devtools`, and `fetch`
And repo `infra-ansible` provides `.claude-ops/mcp.json` defining server `ansible-inventory`
When the entrypoint merges MCP configs
Then the merged config contains all five servers: `docker`, `postgres`, `chrome-devtools`, `fetch`, and `ansible-inventory`

#### Scenario: Repo overrides a baseline MCP server
Given the baseline config defines a `postgres` server with `POSTGRES_CONNECTION` set to a default
And repo `prod-db` provides `.claude-ops/mcp.json` redefining `postgres` with a different connection string
When the entrypoint merges MCP configs
Then the merged config uses the `postgres` definition from `prod-db`, not the baseline

#### Scenario: Multiple repos with name collision
Given repo `alpha-infra` defines MCP server `custom-monitor`
And repo `beta-infra` also defines MCP server `custom-monitor` with different settings
When the entrypoint merges configs in alphabetical order
Then `beta-infra`'s definition of `custom-monitor` wins because it is processed after `alpha-infra`

#### Scenario: Baseline preserved on first run
Given the baseline MCP config exists at `/app/.claude/mcp.json`
When the entrypoint runs for the first time
Then it saves a copy of the baseline to `/app/.claude/mcp.json.baseline` before merging

#### Scenario: Baseline restored each cycle
Given a baseline backup exists at `/app/.claude/mcp.json.baseline`
When a new monitoring cycle begins
Then the entrypoint restores the baseline before re-merging repo configs, ensuring removed repos or changed configs take effect

### REQ-10: Extension Tier Permission Enforcement

All repo-provided extensions (checks, playbooks, skills) MUST operate within the same permission tier model as built-in extensions. The agent MUST NOT execute an operation from a repo extension that exceeds the current tier's permissions, regardless of what the extension instructs.

#### Scenario: Repo extension attempts privilege escalation
Given a repo provides a check file that instructs the agent to run `docker restart` (a Tier 2 action)
When a Tier 1 agent reads this check
Then the Tier 1 agent MUST NOT execute the restart command and MUST note the issue for escalation

#### Scenario: Repo extension within tier permissions
Given a repo provides a check file that instructs the agent to run `curl` to check an HTTP endpoint
When a Tier 1 agent reads this check
Then the Tier 1 agent executes the curl command, as HTTP checks are within Tier 1 permissions

### REQ-11: Read-Only Mount Convention

Repos SHOULD be mounted with the `:ro` (read-only) Docker volume flag by default. The system MUST NOT require write access to mounted repos under normal operation. If a repo requires write access, this MUST be explicitly documented in the repo's `CLAUDE-OPS.md` manifest.

#### Scenario: Standard read-only mount
Given a repo is mounted with the `:ro` flag
When the agent processes the repo
Then all operations complete successfully without requiring write access to the repo

#### Scenario: Write access explicitly documented
Given a repo is mounted with `:rw`
And its `CLAUDE-OPS.md` documents the reason write access is needed
When the agent processes the repo
Then the agent may use write operations only as described in the manifest

### REQ-12: Unified Repo Map

After scanning all repos, the agent MUST build a unified map of discovered repos, their capabilities, extensions, and rules. This map MUST be used throughout the monitoring cycle to inform health check execution, remediation decisions, and escalation context.

#### Scenario: Unified map constructed
Given repos `infra-ansible` (capabilities: service-discovery, redeployment), `docker-images` (capabilities: image-inspection), and `helm-charts` (capabilities: redeployment) are mounted
When the agent completes repo scanning
Then the unified map contains all three repos with their respective capabilities, custom checks, playbooks, skills, and rules

#### Scenario: Map used during remediation
Given the unified map indicates that `infra-ansible` provides redeployment capability restricted to Tier 3
When a Tier 3 agent needs to redeploy a service
Then it consults the map to locate the correct playbook and follows the repo's declared rules

### REQ-13: Extension Composability

Extensions from multiple repos MUST compose without conflict. Two repos defining checks for different services MUST both be executed. The system MUST NOT require coordination between repos except for MCP server naming (REQ-9).

#### Scenario: Independent checks from multiple repos
Given repo `infra-ansible` provides a check for backup freshness
And repo `docker-images` provides a check for base image staleness
When the agent runs health checks
Then both checks execute independently without conflict

#### Scenario: Multiple repos provide playbooks for different services
Given repo `web-app` provides a playbook for restarting the web service
And repo `database` provides a playbook for PostgreSQL recovery
When failures are detected in both services
Then the appropriate playbook from each repo is available for the corresponding failure

## References

- [ADR-0005: Allow Mounted Repos to Extend the Agent via Convention-Based Discovery](../../adrs/ADR-0005-mounted-repo-extension-model.md)
- [Repo Mounting Guide](../../repo-mounting.md)
- [ADR-0006: Use MCP Servers as Primary Infrastructure Access Layer](../../adrs/ADR-0006-mcp-infrastructure-bridge.md) (for MCP config merging details)
