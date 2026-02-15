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

- [ADR-0010: Claude Code CLI Subprocess](/docs/adrs/ADR-0010-claude-code-cli-subprocess.md)
- [ADR-0001: Tiered Model Escalation](/docs/adrs/ADR-0001-tiered-model-escalation.md)
- [ADR-0002: Markdown Executable Instructions](/docs/adrs/ADR-0002-markdown-executable-instructions.md)
- [Claude Code CLI Documentation](https://docs.anthropic.com/en/docs/claude-code)
