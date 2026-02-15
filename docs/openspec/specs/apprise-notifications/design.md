# Design: Apprise Notification Abstraction

## Overview

Claude Ops integrates the Apprise CLI as its universal notification layer, enabling operators to receive health check summaries, remediation reports, and human-attention alerts through any combination of 80+ notification services. The design prioritizes zero-code configuration (a single environment variable), graceful degradation (silent no-op when unconfigured), and alignment with Claude Ops' shell-command-in-markdown execution model.

## Architecture

### Component Interaction

```
+-----------------------------------------------------------+
|                    Docker Container                        |
|                                                           |
|   entrypoint.sh                                           |
|     |                                                     |
|     +-- Reads CLAUDEOPS_APPRISE_URLS from environment     |
|     |   (if set, includes in agent system prompt)         |
|     |                                                     |
|     +-- claude --model haiku --prompt-file tier1-observe  |
|           |                                               |
|           +-- Agent reads prompt instructions             |
|           |   (notification steps reference Apprise)      |
|           |                                               |
|           +-- Agent executes: apprise -t "..." -b "..."   |
|               "$CLAUDEOPS_APPRISE_URLS"                   |
|               |                                           |
|               +-- Apprise CLI parses URL schemes          |
|               +-- Dispatches to service-specific plugins  |
|               +-- ntfy, Slack, email, Discord, etc.       |
+-----------------------------------------------------------+
        |                    |                    |
        v                    v                    v
   +---------+         +---------+         +---------+
   | ntfy    |         | Slack   |         | Email   |
   | server  |         | webhook |         | SMTP    |
   +---------+         +---------+         +---------+
```

Apprise runs entirely within the Claude Ops container as a CLI subprocess. There are no sidecars, message queues, or relay services. The agent invokes `apprise` via Bash the same way it invokes `curl` or `docker` -- as a shell command from within its prompt-driven execution context.

### Installation in Docker Image

Apprise is installed in the Docker image as a Python package:

```dockerfile
# System dependencies include python3 and pip3
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 python3-pip python3-venv ...

# Apprise for notifications
RUN pip3 install --break-system-packages apprise
```

The `--break-system-packages` flag is required because the base image (`node:22-slim`) uses a Debian-based distribution with PEP 668 externally-managed environment markers. Apprise is installed system-wide rather than in a virtual environment to ensure it is available on the default `PATH` for the agent's Bash commands.

This introduces a Python runtime dependency into a primarily Node.js-based container. The trade-off is accepted because:
- Python 3 and pip are lightweight additions to the Debian base
- Apprise's dependency tree is self-contained
- No alternative provides comparable breadth of notification service support without additional infrastructure

### Environment Variable Flow

```
.env file or docker-compose.yaml
  |
  +-- CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/ops,slack://...
        |
        +-- docker-compose.yaml passes to container environment
              |
              +-- entrypoint.sh reads from environment
                    |
                    +-- Conditional inclusion in agent system prompt:
                    |   if [ -n "${CLAUDEOPS_APPRISE_URLS:-}" ]; then
                    |       ENV_CONTEXT+=" CLAUDEOPS_APPRISE_URLS=${CLAUDEOPS_APPRISE_URLS}"
                    |   fi
                    |
                    +-- Agent references $CLAUDEOPS_APPRISE_URLS in Bash commands
```

The conditional inclusion in `entrypoint.sh` (lines 79-81) ensures that:
- When set, the variable is available to the agent for constructing Apprise commands
- When unset, the variable is absent from the agent's context, and prompt instructions tell the agent to silently skip notifications

### Notification Event Flow

