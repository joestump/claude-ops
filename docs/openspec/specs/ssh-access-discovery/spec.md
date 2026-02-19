# SPEC-0020: SSH Access Discovery and Fallback

## Overview

Claude Ops monitors infrastructure health by SSHing into managed hosts to inspect containers, read logs, and apply remediations. Currently, the SSH user is hardcoded to `root@<host>` in all tier prompts and the CLAUDE-OPS.md manifest. This breaks when the manifest is wrong, when root SSH access has been revoked, or when a host's SSH setup changes.

This specification defines an SSH access discovery routine that runs at the beginning of each monitoring cycle, before any host interaction. The routine probes each managed host to determine the best available access method (root, sudo, or limited), caches the results in a host access map, and makes that map available to all tiers for the duration of the run. Hosts that lack root or sudo access are treated as read-only, and remediations that require elevated privileges are either delegated to the PR workflow (SPEC-0018) or reported as needing human intervention.

## Definitions

- **Host access map**: A JSON structure built at the start of each monitoring cycle that records, for each managed host, the SSH user, access method, and capability flags. Cached for the duration of the run.
- **Access method**: One of `root` (direct root login), `sudo` (non-root user with passwordless sudo), `limited` (non-root user without sudo), or `unreachable` (SSH connection failed entirely).
- **Probing**: The process of attempting SSH connections with different users to determine which credentials work on a given host.
- **Manifest user**: A username declared in the CLAUDE-OPS.md manifest's Hosts table for a specific host. Advisory, not authoritative.
- **Common default users**: A fixed list of usernames commonly found on Linux hosts: `root`, `ubuntu`, `debian`, `pi`, `admin`.
- **Read command**: Any command that only inspects state without modifying it (e.g., `docker ps`, `docker logs`, `df`, `cat`).
- **Write command**: Any command that mutates state (e.g., `docker restart`, `systemctl`, `chown`, file edits).

## Requirements

### Requirement: SSH Probing Order

The system MUST attempt SSH connections in a defined order for each managed host. The probing sequence SHALL be:

1. `root@<host>` (preferred, happy path)
2. Any user explicitly declared in the CLAUDE-OPS.md manifest's Hosts table for this host
3. Common default users: `ubuntu`, `debian`, `pi`, `admin`

The system MUST use the first user that successfully connects. The system MUST NOT continue probing after a successful connection.

#### Scenario: Root access available

- **WHEN** `ssh -o BatchMode=yes -o ConnectTimeout=5 root@<host> whoami` succeeds and returns `root`
- **THEN** the system SHALL record `method: "root"` and `user: "root"` for this host
- **AND** the system SHALL NOT attempt any further user probes for this host

#### Scenario: Root fails, manifest user succeeds

- **WHEN** root SSH fails for a host
- **AND** the CLAUDE-OPS.md manifest declares `user: pi` for this host
- **AND** `ssh -o BatchMode=yes -o ConnectTimeout=5 pi@<host> whoami` succeeds
- **THEN** the system SHALL use `pi` as the SSH user for this host
- **AND** the system SHALL proceed to sudo detection (next requirement)

#### Scenario: Root and manifest user fail, common user succeeds

- **WHEN** both root and the manifest-declared user fail for a host
- **AND** `ssh -o BatchMode=yes -o ConnectTimeout=5 ubuntu@<host> whoami` succeeds
- **THEN** the system SHALL use `ubuntu` as the SSH user for this host
- **AND** the system SHALL proceed to sudo detection

#### Scenario: All users fail

- **WHEN** SSH connections fail for root, the manifest user, and all common default users
- **THEN** the system SHALL record `method: "unreachable"` for this host
- **AND** the system SHALL skip all SSH-based checks for this host
- **AND** the system SHALL NOT block the rest of the monitoring cycle

### Requirement: SSH Connection Parameters

All SSH probe attempts MUST use `BatchMode=yes` to prevent interactive prompts and `ConnectTimeout=5` to enforce a 5-second connection timeout. The system MUST NOT use password-based authentication. The system MUST NOT attempt interactive SSH sessions.

#### Scenario: SSH probe uses correct flags

- **WHEN** the system probes a host with any user
- **THEN** the SSH command MUST include `-o BatchMode=yes -o ConnectTimeout=5`
- **AND** the SSH command MUST use `whoami` as the remote command

#### Scenario: Host is slow to respond

