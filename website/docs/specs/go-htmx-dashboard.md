---
sidebar_position: 8
sidebar_label: Go/HTMX Dashboard
---

# SPEC-0008: Go/HTMX/DaisyUI Web Dashboard

## Overview

Claude Ops currently operates as a headless Docker container with no web interface. Operators interact with the system through Docker logs, flat result files, and push notifications. This specification defines a Go-based web dashboard that replaces the bash entrypoint (`entrypoint.sh`) with a compiled binary that serves a web UI, manages Claude Code CLI sessions as subprocesses, streams session output in real time via SSE, and stores health check history in SQLite.

The dashboard provides operational visibility (real-time session output, health check history, cooldown state inspection), configuration management (monitoring interval, model selection), and robust subprocess lifecycle management (scheduling, timeouts, signal forwarding, concurrent sessions) -- capabilities that the current bash loop cannot deliver.

## Definitions

- **Session**: A single invocation of the Claude Code CLI as a subprocess, corresponding to one agent loop iteration (health check, investigation, or remediation).
- **SSE (Server-Sent Events)**: A unidirectional HTTP protocol where the server pushes events to the browser over a long-lived connection. Used for streaming session output in real time.
- **HTMX**: A JavaScript library that extends HTML with attributes (`hx-get`, `hx-post`, `hx-swap`, `hx-trigger`) for making HTTP requests and updating the DOM without writing JavaScript.
- **DaisyUI**: A TailwindCSS component library providing semantic CSS class names (`btn`, `card`, `badge`, `table`) for UI components.
- **Subprocess**: A Claude Code CLI process managed by the Go binary using `os/exec`, with stdout/stderr captured for streaming and logging.
- **Goroutine**: A lightweight concurrent execution unit in Go, used for scheduling sessions, streaming output, and handling HTTP requests simultaneously.
- **Pure-Go SQLite**: A SQLite implementation (`modernc.org/sqlite`) compiled entirely in Go without CGO, enabling clean cross-compilation and static linking.

## Requirements

### REQ-1: Single Binary Entrypoint

The Go application MUST compile to a single statically-linked binary that replaces `entrypoint.sh` as the Docker container's entrypoint. The binary MUST NOT require a runtime interpreter, external libraries, or dynamically linked dependencies.

#### Scenario: Binary replaces bash entrypoint
Given the Docker container is built with the Go binary as its entrypoint
When the container starts
Then the Go binary MUST execute directly without requiring bash, Python, or any interpreter

#### Scenario: Static linking verification
Given the compiled Go binary
When inspected with `ldd` or equivalent
Then the binary MUST report no dynamic library dependencies (or "not a dynamic executable")

### REQ-2: Web Server

The Go application MUST serve an HTTP web interface on a configurable port (default `8080`). The web server MUST use Go's standard library `net/http` package. The server MUST serve HTML pages rendered from Go `html/template` templates.

#### Scenario: Default port binding
Given the environment variable for the dashboard port is not set
When the Go application starts
Then the web server MUST listen on port 8080

#### Scenario: Custom port binding
Given the environment variable for the dashboard port is set to "9090"
When the Go application starts
Then the web server MUST listen on port 9090

#### Scenario: Template rendering
Given a browser requests the dashboard index page
When the server processes the request
Then the response MUST be server-rendered HTML produced by Go's `html/template` package
And the HTML MUST include DaisyUI/TailwindCSS class names for styling

### REQ-3: HTMX-Based Interactivity

The dashboard MUST use HTMX for all dynamic page interactions. The dashboard MUST NOT require a JavaScript build toolchain (webpack, babel, npm, etc.). JavaScript usage MUST be limited to HTMX, Alpine.js (optional for small client-side state), and minimal inline scripts.

#### Scenario: Dynamic content update
Given the dashboard displays a list of health check results
When a new health check completes
Then the result list MUST update via an HTMX request (using `hx-get` or SSE) without a full page reload

#### Scenario: No JavaScript build step
Given the project source code
When the application is built
Then no JavaScript bundler, transpiler, or package manager step MUST be required
And all JavaScript dependencies MUST be served as static files or CDN references

