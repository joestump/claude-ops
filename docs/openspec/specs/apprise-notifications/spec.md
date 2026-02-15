# SPEC-0004: Apprise Notification Abstraction

## Overview

Claude Ops uses the Apprise CLI as a universal notification abstraction layer, enabling operators to receive alerts and reports through any combination of 80+ notification services (email, Slack, Discord, Telegram, ntfy, PagerDuty, etc.) via a single environment variable. This specification defines the requirements for notification integration, event categories, message formatting, and graceful degradation when notifications are not configured.

## Definitions

- **Apprise**: An open-source Python CLI and library that provides a unified notification interface for 80+ services. Each notification target is configured as a URL with a service-specific scheme (e.g., `ntfy://`, `slack://`, `mailto://`).
- **Apprise URL**: A URL conforming to Apprise's service-specific scheme that identifies a notification target and its credentials (e.g., `ntfy://ntfy.sh/my-topic`).
- **Notification Event**: A categorized event that triggers a notification. Claude Ops defines three categories: daily digest, auto-remediation report, and human attention alert.
- **Daily Digest**: A periodic summary of all health check results and system uptime statistics.
- **Auto-Remediation Report**: An immediate notification sent after the system successfully fixes an issue, describing the problem, action taken, and verification result.
- **Human Attention Alert**: An immediate notification sent when remediation fails or cooldown limits are exceeded, requiring manual operator intervention.
- **Graceful Degradation**: The system behavior when `CLAUDEOPS_APPRISE_URLS` is empty or unset: notifications are silently skipped without errors.

## Requirements

### REQ-1: Single Environment Variable Configuration

The system MUST configure all notification targets through a single environment variable: `CLAUDEOPS_APPRISE_URLS`. This variable MUST accept one or more comma-separated Apprise URLs.

The system MUST NOT require any additional configuration files, per-service credential blocks, or service-specific setup for notifications.

#### Scenario: Single notification target
Given an operator sets `CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/ops-alerts`
When the system sends a notification
Then the notification is delivered to the ntfy topic `ops-alerts`

#### Scenario: Multiple notification targets
Given an operator sets `CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/ops-alerts,mailto://user:pass@smtp.example.com?to=ops@example.com`
When the system sends a notification
Then the notification is delivered to both the ntfy topic and the email address

#### Scenario: Notification target change without rebuild
Given an operator modifies `CLAUDEOPS_APPRISE_URLS` in the `.env` file
When the container is restarted
Then the new notification targets are used on the next run
And no image rebuild or prompt modification is required

### REQ-2: Graceful Degradation When Unconfigured

When `CLAUDEOPS_APPRISE_URLS` is empty or unset, the system MUST silently skip all notification operations. No errors, warnings, or log noise MUST be generated due to missing notification configuration.

The system MUST NOT fail a health check run or remediation cycle because notifications cannot be sent.

#### Scenario: No notification URLs configured
Given `CLAUDEOPS_APPRISE_URLS` is not set in the environment
When the system reaches a notification step (daily digest, remediation report, or alert)
Then the notification is silently skipped
And the health check or remediation cycle continues normally

#### Scenario: Empty notification URLs string
Given `CLAUDEOPS_APPRISE_URLS` is set to an empty string
When the system attempts to send a notification
Then no Apprise CLI invocation occurs
And no error is logged

#### Scenario: Notification failure does not block remediation
Given `CLAUDEOPS_APPRISE_URLS` points to a misconfigured target
When the Apprise CLI invocation fails
Then the remediation cycle continues
And the failure is logged but does not interrupt operations

### REQ-3: CLI-Based Invocation

Notifications MUST be sent using the `apprise` CLI tool invoked via shell commands. The system MUST NOT use Apprise as a Python library or import it programmatically.

The invocation format MUST be:
```bash
apprise -t "<title>" -b "<body>" "$CLAUDEOPS_APPRISE_URLS"
```

This requirement aligns with Claude Ops' execution model where all operations are shell commands embedded in markdown prompts that the agent executes.

#### Scenario: Agent sends notification via CLI
Given the Tier 2 agent has successfully remediated a service
When the agent sends a notification
Then the agent executes `apprise -t "..." -b "..." "$CLAUDEOPS_APPRISE_URLS"` via Bash
And the notification is delivered to all configured targets

