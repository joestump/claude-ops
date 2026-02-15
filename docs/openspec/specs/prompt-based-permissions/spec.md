# SPEC-0003: Prompt-Based Permission Enforcement

## Overview

Claude Ops enforces permission boundaries across its three escalation tiers (Tier 1: observe, Tier 2: safe remediation, Tier 3: full remediation) using a two-layer enforcement model: prompt-level instructions embedded in tier-specific markdown files and the `--allowedTools` CLI flag that restricts which Claude Code tools are available at each tier. This specification defines the requirements for how permission tiers are structured, enforced, and extended, along with the "Never Allowed" list of operations that no tier may perform.

## Definitions

- **Permission Tier**: One of three escalation levels (Tier 1, Tier 2, Tier 3) that define what actions an agent is authorized to perform.
- **Prompt Instructions**: Natural-language permission boundaries written in each tier's markdown prompt file (`prompts/tier1-observe.md`, `prompts/tier2-investigate.md`, `prompts/tier3-remediate.md`).
- **Allowed Tools**: The set of Claude Code tools (e.g., `Bash`, `Read`, `Write`, `Glob`, `Grep`, `Task`, `WebFetch`) that the CLI permits for a given invocation, controlled via the `--allowedTools` flag.
- **Never Allowed List**: A set of operations that are prohibited at all tiers and always require human intervention.
- **Cooldown State**: A JSON file tracking remediation attempts per service, providing a secondary safety net that limits blast radius.
- **Subagent**: A Claude model invocation spawned via the `Task` tool with its own prompt context and tier-specific permissions.
- **Tool-Level Boundary**: A hard technical restriction enforced by the Claude Code CLI that prevents a tool from being invoked at all.
- **Semantic Restriction**: A prompt-level instruction that constrains which operations are performed within an allowed tool (e.g., "you have Bash but must not run Ansible").

## Requirements

### REQ-1: Three-Tier Permission Hierarchy

The system MUST define exactly three permission tiers with strictly increasing capabilities:

- **Tier 1 (Observe Only)**: Read-only operations and health checks.
- **Tier 2 (Safe Remediation)**: All Tier 1 capabilities plus safe remediation actions.
- **Tier 3 (Full Remediation)**: All Tier 1 and Tier 2 capabilities plus full remediation actions.

Each tier MUST be a strict superset of the tier below it; no lower tier may have capabilities absent from a higher tier.

#### Scenario: Tier hierarchy is strictly additive
Given the three permission tiers are defined
When Tier 2 capabilities are enumerated
Then every Tier 1 capability is included in Tier 2
And Tier 2 has additional capabilities beyond Tier 1

#### Scenario: Tier 3 includes all lower-tier capabilities
Given the three permission tiers are defined
When Tier 3 capabilities are enumerated
Then every Tier 1 and Tier 2 capability is included in Tier 3
And Tier 3 has additional capabilities beyond Tier 2

### REQ-2: Tier 1 Permitted Operations

Tier 1 MUST be limited to the following operations:

- Read files, configurations, logs, and inventory
- HTTP health checks (e.g., `curl` for status codes and response times)
- DNS verification (e.g., `dig` for hostname resolution)
- Query databases in read-only mode
- Inspect container state (via Docker MCP or CLI read-only commands)
- Read and update the cooldown state file

Tier 1 MUST NOT perform any operation that modifies infrastructure, restarts services, runs playbooks, writes to mounted repositories, or triggers deployment commands.

#### Scenario: Tier 1 agent performs a health check
Given the Tier 1 agent is running an observation cycle
When the agent checks HTTP health of a service
Then the agent executes `curl` to verify the HTTP status code
And no infrastructure is modified

#### Scenario: Tier 1 agent encounters an unhealthy service
Given the Tier 1 agent detects a service returning HTTP 500
When the agent considers restarting the container
Then the prompt instructions prevent the agent from executing remediation
And the agent escalates to Tier 2 instead

#### Scenario: Tier 1 agent reads container state
Given the Tier 1 agent needs to check container health
When the agent inspects container status via Docker CLI
Then the agent uses read-only Docker commands (e.g., `docker ps`, `docker inspect`)
And no containers are started, stopped, or restarted

