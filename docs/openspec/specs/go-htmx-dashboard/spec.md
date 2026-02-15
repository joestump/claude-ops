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

- [ADR-0008: Build a Go/HTMX/DaisyUI Web Dashboard](/docs/adrs/ADR-0008-go-htmx-web-dashboard.md)
- [ADR-0007: Persist Cooldown State in JSON File](/docs/adrs/ADR-0007-json-file-cooldown-state.md) -- the JSON cooldown state that SQLite will replace
- [ADR-0010: Claude Code CLI Subprocess Execution](/docs/adrs/ADR-0010-claude-code-cli-subprocess.md) -- related subprocess management decisions
- [entrypoint.sh](/entrypoint.sh) -- the bash entrypoint that the Go binary replaces
- [CLAUDE.md](/CLAUDE.md) -- execution flow, permission tiers, and cooldown rules
- [HTMX Documentation](https://htmx.org/docs/)
- [DaisyUI Components](https://daisyui.com/components/)
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) -- pure-Go SQLite driver
