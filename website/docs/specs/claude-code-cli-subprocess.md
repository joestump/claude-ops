---
sidebar_position: 10
sidebar_label: Claude Code CLI Subprocess
---

# SPEC-0010: Claude Code CLI Subprocess Invocation

## Overview

Claude Ops invokes Claude models at runtime by shelling out to the Claude Code CLI (`@anthropic-ai/claude-code`) as a subprocess from the Bash entrypoint script. This approach preserves the project's zero-application-code architecture: no Python scripts, no TypeScript applications, no custom agent loops. The CLI is installed globally via npm in the Docker image and invoked with command-line flags that control model selection, prompt loading, tool filtering, system prompt injection, and output mode.

The CLI provides built-in support for MCP server management (reading `.claude/mcp.json`), the Task tool for subagent spawning (enabling tiered escalation), and `--allowedTools` for runtime permission enforcement. These capabilities would otherwise require significant application code to implement.

This specification defines the requirements for how Claude Ops invokes the Claude Code CLI, what features it relies on, and how the subprocess invocation integrates with the entrypoint loop, environment configuration, and tiered escalation model.

## Definitions

- **Claude Code CLI**: The `@anthropic-ai/claude-code` npm package, which provides a `claude` command-line tool for invoking Claude models with tool use, MCP support, and agent capabilities.
- **Subprocess invocation**: Executing the `claude` binary from within the entrypoint shell script using standard shell command syntax, capturing output via stdout/stderr.
- **Zero-application-code architecture**: A design constraint requiring that Claude Ops contains no compiled code, no application runtime in Python/TypeScript/etc., and no `src/` directory. All intelligence lives in markdown prompts; all orchestration lives in a Bash entrypoint script.
- **Prompt file**: A markdown document (`prompts/tier{1,2,3}-*.md`) that defines the agent's instructions, permissions, and behavior for a given tier.
- **Tool filtering**: The `--allowedTools` CLI flag that restricts which tools the agent can invoke at runtime, enforcing permission tiers independently of prompt instructions.
- **System prompt injection**: The `--append-system-prompt` CLI flag that adds runtime context (environment variables, state paths) to the agent's system prompt without modifying the prompt file.
- **MCP server management**: The CLI's native ability to read `.claude/mcp.json`, start configured MCP servers, manage their lifecycle, and route tool calls to the appropriate server.
- **Task tool**: A built-in CLI capability that allows the agent to spawn subagents with different models and prompts, used for tiered escalation.

## Requirements

### REQ-1: CLI installation via npm

The Claude Code CLI MUST be installed globally via `npm install -g @anthropic-ai/claude-code` in the Dockerfile. The installation MUST make the `claude` command available on the system PATH. The CLI MUST NOT require any additional configuration files, initialization commands, or post-install setup beyond the `ANTHROPIC_API_KEY` environment variable.

#### Scenario: CLI is available in the container

Given the Docker image has been built from the Dockerfile
When the entrypoint script starts
Then the `claude` command is available on the system PATH
And `claude --version` returns successfully

#### Scenario: CLI authenticates via environment variable

Given the container is started with `ANTHROPIC_API_KEY` set in the environment
When the entrypoint invokes `claude`
Then the CLI authenticates with the Anthropic API using the environment variable
And no interactive login or configuration file is required

### REQ-2: Subprocess invocation from Bash

The entrypoint script MUST invoke the Claude Code CLI as a subprocess using standard Bash shell syntax. The invocation MUST use command-line flags for all configuration -- model, prompt file, tool filtering, system prompt, and output mode. The invocation MUST NOT require piping input, interactive terminal sessions, or manual tool result handling.

The canonical invocation form MUST be:

```bash
claude \
    --model "${MODEL}" \
    --print \
    --prompt-file "${PROMPT_FILE}" \
    --allowedTools "${ALLOWED_TOOLS}" \
    --append-system-prompt "Environment: ${ENV_CONTEXT}"
```

#### Scenario: Standard tier 1 invocation