### REQ-3: Tier 2 Permitted Operations

Tier 2 MUST be permitted to perform all Tier 1 operations plus the following:

- Restart containers (`docker restart <container>`)
- Bring up stopped containers (`docker compose up -d <service>`)
- Fix file ownership and permissions on known data paths (`chown`, `chmod`)
- Clear temporary and cache directories
- Update API keys via service REST APIs
- Perform browser automation for credential rotation (via Chrome DevTools MCP)
- Send notifications via Apprise

Tier 2 MUST NOT run Ansible playbooks, Helm upgrades, or recreate containers from scratch. Tier 2 MUST NOT perform any operation on the "Never Allowed" list.

#### Scenario: Tier 2 agent restarts an unhealthy container
Given the Tier 2 agent receives an escalation for a crashed service
When the agent determines the service needs a restart
Then the agent executes `docker restart <container>`
And verifies the service is healthy after restart
And updates the cooldown state file

#### Scenario: Tier 2 agent is blocked from running Ansible
Given the Tier 2 agent determines a full redeployment is needed
When the agent considers running `ansible-playbook`
Then the prompt instructions prevent execution of Ansible commands
And the agent escalates to Tier 3 instead

#### Scenario: Tier 2 agent rotates credentials via browser automation
Given the Tier 2 agent needs to rotate an API key
When the agent uses Chrome DevTools MCP to access a service web UI
Then the agent navigates the UI to extract or update the API key
And verifies the integration works with the new key

### REQ-4: Tier 3 Permitted Operations

Tier 3 MUST be permitted to perform all Tier 1 and Tier 2 operations plus the following:

- Run Ansible playbooks for full service redeployment
- Run Helm upgrades for Kubernetes services
- Recreate containers from scratch (`docker compose down && up`)
- Investigate and fix database connectivity issues
- Perform multi-service orchestrated recovery (ordered restart of dependent services)
- Execute complex multi-step recovery procedures

Tier 3 MUST NOT perform any operation on the "Never Allowed" list.

#### Scenario: Tier 3 agent runs Ansible redeployment
Given the Tier 3 agent receives an escalation for a persistently failing service
When the agent determines a full redeployment is needed
Then the agent identifies the correct playbook and inventory from the mounted repo
And executes `ansible-playbook -i <inventory> <playbook> --limit <host>`
And verifies the service is healthy after redeployment

#### Scenario: Tier 3 agent performs multi-service orchestrated recovery
Given multiple services are down due to a database failure
When the Tier 3 agent plans recovery
Then the agent restarts the database first
And waits for the database to be fully healthy
And then restarts dependent services in dependency order
And verifies all services are healthy after recovery

### REQ-5: Never Allowed Operations

The system MUST define and enforce a "Never Allowed" list of operations that are prohibited at ALL tiers, including Tier 3. The following operations MUST always require human intervention:

- Delete persistent data volumes
- Modify inventory files, playbooks, Helm charts, or Dockerfiles
- Change passwords, secrets, or encryption keys
- Modify network configuration (VPN, WireGuard, Caddy, DNS records)
- Execute bulk cleanup commands (e.g., `docker system prune`)
- Push to git repositories
- Perform actions on hosts not listed in the inventory
- Drop or truncate database tables
- Modify the runbook or any prompt files

#### Scenario: Tier 3 agent attempts to delete a volume
Given the Tier 3 agent identifies a corrupted data volume
When the agent considers deleting the volume to recreate it
Then the "Never Allowed" prompt instructions prevent the action
And the agent reports the issue as requiring human attention

#### Scenario: Any tier agent attempts to push to git
Given an agent at any tier modifies a local file during investigation
When the agent considers committing and pushing the change
Then the "Never Allowed" list prevents git push operations
And the agent leaves the change uncommitted for human review

#### Scenario: Tier 3 agent encounters need to change secrets
Given the Tier 3 agent determines a service failure is caused by expired credentials
When the agent considers modifying the secret value
Then the "Never Allowed" list prevents password and secret changes
And the agent sends a notification requesting human intervention

