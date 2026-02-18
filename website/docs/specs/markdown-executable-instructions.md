---
sidebar_position: 2
sidebar_label: Markdown Executable Instructions
---

# SPEC-0002: Markdown as Executable Instructions

## Overview

Claude Ops uses markdown documents as the primary format for health checks, remediation playbooks, agent prompts, and system extensions. Rather than shell scripts, YAML DSLs, or compiled code, all operational procedures are expressed as prose with embedded command examples. The Claude Code CLI reads these markdown files at runtime and interprets them contextually, exercising judgment about how to apply the instructions to the specific infrastructure it encounters. This approach leverages the AI agent's natural language understanding to handle the judgment-heavy, context-dependent nature of infrastructure operations.

## Definitions

- **Check**: A markdown document in `checks/` or `.claude-ops/checks/` that describes how to verify a service's health, what constitutes healthy/unhealthy, and any special cases.
- **Playbook**: A markdown document in `playbooks/` or `.claude-ops/playbooks/` that describes a step-by-step remediation procedure, including prerequisites, verification, and escalation paths.
- **Tier prompt**: A markdown document in `prompts/` that defines an agent tier's identity, permissions, procedures, and output format.
- **Skill**: A markdown document in `.claude-ops/skills/` that defines a custom capability (maintenance task, reporting, etc.) contributed by a mounted repo.
- **Embedded command example**: A code block within a markdown document containing a shell command that the agent is expected to interpret and execute contextually, adapting parameters (hostnames, paths, container names) to the actual infrastructure.
- **Contextual adaptation**: The agent's ability to adjust the execution of markdown instructions based on the specific infrastructure context -- for example, choosing `/health` over `/` when a health endpoint is available.
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

1. **When to run** -- which services or conditions trigger this check
2. **How to check** -- the commands or procedures to execute, provided as embedded code blocks
3. **What constitutes healthy/unhealthy** -- the criteria for evaluating check results
4. **What to record** -- the data points to capture for reporting

Check documents SHOULD also include:
- **Special cases** -- edge conditions where the standard evaluation criteria do not apply

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

1. **Minimum tier** -- which permission tier is required to execute this playbook
2. **When to use** -- the conditions that trigger this playbook
3. **Prerequisites** -- conditions that must be verified before execution (including cooldown checks)
4. **Steps** -- ordered remediation steps with embedded command examples
5. **Verification** -- how to confirm the remediation was successful
6. **Failure path** -- what to do if the remediation does not work (typically escalation)

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

1. **Agent identity** -- the agent's role and escalation level
2. **Environment setup** -- which environment variables to read
3. **Permitted actions** -- what the agent may do at this tier
4. **Prohibited actions** -- what the agent must not do at this tier
5. **Procedural steps** -- the ordered steps the agent follows during a cycle
6. **Output format** -- the structure of the agent's output

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
Given a check document contains the prose "Wait 15-30 seconds (adjust based on service -- databases need longer)"
When the agent restarts a database container
Then the agent SHOULD wait longer than 15 seconds before verifying health
And the agent MAY wait up to 60 seconds for database services

### REQ-6: Contextual Adaptation

The agent MUST be able to exercise judgment when interpreting markdown instructions. Instructions MAY contain conditional guidance expressed in natural language, and the agent MUST adapt its behavior accordingly.

#### Scenario: Conditional health check criteria
Given `checks/http.md` states "Services behind authentication may return 401/403 -- this is expected and healthy"
When the agent checks a service that returns HTTP 403
Then the agent MUST evaluate whether the service requires authentication
And if authentication is expected, the agent MUST classify the service as healthy

#### Scenario: Service-specific adaptation
Given `checks/http.md` states "Some services redirect to a setup wizard on first run -- note this but don't flag as unhealthy"
When the agent checks a service that redirects to `/setup`
Then the agent MUST note the redirect in its report
And the agent MUST NOT classify the service as unhealthy solely due to the redirect