Given the entrypoint is in its health check loop
And MODEL is set to "haiku"
And PROMPT_FILE points to "prompts/tier1-observe.md"
And ALLOWED_TOOLS is set to "Bash,Read,Grep,Glob,Task,WebFetch"
When the entrypoint invokes the claude command
Then the CLI runs with the haiku model
And loads the tier 1 prompt from the specified file
And restricts available tools to the allowed list
And operates in non-interactive print mode

#### Scenario: Invocation output is captured

Given the entrypoint invokes the claude command
When the CLI produces output
Then stdout and stderr are piped through `tee` to a log file
And the output is also visible in the container's stdout for `docker compose logs`

### REQ-3: Model selection via --model flag

The CLI invocation MUST specify the model using the `--model` flag. The model value MUST be configurable via environment variables (`CLAUDEOPS_TIER1_MODEL`, `CLAUDEOPS_TIER2_MODEL`, `CLAUDEOPS_TIER3_MODEL`) with defaults of `haiku`, `sonnet`, and `opus` respectively.

Each tier invocation MUST use the model appropriate to its tier level, enabling the cost/capability trade-off defined in the tiered escalation model (ADR-0001).

#### Scenario: Tier 1 uses haiku by default

Given `CLAUDEOPS_TIER1_MODEL` is not set in the environment
When the entrypoint invokes the claude command for the tier 1 check
Then the `--model` flag is set to `haiku`

#### Scenario: Operator overrides tier model

Given the operator sets `CLAUDEOPS_TIER1_MODEL=sonnet` in the `.env` file
When the entrypoint invokes the claude command for the tier 1 check
Then the `--model` flag is set to `sonnet`

#### Scenario: Subagent uses escalated model

Given a tier 1 agent spawns a tier 2 subagent via the Task tool
When the subagent is created
Then the subagent uses the model specified by `CLAUDEOPS_TIER2_MODEL`

### REQ-4: Prompt loading via --prompt-file

The CLI invocation MUST load the agent's instructions from a markdown file using the `--prompt-file` flag. The prompt file path MUST be configurable via the `CLAUDEOPS_PROMPT` environment variable with a default of `/app/prompts/tier1-observe.md`.

The prompt file MUST be a markdown document that the CLI loads as the agent's primary instructions. The CLI MUST NOT require any special formatting, frontmatter, or non-markdown syntax in the prompt file.

#### Scenario: Default prompt file is loaded

Given `CLAUDEOPS_PROMPT` is not set
When the entrypoint invokes the claude command
Then the `--prompt-file` flag is set to `/app/prompts/tier1-observe.md`
And the CLI reads and uses the file's contents as agent instructions

#### Scenario: Custom prompt file

Given the operator sets `CLAUDEOPS_PROMPT=/app/prompts/custom-check.md`
When the entrypoint invokes the claude command
Then the CLI loads instructions from the custom prompt file

### REQ-5: Tool filtering via --allowedTools

The CLI invocation MUST restrict available tools using the `--allowedTools` flag. The allowed tools list MUST be configurable via the `CLAUDEOPS_ALLOWED_TOOLS` environment variable with a default of `Bash,Read,Grep,Glob,Task,WebFetch`.

Tool filtering MUST be enforced at the CLI runtime level, not just the prompt level. If a tool is not in the allowed list, the agent MUST NOT be able to invoke it regardless of prompt instructions or reasoning. This provides defense-in-depth for the permission tier model.

#### Scenario: Tier 1 cannot use remediation tools

Given the tier 1 invocation uses `--allowedTools "Bash,Read,Grep,Glob,Task,WebFetch"`
When the tier 1 agent attempts to use a tool not in the allowed list (e.g., `Edit` or `Write`)
Then the CLI blocks the tool invocation
And the agent cannot modify files regardless of its prompt instructions

#### Scenario: Tier 2 with expanded tool access

Given a tier 2 subagent is spawned with an expanded allowed tools list
When the tier 2 agent needs to perform safe remediation
Then it has access to the additional tools specified in its allowed list