### REQ-6: Tool-Level Enforcement via --allowedTools

The system MUST use the `--allowedTools` CLI flag to restrict which Claude Code tools are available at each tier. This provides a hard technical boundary that the model cannot bypass.

Tier 1 MUST be configured with a restricted tool set (default: `Bash,Read,Grep,Glob,Task,WebFetch`). Tools not in the allowed list (e.g., `Write`, `Edit`) MUST be rejected by the CLI before the model sees any result.

The allowed tools MUST be configurable via the `CLAUDEOPS_ALLOWED_TOOLS` environment variable.

#### Scenario: Tier 1 agent attempts to use the Write tool
Given the Tier 1 agent is invoked with `--allowedTools "Bash,Read,Grep,Glob,Task,WebFetch"`
When the agent attempts to invoke the `Write` tool
Then the Claude Code CLI rejects the tool call
And the agent receives an error indicating the tool is not allowed

#### Scenario: Operator customizes allowed tools
Given an operator sets `CLAUDEOPS_ALLOWED_TOOLS=Bash,Read,Grep,Glob,Task`
When the entrypoint script invokes the Tier 1 agent
Then the `--allowedTools` flag is set to the operator's custom value
And the `WebFetch` tool is not available to the agent

### REQ-7: Prompt-Level Permission Enforcement

Each tier's prompt file MUST contain an explicit "Your Permissions" section that lists:

1. What the agent MAY do (positive permissions)
2. What the agent MUST NOT do (negative permissions)

The prompt MUST be the authoritative source of semantic restrictions within an allowed tool category. For example, even though Tier 2 has access to `Bash` (and therefore could technically run any shell command), the prompt MUST instruct the agent not to run Ansible playbooks.

#### Scenario: Tier 2 prompt file contains permission section
Given the Tier 2 prompt file `prompts/tier2-investigate.md` is read
When the "Your Permissions" section is parsed
Then the section lists allowed operations (e.g., "Restart containers")
And the section lists prohibited operations (e.g., "Run Ansible playbooks")

#### Scenario: Permission section constrains Bash usage
Given the Tier 2 agent has access to the `Bash` tool
When the agent's prompt instructs it not to run Ansible
Then the agent follows the semantic restriction
And does not execute `ansible-playbook` commands via Bash

### REQ-8: Subagent Tier Isolation

When a lower tier escalates to a higher tier, the system MUST spawn the higher tier as a separate subagent via the `Task` tool. Each subagent MUST receive its own tier-specific prompt with its own permission boundaries.

The escalating tier MUST pass the full context of its findings to the subagent. The subagent SHOULD NOT need to re-run checks that the lower tier already performed.

#### Scenario: Tier 1 escalates to Tier 2
Given the Tier 1 agent has identified unhealthy services
When the agent spawns a Tier 2 subagent via the Task tool
Then the Tier 2 subagent receives the `tier2-investigate.md` prompt
And the subagent receives the full failure summary from Tier 1
And the subagent operates under Tier 2 permissions

#### Scenario: Tier 2 escalates to Tier 3
Given the Tier 2 agent cannot resolve an issue with safe remediation
When the agent spawns a Tier 3 subagent via the Task tool
Then the Tier 3 subagent receives the `tier3-remediate.md` prompt
And the subagent receives investigation findings from Tier 2
And the subagent operates under Tier 3 permissions

#### Scenario: Subagent does not re-run prior checks
Given a Tier 2 subagent receives escalation context from Tier 1
When the subagent begins its investigation
Then the subagent starts from the provided failure context
And does not repeat the health checks already performed by Tier 1

### REQ-9: Cooldown as Secondary Safety Net

The cooldown state system MUST act as a secondary safety net that limits the blast radius of repeated remediation actions, independent of the permission tier.

The system MUST enforce:
- A maximum of 2 container restarts per service per 4-hour window
- A maximum of 1 full redeployment per service per 24-hour window

When cooldown limits are exceeded, the agent MUST stop retrying remediation and MUST send a notification indicating that human attention is required.

