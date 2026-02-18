---
sidebar_position: 5
sidebar_label: Mounted Repo Extensions
---

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

- [ADR-0005: Allow Mounted Repos to Extend the Agent via Convention-Based Discovery](../adrs/adr-0005)
- [Repo Mounting Guide](../usage/repo-mounting)
- [ADR-0006: Use MCP Servers as Primary Infrastructure Access Layer](../adrs/adr-0006) (for MCP config merging details)

---

# Design: Mounted Repo Extension Model

## Overview

The mounted repo extension model enables infrastructure teams to extend Claude Ops' monitoring and remediation capabilities by placing files in well-known paths within their repositories. Repos are mounted as Docker volumes under a parent directory, and the agent discovers extensions through convention-based directory scanning at the start of each monitoring cycle.

This design leverages the agent's native capability of reading and interpreting markdown documents, keeping the extension surface area deliberately narrow: a manifest file for identity/rules, and a four-directory structure for checks, playbooks, skills, and MCP configs.

## Architecture

### Component Overview

```
+-------------------+       +---------------------------+
|   entrypoint.sh   |       |   Claude Code CLI Agent   |
|                   |       |                           |
| - MCP config      |       | - Repo scanning           |
|   merge (jq)      |       | - Manifest parsing        |
| - Baseline backup |       | - Extension discovery     |
| - Cycle loop      |       | - Health check execution  |
+--------+----------+       | - Remediation dispatch    |
         |                  +-------------+-------------+
         |                                |
         v                                v
+-------------------+       +---------------------------+
| .claude/mcp.json  |       |  $CLAUDEOPS_REPOS_DIR     |
| (merged config)   |       |  /repos/                  |
+-------------------+       |  +-- repo-a/              |
                            |  |   +-- CLAUDE-OPS.md    |
                            |  |   +-- .claude-ops/     |
                            |  |       +-- checks/      |
                            |  |       +-- playbooks/   |
                            |  |       +-- skills/      |
                            |  |       +-- mcp.json     |
                            |  +-- repo-b/              |
                            |  |   +-- README.md        |
                            |  +-- repo-c/              |
                            |      +-- CLAUDE-OPS.md    |
                            +---------------------------+
```

The extension model has two phases that run in different process contexts:

1. **MCP config merge** (shell, in `entrypoint.sh`): Before the Claude process starts, the entrypoint script merges `.claude-ops/mcp.json` files from all repos into the baseline MCP configuration. This runs as a `jq` operation in bash.

2. **Extension discovery** (agent, in Claude): When the agent starts its monitoring cycle, it scans `/repos/`, reads manifests, discovers checks/playbooks/skills, and builds the unified repo map. This runs as part of the Tier 1 observation prompt.

### File Layout Convention

Each mounted repo follows this structure:

```
repo-root/
+-- CLAUDE-OPS.md            # Optional: repo manifest
+-- .claude-ops/              # Optional: extension directory
|   +-- checks/              # Optional: custom health checks
|   |   +-- *.md             # Each file = one check
|   +-- playbooks/           # Optional: remediation procedures
|   |   +-- *.md             # Each file = one playbook
|   +-- skills/              # Optional: operational capabilities
|   |   +-- *.md             # Each file = one skill
|   +-- mcp.json             # Optional: additional MCP servers
+-- ...                       # Rest of the repo (infrastructure files)
```

All components are optional. A repo with only `CLAUDE-OPS.md` declares identity and rules without providing extensions. A repo with only `.claude-ops/checks/` adds monitoring without a manifest. A repo with neither is still discovered and partially understood through fallback inference.

## Data Flow

### Phase 1: MCP Config Merge (entrypoint.sh, before each cycle)

```
1. If no baseline backup exists:
     Copy /app/.claude/mcp.json -> /app/.claude/mcp.json.baseline

2. Restore baseline:
     Copy /app/.claude/mcp.json.baseline -> /app/.claude/mcp.json

3. For each repo in $REPOS_DIR/*/ (alphabetical order):
     If .claude-ops/mcp.json exists:
       Merge using jq: baseline * repo (repo keys win on collision)
       Write merged result back to /app/.claude/mcp.json

4. Start Claude CLI with merged config
```

The merge uses `jq -s '.[0].mcpServers as $base | .[1].mcpServers as $repo | .[0] | .mcpServers = ($base * $repo)'`, which performs a shallow merge of the `mcpServers` object. Repo-defined servers are added; same-name servers are overridden by the repo version. The alphabetical processing order means that for multi-repo collisions, the last repo alphabetically wins.

### Phase 2: Extension Discovery (Claude agent, start of each cycle)

```
1. List all subdirectories under $CLAUDEOPS_REPOS_DIR

2. For each subdirectory (repo):
   a. Check for CLAUDE-OPS.md at repo root
      - If found: read and parse capabilities, rules, kind
      - If not found: mark for fallback inference

   b. Check for .claude-ops/ directory
      - If found:
        - Scan checks/ for *.md files -> add to health check queue
        - Scan playbooks/ for *.md files -> add to playbook registry
        - Scan skills/ for *.md files -> add to skills registry
        - Note: mcp.json already merged by entrypoint

   c. If no manifest and no extension directory:
      - Read README.md, directory listing, config files
      - Infer repo purpose from content

3. Build unified repo map:
   {
     repos: [
       { name, path, kind, capabilities, rules, checks, playbooks, skills },
       ...
     ]
   }

4. Use map throughout the cycle:
   - Run all checks (built-in + custom from all repos)
   - Select playbooks from the correct repo for remediation
   - Respect rules from each repo's manifest
   - Pass repo context to escalated tiers
```

