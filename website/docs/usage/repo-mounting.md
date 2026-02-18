---
sidebar_position: 4
---

# Connecting Repos

Claude Ops discovers infrastructure repos at runtime by scanning a mount directory. This guide explains how to onboard your repos.

## The `/repos` convention

All infrastructure repos are mounted as subdirectories under a single parent path (default: `/repos`). Each subdirectory is treated as a separate repository.

```
/repos/
├── infra-ansible/          # Your Ansible inventory and playbooks
├── docker-images/          # Dockerfiles and build configs
├── helm-charts/            # Kubernetes Helm charts
└── ...
```

## How to mount

Add volume mounts in `docker-compose.override.yaml`:

```yaml
services:
  watchdog:
    volumes:
      - /path/to/your/ansible-repo:/repos/infra-ansible:ro
      - /path/to/your/docker-images:/repos/docker-images:ro
```

**Use `:ro` (read-only) by default.** Ansible runs against remote hosts via SSH, not local files. Only mount read-write if Claude genuinely needs write access (rare).

## The `CLAUDE-OPS.md` manifest

Each mounted repo can include a `CLAUDE-OPS.md` file at its root. This tells Claude Ops what the repo is, what it can do, and any rules to follow.

### Example: Ansible repo

```markdown
# Claude Ops Manifest

This repo manages home lab infrastructure via Ansible.

## Kind

Ansible infrastructure

## Capabilities

- **service-discovery**: The inventory at `inventory/ie.yaml` lists all
  deployed services with their hostnames, ports, and configuration.
- **redeployment**: Playbooks in `playbooks/` can redeploy any service.
  Only Opus (Tier 3) should run playbooks.

## Rules

- Never modify any files in this repo
- Always use `--limit` when running playbooks
```

### Example: Docker images repo

```markdown
# Claude Ops Manifest

Custom Docker images built and mirrored to the Gitea registry.

## Kind

Docker images

## Capabilities

- **image-inspection**: Claude can check Dockerfiles for issues,
  verify base image freshness, and understand image structure.

## Rules

- Read-only. Never modify Dockerfiles or build configs.
- Never build or push images.
```

### Example: Helm charts repo

```markdown
# Claude Ops Manifest

Helm charts for the production Kubernetes cluster.

## Kind

Helm charts

## Capabilities

- **service-discovery**: Values files list all deployed services.
- **redeployment**: `helm upgrade` can redeploy services.
  Only Opus (Tier 3) should run Helm commands.

## Rules

- Never modify charts or values files
- Always use `--wait` and `--timeout` with Helm commands
```

## The `.claude-ops/` extension directory

Beyond the manifest, repos can include a `.claude-ops/` directory with custom extensions:

```
your-repo/
├── CLAUDE-OPS.md               # What this repo is (manifest)
├── .claude-ops/                # Extensions for Claude Ops
│   ├── checks/                 # Additional health checks
│   │   └── verify-backups.md
│   ├── playbooks/              # Repo-specific remediation
│   │   └── fix-media-perms.md
│   ├── skills/                 # Custom capabilities
│   │   └── prune-old-logs.md
│   └── mcp.json                # Additional MCP server configs
```

### Custom checks

Markdown files in `.claude-ops/checks/` run alongside the built-in health check suite. Each file describes what to check and how — Claude reads them and executes the appropriate commands.

```markdown
# Check: Backup Freshness

Verify that the nightly backup completed within the last 24 hours.

## How to Check

Look for the most recent backup file in `/data/backups/`:
- File should exist and be less than 24 hours old
- File size should be > 1MB (not an empty/failed backup)

## Healthy

- Backup file exists, is recent, and has reasonable size

## Unhealthy

- No backup file in the last 24 hours
- Backup file is suspiciously small (< 1MB)
```

### Custom playbooks

Remediation procedures specific to your services. Available to Tier 2 and Tier 3 agents.

### Custom skills

Freeform capabilities — maintenance tasks, reporting, cleanup operations. Skills follow the same tier permissions as everything else.

### Custom MCP servers

Additional MCP server definitions in `.claude-ops/mcp.json` are merged with the baseline config before each run:

```json
{
  "mcpServers": {
    "ansible-inventory": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "some-ansible-mcp-package"],
      "env": {
        "INVENTORY_PATH": "/repos/infra-ansible/inventory/ie.yaml"
      }
    }
  }
}
```

**Merge rules:**
- Repo MCP servers are added to the baseline set
- Same-name servers: repo version wins (allows overriding defaults)
- Configs merge in alphabetical order by repo name
- Merging happens before each Claude run — new repos are picked up without restart

## Discovery process

At the start of each run, Claude:

1. Lists all subdirectories under `$CLAUDEOPS_REPOS_DIR`
2. For each, looks for `CLAUDE-OPS.md`
3. If found, reads it for capabilities and rules
4. If not found, infers the repo's purpose from top-level files
5. Checks for `.claude-ops/` extensions
6. Builds a map of repos, capabilities, and all extensions
7. Uses this map throughout the run

## Adding a new repo

1. Add a volume mount to `docker-compose.override.yaml`
2. (Optional) Add a `CLAUDE-OPS.md` to the repo root
3. (Optional) Add `.claude-ops/` extensions
4. Restart the watchdog container
5. Claude discovers it on the next run