#### Scenario: Restart cooldown limit exceeded
Given a service has been restarted 2 times in the last 4 hours
When a Tier 2 agent determines the service needs another restart
Then the agent reads the cooldown state and finds the limit exceeded
And the agent does not restart the service
And the agent sends a "needs human attention" notification

#### Scenario: Redeployment cooldown limit exceeded
Given a service was fully redeployed within the last 24 hours
When a Tier 3 agent determines another redeployment is needed
Then the agent reads the cooldown state and finds the limit exceeded
And the agent does not redeploy the service
And the agent sends a "needs human attention" notification

#### Scenario: Cooldown counters reset on sustained health
Given a service has previous restart entries in the cooldown state
When the service is confirmed healthy for 2 consecutive check cycles
Then the cooldown counters for that service are reset

### REQ-10: Post-Hoc Auditability

All agent actions MUST be logged to the results directory (`$CLAUDEOPS_RESULTS_DIR`). The logs MUST capture:

- The full output of each Claude CLI invocation
- All tool calls and their results
- Any remediation actions taken
- Cooldown state changes

The system SHOULD support post-hoc review of all actions taken during a run for compliance and incident analysis.

#### Scenario: Run output is logged to results directory
Given the entrypoint script invokes the Claude CLI
When the agent performs its health check cycle
Then all output is written to a timestamped log file in `$CLAUDEOPS_RESULTS_DIR`
And the log file captures tool calls, check results, and any actions taken

#### Scenario: Operator reviews remediation actions after the fact
Given a Tier 2 agent restarted a container during the last run
When an operator reviews the log file in the results directory
Then the operator can see what check failed, what remediation was attempted, and the verification result

### REQ-11: Permission Modification Without Rebuild

Permission rules MUST be modifiable by editing prompt markdown files and/or the `CLAUDEOPS_ALLOWED_TOOLS` environment variable without requiring a container image rebuild.

Changes to prompt files SHOULD take effect on the next run cycle. Changes to environment variables MUST take effect on the next container restart.

#### Scenario: Operator adds a new prohibited operation to Tier 2
Given an operator edits `prompts/tier2-investigate.md` to add a new restriction
When the next run cycle begins
Then the Tier 2 agent reads the updated prompt file
And the new restriction is enforced during that cycle

#### Scenario: Operator restricts available tools
Given an operator sets `CLAUDEOPS_ALLOWED_TOOLS=Bash,Read,Glob`
When the container is restarted and the next run begins
Then the Tier 1 agent can only use `Bash`, `Read`, and `Glob`
And `Grep`, `Task`, and `WebFetch` are not available

### REQ-12: Honest Safety Posture

The system MUST acknowledge in its documentation and design that:

1. Prompt-based restrictions within a tool category (e.g., which Bash commands are allowed) rely on model compliance, not technical enforcement.
2. The `--allowedTools` flag provides a genuine hard boundary only at the tool level, not at the command level within a tool.
3. There is no runtime interception layer that blocks a forbidden Bash command before execution.
4. Violations are detectable through post-hoc log review, not prevented by a runtime enforcement layer.

The system SHOULD recommend complementary Docker-level hardening (read-only mounts, capability restrictions, network policies) for the highest-risk operations.

#### Scenario: Documentation describes limitation of prompt enforcement
Given an operator reads the system documentation
When the operator reviews the permission enforcement model
Then the documentation clearly states that prompt instructions rely on model compliance
And recommends complementary Docker-level restrictions for highest-risk operations

#### Scenario: System operates within acknowledged limitations
Given the permission enforcement relies on prompt compliance and tool filtering
When the system encounters an edge case where a model might deviate
Then the cooldown system provides a secondary limit on blast radius
And logging enables post-hoc detection of any deviation

## References

- [ADR-0003: Prompt-Based Permission Enforcement](../../adrs/ADR-0003-prompt-based-permission-enforcement.md)
- [ADR-0001: Tiered Model Escalation](../../adrs/ADR-0001-tiered-model-escalation.md)
- Claude Code CLI `--allowedTools` documentation