#### Scenario: Playbook adaptation for different service types
Given `playbooks/restart-container.md` states "Wait 15-30 seconds (adjust based on service -- databases need longer)"
When the agent restarts a Redis container
Then the agent SHOULD choose a wait time appropriate for the service type
And the agent MAY use a shorter wait for lightweight services and a longer wait for databases

### REQ-7: Repo-Specific Extensions via Markdown

Mounted infrastructure repositories MUST be able to extend the system by providing markdown documents in the `.claude-ops/` directory:

- `.claude-ops/checks/` -- additional health checks
- `.claude-ops/playbooks/` -- additional remediation playbooks
- `.claude-ops/skills/` -- additional capabilities

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

- [ADR-0002: Use Markdown Documents as Executable Instructions](../adrs/adr-0002)
- [ADR-0001: Use Tiered Claude Model Escalation for Cost-Optimized Monitoring](../adrs/adr-0001)
- [ADR-0005: Mounted Repo Extensions](../adrs/adr-0005)

---

# Design: Markdown as Executable Instructions

## Overview

This document describes the technical design of Claude Ops' approach to using markdown documents as the primary format for all operational instructions -- health checks, remediation playbooks, agent prompts, and repo-contributed extensions. Instead of executable scripts or structured DSLs, the system relies on the Claude Code CLI's ability to read prose instructions and interpret them contextually at runtime.

## Architecture

### Document Categories

The system uses four categories of markdown documents, each serving a distinct role:

```
/app/
+-- checks/                     # Built-in health check instructions
|   +-- http.md                 # HTTP endpoint checks
|   +-- dns.md                  # DNS resolution checks
|   +-- containers.md           # Docker container state checks
|   +-- databases.md            # Database connectivity checks
|   +-- services.md             # Service-specific checks
+-- playbooks/                  # Built-in remediation procedures
|   +-- restart-container.md    # Container restart procedure
|   +-- rotate-api-key.md       # API key rotation via browser
|   +-- redeploy-service.md     # Full service redeployment
+-- prompts/                    # Agent tier behavior definitions
|   +-- tier1-observe.md        # Tier 1: observation-only
|   +-- tier2-investigate.md    # Tier 2: safe remediation
|   +-- tier3-remediate.md      # Tier 3: full remediation
+-- CLAUDE.md                   # Top-level agent runbook

/repos/<repo>/                  # Mounted infrastructure repos
+-- .claude-ops/
    +-- checks/                 # Repo-specific health checks
    +-- playbooks/              # Repo-specific playbooks
    +-- skills/                 # Repo-specific capabilities
```

### Execution Model

The execution model is fundamentally different from traditional automation:

1. **No interpreter/executor split**: There is no separate engine that parses instructions and runs them. The Claude model IS the interpreter. It reads the markdown, understands the intent, and executes the appropriate commands using the tools available to it (Bash, Read, Glob, etc.).

2. **No schema or parsing**: Documents are not parsed into structured data. The model reads them as natural language and extracts the relevant information contextually. There is no document schema to validate against.

3. **Contextual parameter substitution**: Embedded command examples use placeholders like `<url>`, `<container>`, and `<service>`. The agent substitutes these with actual values from the infrastructure context (service inventory, container names, URLs) at runtime.

4. **Judgment-based execution**: Instructions can contain conditional guidance in prose ("if a service has a dedicated /health endpoint, prefer that"). The agent interprets these conditions against the actual infrastructure state, rather than following rigid if/else branches.

### Discovery and Loading Flow

```
Monitoring Cycle Start
  |
  +-- 1. Read /app/checks/*.md (built-in checks)
  |
  +-- 2. For each repo in $CLAUDEOPS_REPOS_DIR:
  |     +-- Read CLAUDE-OPS.md (repo manifest)
  |     +-- Read .claude-ops/checks/*.md (repo-specific checks)
  |
  +-- 3. Agent has full set of check instructions in context
  |
  +-- 4. Execute checks against discovered services
  |     +-- For each service x check:
  |           +-- Read check document
  |           +-- Determine if this check applies to this service
  |           +-- Adapt commands to service-specific parameters
  |           +-- Execute and evaluate results
  |
  +-- 5. If issues found, load playbook instructions:
        +-- Read /app/playbooks/*.md
        +-- Read .claude-ops/playbooks/*.md from repos
        +-- Select and execute the appropriate playbook
```

