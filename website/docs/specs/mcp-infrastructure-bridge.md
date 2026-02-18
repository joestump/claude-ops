---
sidebar_position: 6
sidebar_label: MCP Infrastructure Bridge
---

# SPEC-0006: MCP Infrastructure Bridge

## Overview

Claude Ops is an AI agent that monitors and remediates infrastructure from inside a Docker container. To perform its job, it must interact with diverse infrastructure components: Docker containers, PostgreSQL databases, web UIs (for credential rotation), and arbitrary HTTP endpoints. This specification defines the use of Model Context Protocol (MCP) servers as the primary access layer for all infrastructure interactions.

MCP servers run as stdio subprocesses spawned via `npx -y <package>` and expose typed tool interfaces that the Claude Code CLI can invoke directly. A baseline set of four MCP servers covers core infrastructure access patterns, and mounted repos can extend this set by providing additional MCP server definitions that are merged into the configuration before each monitoring cycle.

## Definitions

- **MCP (Model Context Protocol)**: A standard protocol for connecting AI models to external tools and data sources, using JSON Schema-defined tool interfaces over stdio communication.
- **MCP server**: A subprocess that implements the MCP protocol, exposing a set of typed tools that the agent can invoke. Spawned via `npx -y <package>` as a stdio child process.
- **Baseline MCP config**: The MCP server definitions shipped with the Claude Ops container at `/app/.claude/mcp.json`, covering core infrastructure access patterns.
- **Repo MCP config**: Additional MCP server definitions provided by a mounted repo at `.claude-ops/mcp.json`, merged into the baseline before each cycle.
- **Merged config**: The final MCP configuration after combining baseline and all repo configs, written to `/app/.claude/mcp.json` before the Claude CLI process starts.
- **Tool schema**: A JSON Schema definition of a tool's parameters and return type, provided by an MCP server so the agent can reason about how to invoke it.
- **Browser sidecar**: A headless Chromium container (`browserless/chromium`) that provides a Chrome DevTools Protocol endpoint for browser automation via the Chrome DevTools MCP server.

## Requirements

### REQ-1: MCP as Primary Infrastructure Access Layer

The system MUST use MCP servers as the primary mechanism for the agent to interact with infrastructure components. The agent SHOULD prefer MCP tool invocations over raw CLI commands (via the Bash tool) when an MCP server provides equivalent functionality.

#### Scenario: Agent uses Docker MCP for container inspection
Given the Docker MCP server is configured and running
When the agent needs to inspect container state during health checks
Then the agent uses Docker MCP tools rather than running `docker inspect` via Bash

#### Scenario: Agent uses Fetch MCP for HTTP health checks
Given the Fetch MCP server is configured and running
When the agent needs to perform an HTTP health check against a service endpoint
Then the agent uses the Fetch MCP server rather than running `curl` via Bash

#### Scenario: Fallback to Bash when MCP is insufficient
Given the agent needs to perform an operation not supported by any configured MCP server
When no MCP tool provides the required functionality
Then the agent MAY fall back to using the Bash tool with direct CLI commands

### REQ-2: Baseline MCP Server Set

The system MUST ship with a baseline set of MCP servers configured in `/app/.claude/mcp.json`. The baseline MUST include the following servers:

1. **Docker MCP** (`@anthropic-ai/mcp-docker`): Container inspection, management, and lifecycle operations.
2. **PostgreSQL MCP** (`@anthropic-ai/mcp-postgres`): Database querying and inspection.
3. **Chrome DevTools MCP** (`@anthropic-ai/mcp-chrome-devtools`): Browser automation for web UIs that lack programmatic APIs.
4. **Fetch MCP** (`@anthropic-ai/mcp-fetch`): General HTTP request capabilities.

#### Scenario: All baseline servers present
Given the Claude Ops container starts with its default configuration
When the agent reads `/app/.claude/mcp.json`
Then the config contains entries for `docker`, `postgres`, `chrome-devtools`, and `fetch` MCP servers

#### Scenario: Docker MCP provides container tools
Given the Docker MCP server is configured
When the agent invokes Docker MCP tools
Then it can list containers, inspect container state, check health status, view logs, and manage container lifecycle

#### Scenario: PostgreSQL MCP provides database tools
Given the PostgreSQL MCP server is configured
And `$CLAUDEOPS_POSTGRES_URL` is set to a valid connection string
When the agent invokes PostgreSQL MCP tools
Then it can execute read-only queries against the configured database

