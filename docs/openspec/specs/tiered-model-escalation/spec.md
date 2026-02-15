# SPEC-0001: Tiered Model Escalation

## Overview

Claude Ops uses a tiered model escalation strategy to balance cost efficiency with remediation capability across monitoring cycles. The system starts each cycle with the cheapest model (Haiku) for observation, escalates to a mid-tier model (Sonnet) for safe remediation when issues are found, and further escalates to the most capable model (Opus) only when complex recovery is required. Each tier maps directly to a permission scope, ensuring that model capability and access privileges scale together.

## Definitions

- **Monitoring cycle**: A single execution of the Claude Ops loop, from health check discovery through evaluation and optional remediation.
- **Tier 1 (Observe)**: The initial observation phase using a low-cost, fast model. Read-only access.
- **Tier 2 (Investigate and Remediate)**: The first escalation level using a mid-tier model. Safe remediation actions permitted.
- **Tier 3 (Full Remediation)**: The highest escalation level using the most capable model. Full remediation actions permitted.
- **Escalation**: The act of spawning a higher-tier subagent and passing accumulated context forward.
- **Subagent**: A Task tool invocation that runs a separate Claude model with a specific prompt and context.
- **Cooldown state**: A JSON file tracking remediation attempts per service to enforce rate limits.
- **Context handoff**: The serialized summary of findings passed from one tier to the next during escalation.

## Requirements

### REQ-1: Three-Tier Model Hierarchy

The system MUST support exactly three model tiers for monitoring and remediation:

1. Tier 1 — a fast, low-cost model for observation (default: Haiku)
2. Tier 2 — a mid-tier model for safe remediation (default: Sonnet)
3. Tier 3 — the most capable model for full remediation (default: Opus)

#### Scenario: System starts with Tier 1 model
Given the entrypoint script starts a monitoring cycle
When the Claude CLI is invoked
Then it MUST use the Tier 1 model specified by `$CLAUDEOPS_TIER1_MODEL`
And the default MUST be `haiku` if the variable is unset

#### Scenario: All three tiers are available
Given the system is configured with default settings
When a monitoring cycle begins
Then the Tier 1 model MUST be available for observation
And the Tier 2 model MUST be available for escalation
And the Tier 3 model MUST be available for further escalation

### REQ-2: Configurable Model Selection

Each tier's model MUST be configurable via environment variables:

- `CLAUDEOPS_TIER1_MODEL` (default: `haiku`)
- `CLAUDEOPS_TIER2_MODEL` (default: `sonnet`)
- `CLAUDEOPS_TIER3_MODEL` (default: `opus`)

Operators MUST be able to change the model for any tier without modifying code.

#### Scenario: Custom model configuration
Given the environment variable `CLAUDEOPS_TIER1_MODEL` is set to `sonnet`
And `CLAUDEOPS_TIER2_MODEL` is set to `opus`
And `CLAUDEOPS_TIER3_MODEL` is set to `opus`
When a monitoring cycle begins
Then Tier 1 MUST use `sonnet` as the model
And Tier 2 MUST use `opus` as the model if escalation occurs
And Tier 3 MUST use `opus` as the model if further escalation occurs

#### Scenario: Default model fallback
Given `CLAUDEOPS_TIER1_MODEL` is not set
And `CLAUDEOPS_TIER2_MODEL` is not set
And `CLAUDEOPS_TIER3_MODEL` is not set
When a monitoring cycle begins
Then Tier 1 MUST use `haiku`
And Tier 2 MUST use `sonnet`
And Tier 3 MUST use `opus`

### REQ-3: Tier 1 Observe-Only Behavior

The Tier 1 agent MUST be restricted to observation-only actions. It MUST NOT perform any remediation.

Tier 1 permitted actions:
- Read files, configurations, logs, and inventory
- Execute HTTP and DNS health checks (curl, dig)
- Query databases in read-only mode
- Inspect container state (docker ps, Docker MCP)
- Read and update the cooldown state file

Tier 1 prohibited actions:
- Modify any infrastructure
- Restart, stop, or start any container
- Run any playbooks or deployment commands
- Write to any repository under `/repos/`

#### Scenario: Normal health check with no issues
Given the Tier 1 agent runs a health check cycle
When all services report healthy
Then no escalation occurs
And the agent updates `last_run` in the cooldown state file
And the agent exits after outputting a health summary

#### Scenario: Tier 1 detects an unhealthy service
Given the Tier 1 agent runs a health check cycle
When one or more services are detected as unhealthy
Then the Tier 1 agent MUST NOT attempt remediation
And the Tier 1 agent MUST escalate to Tier 2

#### Scenario: Tier 1 with dry run enabled
Given `CLAUDEOPS_DRY_RUN` is set to `true`
When the Tier 1 agent detects unhealthy services
Then the agent MUST report findings but MUST NOT escalate
And the agent MUST NOT trigger any remediation

### REQ-4: Tier 2 Safe Remediation Permissions

