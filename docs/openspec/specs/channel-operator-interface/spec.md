---
status: proposed
date: 2026-03-21
---

# SPEC-0033: Channel-Based Operator Interface

## Overview

Claude Ops communicates with operators through three separate mechanisms: Apprise CLI for outbound notifications (ADR-0004), an OpenAI-compatible chat endpoint for inbound operator messages (ADR-0020), and an inbound webhook endpoint for external alert ingestion (ADR-0024). This specification defines a unified channel plugin (`claudeops-channel`) that leverages Claude Code's native channel protocol to provide bidirectional operator communication (Telegram/Discord chat bridge), webhook event ingestion, and alert push — replacing two of the three mechanisms and supplementing the third.

See [ADR-0032: Channel-Based Operator Interface](../../adrs/ADR-0032-channel-operator-interface.md) for the decision rationale.

**Status note**: This specification is proposed but blocked by a channel authentication constraint. Channels require `claude.ai` login; Claude-Ops uses API key authentication. Implementation MUST NOT proceed until this constraint is resolved. See REQ-8.

## Definitions

- **Channel**: A Claude Code MCP server that declares the `claude/channel` capability, enabling it to push events into a running Claude Code session and expose reply tools for the agent to respond.
- **Channel event**: A message delivered to the Claude session by the channel plugin, formatted as `<channel source="..." ...>content</channel>`. The session receives these events as part of its context.
- **Reply tool**: An MCP tool exposed by the channel plugin (e.g., `mcp__claudeops_channel__reply`) that Claude invokes to send a message back through the channel to the operator or external system.
- **Operator**: A human authorized to interact with Claude Ops via Telegram, Discord, or other configured chat platforms.
- **Sender allowlist**: A list of authorized platform-specific user IDs (Telegram user IDs, Discord user IDs) that the channel plugin will accept messages from. Unauthorized senders are silently dropped.
- **Pairing flow**: The initial process by which an operator's platform user ID is added to the sender allowlist, typically by sending a pairing code to the channel.
- **Webhook receiver**: An HTTP listener within the channel plugin that accepts POST requests from external monitoring tools and forwards them as channel events.

## Requirements

### REQ-1: Channel Plugin Architecture

The system MUST provide a custom MCP server plugin (`claudeops-channel`) that declares the `claude/channel` capability. The plugin MUST be a TypeScript/Bun MCP server conforming to the Claude Code channel protocol. The plugin MUST be installable via the Claude Code plugin system and configurable via environment variables.

The plugin MUST support three independent subsystems that can be enabled or disabled via configuration:

1. Operator chat bridge (Telegram and/or Discord)
2. Webhook receiver (HTTP listener)
3. Reply tool (always enabled when the plugin is active)

#### Scenario: Plugin declares channel capability

Given the `claudeops-channel` plugin is installed
When Claude Code loads the plugin during session startup
Then the plugin MUST declare the `claude/channel` capability in its MCP manifest
And Claude Code MUST recognize it as a channel provider

#### Scenario: Plugin starts with only Telegram enabled

Given `CLAUDEOPS_CHANNEL_TELEGRAM_TOKEN` is set
And `CLAUDEOPS_CHANNEL_DISCORD_TOKEN` is not set
And `CLAUDEOPS_CHANNEL_WEBHOOK_PORT` is not set
When the plugin initializes
Then the Telegram chat bridge MUST be active
And the Discord chat bridge MUST NOT be active
And the webhook receiver MUST NOT be active
And the reply tool MUST be available

#### Scenario: Plugin starts with all subsystems enabled

Given `CLAUDEOPS_CHANNEL_TELEGRAM_TOKEN` is set
And `CLAUDEOPS_CHANNEL_DISCORD_TOKEN` is set
And `CLAUDEOPS_CHANNEL_WEBHOOK_PORT` is set to `9876`
When the plugin initializes
Then all three subsystems MUST be active
And the webhook receiver MUST listen on port `9876`

### REQ-2: Operator Chat Bridge

The plugin MUST support Telegram and Discord as operator chat platforms, configurable via `CLAUDEOPS_CHANNEL_TELEGRAM_TOKEN` and `CLAUDEOPS_CHANNEL_DISCORD_TOKEN` respectively.