```
Health Check Cycle
  |
  +-- All healthy?
  |     |
  |     +-- YES: Daily digest due?
  |     |         |
  |     |         +-- YES: Send daily digest via Apprise
  |     |         +-- NO: Exit quietly
  |     |
  |     +-- NO: Escalate to Tier 2
  |             |
  |             +-- Tier 2 fixes issue?
  |             |     |
  |             |     +-- YES: Send auto-remediation report via Apprise
  |             |     +-- NO (cooldown exceeded): Send human attention alert via Apprise
  |             |     +-- NO (needs more): Escalate to Tier 3
  |             |                           |
  |             |                           +-- Fixed: Send Tier 3 remediation report
  |             |                           +-- Not fixed: Send human attention alert
  |             |
  |             +-- Services in cooldown: Send human attention alert via Apprise
```

## Data Flow

### Apprise URL Parsing

Apprise URLs encode the notification service, credentials, and target in a single string:

```
scheme://[credentials@]host[:port]/target[?params]

Examples:
  ntfy://ntfy.sh/my-topic
  slack://tokenA/tokenB/tokenC/#channel
  mailto://user:pass@smtp.gmail.com?to=ops@example.com
  discord://webhook_id/webhook_token
  tgram://bot_token/chat_id
  pagerduty://integration_key@routing_key
```

Multiple URLs are comma-separated in `CLAUDEOPS_APPRISE_URLS`. Apprise handles the parsing and dispatching internally -- the agent does not need to understand URL schemes. It passes the entire variable value to the `apprise` CLI.

### Message Construction by Tier

Each tier constructs notification messages differently based on the context available:

**Tier 1 (Observe):**
```bash
# Daily digest
apprise -t "Claude Ops: Daily Health Summary" \
  -b "Services checked: 12
Healthy: 10
Degraded: 1 (service-x: high response time)
Down: 1 (service-y: HTTP 503)
In cooldown: 0" \
  "$CLAUDEOPS_APPRISE_URLS"
```

**Tier 2 (Investigate/Remediate):**
```bash
# Successful remediation
apprise -t "Claude Ops: Auto-remediated service-y" \
  -b "Issue: Container was in CrashLoopBackOff (5 restarts in 10 min).
Action: docker restart service-y.
Status: Service responding HTTP 200, latency 45ms." \
  "$CLAUDEOPS_APPRISE_URLS"

# Needs human attention
apprise -t "Claude Ops: Needs human attention -- service-y" \
  -b "Issue: Container keeps crashing after restart.
Cooldown limit reached: 2/2 restarts in 4h window.
Attempts: Restarted twice, checked logs for OOM.
Last error: out of memory (container limit 256MB)." \
  "$CLAUDEOPS_APPRISE_URLS"
```

**Tier 3 (Full Remediation):**
```bash
# Remediated
apprise -t "Claude Ops: Remediated service-y (Tier 3)" \
  -b "Root cause: Database connection pool exhausted due to leaked connections.
Actions taken:
  1. Restarted PostgreSQL
  2. Waited for connection recovery
  3. Restarted service-y
  4. Verified health check passes
Verification: All services healthy, connection count normal.
Recommendations: Review connection pool settings, add connection timeout." \
  "$CLAUDEOPS_APPRISE_URLS"

# Not fixed
apprise -t "Claude Ops: NEEDS HUMAN ATTENTION -- service-y" \
  -b "Issue: Persistent database corruption.
Investigation: Data directory has corrupted WAL files.
Attempted: Controlled restart, checked disk space (OK).
Why it failed: WAL corruption requires manual pg_resetwal.
Recommended next steps: SSH to host, run pg_resetwal, verify data integrity.
Current state: PostgreSQL down, all dependent services degraded." \
  "$CLAUDEOPS_APPRISE_URLS"
```

## Key Decisions

### Why Apprise over direct API integrations

Direct API integrations (Option 2 in the ADR) would avoid the Python dependency but require:
- Writing and maintaining a separate `curl` command for each notification service
- Managing service-specific credential formats (webhook URLs, SMTP credentials, API tokens)
- Duplicating notification logic across tier prompts for each supported service
- Increasing prompt complexity proportional to the number of supported services

Apprise abstracts all of this behind a single CLI invocation and URL-based configuration, keeping the prompts service-agnostic.

