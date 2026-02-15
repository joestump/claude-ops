# Design: Docker Compose Deployment

## Overview

Claude Ops is deployed as a Docker Compose application on a single host. The deployment consists of a primary watchdog container that runs the agent loop and an optional browser automation sidecar activated via Compose profiles. All configuration flows through environment variables and a `.env` file, persistent data is managed through volume mounts, and the container image is built from a single Dockerfile based on `node:22-slim`.

This design document describes the deployment architecture, component interactions, data flow, and key trade-offs of the Docker Compose packaging strategy.

## Architecture

### Service Topology

The Docker Compose file defines two services:

```
docker-compose.yaml
├── watchdog (always started)
│   ├── build: .  (Dockerfile)
│   ├── restart: unless-stopped
│   ├── environment (from .env)
│   └── volumes
│       ├── ./state:/state
│       ├── ./results:/results
│       └── /path/to/repo:/repos/<name>:ro
│
└── chrome (profile: browser, started only with --profile browser)
    ├── image: browserless/chromium
    ├── restart: unless-stopped
    └── ports: 9222:9222
```

The watchdog is the core service. It builds from the project Dockerfile, runs the `entrypoint.sh` loop, and performs all agent work. The chrome sidecar is optional, providing headless Chromium for browser-based automation tasks like credential rotation. Both services share the default Docker Compose network, allowing the watchdog to connect to the chrome service by hostname.

### Dockerfile Layers

The Docker image is built in a single stage from `node:22-slim`:

1. **System dependencies** -- `apt-get install` of `openssh-client`, `curl`, `dnsutils`, `jq`, `python3`, `python3-pip`, `python3-venv`. These provide the tools the Claude agent uses for health checks (curl, dig), data parsing (jq), remote access (ssh), and the Python runtime for apprise.

2. **Apprise** -- Installed via `pip3 install --break-system-packages apprise`. Provides notification capabilities across 80+ services.

3. **Claude Code CLI** -- Installed via `npm install -g @anthropic-ai/claude-code`. This is the agent runtime that interprets markdown prompts and executes tools.

4. **Project files** -- `COPY . .` brings in the entrypoint script, prompt files, check definitions, playbooks, and configuration.

5. **Directory creation** -- `mkdir -p /state /results /repos` creates mount points for volumes.

The image does not use a multi-stage build because there is no compilation step. All project files are runtime artifacts (markdown, shell scripts, JSON configuration).

### Profile Mechanism

Docker Compose profiles allow services to be conditionally started without requiring separate compose files or environment-variable-based conditional logic.

The chrome service is assigned to the `browser` profile:

```yaml
chrome:
  profiles:
    - browser
```

This means:
- `docker compose up -d` starts only the watchdog.
- `docker compose --profile browser up -d` starts both the watchdog and chrome.
- The chrome service definition remains in the same compose file, serving as documentation of the capability even when not active.

This approach avoids the common anti-pattern of maintaining multiple compose files (`docker-compose.yaml`, `docker-compose.browser.yaml`) or using `docker-compose.override.yaml` for optional components.

### Network Model

Docker Compose creates a default bridge network for all services in the compose file. The watchdog can reach the chrome service at hostname `chrome` on port 9222. No manual network creation is needed.

When the browser profile is not active, the chrome service simply does not exist on the network. The watchdog must handle this gracefully -- browser automation capabilities are unavailable when the sidecar is not running.

## Data Flow

### Startup Sequence

```
Operator: docker compose up -d
    │
    ├── Docker Compose reads docker-compose.yaml
    ├── Docker Compose reads .env file
    ├── Docker Compose interpolates environment variables into service definitions
    ├── Docker Compose creates default network (if not exists)
    ├── Docker Compose creates/starts watchdog container
    │   ├── Mounts ./state → /state
    │   ├── Mounts ./results → /results
    │   ├── Mounts operator repos → /repos/<name>
    │   ├── Injects environment variables
    │   └── Runs entrypoint.sh
    │       ├── Initializes cooldown state (if first run)
    │       ├── Merges MCP configs from /repos/*/.claude-ops/mcp.json
    │       └── Enters infinite loop:
    │           ├── Merge MCP configs
    │           ├── Invoke Claude CLI with tier 1 prompt
    │           ├── Log results to /results/
    │           └── Sleep CLAUDEOPS_INTERVAL seconds
    │
    └── (if --profile browser) Creates/starts chrome container
        └── Listens on port 9222 for CDP connections
```

### Configuration Flow

```
.env file (on host)
    │
    ├── ANTHROPIC_API_KEY ──────→ watchdog env → Claude CLI authentication
    ├── CLAUDEOPS_INTERVAL ─────→ watchdog env → entrypoint.sh loop sleep
    ├── CLAUDEOPS_TIER1_MODEL ──→ watchdog env → entrypoint.sh → claude --model
    ├── CLAUDEOPS_TIER2_MODEL ──→ watchdog env → entrypoint.sh → --append-system-prompt
    ├── CLAUDEOPS_TIER3_MODEL ──→ watchdog env → entrypoint.sh → --append-system-prompt
    ├── CLAUDEOPS_DRY_RUN ──────→ watchdog env → entrypoint.sh → --append-system-prompt
    └── CLAUDEOPS_APPRISE_URLS ─→ watchdog env → entrypoint.sh → --append-system-prompt
```

