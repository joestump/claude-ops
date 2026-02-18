---
sidebar_position: 6
---

# Notifications

Claude Ops sends notifications via [Apprise](https://github.com/caronc/apprise), which supports 80+ notification services through URL-based configuration.

## Setup

Set the `CLAUDEOPS_APPRISE_URLS` environment variable to one or more comma-separated Apprise URLs:

```env
# Single service
CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/my-claude-ops-topic

# Multiple services
CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/my-topic,slack://TokenA/TokenB/TokenC,mailto://user:pass@gmail.com
```

If `CLAUDEOPS_APPRISE_URLS` is empty or unset, notifications are silently skipped.

## When notifications fire

### Daily digest

Once per day, Claude Ops sends a summary of all checks and uptime stats.

### Auto-remediated

Immediately after successful remediation:
- What was wrong
- What action was taken
- Verification result

### Needs attention

Immediately when remediation fails or a cooldown limit is exceeded:
- What's wrong
- What was tried
- Why it didn't work

## Common Apprise URLs

| Service | URL Format |
|---------|-----------|
| [ntfy](https://ntfy.sh) | `ntfy://ntfy.sh/your-topic` |
| Slack | `slack://TokenA/TokenB/TokenC` |
| Discord | `discord://WebhookID/WebhookToken` |
| Telegram | `tgram://BotToken/ChatID` |
| Email (Gmail) | `mailto://user:password@gmail.com` |
| PagerDuty | `pagerduty://IntegrationKey@RoutingKey` |
| Pushover | `pover://UserKey@AppToken` |

See the [Apprise wiki](https://github.com/caronc/apprise/wiki) for the full list of supported services and their URL formats.

## Example

```env
# .env
CLAUDEOPS_APPRISE_URLS=ntfy://ntfy.sh/homelab-alerts,slack://xoxb-token/channel
```

With this configuration, both ntfy and Slack receive notifications for daily digests, auto-remediations, and escalations that need human attention.
