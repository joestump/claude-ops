# Design: Go/HTMX/DaisyUI Web Dashboard

## Overview

The web dashboard replaces Claude Ops' bash entrypoint (`entrypoint.sh`) with a Go binary that serves as both the session scheduler/manager and a web-based operational interface. The binary manages Claude Code CLI sessions as subprocesses, streams their output to browser clients via SSE, stores health check history and cooldown state in SQLite, and renders a server-side HTML dashboard using HTMX and DaisyUI.

This design document covers the application architecture, component interactions, data flow patterns, and the rationale behind key technical choices.

## Architecture

### High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        Go Binary (entrypoint)                   │
│                                                                 │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────────┐  │
│  │   HTTP Server │  │  Scheduler   │  │  Session Manager      │  │
│  │   (net/http)  │  │  (goroutine) │  │  (os/exec + goroutine)│  │
│  │              │  │              │  │                       │  │
│  │  - Routes    │  │  - Ticker    │  │  - Subprocess start   │  │
│  │  - Templates │  │  - Interval  │  │  - stdout/stderr pipe │  │
│  │  - SSE       │  │  - Trigger   │  │  - Timeout handling   │  │
│  │  - Static    │  │              │  │  - Signal forwarding  │  │
│  └──────┬───────┘  └──────┬───────┘  └──────────┬────────────┘  │
│         │                 │                      │              │
│         │        ┌────────▼──────────────────────▼────────┐     │
│         │        │           SSE Hub (fan-out)            │     │
│         │        │  Broadcasts session output to clients  │     │
│         │        └───────────────────────────────────────┘     │
│         │                                                       │
│  ┌──────▼──────────────────────────────────────────────────┐    │
│  │                SQLite (modernc.org/sqlite)               │    │
│  │  - sessions table     - health_checks table              │    │
│  │  - cooldown_actions   - config table                     │    │
│  │  - schema_migrations                                     │    │
│  └──────────────────────────────────────────────────────────┘    │
│                                                                 │
│  ┌──────────────┐  ┌───────────────┐  ┌─────────────────────┐   │
│  │  MCP Merger   │  │  Config Mgr   │  │  Signal Handler     │   │
│  │  (pre-session)│  │  (env + DB)   │  │  (SIGTERM/SIGINT)   │   │
│  └──────────────┘  └───────────────┘  └─────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
         │                                          │
         │ HTTP :8080                               │ subprocess
         ▼                                          ▼
    ┌──────────┐                            ┌──────────────┐
    │  Browser  │                            │  claude CLI   │
    │  (HTMX +  │                            │  (subprocess) │
    │  DaisyUI) │                            └──────────────┘
    └──────────┘