- **WHEN** a host does not respond within 5 seconds
- **THEN** the SSH probe MUST time out
- **AND** the system SHALL try the next user in the probe sequence

#### Scenario: Host prompts for password

- **WHEN** a host requires password authentication
- **THEN** `BatchMode=yes` SHALL cause the connection to fail immediately
- **AND** the system SHALL try the next user in the probe sequence

### Requirement: Sudo Access Detection

For any non-root user that successfully connects, the system MUST test for passwordless sudo access by running `sudo -n whoami` on the remote host. If this command returns `root`, the system SHALL record `method: "sudo"`. If sudo is not available or requires a password, the system SHALL record `method: "limited"`.

#### Scenario: Non-root user has passwordless sudo

- **WHEN** user `pi` connects successfully to a host
- **AND** `ssh pi@<host> 'sudo -n whoami 2>/dev/null'` returns `root`
- **THEN** the system SHALL record `method: "sudo"` and `user: "pi"` for this host

#### Scenario: Non-root user lacks sudo

- **WHEN** user `ubuntu` connects successfully to a host
- **AND** `ssh ubuntu@<host> 'sudo -n whoami 2>/dev/null'` does not return `root`
- **THEN** the system SHALL record `method: "limited"` and `user: "ubuntu"` for this host

### Requirement: Docker Access Detection

For every successfully connected host, the system MUST test whether the connected user can run Docker commands by executing `docker info --format '{{.ServerVersion}}'` (with `sudo` prefix if `method` is `sudo`). The result SHALL be recorded as `can_docker: true` or `can_docker: false` in the host access map.

#### Scenario: Root user with Docker

- **WHEN** a host is accessed as root
- **AND** `ssh root@<host> docker info --format '{{.ServerVersion}}'` succeeds
- **THEN** the system SHALL record `can_docker: true`

#### Scenario: Limited user in docker group

- **WHEN** a host is accessed with `method: "limited"` and user `pi`
- **AND** `ssh pi@<host> docker info --format '{{.ServerVersion}}'` succeeds (user is in the docker group)
- **THEN** the system SHALL record `can_docker: true`

#### Scenario: Limited user without Docker access

- **WHEN** a host is accessed with `method: "limited"` and user `ubuntu`
- **AND** `ssh ubuntu@<host> docker info --format '{{.ServerVersion}}'` fails with a permission error
- **THEN** the system SHALL record `can_docker: false`

### Requirement: Host Access Map Structure

The system MUST build a host access map as a JSON structure at the start of each monitoring cycle. The map MUST contain an entry for every managed host discovered from the CLAUDE-OPS.md manifest. Each entry SHALL include: `user` (string), `method` (one of `root`, `sudo`, `limited`, `unreachable`), and `can_docker` (boolean).

#### Scenario: Complete access map

- **WHEN** the system finishes probing all managed hosts
- **THEN** the host access map SHALL contain one entry per host with the structure:
  ```json
  {
    "ie01.stump.rocks": {
      "user": "root",
      "method": "root",
      "can_docker": true
    },
    "pie01.stump.rocks": {
      "user": "pi",
      "method": "sudo",
      "can_docker": true
    }
  }
  ```

#### Scenario: Unreachable host in access map

- **WHEN** a host is unreachable during probing
- **THEN** the host access map entry SHALL be:
  ```json
  {
    "host.example.com": {
      "user": "",
      "method": "unreachable",
      "can_docker": false
    }
  }
  ```

### Requirement: Per-Run Caching

The host access map MUST be computed once at the start of the monitoring cycle and cached for the entire run. The system MUST NOT re-probe SSH access on every command. If the host access map is passed between tiers via the handoff file (SPEC-0016), the receiving tier SHOULD reuse it without re-probing.

#### Scenario: Map reused across commands within a tier

- **WHEN** a Tier 1 agent needs to run multiple SSH commands on the same host during one cycle
- **THEN** the agent SHALL use the cached access map entry for every command
- **AND** the agent SHALL NOT re-probe SSH access

#### Scenario: Map passed in handoff to Tier 2

- **WHEN** Tier 1 writes a handoff file for Tier 2
- **THEN** the handoff file SHOULD include the host access map
- **AND** Tier 2 SHOULD use the map from the handoff without re-probing

#### Scenario: Map passed in handoff to Tier 3