### REQ-6: Runtime context injection via --append-system-prompt

The CLI invocation MUST inject runtime environment context using the `--append-system-prompt` flag. The appended context MUST include:

- `CLAUDEOPS_DRY_RUN` -- whether the agent is in observe-only mode
- `CLAUDEOPS_STATE_DIR` -- path to the cooldown state directory
- `CLAUDEOPS_RESULTS_DIR` -- path to the results/logs directory
- `CLAUDEOPS_REPOS_DIR` -- path to the mounted repos directory
- `CLAUDEOPS_TIER2_MODEL` -- model for tier 2 escalation
- `CLAUDEOPS_TIER3_MODEL` -- model for tier 3 escalation
- `CLAUDEOPS_APPRISE_URLS` -- notification service URLs (if configured)

This mechanism MUST allow runtime values to reach the agent without modifying the prompt files, keeping prompt files as static, version-controlled documents.

#### Scenario: Dry run mode is communicated to the agent

Given `CLAUDEOPS_DRY_RUN=true` is set in the environment
When the entrypoint builds the ENV_CONTEXT string
And appends it via `--append-system-prompt`
Then the agent receives `CLAUDEOPS_DRY_RUN=true` in its system prompt
And knows to operate in observe-only mode

#### Scenario: Apprise URLs are conditionally included

Given `CLAUDEOPS_APPRISE_URLS` is set to a non-empty value
When the entrypoint builds the ENV_CONTEXT string
Then the Apprise URLs are included in the appended system prompt
And the agent can use them for sending notifications

#### Scenario: Apprise URLs are omitted when not configured

Given `CLAUDEOPS_APPRISE_URLS` is not set or is empty
When the entrypoint builds the ENV_CONTEXT string
Then the Apprise URLs key is NOT included in the appended system prompt

### REQ-7: Non-interactive output via --print

The CLI invocation MUST use the `--print` flag to operate in non-interactive mode. In this mode, the CLI MUST:

- Produce output as streaming text on stdout, not as an interactive terminal UI.
- Exit when the agent's task is complete (no interactive prompt for follow-up).
- Be compatible with Unix piping (`|`), output redirection (`>`), and `tee` for simultaneous display and logging.

#### Scenario: Output is logged to file

Given the entrypoint invokes the claude command with `--print`
And pipes output through `tee -a "${LOG_FILE}"`
When the agent produces output during its health check run
Then the output is written to the log file in `/results/`
And is simultaneously visible in the container's stdout

#### Scenario: CLI exits after completion

Given the entrypoint invokes the claude command with `--print`
When the agent completes its health check and any escalations
Then the CLI process exits
And the entrypoint proceeds to the sleep interval

### REQ-8: MCP server configuration

The CLI MUST read MCP server configuration from `.claude/mcp.json` within the working directory (`/app`). The entrypoint MUST merge MCP configurations from mounted repos into this file before each CLI invocation.

The CLI MUST handle MCP server lifecycle management -- starting configured servers, managing connections, and shutting them down when the CLI process exits. The entrypoint MUST NOT need to manage MCP server processes directly.

#### Scenario: Baseline MCP config is loaded

Given `.claude/mcp.json` exists in `/app/`
And it defines MCP servers (e.g., Docker, Postgres)
When the CLI starts
Then it reads the MCP config and starts the configured servers
And makes MCP-provided tools available to the agent

#### Scenario: Repo MCP configs are merged before invocation

Given a mounted repo at `/repos/my-infra/` has `.claude-ops/mcp.json`
When the entrypoint's `merge_mcp_configs` function runs before CLI invocation
Then the repo's MCP servers are added to the baseline `.claude/mcp.json`
And the CLI uses the merged configuration

#### Scenario: MCP servers are cleaned up on exit

Given the CLI has started MCP servers from the merged config
When the CLI process exits (agent task complete)
Then MCP server processes are cleaned up by the CLI
And no orphaned MCP server processes remain

