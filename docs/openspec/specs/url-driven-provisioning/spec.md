---
status: accepted
date: 2026-03-09
---

# SPEC-0028: URL-Driven Service Provisioning

## Overview

Claude Ops currently operates in a reactive posture: monitoring existing services, detecting failures, and remediating within the constraints of its tiered permission model. Operators who want to deploy a new service must manually read documentation, write deployment configuration, commit it, and run the deployment. This specification defines a provisioning workflow where the operator provides a documentation URL and a deployment target, and the agent handles the full lifecycle: fetching requirements, discovering repo conventions, planning, deploying, verifying health, and codifying the result via PR.

Provisioning Mode is a distinct execution mode, separate from the three monitoring tiers. It cannot be reached by tier escalation and cannot escalate to or from monitoring tiers. It is triggered exclusively by explicit user action.

This specification implements [ADR-0027: URL-Driven Service Provisioning via Provisioning Mode](/docs/adrs/ADR-0027-url-driven-service-provisioning.md).

## Definitions

- **Provisioning Mode**: A distinct execution mode, separate from monitoring tiers, that allows the agent to deploy new services or make basic operational edits to existing services. Triggered exclusively by explicit user action.
- **Documentation URL**: A URL provided by the operator that points to deployment documentation for the service to be provisioned. The agent fetches and parses this URL to extract deployment requirements.
- **Deployment Target**: A host or environment identifier, as defined in the repo's manifest or inventory, where the service will be deployed.
- **Provisioning Lifecycle**: The ordered sequence of phases that a provisioning session follows: Fetch, Discover, Plan, Execute, Verify, Codify/Rollback.
- **Provisioning Prompt**: The dedicated prompt file (`prompts/provision.md`) loaded for provisioning sessions. Defines the provisioning workflow, permissions, and constraints.
- **Convention Discovery**: The process by which the agent reads a repo's CLAUDE-OPS.md manifest, existing service configurations, and provisioning-specific extensions to learn how the repo deploys services.
- **Internal Pattern Mapping**: The translation of external deployment requirements (from documentation) into the repo's internal conventions (directory structure, file format, naming, infrastructure idioms).
- **Provisioning-Specific Extensions**: Optional repo-provided instruction files (`.claude-ops/playbooks/provision.md` or `.claude-ops/skills/provision.md`) that describe the repo's provisioning procedure and take precedence over inferred conventions.
- **Codification**: The process of committing successful deployment configuration to the repo and submitting it as a PR for human review.
- **Rollback**: The complete cleanup of a failed provisioning attempt, returning the deployment target to its pre-provisioning state.
- **Provisioning Clone**: A temporary clone of the target repo at `/tmp/provision-<service>-<timestamp>/` used as the working directory for configuration changes and PR creation.

## Requirements

### SPEC-0028-REQ-1: Trigger Mechanism

Provisioning MUST be triggered exclusively through explicit user action. The agent MUST NOT autonomously initiate provisioning during a scheduled monitoring cycle. The system MUST support three trigger methods:

1. **Dashboard**: A dedicated "Provision Service" form that accepts a documentation URL and a deployment target name.
2. **API**: `POST /api/v1/provision` with a JSON body containing `url` (string, required) and `target` (string, required). The endpoint MUST require Bearer token authentication using the same `CLAUDEOPS_CHAT_API_KEY` as other API endpoints.
3. **Ad-hoc prompt**: Via the existing `TriggerAdHoc` mechanism (SPEC-0012). The session manager MUST detect provisioning intent in the prompt and route to `prompts/provision.md` instead of a monitoring prompt.

The API MUST return `201 Created` with a JSON body containing the session ID on success. The API MUST return `400 Bad Request` if `url` or `target` is missing or empty. The API MUST return `401 Unauthorized` if the Bearer token is missing or invalid.

Provisioning sessions MUST be recorded in the database with `trigger = "provision"` and MUST appear in the dashboard with a distinct visual indicator. The provisioning prompt file (`prompts/provision.md`) MUST be loaded only for provisioning sessions.

