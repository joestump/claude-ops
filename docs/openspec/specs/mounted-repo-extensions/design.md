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