- **WHEN** Tier 2 writes a handoff file for Tier 3
- **THEN** the handoff file SHOULD include the host access map
- **AND** Tier 3 SHOULD use the map from the handoff without re-probing

### Requirement: Command Prefix Based on Access Method

When executing commands on a remote host, the system MUST select the correct SSH command prefix based on the host's access map entry:

- `method: "root"` → `ssh root@<host> <command>`
- `method: "sudo"` → `ssh <user>@<host> sudo <command>` for write commands; `ssh <user>@<host> <command>` for read commands
- `method: "limited"` → `ssh <user>@<host> <command>` (read commands only)

#### Scenario: Root access command execution

- **WHEN** a host has `method: "root"`
- **AND** the agent needs to run `docker restart jellyfin`
- **THEN** the command SHALL be `ssh root@<host> docker restart jellyfin`

#### Scenario: Sudo access command execution

- **WHEN** a host has `method: "sudo"` and `user: "pi"`
- **AND** the agent needs to run `docker restart jellyfin`
- **THEN** the command SHALL be `ssh pi@<host> sudo docker restart jellyfin`

#### Scenario: Sudo access read command

- **WHEN** a host has `method: "sudo"` and `user: "pi"`
- **AND** the agent needs to run `docker ps`
- **THEN** the command MAY be `ssh pi@<host> docker ps` if the user has Docker access
- **OR** the command SHALL be `ssh pi@<host> sudo docker ps` if the user lacks direct Docker access

#### Scenario: Limited access read command

- **WHEN** a host has `method: "limited"` and `user: "ubuntu"` and `can_docker: true`
- **AND** the agent needs to run `docker ps`
- **THEN** the command SHALL be `ssh ubuntu@<host> docker ps`

### Requirement: Write Command Gating

The system MUST NOT execute write commands on hosts where the access method is `limited`. Write commands (e.g., `docker restart`, `systemctl start`, `chown`, file modifications) REQUIRE `method: "root"` or `method: "sudo"`. If a write command is needed on a limited-access host, the system MUST follow the limited-access fallback procedure.

#### Scenario: Write command blocked on limited host

- **WHEN** a Tier 2 agent determines that `docker restart jellyfin` is needed on a host
- **AND** the host has `method: "limited"`
- **THEN** the agent MUST NOT execute the restart command
- **AND** the agent MUST follow the limited-access fallback procedure

#### Scenario: Write command allowed on sudo host

- **WHEN** a Tier 2 agent determines that `docker restart jellyfin` is needed on a host
- **AND** the host has `method: "sudo"`
- **THEN** the agent MAY execute `ssh <user>@<host> sudo docker restart jellyfin`

### Requirement: Limited Access Fallback

When remediation requires elevated privileges on a host with `method: "limited"`, the system SHALL attempt the following fallback sequence:

1. If a mounted repo exists under `/repos/` that manages the affected host's infrastructure, and the PR workflow is available (SPEC-0018), the system SHOULD generate a fix and create a pull request proposing the remediation.
2. If PR creation is not possible (no matching repo, no git provider configured, or the change is outside allowed PR scope), the system MUST report the finding with the message: "Remediation requires root access on `<host>` which is not available. Manual intervention needed." and include the specific command(s) that would fix the issue.

#### Scenario: Limited host with PR fallback available

- **WHEN** remediation requires root on a limited-access host
- **AND** a mounted repo manages the host's infrastructure
- **AND** the PR workflow (SPEC-0018) is configured
- **THEN** the system SHOULD create a PR proposing the fix
- **AND** the system SHALL send a notification (if Apprise is configured) describing the proposed change

#### Scenario: Limited host without PR fallback

- **WHEN** remediation requires root on a limited-access host
- **AND** no mounted repo manages the host or the PR workflow is not available
- **THEN** the system MUST report: "Remediation requires root access on `<host>` which is not available. Manual intervention needed."
- **AND** the report MUST include the exact command(s) that would resolve the issue

#### Scenario: Limited host with read-only inspection

- **WHEN** a host has `method: "limited"` and `can_docker: true`
- **AND** the current tier only needs to inspect container state (Tier 1 observation)
- **THEN** the system SHALL run read commands normally via `ssh <user>@<host> docker ps` and similar
- **AND** the system SHALL NOT escalate solely because of limited access

### Requirement: Discovery Logging