The model used for provisioning MUST be configurable via the `CLAUDEOPS_PROVISION_MODEL` environment variable. If not set, it MUST default to the Tier 3 model.

#### Scenario: API trigger with valid credentials

- **WHEN** an operator sends `POST /api/v1/provision` with `{"url": "https://docs.example.com/deploy", "target": "ie01"}` and a valid Bearer token
- **THEN** the system MUST return `201 Created` with `{"session_id": "<id>"}`
- **AND** a provisioning session MUST be started using `prompts/provision.md`

#### Scenario: API trigger with missing URL

- **WHEN** an operator sends `POST /api/v1/provision` with `{"target": "ie01"}` (no URL)
- **THEN** the system MUST return `400 Bad Request`

#### Scenario: API trigger without authentication

- **WHEN** a request is sent to `POST /api/v1/provision` without a Bearer token
- **THEN** the system MUST return `401 Unauthorized`

#### Scenario: Ad-hoc prompt routes to provisioning

- **WHEN** an operator triggers an ad-hoc session with the prompt "Provision https://docs.example.com/deploy to ie01"
- **THEN** the session manager MUST detect provisioning intent
- **AND** the session MUST load `prompts/provision.md` instead of a monitoring prompt

#### Scenario: Scheduled monitoring cannot trigger provisioning

- **WHEN** a scheduled monitoring cycle runs
- **THEN** the system MUST NOT load `prompts/provision.md`
- **AND** provisioning capabilities MUST NOT be available to the agent

### SPEC-0028-REQ-2: Documentation Fetch

The agent MUST fetch the documentation URL provided by the operator and extract deployment requirements. The agent MUST use WebFetch to retrieve and parse the documentation.

The agent MUST extract the following requirements when documented:

- Container image (registry, name, tag)
- Required ports (and protocols)
- Required volumes and persistent storage paths
- Environment variables, with classification of each as either a secret or a configuration value
- Dependencies (databases, other services, external APIs)
- Resource requirements (memory, CPU) if documented
- Health check endpoints (HTTP paths, TCP ports)

The agent MAY use WebSearch to supplement the documentation with additional context (e.g., locating the correct Docker image name when the documentation references only a source repository).

If the documentation URL is unreachable or returns an error, the agent MUST report the failure to the operator via notification and MUST NOT proceed to subsequent phases.

#### Scenario: Successful documentation fetch

- **WHEN** the agent fetches `https://docs.example.com/deploy` and the page contains deployment instructions
- **THEN** the agent MUST extract the container image, ports, volumes, environment variables, and health check endpoints
- **AND** the agent MUST classify each environment variable as secret or configuration

#### Scenario: Documentation URL unreachable

- **WHEN** the agent attempts to fetch a URL that returns HTTP 404 or a network error
- **THEN** the agent MUST send an Apprise notification reporting the failure
- **AND** the agent MUST NOT proceed to the Discover phase

#### Scenario: Supplementary search

- **WHEN** the documentation references a GitHub repository but does not specify a Docker image
- **THEN** the agent MAY use WebSearch to locate the correct container image on Docker Hub or the project's container registry

### SPEC-0028-REQ-3: Convention Discovery

The agent MUST discover the target repo's deployment conventions before generating a deployment plan. The agent MUST NOT assume any specific deployment framework (Ansible, Docker Compose, Helm, or any other tooling). The repo's conventions are the sole authority on how deployment is performed.

The agent MUST examine the following sources during discovery:

1. **CLAUDE-OPS.md manifest** -- capabilities, rules, deployment targets, and tooling declarations.
2. **Provisioning-specific extensions** -- `.claude-ops/playbooks/provision.md` or `.claude-ops/skills/provision.md`. If present, these MUST take precedence over inferred conventions.
3. **Existing service configurations** -- the agent MUST read multiple existing services to identify patterns for file format, directory structure, naming conventions, port allocation, volume structure, DNS/reverse-proxy/ingress handling, and database provisioning.
4. **Deployment tooling** -- commands, playbooks, or scripts used to deploy services.
5. **Inventory/host structure** -- where the deployment target maps in the repo's infrastructure model.
6. **Infrastructure idioms** -- how the repo handles cross-cutting concerns (reverse proxying, DNS, TLS, logging, database provisioning). The agent MUST discover and follow these idioms so that a newly provisioned service receives the same infrastructure wiring as existing services.

#### Scenario: Discovery with provisioning-specific extension

- **WHEN** the target repo provides `.claude-ops/playbooks/provision.md`
- **THEN** the agent MUST use the instructions in that file as the primary provisioning procedure
- **AND** inferred conventions MUST be supplementary, not overriding

#### Scenario: Discovery without provisioning extensions

- **WHEN** the target repo does not provide `.claude-ops/playbooks/provision.md` or `.claude-ops/skills/provision.md`
- **THEN** the agent MUST infer the provisioning procedure by examining existing service configurations, deployment tooling, and directory structure

#### Scenario: Framework-agnostic discovery

- **WHEN** the target repo uses Ansible for deployments
- **THEN** the agent MUST generate Ansible-compatible configuration
- **WHEN** the target repo uses Docker Compose for deployments
- **THEN** the agent MUST generate Docker Compose-compatible configuration

#### Scenario: Infrastructure idiom discovery

- **WHEN** the target repo uses Docker labels for reverse proxy auto-discovery (e.g., Traefik labels, Caddy Docker Proxy labels)
- **THEN** the agent MUST include the appropriate labels in the service configuration
- **AND** the new service MUST receive reverse proxy wiring consistent with existing services

### SPEC-0028-REQ-4: Internal Pattern Mapping

The agent MUST translate external deployment requirements (from the Fetch phase) into the repo's internal conventions (from the Discover phase). The mapped plan MUST resemble "another service in this repo" -- not a foreign deployment.

The agent MUST map each external requirement to the repo's idiom:

- **Container image** -- the repo's image declaration format (inventory field, compose service, helm values, etc.)
- **Ports** -- the repo's port allocation convention (port range, assignment scheme)
- **Volumes** -- the repo's volume path convention (e.g., `/volumes/<service>/`, host-specific paths, NFS mounts)
- **Environment variables** -- the repo's env var convention (inline, env files, secrets manager references)
- **Dependencies (databases)** -- the repo's database provisioning pattern. If the repo provisions databases as part of service deployment, the agent MUST use that convention rather than deploying a sidecar database.
- **Reverse proxy / ingress** -- the repo's convention for exposing services. If the repo handles this via its deployment conventions, the agent MUST follow that pattern.
- **DNS** -- the repo's DNS convention. If the repo's tooling automatically creates DNS records (e.g., via Ansible tasks triggered by inventory fields), the agent MUST include the appropriate fields. DNS is only a manual post-provisioning step if the repo has no automation for it.
- **Health checks** -- the repo's health check convention (Docker HEALTHCHECK, monitoring endpoint registration)

#### Scenario: Mapping ports to repo convention

- **WHEN** the external documentation specifies port 8080
- **AND** the repo allocates ports via an inventory field `port:` in host vars
- **THEN** the agent MUST assign an unused port following the repo's convention and map it to the service's internal port 8080

#### Scenario: Mapping volumes to repo convention

- **WHEN** the external documentation requires persistent storage at `/data`
- **AND** the repo uses the convention `/volumes/<service>/data`
- **THEN** the agent MUST create the volume mapping using `/volumes/<service-name>/data:/data`

#### Scenario: Database dependency mapping

- **WHEN** the external documentation requires a PostgreSQL database
- **AND** the repo provisions databases via an inventory field `db:` that triggers schema creation during deployment
- **THEN** the agent MUST use the repo's database provisioning convention
- **AND** the agent MUST NOT deploy a separate PostgreSQL container

