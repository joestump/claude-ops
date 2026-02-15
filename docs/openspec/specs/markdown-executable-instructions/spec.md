# SPEC-0002: Markdown as Executable Instructions

## Overview

Claude Ops uses markdown documents as the primary format for health checks, remediation playbooks, agent prompts, and system extensions. Rather than shell scripts, YAML DSLs, or compiled code, all operational procedures are expressed as prose with embedded command examples. The Claude Code CLI reads these markdown files at runtime and interprets them contextually, exercising judgment about how to apply the instructions to the specific infrastructure it encounters. This approach leverages the AI agent's natural language understanding to handle the judgment-heavy, context-dependent nature of infrastructure operations.

## Definitions

- **Check**: A markdown document in `checks/` or `.claude-ops/checks/` that describes how to verify a service's health, what constitutes healthy/unhealthy, and any special cases.
- **Playbook**: A markdown document in `playbooks/` or `.claude-ops/playbooks/` that describes a step-by-step remediation procedure, including prerequisites, verification, and escalation paths.
- **Tier prompt**: A markdown document in `prompts/` that defines an agent tier's identity, permissions, procedures, and output format.
- **Skill**: A markdown document in `.claude-ops/skills/` that defines a custom capability (maintenance task, reporting, etc.) contributed by a mounted repo.
- **Embedded command example**: A code block within a markdown document containing a shell command that the agent is expected to interpret and execute contextually, adapting parameters (hostnames, paths, container names) to the actual infrastructure.
- **Contextual adaptation**: The agent's ability to adjust the execution of markdown instructions based on the specific infrastructure context — for example, choosing `/health` over `/` when a health endpoint is available.
- **Repo-specific extension**: A markdown document contributed by a mounted infrastructure repo via its `.claude-ops/` directory, which extends the base set of checks, playbooks, or skills.

## Requirements

### REQ-1: Markdown as the Sole Instruction Format

All health checks, remediation playbooks, and agent tier prompts MUST be expressed as markdown documents. The system MUST NOT require compiled code, shell scripts, or structured DSLs for defining operational procedures.

#### Scenario: Adding a new health check
Given an infrastructure engineer wants to add a new health check
When they create a markdown file in `checks/` or `.claude-ops/checks/`
Then the agent MUST discover and execute the check on its next monitoring cycle
And no build step, compilation, or schema validation MUST be required

#### Scenario: No script execution engine
Given the system needs to execute a health check procedure
When the agent reads the check's markdown file
Then the agent MUST interpret the prose instructions and execute the described commands
And the system MUST NOT invoke a separate script interpreter or execution engine

### REQ-2: Check Document Structure

Health check documents (`checks/*.md`) MUST describe:

1. **When to run** — which services or conditions trigger this check
2. **How to check** — the commands or procedures to execute, provided as embedded code blocks
3. **What constitutes healthy/unhealthy** — the criteria for evaluating check results
4. **What to record** — the data points to capture for reporting

Check documents SHOULD also include:
- **Special cases** — edge conditions where the standard evaluation criteria do not apply

#### Scenario: HTTP health check document structure
Given the file `checks/http.md` exists
When the Tier 1 agent reads the file
Then the agent MUST find instructions for when to run HTTP checks
And the agent MUST find embedded curl commands for executing the checks
And the agent MUST find criteria for evaluating HTTP status codes (e.g., 200-299 is healthy, 500-599 is unhealthy)
And the agent MUST find special cases (e.g., 401/403 is healthy for authenticated services)

#### Scenario: Agent adapts check to specific service
Given `checks/http.md` instructs "if a service has a dedicated /health endpoint, prefer that over the root URL"
When the agent checks a service that exposes `/health`
Then the agent SHOULD check `/health` instead of `/`
And the agent MUST record which URL was actually checked

### REQ-3: Playbook Document Structure

Playbook documents (`playbooks/*.md`) MUST describe:

1. **Minimum tier** — which permission tier is required to execute this playbook
2. **When to use** — the conditions that trigger this playbook
3. **Prerequisites** — conditions that must be verified before execution (including cooldown checks)
4. **Steps** — ordered remediation steps with embedded command examples
5. **Verification** — how to confirm the remediation was successful
6. **Failure path** — what to do if the remediation does not work (typically escalation)

#### Scenario: Container restart playbook structure
Given the file `playbooks/restart-container.md` exists
When the Tier 2 agent needs to restart a container
Then the agent MUST read the playbook and find the minimum tier requirement
And the agent MUST check the prerequisite cooldown state before proceeding
And the agent MUST follow the steps in order: record pre-restart state, restart, wait, verify, update cooldown
And the agent MUST follow the failure path if the restart does not restore health