The agent does not load all documents into memory at once. It reads them on demand as needed during the monitoring cycle, using the Read tool to access files from the filesystem.

### Document Format Conventions

While documents are freeform prose, conventions have emerged for consistency:

**Check documents** follow a pattern:
- Title (H1): what the check covers
- "When to Run" (H2): which services trigger this check
- "How to Check" (H2): embedded command examples in code blocks
- "What's Healthy" (H2): evaluation criteria, often as a bullet list
- "What to Record" (H2): data points to capture
- "Special Cases" (H2): edge conditions and exceptions

**Playbook documents** follow a pattern:
- Title (H1): the remediation procedure name
- **Tier** (bold text): minimum permission tier required
- "When to Use" (H2): triggering conditions
- "Prerequisites" (H2): conditions to verify before execution
- "Steps" (H2): ordered remediation steps with embedded commands
- "If It Doesn't Work" (H2): escalation and failure handling

**Tier prompts** follow a pattern:
- Title (H1): tier identity
- Environment section: which variables to read
- Permissions section: explicit lists of permitted and prohibited actions
- Steps section: ordered procedural steps for the cycle
- Output format section: expected output structure

These patterns are conventions, not enforced schemas. The agent interprets documents that deviate from the pattern using its general language understanding.

## Data Flow

### Check Execution Flow

```
Agent reads checks/http.md
  -> Understands: "check services with web endpoints using curl"
  -> Understands: "200-299 is healthy, 500-599 is unhealthy"
  -> Understands: "prefer /health endpoint if available"

Agent retrieves service inventory
  -> Service: myapp at https://myapp.example.com
  -> Service has known /health endpoint

Agent adapts and executes:
  -> curl -s -o /dev/null -w "HTTP %{http_code}" --max-time 10 https://myapp.example.com/health
  -> Result: HTTP 200
  -> Classification: healthy
  -> Records: service=myapp, url=https://myapp.example.com/health, status=200, result=healthy
```

### Playbook Execution Flow

```
Agent reads playbooks/restart-container.md
  -> Understands: requires Tier 2 minimum
  -> Understands: check cooldown first (max 2 restarts per 4h)
  -> Understands: record state -> restart -> wait -> verify -> update cooldown

Agent checks prerequisites:
  -> Reads cooldown.json: myapp restart_count_4h = 1 (under limit)
  -> Verifies container exists and is unhealthy

Agent executes steps:
  1. docker inspect myapp -> records state
  2. docker restart myapp -> executes restart
  3. Waits 20 seconds (not a database, so mid-range wait)
  4. curl https://myapp.example.com/health -> HTTP 200
  5. Updates cooldown.json: restart_count_4h = 2
```

### Extension Loading Flow

```
Tier 1 discovers repos:
  -> /repos/myinfra/CLAUDE-OPS.md exists -> reads manifest
  -> /repos/myinfra/.claude-ops/checks/custom-api.md exists -> queued for execution
  -> /repos/myinfra/.claude-ops/playbooks/fix-custom.md exists -> available for Tier 2+

Agent runs standard checks:
  -> checks/http.md, checks/dns.md, checks/containers.md, ...

Agent runs repo-specific checks:
  -> /repos/myinfra/.claude-ops/checks/custom-api.md

If remediation needed, repo playbooks are available:
  -> /repos/myinfra/.claude-ops/playbooks/fix-custom.md
```

## Key Decisions

### Why prose instructions instead of executable scripts

Traditional automation uses scripts because execution must be deterministic and mechanical. In Claude Ops, the executor is an AI model that excels at interpreting natural language. Prose instructions leverage this capability:

- **Special cases are trivially expressed**: "Services behind authentication may return 401/403 -- this is expected and healthy" is a single sentence in prose but would require explicit conditional logic in a script.
- **Contextual adaptation is built-in**: "If a service has a dedicated /health endpoint, prefer that" requires no code -- the model adapts naturally.
- **No execution engine to maintain**: There is no parser, schema validator, or DSL runtime. The model IS the runtime.

