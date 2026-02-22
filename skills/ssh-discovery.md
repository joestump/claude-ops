# Skill: SSH Access Discovery

Discover the SSH access method for each managed host at the start of every monitoring cycle, before any health checks run. The result is a host access map that all tiers use to construct SSH commands for the rest of the run.

## When to Run

Run once per cycle, after repo and service discovery (Step 2) and before health checks (Step 3). Extract the host list from the CLAUDE-OPS.md manifest's Hosts table.

<!-- Governing: SPEC-0020 "Manifest as Advisory" — CLAUDE-OPS.md SSH config is advisory, verified at runtime -->

## Probing Order

For each host, attempt SSH connections in this order. Stop on the first success.

1. `root@<host>`
2. The user declared in the CLAUDE-OPS.md manifest's Hosts table for this host (if any)
3. Common defaults: `ubuntu`, `debian`, `pi`, `admin`

Each probe command:

```bash
ssh -o BatchMode=yes -o ConnectTimeout=5 <user>@<host> whoami
```

- `BatchMode=yes` prevents interactive prompts and password auth
- `ConnectTimeout=5` enforces a 5-second timeout
- If a probe succeeds, do NOT continue to the next user

If all probes fail for a host, record it as `unreachable` and move on. Do not block the rest of the cycle.

## Sudo Detection

For any non-root user that successfully connects, test for passwordless sudo:

```bash
ssh <user>@<host> 'sudo -n whoami 2>/dev/null'
```

- If the output is `root`, record `method: "sudo"`
- Otherwise, record `method: "limited"`

## Docker Access Detection

For every successfully connected host, test Docker access:

```bash
# For method: root
ssh root@<host> docker info --format '{{.ServerVersion}}'

# For method: sudo
ssh <user>@<host> sudo docker info --format '{{.ServerVersion}}'

# For method: limited
ssh <user>@<host> docker info --format '{{.ServerVersion}}'
```

- If the command succeeds, record `can_docker: true`
- If it fails (permission error), record `can_docker: false`

<!-- Governing: SPEC-0020 "Host Access Map Structure" — per-host JSON with user, method, can_docker -->

## Host Access Map Schema

Build a JSON map with one entry per host:

```json
{
  "ssh_access_map": {
    "ie01.stump.rocks": {
      "user": "root",
      "method": "root",
      "can_docker": true
    },
    "pie01.stump.rocks": {
      "user": "pi",
      "method": "sudo",
      "can_docker": true
    },
    "pie03.stump.rocks": {
      "user": "pi",
      "method": "limited",
      "can_docker": false
    },
    "host.example.com": {
      "user": "",
      "method": "unreachable",
      "can_docker": false
    }
  }
}
```

<!-- Governing: SPEC-0020 "Per-Run Caching" — computed once per cycle, passed via handoff to higher tiers -->

## Caching

The map is computed once per cycle and cached for the duration of the run. Do NOT re-probe SSH access on every command. When escalating to Tier 2 or Tier 3, include the map in the handoff file so the receiving tier can reuse it without re-probing.

## Logging

Log the discovery result for each host:

- **Root access**: `SSH discovery: <host> -> root@<host> (root access)`
- **Fallback to non-root**: `SSH discovery: <host> -> <user>@<host> (sudo|limited) [root failed]`
- **Unreachable**: `SSH discovery: <host> -> unreachable (tried: root, <manifest_user>, ubuntu, debian, pi, admin)`

## Command Execution Rules

When running commands on remote hosts, consult the host access map to construct the correct SSH command:

### method: root
```bash
ssh root@<host> <command>
```
No prefix needed. Full access to all commands.

### method: sudo (write command)
```bash
ssh <user>@<host> sudo <command>
```
Write commands (docker restart, systemctl, chown, file edits) require the `sudo` prefix.

### method: sudo (read command)
```bash
# If can_docker is true, Docker read commands work without sudo:
ssh <user>@<host> docker ps

# If can_docker is false, Docker commands need sudo:
ssh <user>@<host> sudo docker ps

# Non-Docker read commands (df, cat, uptime) never need sudo:
ssh <user>@<host> <command>
```

### method: limited
```bash
ssh <user>@<host> <command>
```
Read commands only. Write commands MUST be refused — follow the limited access fallback below.

### method: unreachable
Skip all SSH-based checks for this host. Rely on HTTP/DNS checks only.

## Limited Access Fallback

When remediation requires elevated privileges on a host with `method: "limited"`:

1. **PR workflow (preferred)**: If a mounted repo manages the host and the PR workflow (SPEC-0018) is available, generate a fix and create a pull request proposing the remediation.
2. **Report for human action**: If PR creation is not possible, report: "Remediation requires root access on `<host>` which is not available. Manual intervention needed." Include the exact command(s) that would fix the issue.

Do NOT escalate to a higher tier solely because of limited access — a higher tier does not grant more SSH access.