#### Scenario: Playbook prevents unauthorized tier execution
Given a playbook specifies "Tier: 2 (Sonnet) minimum"
When a Tier 1 agent encounters the playbook
Then the Tier 1 agent MUST NOT execute the playbook
And the Tier 1 agent MUST escalate to Tier 2 instead

### REQ-4: Tier Prompt Document Structure

Tier prompt documents (`prompts/*.md`) MUST define:

1. **Agent identity** — the agent's role and escalation level
2. **Environment setup** — which environment variables to read
3. **Permitted actions** — what the agent may do at this tier
4. **Prohibited actions** — what the agent must not do at this tier
5. **Procedural steps** — the ordered steps the agent follows during a cycle
6. **Output format** — the structure of the agent's output

#### Scenario: Tier 1 prompt completeness
Given the file `prompts/tier1-observe.md` exists
When the entrypoint invokes the Tier 1 agent with this prompt
Then the prompt MUST define the agent as an observer with no remediation permissions
And the prompt MUST list the steps: discover repos, discover services, run checks, read cooldown, evaluate, report/escalate
And the prompt MUST specify the output format for the health check summary

#### Scenario: Tier 2 prompt references Tier 1 context
Given the file `prompts/tier2-investigate.md` exists
When the Tier 2 agent is spawned with this prompt
Then the prompt MUST instruct the agent to start from the Tier 1 failure context
And the prompt MUST explicitly state that health checks should NOT be re-run

### REQ-5: Embedded Command Examples

Markdown documents MUST include embedded code blocks containing shell commands that serve as examples for the agent. The agent MUST interpret these commands contextually, adapting parameters to the actual infrastructure.

#### Scenario: Agent adapts a curl command
Given a check document contains the code block `curl -s -o /dev/null -w "HTTP %{http_code}" --max-time 10 <url>`
When the agent executes this check for a service at `https://myapp.example.com`
Then the agent MUST substitute `<url>` with `https://myapp.example.com`
And the agent MUST execute the adapted command

#### Scenario: Agent adapts a docker command
Given a playbook contains the code block `docker restart <container>`
When the agent remediates a service named `postgres`
Then the agent MUST substitute `<container>` with the actual container name for the postgres service
And the agent MUST execute the adapted command

#### Scenario: Agent interprets prose guidance around commands
Given a check document contains the prose "Wait 15-30 seconds (adjust based on service — databases need longer)"
When the agent restarts a database container
Then the agent SHOULD wait longer than 15 seconds before verifying health
And the agent MAY wait up to 60 seconds for database services

### REQ-6: Contextual Adaptation

The agent MUST be able to exercise judgment when interpreting markdown instructions. Instructions MAY contain conditional guidance expressed in natural language, and the agent MUST adapt its behavior accordingly.

#### Scenario: Conditional health check criteria
Given `checks/http.md` states "Services behind authentication may return 401/403 — this is expected and healthy"
When the agent checks a service that returns HTTP 403
Then the agent MUST evaluate whether the service requires authentication
And if authentication is expected, the agent MUST classify the service as healthy

#### Scenario: Service-specific adaptation
Given `checks/http.md` states "Some services redirect to a setup wizard on first run — note this but don't flag as unhealthy"
When the agent checks a service that redirects to `/setup`
Then the agent MUST note the redirect in its report
And the agent MUST NOT classify the service as unhealthy solely due to the redirect

#### Scenario: Playbook adaptation for different service types
Given `playbooks/restart-container.md` states "Wait 15-30 seconds (adjust based on service — databases need longer)"
When the agent restarts a Redis container
Then the agent SHOULD choose a wait time appropriate for the service type
And the agent MAY use a shorter wait for lightweight services and a longer wait for databases

### REQ-7: Repo-Specific Extensions via Markdown

Mounted infrastructure repositories MUST be able to extend the system by providing markdown documents in the `.claude-ops/` directory:

- `.claude-ops/checks/` — additional health checks
- `.claude-ops/playbooks/` — additional remediation playbooks
- `.claude-ops/skills/` — additional capabilities

These extensions MUST follow the same format requirements as the built-in checks, playbooks, and skills.

#### Scenario: Repo contributes a custom health check
Given a mounted repo at `/repos/myinfra` contains `.claude-ops/checks/custom-api.md`
When the Tier 1 agent discovers the repo
Then the agent MUST read and execute the custom check alongside built-in checks
And the custom check MUST be treated with the same priority as built-in checks

