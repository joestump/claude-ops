---
sidebar_position: 9
sidebar_label: Docker Compose Deployment
---

# SPEC-0009: Docker Compose Deployment

## Overview

Claude Ops is packaged and deployed as a Docker Compose application targeting single-host environments. The deployment model uses a `docker-compose.yaml` file as the single source of truth for service definitions, volume mounts, environment variables, restart policies, and optional sidecar services. Operators deploy by cloning the repository, configuring a `.env` file, and running `docker compose up -d`. Optional components (such as the browser automation sidecar) are activated through Docker Compose profiles without modifying the base compose file.

This specification defines the requirements for the Docker Compose deployment topology, including service definitions, persistence, configuration, sidecar management, restart behavior, and CI/CD integration.

## Definitions

- **Watchdog**: The primary Claude Ops container that runs the entrypoint loop, invokes Claude models, and performs health checks and remediation.
- **Sidecar**: An auxiliary container that provides additional capabilities to the watchdog (e.g., Chromium for browser automation).
- **Profile**: A Docker Compose feature that allows services to be conditionally included in a deployment based on a named profile flag.
- **Operator**: The human user who deploys, configures, and manages a Claude Ops instance.
- **Compose file**: The `docker-compose.yaml` file that declares the complete deployment topology.
- **State directory**: The `/state` mount point inside the watchdog container where persistent cooldown and operational state is stored.
- **Results directory**: The `/results` mount point inside the watchdog container where run logs are written.
- **Repos directory**: The `/repos` mount point inside the watchdog container where infrastructure repositories are mounted for monitoring.

## Requirements

### REQ-1: Single-command deployment

The system MUST be deployable via a single `docker compose up -d` command after initial configuration. The operator MUST NOT be required to run additional setup scripts, install host-level dependencies (beyond Docker), or manually create Docker networks.

#### Scenario: First-time deployment

Given the operator has cloned the Claude Ops repository
And has created a `.env` file with at minimum `ANTHROPIC_API_KEY`
When they run `docker compose up -d`
Then the watchdog container starts successfully
And the entrypoint begins its health check loop
And no manual network or volume creation is required

#### Scenario: Deployment after host reboot

Given Claude Ops was previously deployed with `docker compose up -d`
And the host has rebooted
When Docker starts
Then the watchdog container automatically restarts due to the restart policy
And the health check loop resumes without operator intervention

### REQ-2: Declarative topology in a single compose file

The system MUST declare the complete deployment topology -- all services, volumes, environment variables, restart policies, and profiles -- in a single `docker-compose.yaml` file. The compose file MUST serve as both the runtime configuration and the authoritative documentation of the deployment architecture.

#### Scenario: Inspecting the deployment topology

Given an operator wants to understand what Claude Ops deploys
When they read the `docker-compose.yaml` file
Then they can see all services (watchdog, optional sidecars)
And all volume mounts (state, results, repos)
And all environment variables with their defaults
And all restart policies
And all optional profiles

### REQ-3: Watchdog service definition

The compose file MUST define a `watchdog` service with the following properties:

- The service MUST build from the project's `Dockerfile` (via `build: .`).
- The service MUST set `container_name: claude-ops`.
- The service MUST set `restart: unless-stopped` to ensure automatic recovery from container crashes and host reboots.
- The service MUST pass environment variables for `ANTHROPIC_API_KEY`, `CLAUDEOPS_INTERVAL`, `CLAUDEOPS_TIER1_MODEL`, `CLAUDEOPS_TIER2_MODEL`, `CLAUDEOPS_TIER3_MODEL`, `CLAUDEOPS_DRY_RUN`, and `CLAUDEOPS_APPRISE_URLS` from the `.env` file or with defaults.
- The service MUST mount volumes for persistent state (`/state`), run results (`/results`), and infrastructure repos (`/repos`).

#### Scenario: Watchdog starts with default configuration