Messages from authorized operators MUST arrive in the Claude session as `<channel>` events with the following metadata attributes:

- `source`: MUST be `"claudeops"`
- `sender`: MUST be `"operator"`
- `platform`: MUST be the originating platform (`"telegram"` or `"discord"`)

The message text MUST be the content of the `<channel>` element.

#### Scenario: Telegram message arrives as channel event

Given the Telegram chat bridge is active
And user ID `123456` is in the sender allowlist
When the operator sends "restart jellyfin" in the Telegram conversation
Then a channel event MUST be delivered to the Claude session:
```
<channel source="claudeops" sender="operator" platform="telegram">
restart jellyfin
</channel>
```

#### Scenario: Discord message arrives as channel event

Given the Discord chat bridge is active
And user ID `987654321` is in the sender allowlist
When the operator sends "check DNS for loki" in the Discord conversation
Then a channel event MUST be delivered to the Claude session:
```
<channel source="claudeops" sender="operator" platform="discord">
check DNS for loki
</channel>
```

#### Scenario: Unauthorized sender is silently dropped

Given the Telegram chat bridge is active
And user ID `999999` is NOT in the sender allowlist
When user `999999` sends a message in the Telegram conversation
Then the message MUST NOT be forwarded to the Claude session
And no error response MUST be sent to the unauthorized sender

### REQ-3: Reply Tool

The plugin MUST expose a `reply` tool that Claude can use to send messages back to the operator through the channel. The reply tool MUST accept the following parameters:

- `platform`: The target platform (`"telegram"` or `"discord"`). REQUIRED.
- `message`: The message text to send. REQUIRED.

The reply MUST appear in the operator's Telegram or Discord conversation. If the specified platform is not active (not configured), the reply tool MUST return an error indicating the platform is unavailable.

#### Scenario: Claude replies to operator via Telegram

Given the operator sent a message via Telegram
When Claude invokes `mcp__claudeops_channel__reply` with `platform: "telegram"` and `message: "Jellyfin has been restarted and is now healthy."`
Then the message MUST appear in the operator's Telegram conversation
And the reply tool MUST return a success confirmation

#### Scenario: Claude replies to operator via Discord

Given the operator sent a message via Discord
When Claude invokes `mcp__claudeops_channel__reply` with `platform: "discord"` and `message: "DNS check complete: pi04.stump.rocks still has no A record."`
Then the message MUST appear in the operator's Discord conversation

#### Scenario: Reply to unconfigured platform returns error

Given `CLAUDEOPS_CHANNEL_DISCORD_TOKEN` is not set
When Claude invokes `mcp__claudeops_channel__reply` with `platform: "discord"` and `message: "test"`
Then the reply tool MUST return an error: `"Discord platform is not configured"`
And no message MUST be sent

### REQ-4: Webhook Receiver

The plugin MUST listen on a configurable HTTP port (`CLAUDEOPS_CHANNEL_WEBHOOK_PORT`) for incoming webhook payloads. Webhook payloads MUST be forwarded as channel events with the following metadata attributes:

- `source`: MUST be `"claudeops"`
- `type`: MUST be `"webhook"`
- `sender`: SHOULD be derived from the payload or a query parameter (e.g., `?source=uptimekuma`). If no sender can be determined, MUST default to `"unknown"`.
- `content-type`: MUST reflect the original `Content-Type` header of the incoming request.

The plugin MUST accept any `Content-Type` including `application/json`, `application/x-www-form-urlencoded`, and `text/plain`. The raw payload body MUST be the content of the `<channel>` element.

#### Scenario: UptimeKuma webhook arrives as channel event

Given the webhook receiver is listening on port `9876`
And `CLAUDEOPS_CHANNEL_WEBHOOK_TOKEN` is set to `secret123`
When UptimeKuma sends:
```
POST http://localhost:9876/webhook?source=uptimekuma
Authorization: Bearer secret123
Content-Type: application/json

{"heartbeat":{"status":0},"monitor":{"name":"Jellyfin","url":"https://jellyfin.stump.rocks"}}
```
Then a channel event MUST be delivered to the Claude session:
```
<channel source="claudeops" type="webhook" sender="uptimekuma" content-type="application/json">
{"heartbeat":{"status":0},"monitor":{"name":"Jellyfin","url":"https://jellyfin.stump.rocks"}}
</channel>
```