#### Scenario: Repo contributes a custom playbook
Given a mounted repo contains `.claude-ops/playbooks/rotate-custom-cert.md`
When the Tier 2 agent needs to remediate a TLS certificate issue
Then the agent MUST consider the custom playbook as a remediation option
And the agent MUST respect the minimum tier specified in the playbook

#### Scenario: Extension follows same format requirements
Given a team writes a custom check for `.claude-ops/checks/`
When the check document omits the "How to Check" section
Then the agent MAY still attempt the check using its own judgment
But the check SHOULD include all required sections for reliable execution

### REQ-8: No Build Step or Schema Validation

Adding, modifying, or removing markdown instruction files MUST NOT require any build step, compilation, schema validation, or registration process. Changes to markdown files MUST take effect on the next monitoring cycle.

#### Scenario: Immediate effect of new check
Given an engineer adds a new file `checks/custom-check.md`
When the next monitoring cycle begins
Then the agent MUST discover and execute the new check
And no restart, rebuild, or reconfiguration MUST be required

#### Scenario: Immediate effect of modified playbook
Given an engineer modifies `playbooks/restart-container.md` to add a new step
When the next remediation cycle reads the playbook
Then the agent MUST follow the updated steps
And no restart, rebuild, or reconfiguration MUST be required

#### Scenario: Immediate effect of deleted check
Given an engineer removes `checks/old-check.md`
When the next monitoring cycle begins
Then the agent MUST NOT attempt to run the deleted check
And no error MUST occur due to the missing file

### REQ-9: Self-Documenting Instructions

The markdown instruction documents MUST serve as both executable instructions and human-readable documentation. There MUST NOT be a separate documentation layer that can drift from the actual instructions.

#### Scenario: Check document is readable documentation
Given an infrastructure engineer reads `checks/http.md`
When they review the document
Then they MUST be able to understand what the check does, how it evaluates results, and what special cases it handles
And the document MUST NOT require knowledge of a custom DSL or programming language to understand

#### Scenario: Playbook document is readable documentation
Given an on-call engineer reads `playbooks/restart-container.md`
When they review the document
Then they MUST be able to understand the prerequisites, steps, and failure paths
And they MUST be able to follow the same procedure manually if needed

### REQ-10: Agent Reads Checks at Runtime

The agent MUST read and interpret check and playbook markdown files at runtime during each monitoring cycle. The agent MUST NOT cache or pre-compile instructions across cycles.

#### Scenario: Agent reads checks from the filesystem
Given the Tier 1 agent begins a monitoring cycle
When the agent needs to execute health checks
Then the agent MUST read the files in `/app/checks/` at runtime
And the agent MUST read any files in `.claude-ops/checks/` from mounted repos
And the agent MUST interpret the contents of each file as instructions for the current cycle

#### Scenario: Changes between cycles are reflected
Given `checks/http.md` is modified between two monitoring cycles
When the agent runs the second cycle
Then the agent MUST read the updated version of the file
And the agent MUST follow the updated instructions

### REQ-11: Playbook Tier Gating

Each playbook document MUST specify its minimum required permission tier. An agent MUST NOT execute a playbook that requires a higher tier than its own.

#### Scenario: Tier 2 playbook executed by Tier 2 agent
Given `playbooks/restart-container.md` specifies "Tier: 2 (Sonnet) minimum"
When the Tier 2 agent considers this playbook for remediation
Then the agent MAY execute the playbook
And the agent MUST follow all prerequisite checks before execution

#### Scenario: Tier 3 playbook rejected by Tier 2 agent
Given `playbooks/redeploy-service.md` specifies "Tier: 3 (Opus) minimum"
When the Tier 2 agent considers this playbook for remediation
Then the agent MUST NOT execute the playbook
And the agent MUST escalate to Tier 3 if this playbook is needed

#### Scenario: Higher tier can execute lower tier playbooks
Given `playbooks/restart-container.md` specifies "Tier: 2 (Sonnet) minimum"
When the Tier 3 agent considers this playbook
Then the agent MAY execute the playbook
And the playbook's minimum tier requirement MUST be treated as a floor, not a ceiling

## References

- [ADR-0002: Use Markdown Documents as Executable Instructions](../../adrs/ADR-0002-markdown-as-executable-instructions.md)
- [ADR-0001: Use Tiered Claude Model Escalation for Cost-Optimized Monitoring](../../adrs/ADR-0001-tiered-model-escalation.md)
- [ADR-0005: Mounted Repo Extensions](../../adrs/ADR-0005-mounted-repo-extensions.md)