### SPEC-0028-REQ-5: Plan Validation

Before executing any deployment, the agent MUST validate the mapped plan against the following checks:

1. **Port conflict check**: The agent MUST verify that required ports are not already in use by existing services. This check MUST be performed by reading the repo's inventory or configuration, not by probing the network.
2. **Dependency satisfaction**: The agent MUST verify that all dependencies (databases, services) either already exist and are healthy, or will be provisioned as part of the deployment per the repo's conventions. Dependencies that require operator action MUST be flagged.
3. **Volume path planning**: The agent MUST confirm volume paths follow the repo's conventions and do not conflict with existing mounts.
4. **Secret identification**: The agent MUST identify which environment variables are secrets (see REQ-6 for handling).
5. **Resource estimation**: If the repo tracks resource allocation, the agent SHOULD verify the target has sufficient capacity.
6. **Naming**: The agent MUST generate a service name following the repo's naming conventions.

The plan MUST be sent to the operator via Apprise notification and MUST be logged for auditability.

#### Scenario: Port conflict detected

- **WHEN** the plan assigns port 3000 to the new service
- **AND** an existing service in the repo's configuration already uses port 3000 on the same host
- **THEN** the agent MUST detect the conflict
- **AND** the agent MUST either select an alternative port or report the conflict to the operator

#### Scenario: Unsatisfied dependency flagged

- **WHEN** the service requires a Redis instance
- **AND** no Redis instance exists on the target and the repo's conventions do not auto-provision Redis
- **THEN** the agent MUST flag the dependency as requiring operator action
- **AND** the agent MUST include the dependency gap in the plan notification

#### Scenario: Plan notification sent

- **WHEN** the agent completes plan validation
- **THEN** the agent MUST send an Apprise notification containing: service name, target host, assigned ports, volume paths, identified secrets, and any flagged dependencies
- **AND** the plan MUST be logged to the session results

### SPEC-0028-REQ-6: Secret Handling

The agent MUST NOT use the LLM to generate secret values. Model outputs are deterministic and logged, making them unsuitable as cryptographic material.

The agent MAY use standard system tools to generate cryptographically sound values:

- `openssl rand -hex 32` or `openssl rand -base64 32` for tokens, seeds, and passwords
- `head -c 32 /dev/urandom | base64` as an alternative

The agent MAY provision credentials using tooling available in mounted repos:

- Terraform to create and store secrets in parameter stores
- Ansible to provision OIDC client/secret from an identity provider
- CLI tools to register API keys with external services

Secrets MUST be deferred to the operator only when no available tooling can provision them (e.g., a vendor-issued API key that requires manual registration with a third party).

The plan notification (REQ-5) MUST distinguish between secrets that can be auto-provisioned and secrets that require operator action.

#### Scenario: LLM-generated secret rejected

- **WHEN** the agent needs to generate a database password
- **THEN** the agent MUST NOT use the LLM to produce the password value
- **AND** the agent MUST use `openssl rand` or an equivalent system tool

#### Scenario: System-tool secret generation

- **WHEN** the documentation requires an `APP_SECRET` environment variable with a random value
- **THEN** the agent MAY generate it via `openssl rand -hex 32`
- **AND** the generated value MUST be passed directly to the deployment, not logged in the session output

#### Scenario: Repo-tooling credential provisioning

- **WHEN** the service requires an OIDC client and the repo provides an Ansible role for identity provider integration
- **THEN** the agent MAY use that Ansible role to provision the OIDC client and secret

#### Scenario: Secret deferred to operator

- **WHEN** the service requires a third-party API key that cannot be provisioned via any available tooling
- **THEN** the agent MUST flag the secret as requiring operator action
- **AND** the plan notification MUST include instructions for the manual step

### SPEC-0028-REQ-7: Deployment Execution

The agent MUST deploy the service using whatever tooling the repo provides, as discovered in the Convention Discovery phase (REQ-3). The agent MUST NOT hardcode knowledge of any specific deployment framework.