The Tier 2 agent MUST be limited to safe remediation actions. It inherits all Tier 1 permissions and additionally MAY:

- Restart containers (`docker restart <container>`)
- Bring up stopped containers (`docker compose up -d`)
- Fix file ownership and permissions on known data paths
- Clear temporary and cache directories
- Update API keys via service REST APIs
- Use browser automation (Chrome DevTools MCP) for credential rotation
- Send notifications via Apprise

The Tier 2 agent MUST NOT:
- Run Ansible playbooks or Helm upgrades
- Recreate containers from scratch
- Perform any action in the "Never Allowed" list

#### Scenario: Tier 2 restarts a failed container
Given the Tier 2 agent receives a failure context indicating a container is unhealthy
When the container has not exceeded its restart cooldown limit
Then the agent MAY execute `docker restart <container>`
And the agent MUST verify the service is healthy after restart
And the agent MUST update the cooldown state file

#### Scenario: Tier 2 cannot resolve the issue
Given the Tier 2 agent has attempted safe remediation
When the remediation does not restore the service to healthy
Then the Tier 2 agent MUST escalate to Tier 3
And the Tier 2 agent MUST pass all investigation findings and attempted remediation details

#### Scenario: Tier 2 encounters a problem requiring Ansible
Given the Tier 2 agent investigates a failed service
When the root cause requires a full service redeployment via Ansible
Then the Tier 2 agent MUST NOT run the Ansible playbook
And the Tier 2 agent MUST escalate to Tier 3

### REQ-5: Tier 3 Full Remediation Permissions

The Tier 3 agent MUST have full remediation capabilities. It inherits all Tier 1 and Tier 2 permissions and additionally MAY:

- Run Ansible playbooks for full service redeployment
- Run Helm upgrades for Kubernetes services
- Recreate containers from scratch (docker compose down + up)
- Investigate and fix database connectivity issues
- Execute multi-service orchestrated recovery
- Execute complex multi-step recovery procedures

The Tier 3 agent MUST NOT perform any action in the "Never Allowed" list.

#### Scenario: Tier 3 redeploys a service via Ansible
Given the Tier 3 agent receives escalation context from Tier 2
When the root cause requires a full redeployment
And the service has not been redeployed in the last 24 hours
Then the agent MAY run the appropriate Ansible playbook
And the agent MUST verify the service is healthy after redeployment
And the agent MUST update the cooldown state file

#### Scenario: Tier 3 performs multi-service orchestrated recovery
Given multiple services are down due to a cascading failure
When the Tier 3 agent identifies the root cause service
Then the agent MUST recover services in dependency order (e.g., database first, then app servers, then frontends)
And the agent MUST verify each service is healthy before proceeding to the next
And the agent MUST run a full health check sweep after all services are recovered

#### Scenario: Tier 3 cannot fix the issue
Given the Tier 3 agent has attempted full remediation
When the issue persists after all remediation attempts
Then the agent MUST send a notification via Apprise (if configured) indicating "NEEDS HUMAN ATTENTION"
And the notification MUST include the root cause analysis, attempted actions, and recommended next steps

### REQ-6: Escalation Context Forwarding

When escalating from one tier to the next, the escalating agent MUST pass the full accumulated context to the next tier. The receiving tier MUST NOT re-run checks that have already been performed.

The context handoff MUST include:
- The original failure summary (service names, check results, error messages)
- Any investigation findings from the current tier
- Remediation actions attempted and their outcomes
- The current cooldown state

#### Scenario: Tier 1 escalates to Tier 2 with full context
Given Tier 1 has completed health checks and found failures
When Tier 1 spawns a Tier 2 subagent via the Task tool
Then the Task prompt MUST include the contents of the Tier 2 prompt file
And the Task prompt MUST include the full failure summary
And the Task prompt MUST include the current cooldown state
And the Tier 2 agent MUST NOT re-run the health checks

#### Scenario: Tier 2 escalates to Tier 3 with full context
Given Tier 2 has investigated and attempted remediation
When Tier 2 spawns a Tier 3 subagent via the Task tool
Then the Task prompt MUST include the contents of the Tier 3 prompt file
And the Task prompt MUST include the original Tier 1 failure summary
And the Task prompt MUST include the Tier 2 investigation findings
And the Task prompt MUST include what was attempted and why it failed
And the Tier 3 agent MUST NOT re-run basic checks or re-attempt failed remediations

### REQ-7: Escalation Mechanism

Escalation MUST be implemented using the Claude Code Task tool to spawn subagents. The Task invocation MUST specify the model for the target tier.

#### Scenario: Task tool invocation for Tier 2
Given Tier 1 needs to escalate to Tier 2
When the escalation is triggered
Then the agent MUST use the Task tool with `model` set to the value of `$CLAUDEOPS_TIER2_MODEL`
And the agent MUST set `subagent_type` to `"general-purpose"`
And the agent MUST include the Tier 2 prompt file contents and failure context in the prompt