#### Scenario: Chrome DevTools MCP enables browser automation
Given the Chrome DevTools MCP server is configured
And the browser sidecar container is running with CDP endpoint `ws://chrome:9222`
When the agent invokes Chrome DevTools MCP tools
Then it can navigate pages, take snapshots, fill forms, click elements, and extract page content

#### Scenario: Fetch MCP provides HTTP capabilities
Given the Fetch MCP server is configured
When the agent invokes Fetch MCP tools
Then it can make HTTP requests to arbitrary endpoints and process responses

### REQ-3: MCP Server Spawning via npx

MCP servers MUST be spawned as stdio subprocesses using `npx -y <package>`. The `npx -y` flag ensures automatic installation without interactive prompts. The system MUST NOT require pre-installation of MCP server packages in the container image (though pre-installation MAY be used for performance optimization).

#### Scenario: MCP server started on demand
Given an MCP server is defined in the config with `"command": "npx"` and `"args": ["-y", "@anthropic-ai/mcp-docker"]`
When the Claude CLI starts and needs to use the Docker MCP server
Then `npx` downloads and starts the MCP server package as a stdio subprocess

#### Scenario: No pre-installation required
Given a new MCP server package `@custom/mcp-prometheus` is added to the config
And it is not pre-installed in the container
When the Claude CLI starts
Then `npx -y` fetches and runs the package from the npm registry

### REQ-4: MCP Server Environment Isolation

Each MCP server MUST run in its own subprocess with its own environment variables. Sensitive configuration (e.g., database connection strings, API keys) MUST be scoped to the specific MCP server that requires them via the `env` field in the MCP config.

#### Scenario: PostgreSQL credentials scoped to PostgreSQL MCP
Given the MCP config sets `POSTGRES_CONNECTION` in the `env` field of the `postgres` server
When the Docker MCP server runs
Then the Docker MCP server's process does not have access to `POSTGRES_CONNECTION`

#### Scenario: Custom MCP server with its own credentials
Given a repo provides an MCP server config with `"env": {"API_KEY": "secret-key"}`
When the MCP server is spawned
Then only that server's process has access to `API_KEY`

### REQ-5: Repo-Provided MCP Server Extension

Mounted repos MAY provide additional MCP server definitions in `.claude-ops/mcp.json`. These MUST be merged into the baseline configuration by the entrypoint script before each monitoring cycle, following the merge semantics defined in SPEC-0005 (Mounted Repo Extension Model, REQ-9).

#### Scenario: Repo adds custom MCP integration
Given a repo `infra-ansible` provides `.claude-ops/mcp.json` with an `ansible-inventory` server definition
When the entrypoint merges MCP configs before the next cycle
Then the merged config includes the `ansible-inventory` server alongside all baseline servers

#### Scenario: Repo overrides baseline server
Given a repo provides `.claude-ops/mcp.json` redefining the `postgres` server with a different connection
When the entrypoint merges configs
Then the repo's `postgres` definition replaces the baseline's `postgres` definition

#### Scenario: Merge happens before each cycle
Given the entrypoint runs the MCP merge function before invoking the Claude CLI
When the Claude CLI starts
Then it sees the fully merged MCP config with all repo-provided servers available

### REQ-6: MCP Config Structure

Each MCP server definition in the config MUST include the following fields:

- `type`: MUST be `"stdio"` for subprocess-based MCP servers.
- `command`: The command to execute (typically `"npx"`).
- `args`: An array of arguments passed to the command (e.g., `["-y", "@anthropic-ai/mcp-docker"]`).

Each definition MAY include:

- `env`: An object of environment variables passed to the MCP server subprocess.

#### Scenario: Valid MCP server config
Given a config entry for a server named `docker`
Then it contains `"type": "stdio"`, `"command": "npx"`, and `"args": ["-y", "@anthropic-ai/mcp-docker"]`

#### Scenario: Config with environment variables
Given a config entry for the `postgres` server
Then it contains an `env` field with `"POSTGRES_CONNECTION"` set to the database connection string

#### Scenario: Config without environment variables
Given a config entry for the `fetch` server
Then it contains `type`, `command`, and `args` but does not require an `env` field

### REQ-7: Browser Sidecar for Chrome DevTools MCP