Execution MUST follow these constraints:

1. The agent MUST work from a temporary clone of the target repo at `/tmp/provision-<service>-<timestamp>/`. Repos mounted under `$CLAUDEOPS_REPOS_DIR` are read-only (ADR-0005).
2. Configuration changes MUST be made in the clone, not in the mounted repo.
3. Deployment MUST be executed on the deployment target via the same SSH/remote access methods used by the monitoring tiers.
4. The agent MUST follow the repo's discovered deployment procedure -- the Discover phase (REQ-3) and any provisioning-specific extensions are the sole authority on how deployment is performed.

#### Scenario: Clone-based execution

- **WHEN** the agent begins the Execute phase
- **THEN** the agent MUST clone the repo to `/tmp/provision-<service>-<timestamp>/`
- **AND** all configuration changes MUST be made in the clone

#### Scenario: Deployment via repo's tooling

- **WHEN** the repo uses Ansible for deployments
- **THEN** the agent MUST generate the necessary Ansible configuration and run the appropriate playbook from the clone
- **WHEN** the repo uses Docker Compose
- **THEN** the agent MUST generate the compose service definition and run `docker compose up -d` from the clone

#### Scenario: Read-only mount preserved

- **WHEN** the agent executes a deployment
- **THEN** the agent MUST NOT modify any files in the mounted repo under `$CLAUDEOPS_REPOS_DIR`

### SPEC-0028-REQ-8: Health Verification

After deployment, the agent MUST verify the service is healthy before proceeding to codification. The agent MUST run the following health checks as applicable:

1. HTTP health endpoint (if the service exposes one)
2. TCP port reachability
3. Container status (running, healthy)
4. Log inspection for startup errors
5. Dependency connectivity (can the service reach its database, etc.)

Verification MUST use a configurable timeout. The timeout MUST be configurable via the `CLAUDEOPS_PROVISION_VERIFY_TIMEOUT` environment variable. If not set, the timeout MUST default to 120 seconds. The agent MUST retry health checks within the timeout period.

The service MUST pass all applicable health checks before the deployment is considered successful. If any health check fails after the timeout expires, the deployment MUST proceed to the Rollback phase (REQ-10).

#### Scenario: Healthy service passes verification

- **WHEN** the deployed service responds with HTTP 200 on its health endpoint within the timeout
- **AND** the container status is "running"
- **AND** no startup errors appear in logs
- **THEN** verification MUST succeed and the agent MUST proceed to the Codify phase

#### Scenario: Service fails health check

- **WHEN** the deployed service does not respond on its health endpoint within the timeout
- **THEN** verification MUST fail
- **AND** the agent MUST proceed to the Rollback phase

#### Scenario: Custom timeout

- **WHEN** `CLAUDEOPS_PROVISION_VERIFY_TIMEOUT` is set to `300`
- **THEN** the agent MUST wait up to 300 seconds for health checks to pass

### SPEC-0028-REQ-9: Codification on Success

When verification passes, the agent MUST codify the deployment by creating a PR following ADR-0018 conventions.

The agent MUST:

1. Commit the configuration changes in the cloned repo.
2. Push to a feature branch named `claude-ops/provision/<service-name>`.
3. Open a PR with a structured body containing:
   - What service was deployed
   - Source documentation URL
   - Configuration generated (ports, volumes, environment variables)
   - Health check results
   - The operator who requested the provisioning
   - A note that the service is already running and the PR codifies the existing state
4. Apply the labels `claude-ops` and `provision` to the PR.
5. Send an Apprise notification: "Service `<name>` provisioned successfully. PR #N opened to codify the configuration. Human review required before merging."

The PR MUST follow all existing ADR-0018 conventions for branch naming, labels, and the provider interface. The only difference from monitoring-cycle PRs is that the service is already running -- the PR codifies working state rather than proposing future state.

The PR MUST include a footer noting that the service is live and the PR codifies existing state. This distinguishes provisioning PRs from standard monitoring PRs.