### REQ-9: Subagent spawning via the Task tool

The CLI MUST support the Task tool for spawning subagents with different models and prompts. This is the mechanism for tiered escalation: Tier 1 spawns Tier 2, Tier 2 spawns Tier 3.

When a subagent is spawned via the Task tool, the spawning agent MUST be able to:
- Specify the subagent's model.
- Pass the full context of findings to the subagent.
- Receive the subagent's results.

The Task tool MUST work without any custom orchestration code. The CLI provides this capability natively.

#### Scenario: Tier 1 escalates to Tier 2

Given the tier 1 agent has identified issues requiring investigation
When the agent uses the Task tool to spawn a subagent
And specifies the tier 2 model and passes failure context
Then a tier 2 subagent starts with the specified model
And receives the full context from tier 1
And its results are returned to the tier 1 agent

#### Scenario: Tier 2 escalates to Tier 3

Given the tier 2 agent cannot resolve an issue with safe remediation
When the agent uses the Task tool to spawn a tier 3 subagent
And passes investigation findings and attempted remediation details
Then a tier 3 subagent starts with the opus model
And receives the complete investigation context
And can perform full remediation actions

### REQ-10: Error handling and exit codes

The entrypoint MUST handle CLI exit codes gracefully. A non-zero exit code from the CLI MUST NOT terminate the entrypoint loop. The entrypoint MUST continue to the sleep interval and run the next cycle regardless of the previous cycle's exit code.

The entrypoint MUST use `|| true` or equivalent to prevent `set -e` from terminating the loop on CLI failures.

#### Scenario: CLI exits with error

Given the CLI encounters an error (e.g., rate limit, network timeout, context window exceeded)
And exits with a non-zero exit code
When the entrypoint processes the exit
Then the error output is captured in the log file
And the entrypoint continues to the sleep interval
And the next health check cycle runs normally

#### Scenario: Repeated failures do not stop the loop

Given multiple consecutive CLI invocations fail
When each invocation exits with a non-zero code
Then the entrypoint continues looping and retrying on schedule
And each failure is logged to a separate run log file

### REQ-11: Zero application code constraint

The CLI invocation mechanism MUST NOT require any application code in Python, TypeScript, JavaScript, or any other programming language. The complete invocation MUST be expressible as a shell command within the Bash entrypoint script.

The project MUST NOT contain a `src/` directory, `main.py`, `index.ts`, `tsconfig.json`, or any compiled artifacts for the purpose of CLI invocation. Custom application code for features not provided by the CLI (e.g., custom tool implementations, structured result parsing) MUST NOT be introduced.

#### Scenario: No application code exists

Given the project repository
When an operator inspects the file structure
Then there is no `src/` directory
And there is no `main.py`, `index.ts`, or equivalent application entry point
And there are no compiled artifacts or build outputs
And the only executable is `entrypoint.sh`

#### Scenario: All features are CLI-provided

Given the system needs model selection, prompt loading, tool filtering, system prompt injection, MCP management, and subagent spawning
When these features are used in a health check cycle
Then all features are provided by CLI flags and built-in capabilities
And no custom code is needed to implement any of these features

## References