#### Scenario: Notification command is executed from markdown prompt context
Given a tier prompt file contains notification instructions
When the agent follows the prompt instructions to send a notification
Then the agent constructs and executes an `apprise` CLI command
And does not use Python import statements or library calls

### REQ-4: Environment Variable Passthrough to Agent

The `CLAUDEOPS_APPRISE_URLS` environment variable MUST be passed to the Claude agent's execution context so the agent can reference it in shell commands.

The entrypoint script MUST include `CLAUDEOPS_APPRISE_URLS` in the environment context appended to the system prompt, but only when the variable is set and non-empty.

#### Scenario: Apprise URLs passed to agent context
Given `CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/my-topic` is set
When the entrypoint script builds the environment context
Then `CLAUDEOPS_APPRISE_URLS` is included in the `--append-system-prompt` value
And the agent can reference `$CLAUDEOPS_APPRISE_URLS` in its shell commands

#### Scenario: Apprise URLs omitted when unset
Given `CLAUDEOPS_APPRISE_URLS` is not set
When the entrypoint script builds the environment context
Then `CLAUDEOPS_APPRISE_URLS` is not included in the system prompt
And the agent sees no Apprise configuration

### REQ-5: Three Notification Event Categories

The system MUST support three distinct notification event categories, each with its own trigger conditions and urgency level:

1. **Daily Digest** -- sent once per day summarizing all health check results and uptime statistics.
2. **Auto-Remediation Report** -- sent immediately after a successful remediation, describing the issue, action taken, and verification result.
3. **Human Attention Alert** -- sent immediately when remediation fails or cooldown limits are exceeded, indicating that manual intervention is required.

#### Scenario: Daily digest is sent once per day
Given the system has completed a health check cycle
When a daily digest has not been sent in the current day (checked via `last_daily_digest` in cooldown state)
Then the agent composes a summary of all services, their health status, and uptime
And sends it via Apprise with a descriptive title
And updates `last_daily_digest` in the cooldown state

#### Scenario: Daily digest is skipped when already sent today
Given a daily digest was already sent during the current day
When the next health check cycle completes
Then no daily digest is sent
And the agent proceeds with normal operations

#### Scenario: Auto-remediation report sent after fix
Given the Tier 2 agent successfully restarts a crashed container
When the agent verifies the service is healthy after the restart
Then the agent sends a notification via Apprise with title "Claude Ops: Auto-remediated <service>"
And the body includes what was wrong, what action was taken, and the verification result

#### Scenario: Human attention alert sent on cooldown exceeded
Given a service has exceeded its restart cooldown limit (2 restarts in 4 hours)
When the agent determines another restart would violate cooldown
Then the agent sends a notification via Apprise with title "Claude Ops: Needs human attention -- <service>"
And the body includes the issue description, cooldown state, and what was previously attempted

### REQ-6: Notification Message Format

Notifications MUST include both a title (`-t` flag) and a body (`-b` flag).

The title MUST follow the format: `Claude Ops: <action> <service_name>` where action is one of:
- `Auto-remediated` for successful remediations
- `Remediated ... (Tier 3)` for Tier 3 remediations
- `Needs human attention --` for failed remediations or cooldown-exceeded situations
- A descriptive summary for daily digests

The body MUST include contextually appropriate details:

**Auto-remediation reports** MUST include:
- What was wrong (the detected issue)
- What action was taken (the remediation performed)
- Verification result (the post-remediation health check result)

**Human attention alerts** MUST include:
- Issue description
- What was attempted (remediation actions tried)
- Why it failed or why remediation was stopped
- Current system state or recommended next steps

**Tier 3 remediation reports** MUST include:
- Root cause analysis
- Actions taken (step by step)
- Verification result
- Recommendations for follow-up

**Daily digests** MUST include:
- Total services checked
- Count of healthy, degraded, down, and in-cooldown services
- Details for any non-healthy services

#### Scenario: Auto-remediation notification has required fields
Given the Tier 2 agent has restarted a container and verified it is healthy
When the agent composes the notification body
Then the body contains the issue description (e.g., "Container was in CrashLoopBackOff")
And the body contains the action taken (e.g., "Restarted container via docker restart")
And the body contains the verification result (e.g., "Service now responding HTTP 200")