#### Scenario: PR created on successful provisioning

- **WHEN** a service passes health verification
- **THEN** the agent MUST create a branch `claude-ops/provision/<service-name>`
- **AND** the agent MUST open a PR with the structured body described above
- **AND** the PR MUST have labels `claude-ops` and `provision`

#### Scenario: PR body includes documentation URL

- **WHEN** the agent creates a provisioning PR
- **THEN** the PR body MUST include the original documentation URL provided by the operator

#### Scenario: Apprise notification on success

- **WHEN** a provisioning PR is created
- **THEN** the agent MUST send an Apprise notification containing the service name, target host, and PR URL

### SPEC-0028-REQ-10: Rollback on Failure

When verification fails, the agent MUST perform a complete rollback that returns the deployment target to its pre-provisioning state. The agent MUST execute the following cleanup steps:

1. Remove the deployed service from the target (stop container, remove container, etc.).
2. Clean up any volumes created during the attempt. The agent MUST NOT delete pre-existing volumes.
3. Remove any configuration files placed on the target during execution.
4. Clean up the specific temporary clone directory: `rm -rf /tmp/provision-<service>-<timestamp>/`. The path MUST be scoped to this session's directory. The agent MUST NOT use a wildcard pattern (e.g., `/tmp/provision-*`) that could match other sessions.
5. Send an Apprise notification: "Provisioning of `<name>` failed. Rolled back all changes. Details: <failure reason>"
6. Log the full rollback sequence for auditability, including each cleanup step and its result.

After rollback, the deployment target MUST have no remnants of the failed attempt: no dangling containers, no orphaned port allocations, no leftover configuration fragments.

#### Scenario: Complete rollback on failure

- **WHEN** health verification fails for a newly provisioned service
- **THEN** the agent MUST stop and remove the service's container
- **AND** the agent MUST remove volumes created during this attempt
- **AND** the agent MUST remove configuration files placed on the target
- **AND** the agent MUST remove the temporary clone directory

#### Scenario: Pre-existing volumes preserved

- **WHEN** the agent performs rollback
- **AND** the target host has pre-existing volumes for other services
- **THEN** the agent MUST NOT delete any pre-existing volumes
- **AND** only volumes created during this provisioning attempt MUST be removed

#### Scenario: Scoped temp directory cleanup

- **WHEN** the agent cleans up the temporary clone
- **THEN** the agent MUST remove only `/tmp/provision-<service>-<timestamp>/`
- **AND** the agent MUST NOT use a wildcard pattern that could match other provisioning sessions' directories

#### Scenario: Rollback notification

- **WHEN** rollback completes
- **THEN** the agent MUST send an Apprise notification containing the service name, failure reason, and confirmation that all changes were rolled back

### SPEC-0028-REQ-11: Concurrent Session Handling

Provisioning sessions MUST be subject to the same single-session constraint as monitoring sessions. Only one session (of any type) MAY run at a time.

If a provisioning request arrives while any session (monitoring or provisioning) is active, the API MUST return `409 Conflict` with a descriptive message. This is consistent with the existing `TriggerAdHoc` behavior (SPEC-0012).

A scheduled monitoring cycle MUST NOT start while a provisioning session is in progress.

#### Scenario: Provisioning rejected during active session

- **WHEN** a monitoring session is currently running
- **AND** an operator sends `POST /api/v1/provision`
- **THEN** the API MUST return `409 Conflict`
- **AND** the running session MUST NOT be interrupted

#### Scenario: Monitoring blocked during provisioning

- **WHEN** a provisioning session is in progress
- **AND** the scheduled monitoring timer fires
- **THEN** the monitoring cycle MUST NOT start
- **AND** the system MUST wait until the provisioning session completes

#### Scenario: Second provisioning rejected

- **WHEN** a provisioning session is currently running
- **AND** another provisioning request arrives
- **THEN** the API MUST return `409 Conflict`