The trade-off is non-determinism: the same markdown may be interpreted slightly differently across runs. This is acceptable because infrastructure operations inherently involve judgment, and the model's variations are typically within the bounds of reasonable judgment.

### Why no document schema validation

A schema would provide guarantees about document structure but would create friction:

- Contributors would need to learn the schema
- A validation step would be required before changes take effect
- The schema would need to evolve as new document patterns emerge
- The agent can handle missing sections by falling back to general knowledge

Instead, the system relies on conventions (documented in this design) and the agent's ability to interpret incomplete or non-standard documents.

### Why embedded command examples rather than separate command files

Commands are embedded as code blocks within the prose because:

- **Context preservation**: The command appears next to the prose that explains when and how to use it, making the document self-contained.
- **Adaptation guidance**: Prose around the command explains how to adapt it ("adjust based on service -- databases need longer"), which would be lost if commands were in separate files.
- **Single source of truth**: The check description, the command to execute, and the evaluation criteria are all in one document.

### Why playbooks specify minimum tiers in the document

Tier gating is a metadata annotation within the playbook's markdown rather than a separate permissions configuration because:

- **Co-location**: The tier requirement is part of the playbook's operational context, alongside prerequisites and steps.
- **Self-documenting**: A human reading the playbook immediately sees what permission level is needed.
- **No external permission registry**: There is no separate file mapping playbooks to tiers that could drift from the actual documents.

The agent enforces tier gating by reading the playbook's tier annotation and comparing it against its own tier level (defined in its prompt).

## Trade-offs

### Gained

- **Zero authoring friction**: Anyone who can write prose can add a health check or playbook. No programming language, DSL syntax, or build tools required.
- **Self-documenting system**: The instructions ARE the documentation. There is no separate doc layer that can drift from the actual procedures.
- **Graceful handling of ambiguity**: Infrastructure operations involve judgment calls that are naturally expressed in prose and naturally handled by the AI agent.
- **Instant extensibility**: Drop a markdown file into `.claude-ops/checks/` and it takes effect on the next cycle. No registration, compilation, or restart.
- **Reduced maintenance surface**: No script interpreter, DSL parser, schema validator, or plugin system to maintain.

### Lost

- **Deterministic execution**: The same markdown may be interpreted differently across runs. The agent may choose different wait times, check different endpoints, or classify edge cases differently.
- **Static analysis**: Typos in embedded commands are not caught until runtime. A missing section in a check document is only discovered when the agent tries to use it.
- **Unit testability**: There is no function to call with known inputs. Testing requires running the full agent against real or mock infrastructure.
- **Performance overhead**: The agent reads and reasons about prose on every cycle. A pre-parsed script or DSL would execute faster. However, since the monitoring interval is 60 minutes and a cycle typically completes in seconds to a few minutes, this overhead is negligible.
- **Debugging opacity**: When the agent does something unexpected, debugging requires reading agent logs to understand how it interpreted the instructions, rather than stepping through deterministic code.

## Future Considerations

- **Document linting**: A lightweight linter could verify that check documents contain the expected sections (When to Run, How to Check, etc.) without imposing a rigid schema. This would catch structural omissions before runtime.
- **Instruction testing framework**: A test harness could run the agent against mock infrastructure with expected outcomes, validating that markdown instructions produce correct behavior. This would not be unit testing but rather integration testing of the instruction set.
- **Version-controlled instruction sets**: Tagging instruction versions would allow rollback if a modified check or playbook causes issues. Currently, changes take effect immediately on the next cycle with no rollback mechanism.
- **Instruction metrics**: Tracking which checks and playbooks are read, how often they lead to successful remediation, and which produce false positives would enable data-driven refinement of the instruction set.
- **Template system**: For teams contributing many similar checks, a lightweight template mechanism (markdown includes or parameterized documents) could reduce duplication while preserving the prose-based approach.