Given a `.env` file with only `ANTHROPIC_API_KEY` set
When the operator runs `docker compose up -d`
Then the watchdog service starts with container name `claude-ops`
And uses the default interval of 3600 seconds
And uses `haiku` as the Tier 1 model
And uses `sonnet` as the Tier 2 model
And uses `opus` as the Tier 3 model
And operates in non-dry-run mode

#### Scenario: Watchdog starts with custom configuration

Given a `.env` file with `ANTHROPIC_API_KEY`, `CLAUDEOPS_INTERVAL=1800`, and `CLAUDEOPS_DRY_RUN=true`
When the operator runs `docker compose up -d`
Then the watchdog runs health checks every 1800 seconds
And operates in dry-run mode (observe only, no remediation)

### REQ-4: Persistent volume mounts

The system MUST use Docker volume mounts to persist data across container restarts and upgrades. The following mount points MUST be supported:

- `/state` -- cooldown state and operational data, mounted from `./state` on the host.
- `/results` -- run logs, mounted from `./results` on the host.
- `/repos` -- infrastructure repositories, mounted from operator-specified host paths.

Volume mounts for repos MUST support read-only mode (`:ro` suffix) to prevent the agent from modifying mounted repositories.

#### Scenario: State survives container recreation

Given the watchdog has been running and has written cooldown state to `/state/cooldown.json`
When the operator runs `docker compose down && docker compose up -d`
Then the new watchdog container reads the existing `cooldown.json` from the host's `./state` directory
And cooldown counters are preserved from the previous run

#### Scenario: Mounting infrastructure repos read-only

Given the operator has added a volume mount `-/path/to/ansible-repo:/repos/infra-ansible:ro` to the compose file
When the watchdog starts
Then the agent can read files from `/repos/infra-ansible/`
And any attempt to write to `/repos/infra-ansible/` fails due to read-only mount

#### Scenario: Results persist across upgrades

Given the watchdog has completed multiple health check runs
And run logs exist in the host's `./results` directory
When the operator pulls a new image and recreates the container
Then historical run logs remain intact on the host

### REQ-5: Optional browser sidecar via profiles

The system MUST support an optional browser automation sidecar (Chromium) that is NOT started by default. The sidecar MUST be included in the compose file under a named profile so that it can be activated with `docker compose --profile browser up -d`.

The sidecar service MUST:
- Use the `browserless/chromium` image.
- Be assigned to the `browser` profile.
- Set `restart: unless-stopped` for resilience.
- Expose port 9222 for Chrome DevTools Protocol access.
- NOT start when `docker compose up -d` is run without the `--profile browser` flag.

#### Scenario: Default deployment without browser sidecar

Given the operator runs `docker compose up -d` without the `--profile` flag
When Docker Compose starts services
Then only the watchdog container starts
And no Chromium container is created

#### Scenario: Deployment with browser automation

Given the operator needs browser-based credential rotation
When they run `docker compose --profile browser up -d`
Then both the watchdog and the Chromium sidecar containers start
And the Chromium container is accessible on port 9222
And the watchdog can connect to the browser via Chrome DevTools Protocol

#### Scenario: Adding browser sidecar to running deployment

Given the watchdog is already running via `docker compose up -d`
When the operator runs `docker compose --profile browser up -d`
Then the Chromium sidecar starts alongside the existing watchdog
And the watchdog is not restarted

### REQ-6: Environment-based configuration

All runtime configuration MUST be provided through environment variables, loaded from a `.env` file. The system MUST NOT require configuration files inside the container (beyond what is baked into the image) or command-line argument changes to the compose file.

The `.env` file MUST support at minimum:
- `ANTHROPIC_API_KEY` (required, no default)
- `CLAUDEOPS_INTERVAL` (optional, default: `3600`)
- `CLAUDEOPS_TIER1_MODEL` (optional, default: `haiku`)
- `CLAUDEOPS_TIER2_MODEL` (optional, default: `sonnet`)
- `CLAUDEOPS_TIER3_MODEL` (optional, default: `opus`)
- `CLAUDEOPS_DRY_RUN` (optional, default: `false`)
- `CLAUDEOPS_APPRISE_URLS` (optional, default: empty)