### SPEC-0028-REQ-12: Permission Model

Provisioning Mode MUST have its own permission set, distinct from the three monitoring tiers.

**Provisioning Mode MAY:**

- Perform everything Tier 3 can do (read files, health checks, restart services, deploy, etc.)
- Create new service definitions in cloned repos (new files, new entries in existing config files)
- Deploy new services to hosts listed in the repo's inventory
- Create new persistent volume directories following the repo's conventions
- Generate and apply new configuration (playbook entries, compose services, helm values)
- Write temporary files to `/tmp/` for the clone and deployment workspace
- Perform minor modifications to existing services when explicitly requested by the operator: version bumps, resource limit adjustments, replica count changes, environment variable updates. The same deploy-verify-rollback lifecycle applies.

**Provisioning Mode MUST NOT:**

- Remove existing services or delete their data
- Restructure existing inventory, configuration layouts, or deployment patterns
- Use the LLM to generate secret values (see REQ-6)
- Deploy to hosts not listed in the repo's inventory
- Run during a scheduled monitoring cycle (provisioning is user-triggered only)
- Modify the runbook, prompts, or Claude Ops' own configuration
- Make changes that affect other services' operation (e.g., changing a shared database's config, modifying shared network rules)

Permission enforcement MUST follow the same two-layer model as the monitoring tiers (ADR-0003): `--allowedTools` at the CLI level and prompt instructions for semantic boundaries.

#### Scenario: Provisioning creates new service files

- **WHEN** the agent provisions a new service
- **THEN** the agent MAY create new files in the cloned repo (host vars, playbook entries, compose services)

#### Scenario: Provisioning cannot remove existing services

- **WHEN** the agent is in provisioning mode
- **THEN** the agent MUST NOT remove, stop, or delete any existing service or its data

#### Scenario: Provisioning restricted to inventory hosts

- **WHEN** the operator specifies a target not listed in the repo's inventory
- **THEN** the agent MUST reject the provisioning request and report the invalid target

#### Scenario: Minor operational edit

- **WHEN** the operator explicitly requests a version bump for an existing service via provisioning
- **THEN** the agent MAY perform the version bump
- **AND** the agent MUST follow the same deploy-verify-rollback lifecycle as new provisioning

### SPEC-0028-REQ-13: Provisioning-Specific Extensions

Repos MAY provide provisioning-specific instruction files that describe the repo's provisioning procedure and internal idioms. These extensions MUST take precedence over inferred conventions when present.

The system MUST check for the following extension paths within each mounted repo:

1. `.claude-ops/playbooks/provision.md` -- step-by-step provisioning procedure
2. `.claude-ops/skills/provision.md` -- tool orchestration for provisioning (per SPEC-0023)

These files MAY document:

- The repo's specific provisioning workflow
- Internal idioms for DNS, reverse proxy, database provisioning, and secret management
- Required post-provisioning steps
- Validation criteria specific to the repo

Extension discovery MUST follow the same convention-based scanning defined in SPEC-0005.

#### Scenario: Repo provides provisioning playbook

- **WHEN** the target repo contains `.claude-ops/playbooks/provision.md`
- **THEN** the agent MUST read and follow its instructions as the primary provisioning procedure
- **AND** inferred conventions from existing service configurations are supplementary

#### Scenario: Repo provides provisioning skill

- **WHEN** the target repo contains `.claude-ops/skills/provision.md`
- **THEN** the agent MUST use the skill for tool discovery and execution during provisioning
- **AND** the skill MUST integrate with the session-level tool inventory (SPEC-0023 REQ-3)

#### Scenario: No provisioning extensions

- **WHEN** the target repo provides neither `.claude-ops/playbooks/provision.md` nor `.claude-ops/skills/provision.md`
- **THEN** the agent MUST infer the provisioning procedure entirely from existing service configurations and repo conventions

### SPEC-0028-REQ-14: Notification

The agent MUST send Apprise notifications (per SPEC-0004) at the following lifecycle points:

1. **Plan generated**: After the Plan phase completes, containing the service name, target, assigned ports, volumes, identified secrets (distinguishing auto-provisioned from operator-required), and any flagged dependencies.
2. **Provisioning succeeded**: After the Codify phase completes, containing the service name, target, health check results, and PR URL.
3. **Provisioning failed**: After the Rollback phase completes, containing the service name, target, failure reason, and confirmation that rollback completed.

If `CLAUDEOPS_APPRISE_URLS` is not configured, notifications MUST be skipped silently. The agent MUST NOT log an error about missing Apprise configuration.

All Apprise invocations MUST include the `-i markdown` flag for markdown formatting.

#### Scenario: Plan notification

- **WHEN** the agent completes the Plan phase
- **THEN** the agent MUST send an Apprise notification with the deployment plan details

#### Scenario: Success notification

- **WHEN** provisioning succeeds and a PR is created
- **THEN** the agent MUST send an Apprise notification with the service name and PR URL

#### Scenario: Failure notification

- **WHEN** provisioning fails and rollback completes
- **THEN** the agent MUST send an Apprise notification with the failure reason and rollback confirmation

#### Scenario: Apprise not configured

- **WHEN** `CLAUDEOPS_APPRISE_URLS` is empty or unset
- **THEN** the agent MUST skip notifications silently without logging an error

### SPEC-0028-REQ-15: Auditability

Every phase of the provisioning lifecycle MUST be logged to the session results directory. The logs MUST include:

1. **Fetch phase**: The documentation URL, HTTP status, and extracted requirements.
2. **Discover phase**: The repo's detected deployment framework, discovered conventions, and whether provisioning-specific extensions were found.
3. **Plan phase**: The full deployment plan including port assignments, volume paths, environment variables (with secrets redacted), dependencies, and validation results.
4. **Execute phase**: The commands executed, their output, and any errors.
5. **Verify phase**: Each health check performed, its result, and the overall pass/fail determination.
6. **Codify/Rollback phase**: The PR URL and branch name (success), or the rollback steps executed and their results (failure).

Provisioning sessions MUST be queryable in the dashboard by `trigger = "provision"`.

#### Scenario: Fetch phase logged

- **WHEN** the agent completes the Fetch phase
- **THEN** the session log MUST contain the documentation URL, HTTP response status, and the list of extracted requirements

#### Scenario: Secrets redacted in logs

- **WHEN** the agent logs the deployment plan
- **THEN** secret values MUST be redacted (e.g., `APP_SECRET=<redacted>`)
- **AND** the presence and names of secrets MUST be logged

#### Scenario: Provisioning sessions queryable

- **WHEN** an operator views the dashboard session list
- **THEN** provisioning sessions MUST be filterable by `trigger = "provision"`

## References

- [ADR-0027: URL-Driven Service Provisioning via Provisioning Mode](/docs/adrs/ADR-0027-url-driven-service-provisioning.md)
- [ADR-0003: Enforce Permission Tiers via Prompt Instructions](/docs/adrs/ADR-0003-prompt-based-permission-enforcement.md)
- [ADR-0018: PR-Based Workflow for Runbook, Playbook, and Manifest Changes](/docs/adrs/ADR-0018-pr-based-config-changes.md)
- [SPEC-0004: Apprise-Based Notification Delivery](/docs/openspec/specs/apprise-notifications/spec.md)
- [SPEC-0005: Mounted Repo Extension Model](/docs/openspec/specs/mounted-repo-extensions/spec.md)
- [SPEC-0012: Manual Ad-Hoc Session Runs](/docs/openspec/specs/manual-ad-hoc-sessions/spec.md)
- [SPEC-0018: PR-Based Configuration Changes](/docs/openspec/specs/pr-based-config-changes/spec.md)
- [SPEC-0023: Skills-Based Tool Orchestration](/docs/openspec/specs/skills-based-tool-orchestration/spec.md)