#### Scenario: Plain text webhook arrives as channel event

Given the webhook receiver is listening
When a plain text payload is received:
```
POST http://localhost:9876/webhook
Authorization: Bearer secret123
Content-Type: text/plain

CRITICAL: disk usage on ie01 at 95%
```
Then a channel event MUST be delivered with `content-type="text/plain"` and `sender="unknown"`

#### Scenario: Webhook without valid bearer token is rejected

Given the webhook receiver is listening
And `CLAUDEOPS_CHANNEL_WEBHOOK_TOKEN` is set
When a POST request arrives without a valid `Authorization: Bearer` header
Then the webhook receiver MUST respond with HTTP 401 Unauthorized
And the payload MUST NOT be forwarded as a channel event

### REQ-5: Sender Allowlist

The plugin MUST maintain a sender allowlist for chat platforms. Only messages from allowlisted sender IDs MUST be forwarded to the session. The allowlist MUST be stored in `~/.claude/channels/claudeops/access.json` with the following structure:

```json
{
  "telegram": ["123456", "789012"],
  "discord": ["987654321"]
}
```

The plugin MUST support a pairing flow for bootstrapping the allowlist:

1. The operator starts the pairing process (e.g., via a CLI command or environment variable `CLAUDEOPS_CHANNEL_PAIRING_CODE`).
2. The operator sends the pairing code as a message in Telegram/Discord.
3. The plugin verifies the code and adds the sender's platform user ID to the allowlist.
4. The pairing code MUST be single-use and MUST expire after a configurable timeout (default: 5 minutes).

Webhook endpoints SHOULD use bearer token authentication (`CLAUDEOPS_CHANNEL_WEBHOOK_TOKEN`) instead of the sender allowlist. The webhook token MUST be validated before any payload is forwarded.

#### Scenario: Allowlisted sender's message is forwarded

Given user ID `123456` is in the Telegram allowlist
When user `123456` sends a message
Then the message MUST be forwarded to the session as a channel event

#### Scenario: Non-allowlisted sender's message is dropped

Given user ID `999999` is NOT in any allowlist
When user `999999` sends a message via Telegram
Then the message MUST be silently dropped
And the plugin MUST NOT respond to the sender

#### Scenario: Pairing flow adds sender to allowlist

Given `CLAUDEOPS_CHANNEL_PAIRING_CODE` is set to `pair-abc123`
And user ID `555555` is NOT in the Telegram allowlist
When user `555555` sends `pair-abc123` in the Telegram conversation
Then user `555555` MUST be added to the Telegram allowlist in `access.json`
And the pairing code MUST be invalidated (single-use)
And the plugin MUST respond with a confirmation message

### REQ-6: Apprise Coexistence

The channel plugin MUST NOT replace Apprise for non-channel notification targets. When the agent needs to notify via email, Slack, PagerDuty, or other non-Telegram/Discord targets, it MUST use Apprise (`apprise` CLI with `$CLAUDEOPS_APPRISE_URLS`).

When a channel session is active and the notification target is Telegram or Discord, the agent SHOULD use the channel `reply` tool instead of Apprise. This provides bidirectional communication where the operator can respond.

When no channel session is active (e.g., during headless `-p` sessions), the agent MUST fall back to Apprise for all notification targets including Telegram and Discord.

#### Scenario: Agent uses channel reply for Telegram when channel is active

Given the `claudeops-channel` plugin is active with Telegram configured
And the agent needs to send a remediation report
When the notification target includes Telegram
Then the agent SHOULD use `mcp__claudeops_channel__reply` with `platform: "telegram"` for the Telegram notification
And MUST use `apprise` for any non-Telegram/Discord targets in `$CLAUDEOPS_APPRISE_URLS`

#### Scenario: Agent falls back to Apprise when no channel session is active

Given the `claudeops-channel` plugin is NOT active (headless `-p` session)
And the agent needs to send a remediation report
When the notification target includes Telegram
Then the agent MUST use `apprise` with `$CLAUDEOPS_APPRISE_URLS` for all targets including Telegram

#### Scenario: Non-channel targets always use Apprise

