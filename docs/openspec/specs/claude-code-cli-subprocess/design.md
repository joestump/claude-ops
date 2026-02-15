# Design: Claude Code CLI Subprocess Invocation

## Overview

Claude Ops invokes Claude models by executing the Claude Code CLI (`claude`) as a subprocess from a Bash entrypoint script. The CLI is installed globally via npm in the Docker image and called with command-line flags that configure every aspect of the agent's behavior: model, prompt, tool permissions, runtime context, and output mode. This design eliminates the need for application code, keeping the project as a pure AI agent runbook where all intelligence lives in markdown prompts and all orchestration lives in a ~95-line shell script.

## Architecture

### Invocation Chain

```
Docker Container
└── entrypoint.sh (Bash, PID 1)
    └── while true; do
        ├── merge_mcp_configs()          # Shell function: jq-based JSON merge
        ├── Build ENV_CONTEXT string     # Concatenate environment variables
        ├── claude \                     # Subprocess invocation
        │   --model "${MODEL}" \
        │   --print \
        │   --prompt-file "${PROMPT_FILE}" \
        │   --allowedTools "${ALLOWED_TOOLS}" \
        │   --append-system-prompt "..." \
        │   2>&1 | tee -a "${LOG_FILE}"
        │   │
        │   ├── CLI reads .claude/mcp.json → starts MCP servers
        │   ├── CLI loads prompt file → sets agent instructions
        │   ├── CLI applies tool filter → enforces permissions
        │   ├── Agent executes: reads checks, runs commands, evaluates
        │   ├── Agent may spawn subagent via Task tool:
        │   │   └── claude (tier 2/3) as nested subprocess
        │   └── CLI exits → MCP servers cleaned up
        │
        └── sleep "${INTERVAL}"
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
    ↓ Docker Compose interpolation
Layer 2: Container environment
    ↓ entrypoint.sh reads with ${VAR:-default}
Layer 3: CLI invocation
    ├── --model (direct flag)
    ├── --prompt-file (direct flag)
    ├── --allowedTools (direct flag)
    └── --append-system-prompt (bundled as ENV_CONTEXT string)
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
                                     ↓
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
   ├── Copy baseline .claude/mcp.json.baseline → .claude/mcp.json
   ├── For each /repos/*/.claude-ops/mcp.json:
   │   └── jq merge: repo mcpServers override baseline on collision
   └── Result: merged .claude/mcp.json ready for CLI

2. CLI starts:
   ├── Reads .claude/mcp.json
   ├── Starts each configured MCP server process
   ├── Establishes connections
   └── Makes MCP tools available to agent

3. CLI exits:
   └── Cleans up MCP server processes
```

The entrypoint manages the MCP configuration file; the CLI manages the MCP server processes. This separation keeps the entrypoint's responsibility limited to JSON file manipulation (via `jq`), while the CLI handles the complex work of process lifecycle management.

The merge strategy uses `jq`'s `*` operator, which performs a recursive merge where repo values override baseline values on key collision. This allows mounted repos to customize or replace baseline MCP servers.

## Data Flow

### Single Health Check Cycle

```
entrypoint.sh
│
├── Timestamp and log file setup
│   RUN_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
│   LOG_FILE="${RESULTS_DIR}/run-$(date +%Y%m%d-%H%M%S).log"
│
├── MCP config merge
│   merge_mcp_configs()
│
├── Environment context assembly
│   ENV_CONTEXT="CLAUDEOPS_DRY_RUN=... CLAUDEOPS_STATE_DIR=... ..."
│
├── CLI subprocess invocation
│   claude --model haiku --print --prompt-file tier1-observe.md \
│          --allowedTools "Bash,Read,Grep,Glob,Task,WebFetch" \
│          --append-system-prompt "Environment: ${ENV_CONTEXT}" \
│   │
│   │  Agent reads checks/*.md → decides what to check
│   │  Agent runs health checks → curl, dig, docker ps, etc.
│   │  Agent reads cooldown.json → checks remediation limits
│   │  Agent evaluates results → healthy or unhealthy
│   │
│   ├── If healthy: reports status, updates cooldown.json
│   │
│   └── If unhealthy: spawns Tier 2 subagent via Task tool
│       └── claude --model sonnet (nested subprocess)
│           │  Receives failure context from Tier 1
│           │  Attempts safe remediation (restart, clear cache)
│           │  Updates cooldown.json
│           │
│           └── If unresolved: spawns Tier 3 via Task tool
│               └── claude --model opus (nested subprocess)
│                   Receives full investigation findings
│                   Attempts full remediation
│                   Updates cooldown.json and sends notifications
│
├── Output captured via tee → LOG_FILE
│
└── sleep ${INTERVAL}
```

### Subagent Spawning

Subagent spawning via the Task tool creates nested subprocess invocations:

```
PID tree during escalation:

entrypoint.sh (PID 1)
└── claude --model haiku (tier 1)
    └── claude --model sonnet (tier 2, spawned via Task tool)
        └── claude --model opus (tier 3, spawned via Task tool)
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
