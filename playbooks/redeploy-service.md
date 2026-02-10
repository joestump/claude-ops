# Playbook: Redeploy Service

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

## Ansible Redeployment

1. **Identify the playbook and inventory**
   - Find the repo with `service-discovery` or `redeployment` capability
   - Locate the correct playbook for this service
   - Identify the inventory file and host/group

2. **Run the playbook**
   ```bash
   ansible-playbook -i <inventory> <playbook> --limit <host> --tags <service> -v
   ```

3. **Monitor output**
   - Watch for task failures
   - Note any changed/failed tasks

4. **Verify**
   - Wait for containers to start
   - Run health checks
   - Check dependent services

## Docker Compose Redeployment

1. **Locate the compose file**
   ```bash
   docker compose -f <compose-file> down <service>
   docker compose -f <compose-file> pull <service>
   docker compose -f <compose-file> up -d <service>
   ```

2. **Verify**
   - Wait for container healthy status
   - Run health checks

## Helm Redeployment

1. **Locate the chart and values**
   ```bash
   helm upgrade <release> <chart> -f <values> -n <namespace> --wait --timeout 5m
   ```

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