The system MUST log the SSH discovery results for each host. The log MUST include which users were attempted, which succeeded or failed, the final access method, and any fallback that occurred. When a host falls back from root to a non-root user, or lands in limited mode, the log MUST clearly indicate this so the operator knows.

#### Scenario: Root access logged

- **WHEN** root SSH succeeds on the first attempt
- **THEN** the system SHALL log: "SSH discovery: <host> -> root@<host> (root access)"

#### Scenario: Fallback to non-root logged

- **WHEN** root SSH fails and a non-root user succeeds
- **THEN** the system SHALL log: "SSH discovery: <host> -> <user>@<host> (sudo|limited) [root failed]"

#### Scenario: Unreachable host logged

- **WHEN** all SSH probe attempts fail for a host
- **THEN** the system SHALL log: "SSH discovery: <host> -> unreachable (tried: root, <manifest_user>, ubuntu, debian, pi, admin)"

### Requirement: Manifest as Advisory

The CLAUDE-OPS.md manifest's Hosts table MAY declare an SSH user for each host. This user SHALL be treated as advisory: the system MUST include it in the probing sequence (after root, before common defaults) but MUST NOT trust it without verification. The probing result SHALL always take precedence over the manifest declaration.

#### Scenario: Manifest user works

- **WHEN** the manifest declares `user: pi` for a host
- **AND** root SSH fails
- **AND** `ssh pi@<host> whoami` succeeds
- **THEN** the system SHALL use `pi` as the SSH user (matching the manifest)

#### Scenario: Manifest user does not work

- **WHEN** the manifest declares `user: admin` for a host
- **AND** root SSH fails
- **AND** `ssh admin@<host> whoami` fails
- **AND** `ssh ubuntu@<host> whoami` succeeds
- **THEN** the system SHALL use `ubuntu`, overriding the manifest declaration

#### Scenario: Manifest declares no user

- **WHEN** the manifest's Hosts table has no user column or the user field is empty for a host
- **THEN** the system SHALL probe root first, then skip directly to common default users

### Requirement: Tier Integration

SSH access discovery MUST run at Tier 1 (observation phase) before any host interaction. All tiers MUST use the host access map to determine how to execute commands. Tier 1 agents MUST use the map for read-only inspection. Tier 2 and Tier 3 agents MUST consult the map before executing any remediation command and MUST NOT attempt write commands on limited-access hosts.

#### Scenario: Tier 1 uses access map for inspection

- **WHEN** Tier 1 needs to check container state on a host with `method: "sudo"` and `user: "pi"`
- **THEN** the agent SHALL run `ssh pi@<host> docker ps` (or `ssh pi@<host> sudo docker ps` if Docker requires elevated privileges)

#### Scenario: Tier 2 respects limited access

- **WHEN** Tier 2 receives a handoff indicating a service needs restart on a host
- **AND** the host access map shows `method: "limited"`
- **THEN** Tier 2 MUST NOT attempt the restart
- **AND** Tier 2 MUST follow the limited-access fallback procedure

#### Scenario: Tier 3 respects limited access

- **WHEN** Tier 3 determines an Ansible playbook should run against a limited-access host
- **AND** the host access map shows `method: "limited"`
- **THEN** Tier 3 MUST NOT run the playbook via the limited user
- **AND** Tier 3 MUST follow the limited-access fallback procedure

### Requirement: Dry Run Mode

When `CLAUDEOPS_DRY_RUN` is `true`, the SSH discovery routine MUST still run and build the host access map. The map is informational and used for logging purposes. No remediation commands SHALL be executed regardless of access method.

#### Scenario: Dry run still probes SSH

- **WHEN** `CLAUDEOPS_DRY_RUN` is `true`
- **THEN** the system SHALL probe SSH access on all managed hosts
- **AND** the system SHALL build and log the host access map
- **AND** the system SHALL NOT execute any remediation commands

## References

- [SPEC-0001: Tiered Model Escalation](/docs/openspec/specs/tiered-model-escalation/spec.md)
- [SPEC-0005: Mounted Repo Extension Model](/docs/openspec/specs/mounted-repo-extensions/spec.md)
- [SPEC-0016: Session-Based Escalation with Structured Handoff](/docs/openspec/specs/session-based-escalation/spec.md)
- [SPEC-0018: PR-Based Configuration Changes](/docs/openspec/specs/pr-based-config-changes/spec.md)