- [ADR-0010: Claude Code CLI Subprocess](../adrs/adr-0010)
- [ADR-0001: Tiered Model Escalation](../adrs/adr-0001)
- [ADR-0002: Markdown Executable Instructions](../adrs/adr-0002)
- [Claude Code CLI Documentation](https://docs.anthropic.com/en/docs/claude-code)

---

# Design: Claude Code CLI Subprocess Invocation

## Overview

Claude Ops invokes Claude models by executing the Claude Code CLI (`claude`) as a subprocess from a Bash entrypoint script. The CLI is installed globally via npm in the Docker image and called with command-line flags that configure every aspect of the agent's behavior: model, prompt, tool permissions, runtime context, and output mode. This design eliminates the need for application code, keeping the project as a pure AI agent runbook where all intelligence lives in markdown prompts and all orchestration lives in a ~95-line shell script.

## Architecture

### Invocation Chain

```
Docker Container
+-- entrypoint.sh (Bash, PID 1)
    +-- while true; do
        +-- merge_mcp_configs()          # Shell function: jq-based JSON merge
        +-- Build ENV_CONTEXT string     # Concatenate environment variables
        +-- claude \                     # Subprocess invocation
        |   --model "${MODEL}" \
        |   --print \
        |   --prompt-file "${PROMPT_FILE}" \
        |   --allowedTools "${ALLOWED_TOOLS}" \
        |   --append-system-prompt "..." \
        |   2>&1 | tee -a "${LOG_FILE}"
        |   |
        |   +-- CLI reads .claude/mcp.json -> starts MCP servers
        |   +-- CLI loads prompt file -> sets agent instructions
        |   +-- CLI applies tool filter -> enforces permissions
        |   +-- Agent executes: reads checks, runs commands, evaluates
        |   +-- Agent may spawn subagent via Task tool:
        |   |   +-- claude (tier 2/3) as nested subprocess
        |   +-- CLI exits -> MCP servers cleaned up
        |
        +-- sleep "${INTERVAL}"
```

The entrypoint runs as PID 1 in the container. Each health check cycle spawns a `claude` subprocess that lives for the duration of the agent's work (typically minutes), then exits. The entrypoint captures all output, sleeps, and loops.

### CLI as Black Box

The Claude Code CLI operates as a self-contained agent runtime. From the entrypoint's perspective, it is a black box:

**Inputs (controlled by the entrypoint):**
- `--model` -- which Claude model to use
- `--prompt-file` -- markdown instructions
- `--allowedTools` -- tool allowlist
- `--append-system-prompt` -- runtime environment context
- `--print` -- non-interactive output mode
- `.claude/mcp.json` -- MCP server configuration (file, managed by entrypoint)
- `ANTHROPIC_API_KEY` -- authentication (environment variable)

**Outputs (consumed by the entrypoint):**
- stdout/stderr -- agent output (text, captured via `tee`)
- Exit code -- 0 for success, non-zero for failure (ignored via `|| true`)
- Side effects -- files written to `/state`, `/results`; commands executed against infrastructure

The entrypoint has no visibility into the CLI's internal reasoning, tool execution sequence, or MCP communication. This is an intentional trade-off: operational simplicity (one shell command) in exchange for observability (no structured execution trace).

### Environment Variable Flow

Environment variables flow through three layers:

```
Layer 1: .env file (host)
    | Docker Compose interpolation
Layer 2: Container environment
    | entrypoint.sh reads with ${VAR:-default}
Layer 3: CLI invocation
    +-- --model (direct flag)
    +-- --prompt-file (direct flag)
    +-- --allowedTools (direct flag)
    +-- --append-system-prompt (bundled as ENV_CONTEXT string)
```

Some variables map directly to CLI flags (model, prompt file, allowed tools). Others are bundled into a single `ENV_CONTEXT` string and injected via `--append-system-prompt`. This distinction exists because:

- **Direct flags** control CLI behavior (which model to load, which tools to enable).
- **System prompt injection** passes information to the agent (paths, modes, URLs) that the agent needs to reason about but the CLI does not.

The `ENV_CONTEXT` is built as a space-separated key=value string:

```bash
ENV_CONTEXT="CLAUDEOPS_DRY_RUN=${DRY_RUN}"
ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_STATE_DIR=${STATE_DIR}"
# ... more variables appended
```

This is intentionally simple -- no JSON serialization, no structured format. The agent receives it as plain text in its system prompt and parses it naturally through language understanding.

### Permission Enforcement

Tool filtering via `--allowedTools` provides runtime-level permission enforcement that operates independently of prompt instructions:

```
Prompt instructions (soft):     "You may only read files"
--allowedTools (hard):          "Bash,Read,Grep,Glob,Task,WebFetch"
                                     |
                            CLI enforces: Edit, Write, etc. are BLOCKED
                            even if agent reasons it should use them
```

This creates a defense-in-depth model:
1. **Prompt layer** -- tells the agent what it should do (advisory).
2. **CLI layer** -- enforces what the agent can do (mandatory).

If a prompt injection, reasoning error, or ambiguous instruction leads the agent to attempt a disallowed tool, the CLI blocks it. This is particularly important for Tier 1 (observe-only), where the agent must not be able to perform remediation even if a compromised check file instructs it to.

### MCP Configuration Lifecycle

```
Before each CLI invocation:

1. entrypoint.sh: merge_mcp_configs()
   +-- Copy baseline .claude/mcp.json.baseline -> .claude/mcp.json
   +-- For each /repos/*/.claude-ops/mcp.json:
   |   +-- jq merge: repo mcpServers override baseline on collision
   +-- Result: merged .claude/mcp.json ready for CLI

2. CLI starts:
   +-- Reads .claude/mcp.json
   +-- Starts each configured MCP server process
   +-- Establishes connections
   +-- Makes MCP tools available to agent

3. CLI exits:
   +-- Cleans up MCP server processes
```

The entrypoint manages the MCP configuration file; the CLI manages the MCP server processes. This separation keeps the entrypoint's responsibility limited to JSON file manipulation (via `jq`), while the CLI handles the complex work of process lifecycle management.

The merge strategy uses `jq`'s `*` operator, which performs a recursive merge where repo values override baseline values on key collision. This allows mounted repos to customize or replace baseline MCP servers.

## Data Flow

### Single Health Check Cycle

```
entrypoint.sh
|
+-- Timestamp and log file setup
|   RUN_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
|   LOG_FILE="${RESULTS_DIR}/run-$(date +%Y%m%d-%H%M%S).log"
|
+-- MCP config merge
|   merge_mcp_configs()
|
+-- Environment context assembly
|   ENV_CONTEXT="CLAUDEOPS_DRY_RUN=... CLAUDEOPS_STATE_DIR=... ..."
|
+-- CLI subprocess invocation
|   claude --model haiku --print --prompt-file tier1-observe.md \
|          --allowedTools "Bash,Read,Grep,Glob,Task,WebFetch" \
|          --append-system-prompt "Environment: ${ENV_CONTEXT}" \
|   |
|   |  Agent reads checks/*.md -> decides what to check
|   |  Agent runs health checks -> curl, dig, docker ps, etc.
|   |  Agent reads cooldown.json -> checks remediation limits
|   |  Agent evaluates results -> healthy or unhealthy
|   |
|   +-- If healthy: reports status, updates cooldown.json
|   |
|   +-- If unhealthy: spawns Tier 2 subagent via Task tool
|       +-- claude --model sonnet (nested subprocess)
|           |  Receives failure context from Tier 1
|           |  Attempts safe remediation (restart, clear cache)
|           |  Updates cooldown.json
|           |
|           +-- If unresolved: spawns Tier 3 via Task tool
|               +-- claude --model opus (nested subprocess)
|                   Receives full investigation findings
|                   Attempts full remediation
|                   Updates cooldown.json and sends notifications
|
+-- Output captured via tee -> LOG_FILE
|
+-- sleep ${INTERVAL}
```

### Subagent Spawning

Subagent spawning via the Task tool creates nested subprocess invocations:

```
PID tree during escalation:

entrypoint.sh (PID 1)
+-- claude --model haiku (tier 1)
    +-- claude --model sonnet (tier 2, spawned via Task tool)
        +-- claude --model opus (tier 3, spawned via Task tool)
```

Each nested agent:
- Runs as a separate CLI process.
- Has its own model, prompt, and tool permissions.
- Receives context from its parent via the Task tool's prompt parameter.
- Returns results to its parent when it exits.

The parent agent controls what context is passed to the child. This is how the tiered escalation model maintains context continuity: Tier 1 passes failure details to Tier 2, Tier 2 passes investigation findings to Tier 3.

## Key Decisions

### CLI over SDK

The CLI was chosen over the Anthropic Python/TypeScript SDKs because it preserves the zero-application-code constraint. An SDK approach would require a Python or TypeScript application to handle tool execution, MCP connections, and subagent coordination -- capabilities the CLI provides for free.

**Reference:** ADR-0010 documents the full evaluation of CLI vs. SDK vs. Agent SDK approaches.

### `|| true` for error suppression

The CLI invocation uses `|| true` to prevent non-zero exit codes from triggering `set -e` and killing the entrypoint loop. This is a deliberate choice: the entrypoint must keep running on schedule regardless of individual cycle failures. Errors are still captured in log files via `tee`.

The trade-off is that there is no structured error handling -- all failures (rate limits, auth errors, context window exceeded, network timeouts) are treated the same: log and continue.

### String-based ENV_CONTEXT over structured format

Runtime context is passed as a space-separated key=value string rather than JSON or YAML. This is intentional:
- The agent (an LLM) parses natural language natively -- a structured format adds complexity without benefit.
- The entrypoint avoids JSON escaping complexity in Bash.
- Adding a new variable requires only appending one line to the entrypoint.

### `--print` over interactive mode

The `--print` flag forces non-interactive output. Without it, the CLI presents an interactive terminal UI that requires user input. Since Claude Ops runs unattended in a container, interactive mode would hang indefinitely.

`--print` produces streaming text output compatible with Unix piping. The output is human-readable but not machine-parseable in a structured way. If structured output becomes necessary (e.g., for a dashboard), a log processing pipeline would need to be added downstream.

## Trade-offs

### Gained

- **Zero application code**: The entire Claude invocation is one shell command. No Python, no TypeScript, no build step, no dependency management beyond npm.
- **Feature completeness for free**: Model selection, prompt loading, tool filtering, system prompt injection, MCP management, and subagent spawning are all CLI-provided features requiring zero custom implementation.
- **Runtime permission enforcement**: `--allowedTools` provides defense-in-depth that operates independently of prompt-level instructions, hardening the permission tier model.
- **Operational simplicity**: Debugging means reading log files. Upgrading means bumping an npm package version. There is no application state, no database, no configuration files to migrate.
- **Minimal entrypoint**: The entire orchestration logic is ~95 lines of Bash, understandable by any engineer familiar with shell scripting.

### Lost

- **Observability into agent execution**: The CLI is a black box. There is no structured execution trace, no tool call logs, no MCP communication visibility. The operator sees only the agent's text output.
- **Structured error handling**: Exit codes and stderr text are the only error signals. An SDK would provide typed errors (rate limit with retry-after, auth failure, context window exceeded) that could be handled programmatically.
- **Process efficiency**: Each cycle starts a new CLI process, which initializes MCP servers and establishes connections. A long-running SDK-based process would maintain connections across cycles, reducing overhead.
- **CLI coupling**: The project depends on the Claude Code CLI's command-line interface remaining stable. Flag renames, behavioral changes, or breaking updates to the npm package could require entrypoint changes.
- **Image size**: The full Claude Code CLI npm package adds significant size to the Docker image compared to a lightweight SDK.

## Future Considerations

- **Structured output mode**: If the CLI adds a `--json` or `--output-format` flag in the future, it could enable structured result parsing for dashboards and metrics collection.
- **Persistent CLI process**: If the CLI supports a daemon or server mode, the entrypoint could maintain a long-running process and send prompts per cycle, eliminating process startup overhead.
- **CLI version pinning**: The Dockerfile currently installs the latest CLI version (`npm install -g @anthropic-ai/claude-code`). Pinning to a specific version (`@anthropic-ai/claude-code@X.Y.Z`) would improve build reproducibility at the cost of manual version management.
- **Exit code semantics**: If the CLI adopts structured exit codes (e.g., 1 for agent error, 2 for auth failure, 3 for rate limit), the entrypoint could implement differentiated error handling.
- **Log format standardization**: Adding structured log lines (JSON) alongside human-readable output would enable log aggregation and alerting without post-processing.