The Chrome DevTools MCP server MUST connect to a headless Chromium instance via the Chrome DevTools Protocol (CDP). The system MUST support running a browser sidecar container (e.g., `browserless/chromium`) that exposes a CDP WebSocket endpoint. The Chrome DevTools MCP server's `CDP_ENDPOINT` environment variable MUST be configured to connect to this sidecar.

The browser sidecar MUST be an optional component, activated via Docker Compose profile (`--profile browser`). The system MUST function without it when browser automation is not needed.

#### Scenario: Browser sidecar active
Given `docker compose --profile browser up -d` was used to start the system
And the `chrome` container is running with port 9222 exposed
When the agent needs to rotate an API key via a web UI
Then the Chrome DevTools MCP connects to `ws://chrome:9222` and automates the browser

#### Scenario: Browser sidecar not active
Given the system was started without the `browser` profile
When the Chrome DevTools MCP server attempts to connect
Then browser automation tools are unavailable
And the agent uses alternative methods (REST APIs via Fetch MCP) or escalates

### REQ-8: Typed Tool Schemas

MCP servers MUST expose tool schemas that describe available operations, their parameters, and expected return types using JSON Schema. The agent MUST use these schemas to reason about tool capabilities and construct valid tool invocations.

#### Scenario: Agent discovers available tools
Given the Docker MCP server is running
When the agent queries the server's tool list
Then it receives a set of tool definitions with names, descriptions, and JSON Schema parameter definitions

#### Scenario: Agent constructs valid invocations
Given the agent needs to inspect a container named `postgres-db`
And the Docker MCP exposes a tool with a `containerName` parameter
When the agent invokes the tool
Then it constructs a valid invocation with the correct parameter name and value

### REQ-9: Bounded Tool Surface Area

Each MCP server MUST expose a finite, well-defined set of operations. The agent MUST NOT have the ability to execute arbitrary commands through an MCP server. The tool surface area of each configured MCP server SHOULD be auditable by reviewing the server's tool definitions.

#### Scenario: Docker MCP exposes bounded operations
Given the Docker MCP server is configured
When an operator reviews its tool definitions
Then they can enumerate all operations the agent can perform through this server

#### Scenario: MCP preferred over unbounded shell
Given the agent could use either Docker MCP tools or `docker` CLI via Bash
When the agent chooses how to interact with containers
Then it prefers Docker MCP tools because the tool surface area is bounded and auditable

### REQ-10: MCP Usage Aligned with Permission Tiers

MCP tool invocations MUST respect the agent's permission tier model. A Tier 1 (observe-only) agent MUST NOT use MCP tools to perform mutating operations (e.g., restarting containers via Docker MCP). Tier 2 and Tier 3 agents MAY use MCP tools for their respective permitted operations.

#### Scenario: Tier 1 uses MCP for read-only operations
Given a Tier 1 agent has access to the Docker MCP server
When it needs to check container health
Then it uses read-only Docker MCP tools (list, inspect) but does not use restart or stop tools

#### Scenario: Tier 2 uses MCP for safe remediation
Given a Tier 2 agent has access to the Docker MCP server
When it needs to restart an unhealthy container
Then it MAY use the Docker MCP's restart tool as this is within Tier 2 permissions

#### Scenario: Browser automation requires Tier 2
Given browser automation via Chrome DevTools MCP is needed for credential rotation
When a Tier 1 agent encounters an authentication failure
Then it MUST NOT use Chrome DevTools MCP for rotation and MUST escalate to Tier 2

### REQ-11: Baseline Backup and Restoration

The entrypoint script MUST save a backup of the baseline MCP config on the first run and restore it before each subsequent merge cycle. This ensures that removed repos or changed repo configs take effect on the next cycle rather than accumulating indefinitely.

#### Scenario: First run baseline backup
Given the system starts for the first time
And `/app/.claude/mcp.json.baseline` does not exist
When the entrypoint prepares to merge MCP configs
Then it copies `/app/.claude/mcp.json` to `/app/.claude/mcp.json.baseline`

#### Scenario: Subsequent run baseline restoration
Given `/app/.claude/mcp.json.baseline` exists from a previous run
When the entrypoint starts a new cycle
Then it restores the baseline before merging, so the config reflects only current repos