```

### Component Responsibilities

**HTTP Server** (`net/http`): Serves HTML pages rendered from `html/template`, handles HTMX partial requests, serves static assets (CSS, JS) from `embed.FS`, manages SSE connections for real-time output streaming, and processes configuration form submissions.

**Scheduler**: A goroutine running a timer loop (replacing the bash `while true; sleep $INTERVAL; done` pattern). Triggers session creation at the configured interval. Respects the "one active session per tier" constraint -- if a session is already running when the interval fires, it logs the skip and waits for the next interval.

**Session Manager**: Wraps `os/exec.Command` to create, monitor, and terminate Claude Code CLI subprocesses. Captures stdout and stderr via pipes, fans output to the SSE hub, writes output to result log files, and records session metadata (start time, end time, exit code, tier) in SQLite. Handles timeouts by sending SIGTERM after a configurable duration, followed by SIGKILL after a grace period.

**SSE Hub**: A publish-subscribe mechanism for session output. The session manager publishes output lines as they arrive from subprocess pipes. The HTTP server subscribes browser clients to the hub via SSE endpoints. Multiple clients can watch the same session. The hub buffers recent output so that clients connecting mid-session receive a catchup window.

**SQLite Storage**: Single database file on the persistent volume. Stores session history, health check results, cooldown action records, and runtime configuration overrides. Replaces `cooldown.json` with structured, queryable tables.

**MCP Merger**: Replicates the `merge_mcp_configs()` function from `entrypoint.sh`. Before each session invocation, scans `$CLAUDEOPS_REPOS_DIR/*/. claude-ops/mcp.json`, merges all discovered configs into the baseline `.claude/mcp.json`, and resolves name collisions (repo configs win).

**Config Manager**: Reads environment variables on startup, loads any runtime overrides from SQLite, and provides a unified configuration interface to other components. The dashboard configuration page writes overrides to SQLite, which take effect on the next session without a container restart.

**Signal Handler**: Listens for SIGTERM and SIGINT via `os/signal.Notify`. On signal receipt, stops the scheduler, forwards the signal to running subprocesses, waits for subprocess exit (with a grace period), drains the HTTP server, and exits.

## Data Flow

### Session Lifecycle

```
Scheduler tick
    │
    ▼
Session Manager: check if session of this tier is already running
    │
    ├── Running → log "session still active, skipping", wait for next tick
    │
    └── Not running → proceed
           │
           ▼
    MCP Merger: merge repo configs into baseline
           │
           ▼
    Session Manager: create subprocess
           │
           ├── cmd = exec.Command("claude", "--model", model, "--print",
           │        "--prompt-file", promptFile, "--allowedTools", tools,
           │        "--append-system-prompt", envContext)
           │
           ├── pipe stdout and stderr
           │
           ├── store Session record in SQLite (status: running)
           │
           └── start subprocess
                  │
                  ▼
           Goroutine: read stdout/stderr line by line
                  │
                  ├── send each line to SSE Hub
                  ├── append to in-memory buffer
                  └── write to log file in $CLAUDEOPS_RESULTS_DIR
                  │
                  ▼
           Subprocess exits (or times out)
                  │
                  ├── record exit code, end time in SQLite
                  ├── update session status (completed/failed/timed_out)
                  ├── close SSE hub channel for this session
                  └── notify scheduler that tier is available
```

### SSE Streaming Flow

```
Browser: GET /sessions/{id}/stream (Accept: text/event-stream)
    │
    ▼
HTTP Server: create SSE connection
    │
    ├── subscribe to SSE Hub for session {id}
    │
    ├── send buffered output (catchup)
    │
    └── enter event loop:
           │
           ├── receive line from hub → write SSE event to response
           │
           ├── client disconnects → unsubscribe from hub
           │
           └── session ends → send "done" event → close connection
```

### HTMX Page Interaction Flow

```
Browser loads /dashboard (full page)
    │
    ├── HTML includes hx-get="/partials/service-list"
    │       with hx-trigger="every 30s"
    │
    ▼
Every 30 seconds:
    │
    ├── HTMX sends GET /partials/service-list
    │
    ├── Server renders service-list.html template with current data
    │
    ├── HTMX swaps the response into the target div
    │
    └── No full page reload
```

### Cooldown Check Flow (SQLite)

```
Session Manager: before remediation action
    │
    ▼
Query: SELECT COUNT(*) FROM cooldown_actions
       WHERE service = ? AND action_type = 'restart'
       AND timestamp > datetime('now', '-4 hours')
    │
    ├── count < 2 → action permitted
    │       │
    │       ├── perform remediation
    │       │
    │       └── INSERT INTO cooldown_actions
    │           (service, action_type, timestamp, success, tier)
    │           VALUES (?, 'restart', datetime('now'), ?, ?)
    │
    └── count >= 2 → action blocked
           │
           └── send "needs human attention" notification
```

## Database Schema

### Core Tables

```sql
-- Schema versioning for migrations
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Session history
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tier INTEGER NOT NULL,           -- 1, 2, or 3
    model TEXT NOT NULL,             -- haiku, sonnet, opus
    prompt_file TEXT NOT NULL,
    status TEXT NOT NULL,            -- running, completed, failed, timed_out
    started_at TEXT NOT NULL,
    ended_at TEXT,
    exit_code INTEGER,
    log_file TEXT,                   -- path to result log file
    context TEXT                     -- JSON blob: failure context passed from parent tier
);

-- Health check results (parsed from session output)
CREATE TABLE health_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER REFERENCES sessions(id),
    service TEXT NOT NULL,
    check_type TEXT NOT NULL,        -- http, dns, container, database, service
    status TEXT NOT NULL,            -- healthy, degraded, down
    response_time_ms INTEGER,
    error_detail TEXT,
    checked_at TEXT NOT NULL
);

-- Cooldown action records (replaces cooldown.json arrays)
CREATE TABLE cooldown_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    service TEXT NOT NULL,
    action_type TEXT NOT NULL,       -- restart, redeployment
    timestamp TEXT NOT NULL,
    success INTEGER NOT NULL,        -- 0 or 1
    tier INTEGER NOT NULL,           -- 2 or 3
    error TEXT,
    session_id INTEGER REFERENCES sessions(id)
);

-- Consecutive healthy check tracking (replaces cooldown.json counters)
CREATE TABLE service_health_streak (
    service TEXT PRIMARY KEY,
    consecutive_healthy INTEGER NOT NULL DEFAULT 0,
    last_checked_at TEXT
);

-- Runtime configuration overrides
CREATE TABLE config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Indexes for common queries
CREATE INDEX idx_health_checks_service ON health_checks(service, checked_at);
CREATE INDEX idx_health_checks_session ON health_checks(session_id);
CREATE INDEX idx_cooldown_actions_service ON cooldown_actions(service, action_type, timestamp);
CREATE INDEX idx_sessions_status ON sessions(status, started_at);
```

### Migration Strategy

Schema migrations use a simple versioned approach:

1. On startup, read the highest `version` from `schema_migrations`
2. Apply any migrations with higher version numbers, in order
3. Each migration is a Go function that runs SQL statements within a transaction
4. Migrations are embedded in the binary -- no external migration files

This approach avoids external migration tools (goose, migrate) while providing reliable schema evolution.

## Key Decisions

### Go Over Python, Node.js, and Rust (from ADR-0008)

Go was selected for the combination of:

- **Single static binary**: No runtime, interpreter, or shared libraries needed in the container. This keeps the Docker image minimal and eliminates "works on my machine" class issues.
- **First-class concurrency**: Goroutines and channels are the natural fit for managing multiple concurrent concerns: HTTP serving, session subprocess I/O, SSE streaming, scheduling.
- **Standard library HTTP**: `net/http` and `html/template` cover the web server and templating without any third-party framework, reducing dependency surface.
- **Infrastructure community**: Go is the dominant language in the DevOps/infrastructure ecosystem (Docker, Kubernetes, Terraform are all Go). The target contributor base is more likely to know Go than Rust or React.

### HTMX Over React/Vue/Angular

HTMX was chosen because:

- **No JavaScript build toolchain**: The entire frontend is HTML templates with HTMX attributes. No webpack, no babel, no npm install, no node_modules. This eliminates a major class of build, dependency, and maintenance overhead.
- **Server-rendered**: HTML is rendered on the server where the data lives. There is no API contract between frontend and backend, no JSON serialization layer, no client-side state management.
- **SSE integration**: HTMX has built-in SSE support via `hx-ext="sse"`, making real-time streaming a first-class feature without custom JavaScript.
- **Accessibility**: Infrastructure engineers can write HTML templates with DaisyUI classes to add dashboard panels. The skill requirement is HTML + CSS, not React + TypeScript + state management.

### SQLite Over Continuing with JSON Files

The dashboard requires queryable history (health check trends, session logs, cooldown state with time-range queries). The JSON file model from ADR-0007 cannot support these query patterns efficiently. SQLite provides:

- **ACID transactions**: Atomic reads and writes, eliminating the partial-write corruption risk of the JSON file approach.
- **Queryable history**: SQL enables queries like "show all unhealthy checks for nginx in the last 7 days" that would require loading and parsing entire JSON files.
- **Schema enforcement**: Column types and constraints catch data integrity issues at write time.
- **No additional service**: SQLite is an in-process library, maintaining the zero-external-dependency property.

The pure-Go SQLite driver (`modernc.org/sqlite`) was chosen over CGO-based drivers (`mattn/go-sqlite3`) because it enables clean cross-compilation and static linking without requiring a C compiler in the build environment.

### Embedded Static Assets

Using Go's `embed.FS` to embed templates, CSS, and JavaScript into the binary provides:

- **Self-contained deployment**: The binary is the entire application. No volume mounts for templates, no CDN dependencies at runtime.
- **Atomic upgrades**: Updating the binary updates all templates and styles simultaneously. No version skew between binary and assets.
- **Development flexibility**: A build tag or environment variable can switch to file-system loading for hot reload during development.

## Trade-offs

### Gained

- **Real-time visibility**: Operators can watch Claude think and act in their browser as it happens, fundamentally changing the observability model from "read logs after the fact" to "watch in real time."
- **Queryable history**: Health check results, remediation actions, and session logs are stored in a relational database, enabling trend analysis, reporting, and correlation that flat files cannot provide.
- **Robust process management**: Go's `os/exec`, goroutines, and signal handling replace bash's fragile subprocess management. Timeouts, concurrent sessions, and graceful shutdown are handled correctly by the type system and concurrency primitives.
- **Configuration without restart**: Runtime configuration changes via the dashboard take effect on the next session without stopping and restarting the container.
- **Self-contained deployment**: A single binary with embedded assets, serving its own web UI, managing its own subprocesses, and storing its own state. The operational model is "run one binary."

### Lost

- **Simplicity of bash**: The current `entrypoint.sh` is ~95 lines of bash that anyone can read and understand in minutes. The Go application will be substantially larger and requires Go knowledge to modify.
- **Build infrastructure**: The project now requires a Go toolchain for building. CI/CD must compile Go code, run Go tests, and produce a binary. The bash script required no build step.
- **Security surface**: An HTTP port is now exposed, requiring authentication, CSRF protection, and potentially TLS. The headless system had no network-accessible interface beyond the Apprise push notifications.
- **Coupled concerns**: The web UI and session manager run in the same process. If the HTTP server panics (despite recovery middleware), session management stops. The bash loop, while primitive, had no web server to crash.
- **Schema migrations**: SQLite introduces a schema that must be versioned and migrated across upgrades. The JSON file had no schema to manage. Each database change requires a migration function, testing, and rollback consideration.

## Future Considerations

### WebSocket for Bidirectional Communication

The initial design uses SSE (server-to-client only) for session output streaming. If future requirements include client-to-server interaction during sessions (e.g., operator approval for risky actions, interactive debugging), WebSocket connections could replace or supplement SSE. Go's standard library does not include WebSocket support, but `nhooyr.io/websocket` provides a minimal, well-tested implementation.

### Plugin Dashboard Panels

Mounted repos could contribute custom dashboard panels via template files in `.claude-ops/dashboard/`. The Go application would discover and render these templates, allowing repo-specific views (e.g., a custom Kubernetes cluster status panel for a Helm-managed repo). This extends the existing repo extension model (custom checks, playbooks, skills) to the UI layer.

### Multi-User Access Control

The initial design uses basic authentication (single username/password). Future versions may need role-based access control (RBAC) if multiple operators with different permission levels need access. Roles could map to agent tiers: a "viewer" role for Tier 1 visibility only, an "operator" role for triggering Tier 2 remediation, and an "admin" role for Tier 3 actions and configuration changes.

### Horizontal Scaling

The current design assumes a single binary instance managing all sessions. If the system needs to scale to monitoring hundreds of services across multiple regions, the architecture could evolve to a coordinator/worker model: a central dashboard instance that dispatches session work to worker instances. This would require replacing SQLite with a shared database (PostgreSQL) and adding a work queue. This is explicitly out of scope for the initial implementation.

### API Layer

While the initial dashboard is server-rendered HTML, adding a JSON API layer would enable:
- CLI tools for operators who prefer terminal interaction
- Integration with external monitoring systems (Prometheus metrics endpoint, PagerDuty webhooks)
- Mobile notification apps that query session status

The HTMX architecture makes this straightforward: the same handler can return HTML (for browser requests with `HX-Request` header) or JSON (for API requests with `Accept: application/json`), using content negotiation.
