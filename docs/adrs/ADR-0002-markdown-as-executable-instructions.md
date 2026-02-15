---
status: accepted
date: 2025-06-01
---

# Use Markdown Documents as Executable Instructions

## Context and Problem Statement

Claude Ops is an AI infrastructure monitoring and remediation agent that needs to perform health checks, investigate failures, and execute remediation procedures across diverse infrastructure. The system requires a format for defining these operational procedures that can be authored by infrastructure engineers, extended by teams mounting their own repositories, and executed reliably by an AI agent at runtime.

The core question is: **In what format should health checks, remediation playbooks, and agent instructions be expressed?**

Traditional operational tooling uses shell scripts, YAML DSLs, or compiled code. However, Claude Ops is not a traditional automation system -- it is an AI agent that interprets instructions and adapts its behavior based on context. This changes the calculus of what format best serves both the human authors and the AI executor.

## Decision Drivers

- **Authoring accessibility**: Infrastructure engineers should be able to add new checks and playbooks without learning a DSL or programming language.
- **AI interpretability**: The format must be natural for Claude to read, reason about, and execute correctly.
- **Contextual adaptability**: Procedures often require judgment calls (e.g., "if the service has a /health endpoint, prefer that") that are hard to express in rigid formats.
- **Extensibility via repo mounting**: Teams mount their own repos under `/repos/` and extend Claude Ops with custom checks and playbooks via `.claude-ops/` directories. The format must be easy for any team to contribute to.
- **Auditability**: Operators must be able to read and review what the agent will do before it runs.
- **Maintainability**: Adding, modifying, or removing checks should be low-friction and not require build steps or schema migrations.

## Considered Options

1. **Markdown instructions interpreted by Claude** -- Prose documents with embedded code snippets that Claude reads and executes contextually.
2. **Shell scripts / executable code** -- Traditional executable scripts (Bash, Python) that are run directly.
3. **YAML/JSON DSL** -- A structured declarative format (similar to Ansible playbooks or GitHub Actions workflows) that defines checks and remediation steps.
4. **Hybrid approach** -- Structured YAML for check/playbook definitions (inputs, thresholds, commands) with markdown for documentation and special-case guidance.

## Decision Outcome

**Chosen option: "Markdown instructions interpreted by Claude"**, because it best leverages the AI agent's natural language understanding to handle the judgment-heavy, context-dependent nature of infrastructure operations while keeping the authoring experience accessible to any engineer who can write prose.

In this approach:
- Health checks (`checks/*.md`) describe what to check, how to check it, what constitutes healthy/unhealthy, and special cases -- all in prose with embedded command examples.
- Playbooks (`playbooks/*.md`) describe remediation procedures step by step, including prerequisites, verification, and escalation paths.
- Tier prompts (`prompts/*.md`) define the agent's behavior, permissions, and escalation logic.
- Repos extend the system by adding markdown files to `.claude-ops/checks/` and `.claude-ops/playbooks/`.

"Adding a new health check" means writing a markdown file that describes what to look for. There is no code to compile, no schema to validate, no interpreter to maintain.

### Consequences

**Positive:**

- Extremely low barrier to contribution. Any engineer can write a check or playbook by describing the procedure in plain English with command examples.
- Claude can exercise judgment within the instructions. For example, `checks/http.md` says "if a service has a dedicated /health endpoint, prefer that over the root URL" -- Claude adapts per-service without needing per-service configuration.
- Instructions naturally accommodate special cases, edge conditions, and contextual guidance that would require complex branching logic in code or DSL formats.
- Documents are self-documenting. The check description IS the documentation. There is no drift between what the code does and what the docs say.
- Repo-specific extensions are trivial to add. A team writes a markdown file, drops it in `.claude-ops/checks/`, and Claude picks it up on the next run.
- No build step, no compilation, no schema validation required. Changes take effect immediately.

**Negative:**

- Execution is non-deterministic. Claude may interpret the same markdown slightly differently across runs, especially for ambiguous instructions.
- No static analysis or linting. A typo in a command example inside markdown will not be caught until runtime.
- Harder to unit test. There is no function to call with known inputs and assert outputs -- the "test" is whether Claude does the right thing when it reads the document.
- Debugging failures requires reading agent logs to understand how Claude interpreted the instructions, rather than stepping through deterministic code.
- Performance overhead: Claude must read and reason about prose on every run, whereas a compiled script or parsed DSL would execute directly.

## Pros and Cons of the Options

### Markdown Instructions Interpreted by Claude

- Good, because it is the most natural input format for an AI agent -- Claude is trained on and excels at interpreting natural language instructions.
- Good, because it handles ambiguity and special cases gracefully through prose rather than branching logic.
- Good, because the authoring experience is accessible to anyone who can write documentation.
- Good, because instructions are self-documenting -- the procedure description is the documentation.
- Good, because extending the system (via `.claude-ops/` directories in mounted repos) requires only writing a markdown file.
- Bad, because execution is non-deterministic and may vary between runs.
- Bad, because there is no static validation -- errors in command examples are only caught at runtime.
- Bad, because it is difficult to unit test individual checks or playbooks in isolation.

### Shell Scripts / Executable Code

- Good, because execution is deterministic and reproducible.
- Good, because scripts can be tested, linted, and validated before deployment.
- Good, because debugging follows standard practices (exit codes, stderr, set -x).
- Good, because there is a large ecosystem of existing monitoring scripts to draw from.
- Bad, because scripts are rigid -- handling special cases requires explicit branching logic for every condition.
- Bad, because the AI agent becomes a script runner rather than leveraging its reasoning capabilities.
- Bad, because contributing a new check requires programming knowledge (Bash proficiency, error handling, output formatting).
- Bad, because scripts must handle their own output formatting, error reporting, and state management.
- Bad, because contextual adaptation (e.g., "prefer /health if available") requires the script to enumerate and handle every possible variation.

### YAML/JSON DSL

- Good, because it provides structure and can be validated against a schema.
- Good, because it is more accessible than shell scripting for simple check definitions.
- Good, because structured data is easier to process programmatically (reporting, dashboards).
- Neutral, because it can express simple checks well but becomes unwieldy for complex conditional logic.
- Bad, because it requires designing, documenting, and maintaining a custom DSL -- which is itself a significant project.
- Bad, because DSLs inevitably hit expressiveness limits, leading to escape hatches (inline scripts, custom plugins) that undermine the structure.
- Bad, because it adds a parsing and validation layer between the author's intent and execution.
- Bad, because contextual guidance ("if the service redirects to a setup wizard, note this but don't flag as unhealthy") is awkward to express in structured formats.
- Bad, because it creates a learning curve for contributors who must learn the DSL syntax and semantics.

### Hybrid Approach (Structured YAML + Markdown Documentation)

- Good, because structured definitions enable schema validation and tooling support.
- Good, because markdown sections can still provide contextual guidance and special-case documentation.
- Good, because it could offer a migration path -- start structured, fall back to prose for complex cases.
- Bad, because it creates two sources of truth -- the YAML definition and the markdown guidance -- which can drift apart.
- Bad, because it inherits complexity from both approaches without fully committing to either.
- Bad, because contributors must understand both the YAML schema and the markdown conventions.
- Bad, because the AI agent must reconcile structured definitions with prose guidance when they conflict.
- Bad, because it adds maintenance burden: changes may need to be reflected in both the YAML and the markdown sections.
