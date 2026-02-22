<!-- Governing: SPEC-0002 REQ-1 (markdown as sole instruction format), REQ-8 (no build step), REQ-9 (self-documenting) -->

# Playbook: Redeploy Service

<!-- Governing: SPEC-0002 REQ-11 — Playbook Tier Gating, REQ-3 — Playbook Document Structure -->

**Tier**: 3 (Opus) only

## When to Use

- Container restart didn't fix the issue
- Service configuration appears corrupted
- Container image needs to be re-pulled
- Multi-container service needs full recreation

## Prerequisites

- Check cooldown state: max 1 redeployment per service per 24-hour window
- Confirm restart was already attempted and failed
- Identify the deployment method from the mounted repo (Ansible, Docker Compose, Helm)
- Consult the host access map (from the handoff file) and `/app/skills/ssh-discovery.md` to determine the correct SSH user and command prefix for the target host

**If the host has `method: limited`, this playbook CANNOT be executed.** Follow the limited-access fallback procedure in the tier prompt instead.

## Ansible Redeployment

<!-- Governing: SPEC-0002 REQ-5 — Embedded Command Examples -->

1. **Identify the playbook and inventory**
   - Find the repo with `service-discovery` or `redeployment` capability
   - Locate the correct playbook for this service
   - Identify the inventory file and host/group

2. **Run the playbook**
   ```bash
   ansible-playbook -i <inventory> <playbook> --limit <host> --tags <service> -v
   ```

   Replace `<inventory>` with the inventory file path from the mounted repo (e.g., `ie.yaml`). Replace `<playbook>` with the service's playbook. Replace `<host>` with the target host or group from the inventory. Replace `<service>` with the Ansible tag for the service being redeployed.

3. **Monitor output**
   <!-- Governing: SPEC-0002 REQ-6 — Contextual Adaptation -->
   - Watch for task failures
   - Note any changed/failed tasks
   - If a task fails due to a transient issue (e.g., network timeout pulling an image), it may be worth retrying that specific step rather than the full playbook

4. **Verify**
   - Wait for containers to start (allow more time for services that perform database migrations on startup)
   - Run health checks
   - Check dependent services

## Docker Compose Redeployment

1. **Locate the compose file** and use the SSH command from the host access map (e.g., `ssh <user>@<host> [sudo] docker compose ...`):
   ```bash
   ssh <user>@<host> "cd <compose-dir> && docker compose -f <compose-file> down <service>"
   ssh <user>@<host> "cd <compose-dir> && docker compose -f <compose-file> pull <service>"
   ssh <user>@<host> "cd <compose-dir> && docker compose -f <compose-file> up -d <service>"
   ```

   Replace `<user>` and `<host>` with SSH credentials from the host access map. Replace `<compose-dir>` with the directory containing the compose file. Replace `<compose-file>` with the compose filename. Replace `<service>` with the service name within the compose file.

2. **Verify**
   - Wait for container healthy status
   - Run health checks

## Helm Redeployment

1. **Locate the chart and values**
   ```bash
   helm upgrade <release> <chart> -f <values> -n <namespace> --wait --timeout 5m
   ```

   Replace `<release>` with the Helm release name. Replace `<chart>` with the chart path or reference. Replace `<values>` with the values file path. Replace `<namespace>` with the Kubernetes namespace.

2. **Verify**
   - Check rollout status: `kubectl rollout status deployment/<name> -n <namespace>`
   - Run health checks

## After Redeployment

- Update cooldown state: set `last_redeployment` timestamp
- Send detailed email report of what was done
- If redeployment failed, send notification via Apprise with full details

## If It Doesn't Work

- Do NOT attempt a second redeployment
- Send "needs human attention" notification with:
  - Full ansible/compose/helm output
  - Container logs
  - Your analysis of what went wrong
  - Suggested manual steps