Environment variables follow a two-stage interpolation:
1. Docker Compose interpolates `${VAR:-default}` syntax in `docker-compose.yaml` using the `.env` file.
2. The `entrypoint.sh` reads the resulting container environment variables with `${VAR:-default}` Bash syntax and passes them to the Claude CLI.

### Persistence Model

```
Host filesystem              Container filesystem
─────────────────            ─────────────────────
./state/                  →  /state/
  └── cooldown.json              └── cooldown.json (read/write)

./results/                →  /results/
  └── run-*.log                  └── run-*.log (write)

/path/to/repo             →  /repos/<name>/ (read-only)
  ├── CLAUDE-OPS.md              ├── CLAUDE-OPS.md
  ├── .claude-ops/               ├── .claude-ops/
  └── ...                        └── ...
```

State and results directories use bind mounts from the project directory (`./state`, `./results`). These directories are created automatically by Docker if they do not exist on the host.

Infrastructure repos are mounted by the operator as additional volume entries. The `:ro` suffix is recommended but not enforced by the compose file -- the operator decides based on their security posture and whether Tier 3 remediation needs write access to repo files.

## Key Decisions

### Single compose file over split files

The entire deployment is defined in one `docker-compose.yaml` rather than using `docker-compose.override.yaml` or environment-specific variants. This keeps the deployment topology in a single, auditable location. The profiles feature handles the only variation point (browser sidecar) without file splitting.

**Reference:** ADR-0009 chose this approach for operator simplicity and auditability.

### `restart: unless-stopped` over `always`

The `unless-stopped` policy is preferred over `always` because it respects explicit operator intent. If an operator runs `docker compose stop`, the container should stay stopped -- even after a host reboot. The `always` policy would restart the container regardless, which could be surprising and undesirable (e.g., during maintenance).

### Bind mounts over named volumes

State, results, and repos use bind mounts (host path mapped to container path) rather than Docker named volumes. This makes the data directly accessible on the host filesystem for inspection, backup, and debugging. Named volumes would require `docker volume inspect` and `docker cp` to access data, adding friction for operators.

### `.env` over Docker secrets

Docker Compose supports a `secrets` directive, but it requires Docker Swarm mode for full functionality. Since Claude Ops targets single-host deployments without Swarm, `.env` files are the practical choice. The trade-off is that secrets are stored in plain text on disk, relying on host filesystem permissions for protection.

### Build-from-source as default

The compose file uses `build: .` rather than referencing a pre-built image. This means operators build the image locally by default. Pre-built images are available on GHCR for operators who prefer not to build, but this requires modifying the compose file to replace `build: .` with `image: ghcr.io/...`. The build-from-source default reduces the dependency on GHCR availability and ensures the operator always has the latest code.

## Trade-offs

### Gained

- **Operator simplicity**: Three-step deployment (clone, configure, `docker compose up -d`). No Kubernetes cluster, no Helm, no package manager, no systemd unit files.
- **Self-documenting deployment**: The compose file is both configuration and documentation. An operator can read it to understand exactly what will be deployed.
- **Profile-based optionality**: The browser sidecar exists in the compose file (documenting the capability) without being started by default.
- **Universal availability**: Docker Compose works on Linux, macOS, and Windows. No platform-specific instructions needed.
- **Automatic resilience**: `restart: unless-stopped` handles crash recovery and host reboots without external supervision.

### Lost

- **Multi-host deployment**: Docker Compose is strictly single-host. Distributing Claude Ops across multiple monitoring points would require a different orchestration strategy.
- **Self-healing of the compose stack**: No higher-level supervisor monitors the Docker daemon or compose stack. If Docker itself fails, there is no automatic recovery.
- **Rolling updates**: Upgrading requires `docker compose pull && docker compose up -d`, which briefly stops the watchdog. There is no blue-green or canary deployment capability.
- **Sophisticated secret management**: No encryption at rest for the `.env` file, no secret rotation, no audit logging of secret access.
- **Storage abstraction**: Operators manage host directory permissions and paths manually. There is no equivalent of Kubernetes PersistentVolumeClaims to abstract storage provisioning.

## Future Considerations

- **Watchtower integration**: Adding [Watchtower](https://containrrr.dev/watchtower/) as an optional profile could enable automatic image updates, reducing the manual upgrade burden.
- **Docker healthcheck directive**: Adding a `healthcheck` to the watchdog service definition would allow Docker to detect and restart a stalled agent (e.g., if the entrypoint loop hangs).
- **Named volumes for state**: If backup and migration become concerns, named volumes with backup labels could be introduced alongside or instead of bind mounts.
- **Kubernetes migration path**: If multi-host monitoring becomes a requirement, the compose file could serve as the basis for generating a Helm chart via `kompose` or a similar tool.
- **Docker Compose Watch**: The `watch` feature (file sync and rebuild on changes) could be used for development workflows, automatically rebuilding the image when prompt files or checks are modified.
