# Repo Mounting Guide

Claude Ops discovers infrastructure repos at runtime by scanning a mount directory. This guide explains the convention and how to onboard your repos.

## The `/repos` Convention

All infrastructure repos are mounted as subdirectories under a single parent path (default: `/repos`). Each subdirectory is treated as a separate repository.

```
/repos/
├── infra-ansible/          # Your Ansible inventory and playbooks
├── docker-images/          # Dockerfiles and build configs
├── helm-charts/            # Kubernetes Helm charts
└── ...                     # Any other repos
```

## How to Mount

In `docker-compose.yaml`, add volume mounts:

```yaml
services:
  watchdog:
    volumes:
      - /path/to/your/ansible-repo:/repos/infra-ansible:ro
      - /path/to/your/docker-images:/repos/docker-images:ro
```

**Use `:ro` (read-only) by default.** Only mount read-write if Claude needs write access (rare — Ansible runs against remote hosts via SSH, not local files).

## The `CLAUDE-OPS.md` Manifest

Each mounted repo can include a `CLAUDE-OPS.md` file at its root. This tells Claude Ops what the repo is, what it's useful for, and any rules to follow.

### Example: Ansible Infrastructure Repo

```markdown
# Claude Ops Manifest

This repo manages home lab infrastructure via Ansible. It contains the
inventory, playbooks, and roles for all deployed services.

## Kind

Ansible infrastructure

## Capabilities

- **service-discovery**: The inventory at `inventory/ie.yaml` lists all
  deployed services with their hostnames, ports, and configuration.
- **redeployment**: Playbooks in `playbooks/` can redeploy any service.
  Only Opus (Tier 3) should run playbooks.

## Inventory

The main inventory file is `inventory/ie.yaml`. Services are organized
under host groups. Each service has:
- `enabled`: whether it should be running
- `dns`: subdomain for the service
- `ports`: exposed ports
- `healthcheck`: optional health check endpoint

## Rules

- Never modify any files in this repo
- Playbook runs require Opus tier (Tier 3)
- Always use `--limit` when running playbooks to target specific hosts
- Never run playbooks with `--tags all` or without `--tags`
```

### Example: Docker Images Repo

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

### Example: Kubernetes / Helm Repo

```markdown
# Claude Ops Manifest

Helm charts for the production Kubernetes cluster.

## Kind

Helm charts

## Capabilities

- **service-discovery**: Values files list all deployed services
  and their configuration.
- **redeployment**: `helm upgrade` can redeploy services.
  Only Opus (Tier 3) should run Helm commands.

## Rules

- Never modify charts or values files
- Always use `--wait` and `--timeout` with Helm commands
- Never delete Helm releases
```

## The `.claude-ops/` Extension Directory

Beyond the manifest, repos can include a `.claude-ops/` directory with custom extensions that Claude discovers and uses alongside its built-in checks and playbooks.

```
your-repo/
├── CLAUDE-OPS.md               # What this repo is (manifest)
├── .claude-ops/                # Extensions for Claude Ops
│   ├── checks/                 # Additional health checks
│   │   └── verify-backups.md   # "Verify nightly backup completed"
│   ├── playbooks/              # Repo-specific remediation
│   │   └── fix-media-perms.md  # "Fix media directory ownership"
│   ├── skills/                 # Custom capabilities
│   │   └── prune-old-logs.md   # "Clean up logs older than 30 days"
│   └── mcp.json                # Additional MCP server configs
├── ...                         # Rest of your repo
```

### `.claude-ops/checks/`

Custom health checks that run alongside the built-in suite. Each file is a markdown document describing what to check and how. Claude reads them and executes the appropriate commands.

Example (`check-backup-freshness.md`):
```markdown
# Check: Backup Freshness

Verify that the nightly backup job completed within the last 24 hours.

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

### `.claude-ops/playbooks/`

Remediation procedures specific to this repo's services. These supplement the built-in playbooks and are available to Tier 2 and Tier 3 agents.

### `.claude-ops/skills/`

Freeform capabilities — maintenance tasks, reporting, cleanup operations, or anything else the repo owner wants Claude to be able to do. Skills follow the same tier permissions as everything else (a skill that requires `docker restart` needs Tier 2 or higher).

### `.claude-ops/mcp.json`

Additional MCP server definitions that get merged with the baseline config before each run. This lets repos bring their own infrastructure integrations.

Example — an Ansible repo that adds a custom inventory MCP:
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

Merge rules:
- Repo MCP servers are **added** to the baseline set
- If a repo defines a server with the same name as a baseline server, the **repo version wins** (allows overriding defaults)
- Configs from all repos are merged in alphabetical order by repo name
- Merging happens in the entrypoint before each Claude run, so new repos are picked up without container restart

### Extension Discovery

During repo scanning (Step 1 of each run), Claude:
1. Reads `CLAUDE-OPS.md` for the manifest
2. Checks for `.claude-ops/` directory
3. If present, reads the contents of `checks/`, `playbooks/`, and `skills/`
4. MCP configs from `.claude-ops/mcp.json` are already merged by the entrypoint
5. Merges custom checks into the health check routine
6. Makes custom playbooks available for remediation
7. Notes available skills for potential use

Extensions from all mounted repos are combined. If two repos define checks for the same service, both run.

## What If I Can't Add Files to My Repo?

Claude Ops discovers everything through `CLAUDE-OPS.md` manifests and `.claude-ops/` extension directories. If you can't add files to a repo, Claude will still scan its contents and infer its purpose from top-level files (README, directory structure, config files).

## Discovery Process

At the start of each run, Claude:

1. Lists all subdirectories under `$CLAUDEOPS_REPOS_DIR` (default `/repos`)
2. For each subdirectory, looks for `CLAUDE-OPS.md`
3. If found, reads it to understand capabilities and rules
4. If not found, reads the top-level files (README, directory listing) to infer the repo's purpose
5. Checks for `.claude-ops/` directory and reads any custom checks, playbooks, skills, and MCP configs
6. Builds a map of available repos, their capabilities, and all extensions
7. Uses this map throughout the run (e.g., service discovery from the Ansible repo, custom checks from the infra repo, redeployment from the Helm repo)

## Read-Only vs Read-Write

| Repo Type | Mount Mode | Rationale |
|-----------|-----------|-----------|
| Ansible inventory/playbooks | `:ro` | Ansible runs remotely via SSH, doesn't need local writes |
| Docker images | `:ro` | Claude should never modify Dockerfiles |
| Helm charts | `:ro` | Claude should never modify charts |
| Compose files | `:ro` | Docker Compose reads from these, Claude shouldn't edit them |

If you need Claude to write to a repo (unusual), explicitly mount it `:rw` and document why in the repo's `CLAUDE-OPS.md`.

## Adding a New Repo

1. Add a volume mount to `docker-compose.yaml`:
   ```yaml
   - /path/to/repo:/repos/<name>:ro
   ```
2. (Optional) Add a `CLAUDE-OPS.md` to the repo root
3. (Optional) Add `.claude-ops/checks/`, `.claude-ops/playbooks/`, or `.claude-ops/skills/` for custom extensions
4. Restart the watchdog container
5. Claude will discover it on the next run

## Custom Repos Directory

To change the parent directory from `/repos`, set:

```yaml
environment:
  CLAUDEOPS_REPOS_DIR: "/my/custom/path"
```