#### Scenario: Form submission
Given an operator submits a configuration change via the dashboard
When the form is submitted
Then the submission MUST use an HTMX attribute (`hx-post`, `hx-put`) to send the request
And the response MUST update only the relevant page section via `hx-swap`

### REQ-4: DaisyUI/TailwindCSS Styling

The dashboard MUST use DaisyUI component classes for UI elements. The dashboard SHOULD use TailwindCSS utility classes for layout and spacing. The CSS MUST be included as a pre-built stylesheet (CDN or vendored file), not requiring a build-time CSS compilation step.

#### Scenario: Component rendering
Given the dashboard renders a service health status
When the status is displayed
Then it MUST use DaisyUI semantic classes (e.g., `badge badge-success`, `badge badge-error`, `card`, `table`)

#### Scenario: No CSS build step
Given the project source code
When the application is built
Then no TailwindCSS CLI, PostCSS, or CSS preprocessor step MUST be required for production builds

### REQ-5: Claude Code CLI Session Management

The Go application MUST manage Claude Code CLI invocations as subprocesses using Go's `os/exec` package. The application MUST support scheduling sessions at a configurable interval (equivalent to `$CLAUDEOPS_INTERVAL`). The application MUST support concurrent session tracking (one active session per tier at a time).

#### Scenario: Scheduled session invocation
Given the monitoring interval is set to 3600 seconds
When 3600 seconds have elapsed since the last session completed
Then the application MUST invoke the Claude Code CLI with the Tier 1 prompt file

#### Scenario: CLI subprocess creation
Given the application starts a new health check session
When the subprocess is created
Then it MUST use `os/exec.Command` to invoke the `claude` CLI
And it MUST pass the model, prompt file, allowed tools, and system prompt arguments matching the current `entrypoint.sh` invocation pattern

#### Scenario: Session already running
Given a Tier 1 session is currently in progress
When the scheduled interval elapses
Then the application MUST NOT start a second Tier 1 session
And the application SHOULD log that the previous session is still running

### REQ-6: Real-Time Session Output Streaming

The Go application MUST stream Claude Code CLI subprocess output (stdout and stderr) to connected browser clients in real time using Server-Sent Events (SSE). Multiple browser clients MUST be able to watch the same session simultaneously.

#### Scenario: Single client watching a session
Given a Claude Code CLI session is running
When an operator opens the session view in their browser
Then the browser MUST receive session output lines as they are produced via SSE
And the output MUST appear incrementally, not buffered until session completion

#### Scenario: Multiple clients watching the same session
Given a Claude Code CLI session is running
When two operators open the session view in their browsers
Then both browsers MUST receive the same session output via independent SSE connections

#### Scenario: Client connects to completed session
Given a Claude Code CLI session has already completed
When an operator opens the session view
Then the dashboard MUST display the full stored session output
And no SSE connection is REQUIRED

### REQ-7: Subprocess Lifecycle Management

The Go application MUST handle subprocess lifecycle events robustly: startup, normal completion, timeout, error exit, and signal forwarding. The application MUST forward SIGTERM and SIGINT to running subprocesses on container shutdown.

#### Scenario: Normal session completion
Given a Claude Code CLI session is running
When the CLI process exits with code 0
Then the application MUST record the session as completed successfully
And the application MUST store the session output and exit status

#### Scenario: Session timeout
Given a Claude Code CLI session has been running for longer than the configured timeout
When the timeout is reached
Then the application MUST send SIGTERM to the subprocess
And the application MUST wait a grace period before sending SIGKILL
And the application MUST record the session as timed out

#### Scenario: Container shutdown signal forwarding
Given the container receives SIGTERM (e.g., `docker stop`)
When the Go application handles the signal
Then it MUST forward SIGTERM to all running CLI subprocesses
And it MUST wait for subprocesses to exit (with a grace period) before exiting itself

#### Scenario: CLI process crashes
Given a Claude Code CLI session exits with a non-zero exit code
When the exit is detected
Then the application MUST record the session as failed with the exit code
And the application MUST NOT automatically retry the session (cooldown rules apply)

### REQ-8: SQLite State Storage