#### Scenario: Human attention alert has required fields
Given the Tier 2 agent cannot fix a service and cooldown is exceeded
When the agent composes the notification body
Then the body contains the issue description
And the body contains what was attempted
And the body contains why remediation was stopped (e.g., "Cooldown limit exceeded: 2/2 restarts in 4h window")

#### Scenario: Tier 3 notification includes root cause analysis
Given the Tier 3 agent has remediated a complex multi-service failure
When the agent composes the notification body
Then the body contains the root cause analysis
And the body contains step-by-step actions taken
And the body contains the verification result
And the body contains recommendations for follow-up

### REQ-7: Tier-Specific Notification Permissions

Tier 1 MAY send daily digest notifications and cooldown-exceeded alerts.

Tier 2 MUST send auto-remediation reports after successful remediations. Tier 2 MUST send human attention alerts when remediation fails or cooldown limits are exceeded.

Tier 3 MUST send detailed remediation reports after any remediation attempt (successful or not). Tier 3 MUST always send a notification at the end of its execution, regardless of outcome.

#### Scenario: Tier 1 sends daily digest
Given all services are healthy and a daily digest is due
When the Tier 1 agent completes its observation cycle
Then the agent sends a daily digest via Apprise summarizing all check results

#### Scenario: Tier 1 sends cooldown alert
Given a service is in cooldown and requires human attention
When the Tier 1 agent observes the service is still failing
Then the agent sends a "needs human attention" alert via Apprise

#### Scenario: Tier 2 sends remediation report
Given the Tier 2 agent successfully restarted a container
When the agent verifies the service is healthy
Then the agent sends an auto-remediation notification

#### Scenario: Tier 3 always reports
Given the Tier 3 agent has completed its work
When the agent reaches its reporting step
Then the agent always sends a notification via Apprise
And the notification reflects whether remediation succeeded or requires human attention

### REQ-8: Docker Image Installation

The Apprise CLI MUST be installed in the Docker image via `pip3 install --break-system-packages apprise`. The Docker image MUST include the `python3` and `pip3` prerequisites.

The Apprise installation MUST NOT require running additional services or sidecars.

#### Scenario: Apprise is available in the container
Given the Docker image is built from the Dockerfile
When a shell command `apprise --version` is executed inside the container
Then Apprise responds with its version number
And no additional services need to be running

#### Scenario: Apprise works without external dependencies
Given the container is running with no sidecar services
When the agent executes an Apprise CLI command
Then the notification is sent directly from the container
And no message broker, queue, or relay service is required

### REQ-9: Multiple Simultaneous Notification Targets

The system MUST support sending the same notification to multiple targets simultaneously via comma-separated Apprise URLs. All configured targets MUST receive the same notification content.

#### Scenario: Notification delivered to all targets
Given `CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/ops,slack://token_a/token_b/token_c`
When the agent sends a remediation notification
Then both the ntfy topic and the Slack channel receive the notification
And both receive the same title and body content

#### Scenario: One target failure does not prevent delivery to others
Given one of multiple configured Apprise URLs is invalid
When the agent sends a notification
Then Apprise attempts delivery to all configured targets
And valid targets receive the notification regardless of failures on other targets

### REQ-10: No Delivery Guarantee or Retry

The system does NOT provide built-in delivery confirmation or retry logic. If an Apprise CLI invocation fails, the agent MUST continue its current cycle without re-attempting the notification.

The system MAY log the notification failure but MUST NOT treat it as a blocking error.

#### Scenario: Failed notification does not block agent
Given the Apprise CLI invocation fails (e.g., network error to notification service)
When the agent continues its health check or remediation cycle
Then the agent proceeds to the next step without retrying the notification
And the cycle completes normally

#### Scenario: Notification failure is logged
Given the Apprise CLI returns a non-zero exit code
When the agent observes the failure
Then the failure appears in the run log in `$CLAUDEOPS_RESULTS_DIR`
And the agent does not retry the command

## References

- [ADR-0004: Apprise Notification Abstraction](../../adrs/ADR-0004-apprise-notification-abstraction.md)
- [Apprise Documentation](https://github.com/caronc/apprise)
- [Apprise URL Schemes](https://github.com/caronc/apprise/wiki)