Given the `claudeops-channel` plugin is active
And the notification targets include email and PagerDuty
When the agent sends a notification
Then the agent MUST use `apprise` for email and PagerDuty
Because these are not channel-capable targets

### REQ-7: Session Integration

The channel MUST be enabled via `--channels` flag on the Claude Code invocation. The session manager MUST pass the `--channels` flag when starting sessions that require channel support. Events MUST only arrive while the session is active.

The session manager MUST support two invocation modes:

1. **Headless mode** (current) — Single-shot `-p` invocations without `--channels`. Used for scheduled monitoring cycles. API key authentication.
2. **Channel mode** — Persistent session with `--channels` enabled. Used for operator interaction. Requires claude.ai authentication.

The session manager MAY run both modes concurrently: a persistent channel session for operator interaction and scheduled `-p` sessions for monitoring. Conflict resolution (preventing concurrent sessions from interfering) MUST follow the existing mutex-based pattern from ADR-0013.

#### Scenario: Channel session receives operator messages

Given a persistent session is running with `--channels claudeops-channel`
When the operator sends a Telegram message
Then the message arrives as a channel event in the active session
And Claude processes it with full session context (check results, cooldown state, inventory)

#### Scenario: Headless session does not receive channel events

Given a scheduled `-p` session is running without `--channels`
When the operator sends a Telegram message
Then the message MUST NOT arrive in the headless session
And the channel plugin SHOULD queue the message for delivery when the next channel session starts (or discard it if no channel session starts within a configurable timeout)

#### Scenario: Session manager passes channels flag

Given channel mode is enabled in the configuration
When the session manager starts a channel-capable session
Then the `claude` CLI invocation MUST include `--channels claudeops-channel`
And the channel plugin MUST be initialized and connected

### REQ-8: Authentication Prerequisite

The channel system MUST NOT be activated unless `claude.ai` authentication is available. The system MUST fall back to the existing communication architecture (Apprise for notifications, OpenAI chat endpoint for inbound, webhook endpoint for alert ingestion) when only API key authentication (`ANTHROPIC_API_KEY`) is configured.

The plugin MUST detect the authentication method at startup and fail gracefully if channels are not supported.

#### Scenario: Channel plugin disabled with API key auth

Given `ANTHROPIC_API_KEY` is set
And `claude.ai` login is not configured
When the session manager attempts to start a channel session
Then the channel session MUST NOT be started
And a warning MUST be logged: `"Channels require claude.ai authentication. Falling back to existing communication architecture."`
And the system MUST continue using Apprise, OpenAI chat endpoint, and webhook endpoint

#### Scenario: Channel plugin enabled with claude.ai auth

Given `claude.ai` authentication is configured
And `CLAUDEOPS_CHANNEL_TELEGRAM_TOKEN` is set
When the session manager starts a channel session
Then the `--channels claudeops-channel` flag MUST be passed
And the Telegram chat bridge MUST be active

#### Scenario: Graceful degradation preserves all existing functionality

Given the channel plugin cannot be activated (auth constraint)
When the system operates in fallback mode
Then Apprise notifications MUST work as specified in ADR-0004
And the OpenAI chat endpoint MUST work as specified in ADR-0020
And the webhook endpoint MUST work as specified in ADR-0024
And no channel-related errors MUST appear in normal operation logs

## References

- [ADR-0032: Channel-Based Operator Interface](../../adrs/ADR-0032-channel-operator-interface.md)
- [ADR-0004: Apprise Notification Abstraction](../../adrs/ADR-0004-apprise-notification-abstraction.md)
- [ADR-0020: OpenAI-Compatible Chat Endpoint](../../adrs/ADR-0020-openai-compatible-chat-endpoint.md)
- [ADR-0024: Inbound Webhook Alert Ingestion](../../adrs/ADR-0024-inbound-webhook-alert-ingestion.md)
- [ADR-0013: Manual Ad-Hoc Session Runs](../../adrs/ADR-0013-manual-ad-hoc-session-runs.md)
- [SPEC-0004: Apprise Notification Abstraction](../apprise-notifications/spec.md)
- [SPEC-0024: OpenAI-Compatible Chat Endpoint](../openai-chat-endpoint/spec.md)
- [SPEC-0025: Inbound Webhook Alert Ingestion](../inbound-webhook/spec.md)