The Go application MUST use SQLite (via `modernc.org/sqlite` or equivalent pure-Go driver) for persistent state storage, replacing the `cooldown.json` file. The database file MUST be stored on the persistent volume at `$CLAUDEOPS_STATE_DIR/claudeops.db` (or similar path within the state directory).

#### Scenario: Database initialization on first run
Given no SQLite database file exists in the state directory
When the Go application starts
Then it MUST create the database file and initialize the schema

#### Scenario: Schema migration on upgrade
Given a SQLite database from a previous version exists
When the Go application starts with a newer schema version
Then it MUST apply schema migrations to bring the database to the current version
And it MUST NOT lose existing data during migration

#### Scenario: Cooldown state in SQLite
Given the application uses SQLite for state storage
When cooldown limits are checked for a service
Then the application MUST query the SQLite database rather than a JSON file
And cooldown enforcement MUST maintain the same limits: max 2 restarts per 4 hours, max 1 redeployment per 24 hours

### REQ-9: Health Check History

The Go application MUST store health check results in SQLite, enabling queryable history. Each health check result MUST include: service name, check type, status (healthy/degraded/down), timestamp, response time (if applicable), and error details (if applicable).

#### Scenario: Health check result storage
Given a Tier 1 session completes a health check run
When results are available
Then the application MUST store each service's check result in the SQLite database

#### Scenario: Historical query
Given health check results have been stored over multiple runs
When an operator views the health check history page
Then the dashboard MUST display results queryable by service name, status, and time range

#### Scenario: Trend visualization
Given health check results span multiple days
When an operator views a service's history
Then the dashboard SHOULD display uptime trends or response time patterns over time

### REQ-10: Dashboard Pages

The dashboard MUST provide the following pages/views:

1. **Overview/Home**: Summary of all monitored services, their current status, and last check time
2. **Session View**: Real-time streaming output of the currently running session (or most recent completed session)
3. **Service Detail**: Per-service health check history, cooldown status, and remediation log
4. **Cooldown State**: Current cooldown status for all services, showing remaining actions and window expiry times
5. **Configuration**: Current monitoring settings (interval, models, repos) with the ability to modify runtime parameters

#### Scenario: Overview page displays service summary
Given 5 services are being monitored, 4 healthy and 1 degraded
When an operator loads the overview page
Then the page MUST display all 5 services with their current status
And the page MUST show the timestamp of the last health check

#### Scenario: Session view streams output
Given a Tier 2 investigation session is running
When an operator navigates to the session view
Then the page MUST stream the session's stdout/stderr output in real time
And the page MUST indicate the session's tier level and target service

#### Scenario: Cooldown state page
Given service "nginx" has 1 restart remaining in its 4-hour window
When an operator views the cooldown state page
Then the page MUST show "nginx" with 1 of 2 restarts used
And the page MUST show when the cooldown window resets

#### Scenario: Configuration change
Given an operator changes the monitoring interval from 3600 to 1800 on the configuration page
When the change is submitted
Then the application MUST apply the new interval to subsequent session scheduling
And the change MUST take effect without restarting the container

### REQ-11: MCP Configuration Merging

The Go application MUST replicate the MCP configuration merging behavior currently in `entrypoint.sh`: merging `.claude-ops/mcp.json` files from mounted repos into the baseline MCP config before each session invocation.

#### Scenario: Repo MCP config merged before session
Given a mounted repo at `/repos/infra` contains `.claude-ops/mcp.json`
When the application prepares to invoke a Claude Code CLI session
Then the application MUST merge the repo's MCP config into the baseline config
And repo configs MUST override baseline configs on name collision

#### Scenario: Multiple repos with MCP configs
Given repos at `/repos/infra` and `/repos/apps` both contain `.claude-ops/mcp.json`
When MCP configs are merged
Then all MCP server definitions from both repos MUST be included in the merged config

### REQ-12: Environment Variable Compatibility