#### Scenario: Minimal configuration

Given the operator creates a `.env` file containing only `ANTHROPIC_API_KEY=sk-ant-...`
When they run `docker compose up -d`
Then the system starts with all default values
And no errors are raised for missing optional variables

#### Scenario: Notification configuration

Given the operator adds `CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/my-topic` to the `.env` file
When the watchdog detects an issue requiring notification
Then it sends notifications via the configured Apprise URL

### REQ-7: Restart policy and resilience

The watchdog service MUST use `restart: unless-stopped` as its restart policy. This ensures:
- The container automatically restarts after a crash (non-zero exit code).
- The container automatically restarts after a host reboot (assuming Docker is configured to start on boot).
- The container does NOT restart if it was explicitly stopped by the operator via `docker compose stop` or `docker compose down`.

The browser sidecar MUST also use `restart: unless-stopped`.

#### Scenario: Watchdog crash recovery

Given the watchdog container crashes due to an unhandled error
When Docker detects the container has exited with a non-zero exit code
Then Docker automatically restarts the container
And the entrypoint loop resumes from the beginning

#### Scenario: Explicit stop is respected

Given the operator runs `docker compose stop`
When the host reboots
Then the watchdog does NOT automatically restart
Because the operator explicitly stopped it

### REQ-8: CI/CD image publishing

The system MUST support building and publishing the Docker image via CI/CD pipelines. The CI/CD pipeline MUST:
- Build the Docker image from the project's `Dockerfile`.
- Push the image to GitHub Container Registry (GHCR).
- Tag images with semantic version tags on version tag pushes.
- Tag images on pushes to the `main` branch.

Operators MUST be able to pull pre-built images instead of building locally.

#### Scenario: Release workflow

Given a developer pushes a version tag (e.g., `v1.2.0`) to the repository
When the CI/CD pipeline runs
Then the Docker image is built and pushed to GHCR
And the image is tagged with `1.2.0` and `latest`

#### Scenario: Operator uses pre-built image

Given a pre-built image exists on GHCR
When the operator modifies the compose file to reference the GHCR image instead of `build: .`
And runs `docker compose pull && docker compose up -d`
Then the system starts using the pre-built image without local building

### REQ-9: Dockerfile structure

The Dockerfile MUST:
- Use `node:22-slim` as the base image to support the Claude Code CLI.
- Install system dependencies required for health checks and remediation: `openssh-client`, `curl`, `dnsutils`, `jq`, `python3`, `python3-pip`, `python3-venv`.
- Install the `apprise` Python package for notifications.
- Install the Claude Code CLI globally via `npm install -g @anthropic-ai/claude-code`.
- Set the working directory to `/app` and copy all project files.
- Create the `/state`, `/results`, and `/repos` directories.
- Set `entrypoint.sh` as the container entrypoint.

#### Scenario: Container has required tools

Given the Docker image has been built from the Dockerfile
When the watchdog container starts
Then the following tools are available: `claude`, `curl`, `dig`, `jq`, `ssh`, `apprise`, `python3`
And the Claude Code CLI is globally installed and executable

#### Scenario: Clean image build

Given a fresh clone of the repository
When the operator runs `docker compose build`
Then the image builds successfully with no errors
And all dependencies are installed

### REQ-10: SSH key mounting for remote access

The compose file SHOULD include commented examples showing how to mount SSH keys for remote host access. The mount configuration MUST use read-only mode (`:ro`) for SSH keys and known_hosts files.

#### Scenario: Operator enables SSH access

Given the operator uncomments and configures the SSH key volume mounts in the compose file
When the watchdog starts
Then the agent has SSH access to remote hosts via the mounted key
And the key file is mounted read-only to prevent modification

## References