### Phase 3: Extension Execution (Claude agent, during cycle)

Checks, playbooks, and skills are markdown documents that the agent reads and interprets. The agent does not execute them as scripts; it reads the instructions and performs the described actions using its available tools (Bash, MCP servers, etc.).

```
Custom Check Execution:
  1. Agent reads check markdown file
  2. Understands what to check and how (commands, endpoints, thresholds)
  3. Executes the described commands using Bash or MCP tools
  4. Evaluates results against healthy/unhealthy criteria in the file
  5. Records result in the health check report

Custom Playbook Execution:
  1. Agent identifies a failure that matches a custom playbook
  2. Verifies current tier has permission for the playbook's actions
  3. If permitted: reads the playbook and executes the steps
  4. If not permitted: escalates to appropriate tier with context
  5. Records action taken or escalation in the run log
```

## Key Decisions

### Convention over configuration (from ADR-0005)

The extension model uses well-known file paths (`CLAUDE-OPS.md`, `.claude-ops/`) rather than a registration mechanism or configuration file. This means:

- Adding a check = creating a markdown file in `.claude-ops/checks/`
- No registration step, no build process, no central config to update
- Discovery is automatic on every cycle
- The file system is the source of truth

This was chosen over a central config file (bottleneck for multi-team workflows), a plugin registry (excessive infrastructure for markdown files), and API-based registration (requires services to know about the agent).

### Markdown as the extension format

Extensions are markdown documents, not scripts or structured configs. The agent reads them as instructions and executes the appropriate commands itself. This aligns with the agent's core execution model: the Claude Code CLI reads prompts (markdown) and takes actions. There is no impedance mismatch between how the agent works and how extensions are authored.

### MCP merge in shell, extension discovery in agent

MCP config merging happens in `entrypoint.sh` (shell/jq) because MCP servers must be configured before the Claude process starts. Extension discovery (manifests, checks, playbooks) happens within the Claude agent because the agent is the one that reads and acts on them. This split is dictated by the Claude Code CLI's architecture: MCP servers are specified at process start time via `--mcp-config` or the config file, not at runtime.

### Alphabetical merge order for determinism

When multiple repos contribute MCP configs, they are merged in alphabetical order by directory name. This is deterministic (the same repos always merge in the same order) and transparent (operators can predict the outcome by knowing directory names). The alternative of timestamp-based or random ordering would make behavior unpredictable across runs.

### No schema validation for extensions

Extension files (manifests, checks, playbooks) are not validated against a schema. A malformed file will silently degrade behavior rather than failing loudly. This trades robustness for simplicity: adding schema validation would require a validation layer (code) in a system that deliberately avoids application code. The agent's ability to interpret imperfect markdown provides a degree of resilience.

## Trade-offs

### Gained

- **Zero-friction extension authoring**: Adding a check is creating a markdown file. No toolchain, no packaging, no deployment.
- **Decentralized ownership**: Each team maintains their own extensions without needing access to the Claude Ops repo.
- **Runtime adaptability**: New repos and extensions take effect on the next cycle without container restarts.
- **Self-documenting**: `ls .claude-ops/checks/` immediately shows what custom checks a repo provides. `CLAUDE-OPS.md` is human-readable documentation.
- **Graceful degradation**: Repos without any Claude Ops files are still discovered and partially monitored.

### Lost

- **Input validation**: Manifests and extensions are arbitrary text read by the LLM. Malformed or malicious content can degrade agent behavior or attempt prompt injection.
- **Versioning**: No mechanism to detect or migrate breaking changes to the extension format across repos.
- **Conflict detection**: Name collisions in MCP configs are resolved silently by alphabetical order rather than flagged as errors.
- **Explicit dependency management**: Extensions cannot declare dependencies on other extensions or specific MCP servers.

## Security Considerations

### Prompt injection via manifests

`CLAUDE-OPS.md` content is read directly by the LLM as part of its context. A malicious manifest could include instructions attempting to override safety constraints. Mitigations:

- Repos are mounted read-only by convention, limiting what a compromised repo can do at the filesystem level.
- Only trusted repos should be mounted. The operator controls which repos are accessible.
- The agent's system prompt and permission tier model constrain actions regardless of extension content.
- Future mitigation could include manifest content validation or sandboxed parsing.

### MCP server injection

A repo's `.claude-ops/mcp.json` can define arbitrary MCP servers. A malicious server could expose dangerous tools. Mitigations:

- MCP configs are merged before the Claude process starts, so the merged config at `/app/.claude/mcp.json` can be audited.
- MCP servers run as subprocesses with their own process isolation.
- Operators should review repo MCP configs before mounting repos.

### Permission tier enforcement

Repo extensions can describe actions that exceed the current tier's permissions. Enforcement is handled by the agent's prompt-based permission model, not by the extension mechanism. A Tier 1 agent will refuse Tier 2 actions even if a check file instructs them, because the permission constraints are in the system prompt.

## Future Considerations

- **Manifest schema validation**: A lightweight validation step (potentially using the agent itself) could verify that manifests contain expected sections and follow the convention.
- **Extension dependency declarations**: Checks or playbooks could declare that they require specific MCP servers or other extensions to function.
- **Namespaced MCP servers**: Prefixing repo-provided MCP server names with the repo name (e.g., `infra-ansible.custom-monitor`) could eliminate name collision concerns.
- **Extension versioning**: A version field in `CLAUDE-OPS.md` could help detect format incompatibilities across repos.
- **Audit logging for extension sources**: Tracking which repo contributed each check, playbook, or skill in the run log would improve debuggability.