The Go application MUST read and respect all environment variables currently used by `entrypoint.sh` and the agent prompts. At minimum: `CLAUDEOPS_INTERVAL`, `CLAUDEOPS_PROMPT` (or equivalent), `CLAUDEOPS_TIER1_MODEL`, `CLAUDEOPS_TIER2_MODEL`, `CLAUDEOPS_TIER3_MODEL`, `CLAUDEOPS_STATE_DIR`, `CLAUDEOPS_RESULTS_DIR`, `CLAUDEOPS_REPOS_DIR`, `CLAUDEOPS_ALLOWED_TOOLS`, `CLAUDEOPS_DRY_RUN`, `CLAUDEOPS_APPRISE_URLS`.

#### Scenario: Default values match current behavior
Given no environment variables are set
When the application reads configuration
Then `CLAUDEOPS_INTERVAL` MUST default to 3600
And `CLAUDEOPS_STATE_DIR` MUST default to "/state"
And `CLAUDEOPS_RESULTS_DIR` MUST default to "/results"
And `CLAUDEOPS_REPOS_DIR` MUST default to "/repos"
And `CLAUDEOPS_TIER1_MODEL` MUST default to "haiku"

#### Scenario: Custom environment variables
Given `CLAUDEOPS_INTERVAL` is set to "1800" and `CLAUDEOPS_DRY_RUN` is set to "true"
When the application reads configuration
Then the monitoring interval MUST be 1800 seconds
And the application MUST pass DRY_RUN=true to Claude Code CLI sessions

### REQ-13: Graceful Shutdown

The Go application MUST handle SIGTERM and SIGINT signals gracefully. On receiving a shutdown signal, the application MUST stop accepting new HTTP connections, forward signals to running subprocesses, wait for subprocesses to exit (with a configurable grace period), and then exit cleanly.

#### Scenario: Clean shutdown with active session
Given a Claude Code CLI session is running and the container receives SIGTERM
When the Go application processes the signal
Then it MUST stop scheduling new sessions
And it MUST forward SIGTERM to the running CLI subprocess
And it MUST wait up to 30 seconds for the subprocess to exit
And it MUST then shut down the HTTP server
And it MUST exit with code 0

#### Scenario: Shutdown with no active sessions
Given no Claude Code CLI sessions are running and the container receives SIGTERM
When the Go application processes the signal
Then it MUST shut down the HTTP server
And it MUST exit with code 0

### REQ-14: Static Asset Embedding

The Go application SHOULD embed HTML templates, CSS files, and JavaScript files (HTMX, DaisyUI) into the binary using Go's `embed.FS`. This ensures the binary is fully self-contained with no external file dependencies at runtime.

#### Scenario: Binary contains all assets
Given the Go binary is deployed without any accompanying files
When the web server serves a page
Then all HTML templates, CSS, and JavaScript MUST be available from the embedded filesystem

#### Scenario: Development mode with file system templates
Given an environment variable or build flag enables development mode
When the web server serves a page
Then templates MAY be loaded from the file system (enabling hot reload during development)

### REQ-15: Authentication and Security

The dashboard MUST implement basic authentication to prevent unauthorized access. The dashboard MUST protect against CSRF attacks on state-modifying endpoints. The dashboard SHOULD support HTTPS via TLS termination (either directly or via a reverse proxy).

#### Scenario: Unauthenticated access denied
Given authentication is enabled and no credentials are provided
When a browser requests any dashboard page
Then the server MUST return a 401 Unauthorized response
And the server MUST prompt for credentials

#### Scenario: Authenticated access granted
Given valid credentials are provided
When a browser requests a dashboard page
Then the server MUST serve the requested page

#### Scenario: CSRF protection on POST endpoints
Given an operator submits a configuration change
When the form is submitted
Then the request MUST include a valid CSRF token
And requests without a valid CSRF token MUST be rejected

### REQ-16: Result File Compatibility

The Go application MUST continue writing session output to the results directory (`$CLAUDEOPS_RESULTS_DIR`) in the same format as the current `entrypoint.sh` implementation, maintaining backward compatibility with any external tools or processes that consume these files.

#### Scenario: Session output written to results directory
Given a Claude Code CLI session completes
When the session output is stored
Then the application MUST write the output to `$CLAUDEOPS_RESULTS_DIR/run-YYYYMMDD-HHMMSS.log`
And the file format MUST match the current entrypoint output format

