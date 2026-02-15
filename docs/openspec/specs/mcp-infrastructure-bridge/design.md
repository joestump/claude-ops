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