#### Scenario: Removed repo config cleaned up
Given repo `old-infra` was previously mounted and contributed MCP servers
And repo `old-infra` has been unmounted
When the next cycle begins
Then the baseline is restored (without `old-infra`'s servers) and only currently mounted repos' configs are merged

## References

- [ADR-0006: Use MCP Servers as Primary Infrastructure Access Layer](../adrs/adr-0006)
- [SPEC-0005: Mounted Repo Extension Model](./mounted-repo-extensions) (for MCP config merging semantics)
- [Model Context Protocol specification](https://modelcontextprotocol.io/)

---

# Design: MCP Infrastructure Bridge

## Overview

Claude Ops uses Model Context Protocol (MCP) servers as its primary mechanism for interacting with infrastructure. Rather than constructing raw CLI commands via Bash, the agent invokes typed tool interfaces exposed by MCP servers running as stdio subprocesses. This provides bounded tool surface areas, structured parameter schemas, and environment isolation between infrastructure concerns.

Four baseline MCP servers cover the core access patterns (container management, database querying, browser automation, HTTP requests), and mounted repos can extend this set with custom MCP servers for specialized integrations.

## Architecture

### Component Topology

```
+------------------------------------------------------------------+
|  Claude Ops Container                                            |
|                                                                  |
|  +------------------+     +---------------------------------+    |
|  |  entrypoint.sh   |     |     Claude Code CLI Process     |    |
|  |                  |     |                                 |    |
|  | Merge MCP configs|---->| Reads /app/.claude/mcp.json     |    |
|  | from repos       |     |                                 |    |
|  +------------------+     | Spawns MCP servers as children: |    |
|                           |                                 |    |
|                           |  +--------+  +----------+       |    |
|                           |  | Docker |  | Postgres |       |    |
|                           |  |  MCP   |  |   MCP    |       |    |
|                           |  +---+----+  +----+-----+       |    |
|                           |      |            |              |    |
|                           |  +---+------+  +--+--------+    |    |
|                           |  | Chrome   |  |  Fetch    |    |    |
|                           |  | DevTools |  |   MCP     |    |    |
|                           |  |   MCP    |  +-----------+    |    |
|                           |  +---+------+                   |    |
|                           +---------------------------------+    |
|                                  |                               |
+------------------------------------------------------------------+
                                   | CDP WebSocket
                                   v
                      +------------------------+
                      |  chrome sidecar        |
                      |  browserless/chromium   |
                      |  ws://chrome:9222       |
                      +------------------------+
```

### MCP Server Lifecycle

1. **Configuration**: MCP server definitions are read from `/app/.claude/mcp.json` when the Claude CLI process starts.
2. **Spawning**: Each MCP server is started as a stdio subprocess (stdin/stdout pipe) when the agent first invokes one of its tools.
3. **Communication**: The Claude CLI sends JSON-RPC requests over stdin and receives responses over stdout, following the MCP protocol.
4. **Isolation**: Each server runs in its own process with its own environment variables. There is no shared state between servers.
5. **Termination**: MCP server subprocesses are terminated when the Claude CLI process exits (end of monitoring cycle).

### Baseline Server Mapping

| Server | Package | Infrastructure Domain | Primary Use Cases |
|--------|---------|----------------------|-------------------|
| `docker` | `@anthropic-ai/mcp-docker` | Container runtime | List containers, inspect state, check health, view logs, restart/stop/start |
| `postgres` | `@anthropic-ai/mcp-postgres` | Database | Execute SQL queries, inspect schema, check connectivity |
| `chrome-devtools` | `@anthropic-ai/mcp-chrome-devtools` | Web UIs | Navigate pages, fill forms, click elements, extract content, take screenshots |
| `fetch` | `@anthropic-ai/mcp-fetch` | HTTP/REST APIs | GET/POST/PUT requests, check endpoints, interact with REST APIs |

### Mapping to Operational Tasks

The baseline servers map directly to the checks and playbooks the agent executes:

| Check/Playbook | MCP Server(s) Used |
|---------------|-------------------|
| `checks/containers.md` | Docker MCP (list, inspect, health status) |
| `checks/http.md` | Fetch MCP (GET requests to health endpoints) |
| `checks/dns.md` | Bash fallback (`dig` command -- no DNS MCP server) |
| `checks/databases.md` | PostgreSQL MCP (connection check, query execution) |
| `checks/services.md` | Fetch MCP + Docker MCP |
| `playbooks/restart-container.md` | Docker MCP (restart command) |
| `playbooks/rotate-api-key.md` | Fetch MCP (REST API rotation) + Chrome DevTools MCP (browser-based rotation) |
| `playbooks/redeploy-service.md` | Bash (Ansible/Helm CLI -- no MCP equivalent) |

## Data Flow

### MCP Config Merge Pipeline

```
entrypoint.sh (before each cycle):

/app/.claude/mcp.json.baseline     (original 4 servers)
         |
         | cp (restore baseline)
         v
/app/.claude/mcp.json              (working copy)
         |
         | jq merge: + /repos/alpha/.claude-ops/mcp.json
         | jq merge: + /repos/beta/.claude-ops/mcp.json
         | jq merge: + /repos/gamma/.claude-ops/mcp.json
         v
/app/.claude/mcp.json              (merged: baseline + all repos)
         |
         | Claude CLI reads at startup
         v
MCP servers available to agent
```

The `jq` merge operation:
```
jq -s '.[0].mcpServers as $base |
       .[1].mcpServers as $repo |
       .[0] | .mcpServers = ($base * $repo)'
```

This is a shallow object merge on the `mcpServers` key. The `*` operator in `jq` merges objects, with the right operand's keys overriding the left's on collision. Since repos are processed alphabetically, the effective precedence is:

```
baseline < repo-alpha < repo-beta < repo-gamma
```

### Tool Invocation Flow

```
Agent (Claude)                 MCP Server (subprocess)        Infrastructure
      |                              |                              |
      |-- invoke tool(params) ------>|                              |
      |   (JSON-RPC over stdin)      |                              |
      |                              |-- infrastructure API call -->|
      |                              |   (Docker socket, SQL,       |
      |                              |    HTTP, CDP WebSocket)      |
      |                              |                              |
      |                              |<-- response ------------------|
      |                              |                              |
      |<-- tool result --------------|                              |
      |   (JSON-RPC over stdout)     |                              |
      |                              |                              |
      | (agent reasons about result  |                              |
      |  and decides next action)    |                              |
```

### Browser Automation Flow (Chrome DevTools MCP)

```
Agent          Chrome DevTools MCP       chrome sidecar (CDP)
  |                    |                        |
  |-- navigate_page -->|                        |
  |                    |-- CDP: Page.navigate ->|
  |                    |<-- page loaded --------|
  |<-- result ---------|                        |
  |                    |                        |
  |-- take_snapshot -->|                        |
  |                    |-- CDP: DOM.getDoc... ->|
  |                    |<-- a11y tree ----------|
  |<-- snapshot -------|                        |
  |                    |                        |
  |-- fill(uid, val) ->|                        |
  |                    |-- CDP: Input.dispatch->|
  |                    |<-- ok -----------------|
  |<-- result ---------|                        |
```

The Chrome DevTools MCP server connects to the browser sidecar via WebSocket (`ws://chrome:9222`). The sidecar is a headless Chromium instance provided by `browserless/chromium`, deployed as a separate Docker Compose service under the `browser` profile.

## Key Decisions

### MCP over direct CLI execution (from ADR-0006)

Direct CLI execution (e.g., `docker inspect`, `psql -c`, `curl`) is universally understood and easily debuggable, but it gives the agent unbounded shell access. Any command can be constructed, making the tool surface area impossible to audit. MCP servers expose a finite set of typed tools, which:

- Constrains what the agent can do through each integration point
- Provides parameter validation via JSON Schema
- Handles connection management, error formatting, and response structure
- Is inspectable: operators can review tool definitions to understand the agent's capabilities

The trade-off is an additional protocol layer that can obscure what is happening and complicate debugging compared to readable CLI commands in logs.

### npx-based spawning over pre-installation

MCP servers are spawned via `npx -y <package>`, which downloads packages on demand from the npm registry. This means:

- No packages need to be pre-installed in the container image
- Adding a new MCP server requires only a config change, not a container rebuild
- Repo-provided MCP servers work automatically without the operator knowing what packages they need

The trade-off is a runtime dependency on npm registry availability and network connectivity. If the registry is unreachable, the agent cannot start MCP servers. For production deployments, the container image could pre-install common packages for reliability, while still supporting `npx` for repo-provided servers.

### Separate browser sidecar over embedded browser

The Chrome DevTools MCP server connects to an external Chromium instance rather than bundling a browser in the main container. This was chosen because:

- Browser dependencies (Chromium, fonts, graphics libraries) significantly increase container image size
- Not all deployments need browser automation
- Docker Compose profiles allow optional activation (`--profile browser`)
- The sidecar can be upgraded independently from the main container

The trade-off is additional deployment complexity (two containers instead of one) and network communication overhead (CDP over WebSocket between containers).

### Environment variable isolation per server

Each MCP server receives only its declared environment variables, not the full container environment. This is enforced by the `env` field in the MCP config. For example, the PostgreSQL connection string is only available to the PostgreSQL MCP server, not to the Docker or Fetch MCP servers.

This reduces the blast radius of a compromised or malicious MCP server: it can only access credentials explicitly given to it.

### Shallow merge semantics for config composition

The `jq` merge uses the `*` operator for shallow object merge on the `mcpServers` key. This means:

- Adding a new server: the repo's server definition is added to the merged config
- Overriding an existing server: the repo's entire server definition replaces the baseline's
- There is no deep merge of individual fields within a server definition

This was chosen for simplicity and predictability. A deep merge could cause confusing behavior (e.g., a repo that overrides only `env` but inherits `args` from the baseline).

## Trade-offs

### Gained

- **Bounded, auditable tool surface area**: Each MCP server exposes a known set of operations. Operators can review what the agent can do by inspecting tool definitions.
- **Structured interactions**: Typed tool schemas reduce malformed invocations compared to constructing CLI commands from memory.
- **Environment isolation**: Sensitive credentials are scoped to the specific MCP server that needs them, not shared across all infrastructure access.
- **Extensibility without rebuilds**: New MCP servers can be added via config changes or repo-provided configs, without modifying the container image.
- **Ecosystem leverage**: Building on MCP means benefiting from Anthropic's and the community's investment in server quality and new integrations.

### Lost

- **Debuggability of raw CLI commands**: CLI commands are universally understood and can be copy-pasted to reproduce actions. MCP tool invocations require understanding the protocol layer and inspecting tool schemas.
- **Offline reliability**: The `npx -y` spawning model requires npm registry access. A network-isolated deployment needs pre-installed packages, losing the "zero pre-installation" benefit.
- **Version stability**: Without explicit version pinning (e.g., `@anthropic-ai/mcp-docker@1.2.3`), MCP server behavior may change between runs due to automatic package updates.
- **Protocol maturity**: MCP is a relatively new protocol. Server implementations may have bugs, incomplete tool coverage, or undocumented behavior.

## Security Considerations

### MCP server trust model

MCP servers are npm packages that run as subprocesses with access to specific infrastructure. The security model assumes:

- **Baseline servers are trusted**: They are maintained by Anthropic and installed in the container by the operator.
- **Repo-provided servers are semi-trusted**: Operators choose which repos to mount and should review `.claude-ops/mcp.json` before mounting.
- **Environment variable scoping provides defense in depth**: A compromised MCP server can only access credentials explicitly given to it.

### Malicious repo MCP config

A mounted repo could define an MCP server that:
- Connects to an attacker-controlled endpoint and exfiltrates data
- Exposes dangerous tools that the agent might invoke
- Overrides a baseline server with a malicious implementation

Mitigations: the merged config can be audited at `/app/.claude/mcp.json` before the agent runs; repos should be reviewed before mounting; only trusted repos should be mounted.

### npm supply chain risk

MCP servers are fetched from npm via `npx -y`. If a package is compromised (typosquatting, maintainer account compromise, malicious update), the agent would run malicious code. Mitigations: use version-pinned packages in production; use a private npm registry or proxy; pre-install known-good versions in the container image.

## Future Considerations

- **Version pinning**: Production deployments should pin MCP server versions (e.g., `@anthropic-ai/mcp-docker@1.2.3`) to prevent unexpected behavior changes.
- **Pre-installation for reliability**: Common MCP servers could be `npm install`'d during container build for faster startup and offline resilience.
- **MCP server health monitoring**: The agent could verify that MCP servers are responsive before relying on them, falling back to Bash if a server fails to start.
- **Additional baseline servers**: As the MCP ecosystem grows, servers for Kubernetes, Prometheus, Redis, and other infrastructure could join the baseline set.
- **MCP server output in run logs**: Logging the raw MCP tool invocations and responses in the run log would improve auditability and debugging.
- **Allowlisting repo MCP servers**: An operator-maintained allowlist of permitted MCP server packages could prevent repos from introducing arbitrary servers.