## References

- [ADR-0008: Build a Go/HTMX/DaisyUI Web Dashboard](../adrs/adr-0008)
- [ADR-0007: Persist Cooldown State in JSON File](../adrs/adr-0007) -- the JSON cooldown state that SQLite will replace
- [ADR-0010: Claude Code CLI Subprocess Execution](../adrs/adr-0010) -- related subprocess management decisions
- `entrypoint.sh` -- the bash entrypoint that the Go binary replaces
- `CLAUDE.md` -- execution flow, permission tiers, and cooldown rules
- [HTMX Documentation](https://htmx.org/docs/)
- [DaisyUI Components](https://daisyui.com/components/)
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) -- pure-Go SQLite driver

---

# Design: Go/HTMX/DaisyUI Web Dashboard

## Overview

The web dashboard replaces Claude Ops' bash entrypoint (`entrypoint.sh`) with a Go binary that serves as both the session scheduler/manager and a web-based operational interface. The binary manages Claude Code CLI sessions as subprocesses, streams their output to browser clients via SSE, stores health check history and cooldown state in SQLite, and renders a server-side HTML dashboard using HTMX and DaisyUI.

This design document covers the application architecture, component interactions, data flow patterns, and the rationale behind key technical choices.

## Architecture

### High-Level Component Diagram

```
+-----------------------------------------------------------------+
|                        Go Binary (entrypoint)                   |
|                                                                 |
|  +--------------+  +--------------+  +-----------------------+  |
|  |   HTTP Server |  |  Scheduler   |  |  Session Manager      |  |
|  |   (net/http)  |  |  (goroutine) |  |  (os/exec + goroutine)|  |
|  |              |  |              |  |                       |  |
|  |  - Routes    |  |  - Ticker    |  |  - Subprocess start   |  |
|  |  - Templates |  |  - Interval  |  |  - stdout/stderr pipe |  |
|  |  - SSE       |  |  - Trigger   |  |  - Timeout handling   |  |
|  |  - Static    |  |              |  |  - Signal forwarding  |  |
|  +------+-------+  +------+-------+  +----------+------------+  |
|         |                 |                      |              |
|         |        +--------v----------------------v--------+     |
|         |        |           SSE Hub (fan-out)            |     |
|         |        |  Broadcasts session output to clients  |     |
|         |        +---------------------------------------+     |
|         |                                                       |
|  +------v------------------------------------------------------+|
|  |                SQLite (modernc.org/sqlite)               |    |
|  |  - sessions table     - health_checks table              |    |
|  |  - cooldown_actions   - config table                     |    |
|  |  - schema_migrations                                     |    |
|  +----------------------------------------------------------+    |
|                                                                 |
|  +--------------+  +---------------+  +---------------------+   |
|  |  MCP Merger   |  |  Config Mgr   |  |  Signal Handler     |   |
|  |  (pre-session)|  |  (env + DB)   |  |  (SIGTERM/SIGINT)   |   |
|  +--------------+  +---------------+  +---------------------+   |
|                                                                 |
+-----------------------------------------------------------------+
         |                                          |
         | HTTP :8080                               | subprocess
         v                                          v
    +----------+                            +--------------+
    |  Browser  |                            |  claude CLI   |
    |  (HTMX +  |                            |  (subprocess) |
    |  DaisyUI) |                            +--------------+
    +----------+
```

### Component Responsibilities

**HTTP Server** (`net/http`): Serves HTML pages rendered from `html/template`, handles HTMX partial requests, serves static assets (CSS, JS) from `embed.FS`, manages SSE connections for real-time output streaming, and processes configuration form submissions.

**Scheduler**: A goroutine running a timer loop (replacing the bash `while true; sleep $INTERVAL; done` pattern). Triggers session creation at the configured interval. Respects the "one active session per tier" constraint -- if a session is already running when the interval fires, it logs the skip and waits for the next interval.

**Session Manager**: Wraps `os/exec.Command` to create, monitor, and terminate Claude Code CLI subprocesses. Captures stdout and stderr via pipes, fans output to the SSE hub, writes output to result log files, and records session metadata (start time, end time, exit code, tier) in SQLite. Handles timeouts by sending SIGTERM after a configurable duration, followed by SIGKILL after a grace period.

**SSE Hub**: A publish-subscribe mechanism for session output. The session manager publishes output lines as they arrive from subprocess pipes. The HTTP server subscribes browser clients to the hub via SSE endpoints. Multiple clients can watch the same session. The hub buffers recent output so that clients connecting mid-session receive a catchup window.

**SQLite Storage**: Single database file on the persistent volume. Stores session history, health check results, cooldown action records, and runtime configuration overrides. Replaces `cooldown.json` with structured, queryable tables.

**MCP Merger**: Replicates the `merge_mcp_configs()` function from `entrypoint.sh`. Before each session invocation, scans `$CLAUDEOPS_REPOS_DIR/*/.claude-ops/mcp.json`, merges all discovered configs into the baseline `.claude/mcp.json`, and resolves name collisions (repo configs win).

**Config Manager**: Reads environment variables on startup, loads any runtime overrides from SQLite, and provides a unified configuration interface to other components. The dashboard configuration page writes overrides to SQLite, which take effect on the next session without a container restart.

**Signal Handler**: Listens for SIGTERM and SIGINT via `os/signal.Notify`. On signal receipt, stops the scheduler, forwards the signal to running subprocesses, waits for subprocess exit (with a grace period), drains the HTTP server, and exits.

## Data Flow

### Session Lifecycle

```
Scheduler tick
    |
    v
Session Manager: check if session of this tier is already running
    |
    +-- Running -> log "session still active, skipping", wait for next tick
    |
    +-- Not running -> proceed
           |
           v
    MCP Merger: merge repo configs into baseline
           |
           v
    Session Manager: create subprocess
           |
           +-- cmd = exec.Command("claude", "--model", model, "--print",
           |        "--prompt-file", promptFile, "--allowedTools", tools,
           |        "--append-system-prompt", envContext)
           |
           +-- pipe stdout and stderr
           |
           +-- store Session record in SQLite (status: running)
           |
           +-- start subprocess
                  |
                  v
           Goroutine: read stdout/stderr line by line
                  |
                  +-- send each line to SSE Hub
                  +-- append to in-memory buffer
                  +-- write to log file in $CLAUDEOPS_RESULTS_DIR
                  |
                  v
           Subprocess exits (or times out)
                  |
                  +-- record exit code, end time in SQLite
                  +-- update session status (completed/failed/timed_out)
                  +-- close SSE hub channel for this session
                  +-- notify scheduler that tier is available
```

### SSE Streaming Flow

```
Browser: GET /sessions/{id}/stream (Accept: text/event-stream)
    |
    v
HTTP Server: create SSE connection
    |
    +-- subscribe to SSE Hub for session {id}
    |
    +-- send buffered output (catchup)
    |
    +-- enter event loop:
           |
           +-- receive line from hub -> write SSE event to response
           |
           +-- client disconnects -> unsubscribe from hub
           |
           +-- session ends -> send "done" event -> close connection
```

### HTMX Page Interaction Flow

```
Browser loads /dashboard (full page)
    |
    +-- HTML includes hx-get="/partials/service-list"
    |       with hx-trigger="every 30s"
    |
    v
Every 30 seconds:
    |
    +-- HTMX sends GET /partials/service-list
    |
    +-- Server renders service-list.html template with current data
    |
    +-- HTMX swaps the response into the target div
    |
    +-- No full page reload
```

### Cooldown Check Flow (SQLite)

```
Session Manager: before remediation action
    |
    v
Query: SELECT COUNT(*) FROM cooldown_actions
       WHERE service = ? AND action_type = 'restart'
       AND timestamp > datetime('now', '-4 hours')
    |
    +-- count < 2 -> action permitted
    |       |
    |       +-- perform remediation
    |       |
    |       +-- INSERT INTO cooldown_actions
    |           (service, action_type, timestamp, success, tier)
    |           VALUES (?, 'restart', datetime('now'), ?, ?)
    |
    +-- count >= 2 -> action blocked
           |
           +-- send "needs human attention" notification
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