- [ADR-0009: Docker Compose Deployment](/docs/adrs/ADR-0009-docker-compose-deployment.md)
- [ADR-0001: Tiered Model Escalation](/docs/adrs/ADR-0001-tiered-model-escalation.md)
- [Docker Compose Documentation](https://docs.docker.com/compose/)
- [Docker Compose Profiles](https://docs.docker.com/compose/profiles/)

---

# Design: Docker Compose Deployment

## Overview

Claude Ops is deployed as a Docker Compose application on a single host. The deployment consists of a primary watchdog container that runs the agent loop and an optional browser automation sidecar activated via Compose profiles. All configuration flows through environment variables and a `.env` file, persistent data is managed through volume mounts, and the container image is built from a single Dockerfile based on `node:22-slim`.

This design document describes the deployment architecture, component interactions, data flow, and key trade-offs of the Docker Compose packaging strategy.

## Architecture

### Service Topology

The Docker Compose file defines two services:

```
docker-compose.yaml
+-- watchdog (always started)
|   +-- build: .  (Dockerfile)
|   +-- restart: unless-stopped
|   +-- environment (from .env)
|   +-- volumes
|       +-- ./state:/state
|       +-- ./results:/results
|       +-- /path/to/repo:/repos/<name>:ro
|
+-- chrome (profile: browser, started only with --profile browser)
    +-- image: browserless/chromium
    +-- restart: unless-stopped
    +-- ports: 9222:9222
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
    |
    +-- Docker Compose reads docker-compose.yaml
    +-- Docker Compose reads .env file
    +-- Docker Compose interpolates environment variables into service definitions
    +-- Docker Compose creates default network (if not exists)
    +-- Docker Compose creates/starts watchdog container
    |   +-- Mounts ./state -> /state
    |   +-- Mounts ./results -> /results
    |   +-- Mounts operator repos -> /repos/<name>
    |   +-- Injects environment variables
    |   +-- Runs entrypoint.sh
    |       +-- Initializes cooldown state (if first run)
    |       +-- Merges MCP configs from /repos/*/.claude-ops/mcp.json
    |       +-- Enters infinite loop:
    |           +-- Merge MCP configs
    |           +-- Invoke Claude CLI with tier 1 prompt
    |           +-- Log results to /results/
    |           +-- Sleep CLAUDEOPS_INTERVAL seconds
    |
    +-- (if --profile browser) Creates/starts chrome container
        +-- Listens on port 9222 for CDP connections
```

### Configuration Flow

```
.env file (on host)
    |
    +-- ANTHROPIC_API_KEY -------> watchdog env -> Claude CLI authentication
    +-- CLAUDEOPS_INTERVAL ------> watchdog env -> entrypoint.sh loop sleep
    +-- CLAUDEOPS_TIER1_MODEL ---> watchdog env -> entrypoint.sh -> claude --model
    +-- CLAUDEOPS_TIER2_MODEL ---> watchdog env -> entrypoint.sh -> --append-system-prompt
    +-- CLAUDEOPS_TIER3_MODEL ---> watchdog env -> entrypoint.sh -> --append-system-prompt
    +-- CLAUDEOPS_DRY_RUN -------> watchdog env -> entrypoint.sh -> --append-system-prompt
    +-- CLAUDEOPS_APPRISE_URLS --> watchdog env -> entrypoint.sh -> --append-system-prompt
```

Environment variables follow a two-stage interpolation:
1. Docker Compose interpolates `${VAR:-default}` syntax in `docker-compose.yaml` using the `.env` file.
2. The `entrypoint.sh` reads the resulting container environment variables with `${VAR:-default}` Bash syntax and passes them to the Claude CLI.

### Persistence Model

```
Host filesystem              Container filesystem
-----------------            ---------------------
./state/                  ->  /state/
  +-- cooldown.json              +-- cooldown.json (read/write)

./results/                ->  /results/
  +-- run-*.log                  +-- run-*.log (write)

/path/to/repo             ->  /repos/<name>/ (read-only)
  +-- CLAUDE-OPS.md              +-- CLAUDE-OPS.md
  +-- .claude-ops/               +-- .claude-ops/
  +-- ...                        +-- ...
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