### Why CLI invocation instead of library usage

Claude Ops is a runbook system where the agent executes shell commands from markdown instructions. Using Apprise as a Python library would require either:
- Writing Python scripts that the agent calls (introducing application code into a no-code runbook)
- Having the agent write inline Python (fragile and error-prone)

CLI invocation aligns with the same pattern used for `curl`, `docker`, `dig`, and all other operations -- the agent constructs a shell command and executes it via Bash.

### Why a single environment variable

Each alternative approach evaluated in the ADR required more complex configuration:
- Direct API integrations: per-service environment variables
- AWS SNS: IAM credentials, topic ARNs, subscription management
- Webhook approach: relay service configuration

A single comma-separated environment variable means operators can configure notifications by adding one line to their `.env` file. Adding a new notification channel is appending a URL to an existing string.

### Why graceful degradation over required configuration

Notifications are an operational convenience, not a core function. Claude Ops' primary job is to detect and remediate infrastructure issues. Making notifications a hard requirement would mean:
- New deployments fail until notification is configured
- Test environments need notification targets
- Air-gapped environments without outbound notification access cannot run Claude Ops

Silent no-op when unconfigured ensures Claude Ops works immediately out of the box, with notifications as an opt-in enhancement.

## Trade-offs

### Gained

- **Universal coverage**: 80+ notification services supported without any per-service code or configuration blocks. Operators choose their preferred service by specifying its URL.
- **Zero-code integration**: Notifications are CLI commands in markdown prompts. No application code, no Python scripts, no compiled modules.
- **Configuration simplicity**: One environment variable. Add a channel by appending a URL. Remove a channel by deleting a URL. No image rebuilds.
- **Service independence**: Apprise runs entirely within the container. No external relay services, message queues, or cloud provider dependencies.
- **Graceful opt-in**: Notifications work when configured and silently disappear when not, with no error states or required setup.

### Lost

- **Python dependency**: The Docker image carries `python3`, `pip3`, and Apprise's Python dependency tree in what is otherwise a Node.js container. This increases image size.
- **No delivery guarantees**: If Apprise fails to deliver (network error, misconfigured URL, service outage), there is no retry, no dead letter queue, no delivery confirmation. The notification is lost.
- **URL syntax learning curve**: Each notification service has its own Apprise URL scheme with service-specific parameters. Operators must consult Apprise documentation to construct valid URLs.
- **Community dependency**: Apprise is community-maintained open-source software without commercial support. A breaking change, abandonment, or security vulnerability would require migration to an alternative.
- **No priority/urgency differentiation**: All notifications use the same Apprise invocation. There is no built-in mechanism to send human-attention alerts at a higher priority than daily digests, unless the notification service supports priority via URL parameters.

## Future Considerations

### Priority-based routing

A future enhancement could support routing different notification categories to different targets:
- `CLAUDEOPS_APPRISE_DIGEST_URLS` for daily digests (low priority, email)
- `CLAUDEOPS_APPRISE_ALERT_URLS` for human attention alerts (high priority, PagerDuty)
- `CLAUDEOPS_APPRISE_URLS` as the fallback for all categories

This would allow operators to avoid alert fatigue by routing routine digests to email while sending critical alerts to paging systems.

### Notification templating

The current design has each tier's prompt file define the notification message format inline. A future evolution could introduce notification templates (markdown files in a `notifications/` directory) that standardize message format across tiers and make the format independently configurable.

### Delivery status tracking

If notification reliability becomes critical, a future enhancement could:
- Log each Apprise invocation's exit code and stderr to the results directory
- Track notification delivery success rates over time
- Alert on sustained notification delivery failures (meta-notification problem)

### Retry with backoff

For environments where notification delivery is critical, a simple retry mechanism could be added:
- Retry failed Apprise invocations up to 3 times with exponential backoff
- This could be implemented as a wrapper script rather than modifying Apprise itself
- Only worth pursuing if notification loss proves to be a real operational problem
