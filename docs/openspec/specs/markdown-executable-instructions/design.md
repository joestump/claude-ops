# Design: Markdown as Executable Instructions

## Overview

This document describes the technical design of Claude Ops' approach to using markdown documents as the primary format for all operational instructions — health checks, remediation playbooks, agent prompts, and repo-contributed extensions. Instead of executable scripts or structured DSLs, the system relies on the Claude Code CLI's ability to read prose instructions and interpret them contextually at runtime.

## Architecture

### Document Categories

The system uses four categories of markdown documents, each serving a distinct role:

```
/app/
├── checks/                     # Built-in health check instructions
│   ├── http.md                 # HTTP endpoint checks
│   ├── dns.md                  # DNS resolution checks
│   ├── containers.md           # Docker container state checks
│   ├── databases.md            # Database connectivity checks
│   └── services.md             # Service-specific checks
├── playbooks/                  # Built-in remediation procedures
│   ├── restart-container.md    # Container restart procedure
│   ├── rotate-api-key.md       # API key rotation via browser
│   └── redeploy-service.md     # Full service redeployment
├── prompts/                    # Agent tier behavior definitions
│   ├── tier1-observe.md        # Tier 1: observation-only
│   ├── tier2-investigate.md    # Tier 2: safe remediation
│   └── tier3-remediate.md      # Tier 3: full remediation
└── CLAUDE.md                   # Top-level agent runbook

/repos/<repo>/                  # Mounted infrastructure repos
└── .claude-ops/
    ├── checks/                 # Repo-specific health checks
    ├── playbooks/              # Repo-specific playbooks
    └── skills/                 # Repo-specific capabilities
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
  │
  ├── 1. Read /app/checks/*.md (built-in checks)
  │
  ├── 2. For each repo in $CLAUDEOPS_REPOS_DIR:
  │     ├── Read CLAUDE-OPS.md (repo manifest)
  │     └── Read .claude-ops/checks/*.md (repo-specific checks)
  │
  ├── 3. Agent has full set of check instructions in context
  │
  ├── 4. Execute checks against discovered services
  │     └── For each service × check:
  │           ├── Read check document
  │           ├── Determine if this check applies to this service
  │           ├── Adapt commands to service-specific parameters
  │           └── Execute and evaluate results
  │
  └── 5. If issues found, load playbook instructions:
        ├── Read /app/playbooks/*.md
        ├── Read .claude-ops/playbooks/*.md from repos
        └── Select and execute the appropriate playbook
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
  → Understands: "check services with web endpoints using curl"
  → Understands: "200-299 is healthy, 500-599 is unhealthy"
  → Understands: "prefer /health endpoint if available"

Agent retrieves service inventory
  → Service: myapp at https://myapp.example.com
  → Service has known /health endpoint

Agent adapts and executes:
  → curl -s -o /dev/null -w "HTTP %{http_code}" --max-time 10 https://myapp.example.com/health
  → Result: HTTP 200
  → Classification: healthy
  → Records: service=myapp, url=https://myapp.example.com/health, status=200, result=healthy
```

### Playbook Execution Flow

```
Agent reads playbooks/restart-container.md
  → Understands: requires Tier 2 minimum
  → Understands: check cooldown first (max 2 restarts per 4h)
  → Understands: record state → restart → wait → verify → update cooldown

Agent checks prerequisites:
  → Reads cooldown.json: myapp restart_count_4h = 1 (under limit)
  → Verifies container exists and is unhealthy

Agent executes steps:
  1. docker inspect myapp → records state
  2. docker restart myapp → executes restart
  3. Waits 20 seconds (not a database, so mid-range wait)
  4. curl https://myapp.example.com/health → HTTP 200
  5. Updates cooldown.json: restart_count_4h = 2
```

### Extension Loading Flow

```
Tier 1 discovers repos:
  → /repos/myinfra/CLAUDE-OPS.md exists → reads manifest
  → /repos/myinfra/.claude-ops/checks/custom-api.md exists → queued for execution
  → /repos/myinfra/.claude-ops/playbooks/fix-custom.md exists → available for Tier 2+

Agent runs standard checks:
  → checks/http.md, checks/dns.md, checks/containers.md, ...

Agent runs repo-specific checks:
  → /repos/myinfra/.claude-ops/checks/custom-api.md

If remediation needed, repo playbooks are available:
  → /repos/myinfra/.claude-ops/playbooks/fix-custom.md
```

## Key Decisions

### Why prose instructions instead of executable scripts

Traditional automation uses scripts because execution must be deterministic and mechanical. In Claude Ops, the executor is an AI model that excels at interpreting natural language. Prose instructions leverage this capability:

- **Special cases are trivially expressed**: "Services behind authentication may return 401/403 — this is expected and healthy" is a single sentence in prose but would require explicit conditional logic in a script.
- **Contextual adaptation is built-in**: "If a service has a dedicated /health endpoint, prefer that" requires no code — the model adapts naturally.
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
- **Adaptation guidance**: Prose around the command explains how to adapt it ("adjust based on service — databases need longer"), which would be lost if commands were in separate files.
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