#### Scenario: Task tool invocation for Tier 3
Given Tier 2 needs to escalate to Tier 3
When the escalation is triggered
Then the agent MUST use the Task tool with `model` set to the value of `$CLAUDEOPS_TIER3_MODEL`
And the agent MUST set `subagent_type` to `"general-purpose"`
And the agent MUST include the Tier 3 prompt file contents and investigation findings in the prompt

### REQ-8: Permission-Model Alignment

The permission scope of each tier MUST align with the model's capability level. A lower-capability model MUST NOT have access to higher-tier remediation actions.

#### Scenario: Haiku cannot perform remediation
Given Tier 1 is running with the Haiku model
When the Tier 1 prompt is loaded
Then the prompt MUST explicitly prohibit remediation actions
And the allowed tools list MUST NOT include destructive operations

#### Scenario: Sonnet cannot perform full redeployment
Given Tier 2 is running with the Sonnet model
When the Tier 2 prompt is loaded
Then the prompt MUST explicitly prohibit Ansible playbooks, Helm upgrades, and container recreation
And the prompt MUST permit only safe remediation actions

#### Scenario: Opus has full remediation access
Given Tier 3 is running with the Opus model
When the Tier 3 prompt is loaded
Then the prompt MUST permit all remediation actions except those in the "Never Allowed" list

### REQ-9: Separate Prompt Files Per Tier

Each tier MUST have its own dedicated prompt file that defines the agent's role, permissions, procedures, and output format:

- Tier 1: `prompts/tier1-observe.md`
- Tier 2: `prompts/tier2-investigate.md`
- Tier 3: `prompts/tier3-remediate.md`

#### Scenario: Tier 1 prompt defines observation behavior
Given the file `prompts/tier1-observe.md` exists
When the Tier 1 agent is invoked
Then the prompt MUST instruct the agent to discover repos, run health checks, evaluate results, and escalate or report
And the prompt MUST NOT include remediation procedures

#### Scenario: Tier 2 prompt defines investigation and safe remediation
Given the file `prompts/tier2-investigate.md` exists
When the Tier 2 agent is invoked
Then the prompt MUST instruct the agent to review context from Tier 1, investigate root causes, and apply safe remediations
And the prompt MUST define escalation to Tier 3 when remediation fails

#### Scenario: Tier 3 prompt defines full remediation
Given the file `prompts/tier3-remediate.md` exists
When the Tier 3 agent is invoked
Then the prompt MUST instruct the agent to review context from Tier 2, analyze root causes, and perform full remediation
And the prompt MUST define reporting for both successful and unsuccessful outcomes

### REQ-10: Cost Optimization

The system SHOULD minimize cost for the common case where no issues are detected. Tier 1 (observation-only) cycles SHOULD complete using only the cheapest available model.

#### Scenario: Routine healthy cycle costs
Given all infrastructure services are healthy
When a monitoring cycle completes
Then only the Tier 1 model SHOULD have been invoked
And no Tier 2 or Tier 3 model invocations SHOULD occur
And the cycle cost SHOULD be proportional to the cheapest model tier

#### Scenario: Escalation adds cost only when needed
Given a service is unhealthy and requires Tier 2 remediation
When the Tier 2 agent successfully remediates the issue
Then only Tier 1 and Tier 2 models SHOULD have been invoked
And no Tier 3 model invocation SHOULD occur

### REQ-11: Never-Allowed Actions

Regardless of tier, certain actions MUST NEVER be performed by any agent. These actions always require human intervention:

- Deleting persistent data volumes
- Modifying inventory files, playbooks, Helm charts, or Dockerfiles
- Changing passwords, secrets, or encryption keys
- Modifying network configuration (VPN, WireGuard, Caddy, DNS records)
- Running `docker system prune` or any bulk cleanup
- Pushing to git repositories
- Acting on hosts not listed in the inventory
- Dropping or truncating database tables
- Modifying the runbook or any prompt files

#### Scenario: Tier 3 agent encounters a task requiring secret rotation
Given the Tier 3 agent has full remediation permissions
When the root cause of a failure is an expired encryption key
Then the agent MUST NOT change the encryption key
And the agent MUST report the issue as needing human attention

#### Scenario: Any tier agent is asked to delete a data volume
Given any tier agent encounters a situation where deleting a data volume would fix the issue
When the agent evaluates remediation options
Then the agent MUST NOT delete the data volume
And the agent MUST report the issue as needing human attention

## References

- [ADR-0001: Use Tiered Claude Model Escalation for Cost-Optimized Monitoring](../../adrs/ADR-0001-tiered-model-escalation.md)
- [ADR-0002: Use Markdown Documents as Executable Instructions](../../adrs/ADR-0002-markdown-as-executable-instructions.md)
- [ADR-0003: Prompt-Based Permissions](../../adrs/ADR-0003-prompt-based-permission-tiers.md)
