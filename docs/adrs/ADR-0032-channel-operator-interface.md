---
status: proposed
date: 2026-03-21
decision-makers: Joe Stump
partially-supersedes: ADR-0020, ADR-0024
supplements: ADR-0004, ADR-0013
---

# ADR-0032: Channel-Based Operator Interface

## Context and Problem Statement

Claude-Ops currently uses three separate mechanisms for external communication:

1. **Apprise CLI** (ADR-0004) — One-way notification gateway supporting 80+ services (Telegram, Discord, email, Slack, etc.). The agent invokes `apprise` to push notifications. Operators cannot respond.

2. **OpenAI-compatible chat endpoint** (ADR-0020) — `/v1/chat/completions` endpoint that lets operators send messages from mobile chat apps (like ChatGPT-compatible clients). Stateless: each message is a new session, no conversation history. Requires a compatible client app.

3. **Inbound webhook endpoint** (ADR-0024) — `POST /api/v1/webhook` accepts arbitrary JSON/text payloads (from UptimeKuma, Grafana, PagerDuty, etc.), synthesizes an investigation prompt via LLM intermediary, triggers an ad-hoc session.

Claude Code now supports **Channels** — MCP servers that push events bidirectionally into a running Claude Code session. Channels enable:

- **Chat bridges**: Telegram/Discord messages arrive in the session, Claude responds through the same channel
- **Webhook receivers**: HTTP POST events pushed directly to the session as `<channel>` events
- **Two-way communication**: Reply tools let Claude send messages back through the channel

A custom Claude-Ops channel plugin could unify all three communication mechanisms:

- **Operator commands via Telegram/Discord** (replaces OpenAI chat endpoint for mobile access)
- **Event/alert push to operator** (supplements Apprise for Telegram/Discord with bidirectional capability)
- **Webhook ingestion as channel events** (replaces custom webhook endpoint)

**CRITICAL BLOCKER**: Channels currently require `claude.ai` login. API key authentication (`ANTHROPIC_API_KEY`), which Claude-Ops uses, is explicitly NOT supported. This ADR is proposed but blocked pending resolution of this authentication constraint. The ADR documents the intended architecture so implementation can proceed immediately once the constraint is lifted.

## Decision Drivers

* **Unify three separate communication mechanisms into one native architecture** — Apprise (outbound-only), OpenAI chat endpoint (inbound-only, stateless), and webhook endpoint (inbound-only, LLM synthesis) are each solving one piece of the operator communication problem. Channels address all three natively.
* **Enable bidirectional operator communication** — Operators cannot reply to Apprise notifications. With channels, a Telegram message from the operator arrives in the session, and Claude can reply in the same thread. This eliminates the "receive alert, context-switch to chat app" workflow.
* **Leverage Claude Code's native channel protocol** — Channels are a first-class Claude Code feature with a well-defined MCP capability declaration, event format, and reply tool. Building on the native protocol is more maintainable than custom Go HTTP endpoints.
* **Eliminate the need for OpenAI-compatible client apps** — ADR-0020 requires operators to install and configure an OpenAI-compatible app. With a Telegram/Discord channel, operators use apps they already have for daily communication.
* **Maintain Apprise for non-channel targets** — Apprise's strength is breadth: email, Slack, PagerDuty, ntfy, and 80+ services. Channels excel at Telegram/Discord bidirectional. The two are complementary, not competing.
* **Channels are in research preview and may change** — The channel API, event format, and MCP capability declaration are not yet stable. Building on them introduces the risk of breaking changes.
* **Auth blocker prevents immediate adoption** — Channels require `claude.ai` login. Claude-Ops runs headless with `ANTHROPIC_API_KEY`. Until API key support is added to channels (or Claude-Ops switches auth models), this ADR cannot be implemented.

## Considered Options

1. **Custom Claude-Ops channel plugin** — Build a channel MCP server that handles operator chat (Telegram/Discord), webhook ingestion, and event push. Package as a Claude Code plugin.
2. **Adopt official Telegram/Discord plugins directly** — Use the official claude-plugins-official Telegram and Discord plugins as-is, without a custom wrapper.
3. **Keep current architecture** — Maintain separate Apprise, OpenAI endpoint, and webhook endpoint.
4. **Webhook-only channel** — Build a channel plugin that only handles webhook ingestion, keeping Apprise and OpenAI endpoint separate for operator communication.

## Decision Outcome

Chosen option: **"Custom Claude-Ops channel plugin"**, because it unifies all three communication paths into a single MCP server with a coherent configuration model, provides bidirectional operator communication through native chat platforms, and replaces two custom Go endpoints (OpenAI chat, webhook ingestion) with a single channel plugin that leverages Claude Code's native event delivery.

### Plugin Architecture

The `claudeops-channel` plugin is a TypeScript/Bun MCP server that declares the `claude/channel` capability and provides three functions:

1. **Operator chat bridge** — Connects to Telegram Bot API and/or Discord Bot API (configurable). Operator sends a message in Telegram/Discord. The message arrives in the Claude session as:
   ```
   <channel source="claudeops" sender="operator" platform="telegram">
   restart jellyfin — it's been down since 3pm
   </channel>
   ```
   Claude processes the message in the running session context and replies via the channel's `reply` tool. The reply appears in the operator's Telegram/Discord conversation. This replaces ADR-0020's OpenAI chat endpoint for mobile access — operators use their native chat app instead of a specialized OpenAI client.

2. **Webhook receiver** — Listens on a configurable HTTP port for incoming webhooks. A POST payload from UptimeKuma arrives as:
   ```
   <channel source="claudeops" type="webhook" sender="uptimekuma">
   {"heartbeat":{"status":0},"monitor":{"name":"Jellyfin","url":"https://jellyfin.stump.rocks"}}
   </channel>
   ```
   This replaces ADR-0024's custom webhook endpoint. The LLM synthesis step from ADR-0024 is eliminated — Claude interprets the raw payload directly in session context, which is richer than a synthesized one-paragraph prompt because the agent has access to the full monitoring state.

3. **Event/alert push** — When Claude needs to notify the operator, it uses the channel's `reply` tool to push messages to Telegram/Discord. For non-channel targets (email, Slack, PagerDuty), Apprise remains the notification path.

### Sender Allowlist

Only authorized operator IDs can push messages into the session. The allowlist is stored in `~/.claude/channels/claudeops/access.json` and bootstrapped via a pairing flow (standard channel security model). Webhook endpoints use bearer token authentication (`CLAUDEOPS_CHANNEL_WEBHOOK_TOKEN`), consistent with the existing `CLAUDEOPS_CHAT_API_KEY` pattern.

### What This Supersedes and Supplements

- **Partially supersedes ADR-0020** (OpenAI chat endpoint) — The channel chat bridge provides a more direct mobile interface through native Telegram/Discord. The OpenAI endpoint MAY be retained for operators who prefer OpenAI-compatible client apps or who do not use Telegram/Discord.
- **Partially supersedes ADR-0024** (Webhook ingestion) — The webhook channel replaces the custom Go endpoint. LLM prompt synthesis is eliminated because Claude interprets webhooks directly in the session, with full monitoring context.
- **Supplements ADR-0004** (Apprise) — Channels handle Telegram/Discord bidirectionally. Apprise continues handling email, Slack, PagerDuty, and other non-channel targets. When a channel session is active, the agent SHOULD prefer the channel `reply` tool for Telegram/Discord over invoking `apprise`.
- **Supplements ADR-0013** (Ad-hoc sessions) — Operator messages via channel can trigger investigation within the running session context. Unlike `TriggerAdHoc()`, which starts a new session, channel messages arrive in the active session, preserving the full monitoring state (check results, cooldown data, inventory context).

### Configuration

The plugin is configured via environment variables:

| Variable | Purpose | Required |
|----------|---------|----------|
| `CLAUDEOPS_CHANNEL_TELEGRAM_TOKEN` | Telegram Bot API token | If Telegram bridge enabled |
| `CLAUDEOPS_CHANNEL_DISCORD_TOKEN` | Discord Bot API token | If Discord bridge enabled |
| `CLAUDEOPS_CHANNEL_WEBHOOK_PORT` | HTTP port for webhook ingestion | If webhook receiver enabled |
| `CLAUDEOPS_CHANNEL_WEBHOOK_TOKEN` | Bearer token for webhook auth | If webhook receiver enabled |

### Auth Blocker

This architecture requires a persistent running session with `--channels` enabled. Channels require `claude.ai` login, not API key auth. Claude-Ops currently uses `ANTHROPIC_API_KEY` for headless `-p` mode. Resolution paths:

1. **Claude channels add API key support** — The most likely resolution. Channels are in research preview; API key auth may be added before GA.
2. **Claude-Ops switches to claude.ai auth (OAuth)** — For the persistent session only. This would be a significant architectural change (ADR-0010 assumes headless `-p` invocations).
3. **Hybrid approach** — Headless `-p` sessions for monitoring (API key), persistent session for channels (claude.ai login). Two session types coexist. This is architecturally complex but preserves the existing monitoring flow.

### Consequences

**Positive:**

* Unifies three separate communication mechanisms (Apprise outbound, OpenAI chat inbound, webhook inbound) into one native channel architecture.
* Enables true bidirectional operator interaction — operators can reply to alerts in the same Telegram/Discord thread.
* Leverages Claude Code's native channel protocol, reducing custom code (eliminates the OpenAI SSE transform in `internal/web/chat_handler.go` and the webhook handler in `internal/web/webhook_handler.go`).
* Webhook payloads are interpreted directly by Claude in session context, which is richer than LLM-synthesized one-paragraph prompts. The agent has access to check results, cooldown state, and inventory when processing the webhook.
* Channel security model (sender allowlist, pairing flow) is well-defined and handled by the channel protocol.
* Operators use their native chat apps (Telegram, Discord) instead of requiring a specialized OpenAI-compatible client.
* Apprise continues unchanged for non-channel targets, preserving the full breadth of notification services.

**Negative:**

* **Auth blocker prevents immediate adoption** — Channels require claude.ai login; Claude-Ops uses API key auth. This ADR cannot be implemented until the constraint is resolved.
* **Channels are in research preview** — The API may change, breaking the plugin. Early adoption carries maintenance risk.
* **Introduces a TypeScript/Bun dependency** — The channel plugin is a TypeScript MCP server, adding a language and runtime to a project that is otherwise pure Go and bash/markdown.
* **Persistent session required** — Channels require a long-running Claude Code session, which is an architectural shift from the current single-shot `-p` invocations (ADR-0010). The entrypoint loop would need to be replaced or supplemented with a persistent session model.
* **Apprise still needed for non-channel targets** — Email, Slack, PagerDuty, and other services not covered by the Telegram/Discord bridge still go through Apprise. The architecture is not fully unified.
* **Plugin maintenance burden** — The `claudeops-channel` plugin must track Telegram Bot API changes, Discord Bot API changes, and Claude Code channel protocol changes.

## Pros and Cons of the Options

### Custom Claude-Ops Channel Plugin

Build a unified channel MCP server (`claudeops-channel`) in TypeScript/Bun that handles operator chat (Telegram/Discord), webhook ingestion, and event push. Declares the `claude/channel` capability. Provides a `reply` tool for Claude to send messages back.

* Good, because it unifies three communication mechanisms into a single, coherent architecture with one configuration model.
* Good, because it enables true bidirectional operator interaction — alerts and responses in the same Telegram/Discord thread.
* Good, because it leverages Claude Code's native channel protocol, event format, and reply tool rather than building custom HTTP endpoints.
* Good, because webhook payloads are interpreted directly by Claude in session context, eliminating the LLM synthesis intermediary and its associated latency and failure modes.
* Good, because operators use their native chat apps (Telegram, Discord) instead of requiring a specialized OpenAI-compatible client.
* Good, because the channel security model (sender allowlist, pairing flow) is standardized and well-defined.
* Good, because it eliminates custom Go code for the OpenAI SSE transform and webhook handler.
* Bad, because channels require `claude.ai` login, which Claude-Ops does not currently use (API key auth only). This is a hard blocker.
* Bad, because channels are in research preview — the protocol, event format, and capability declaration may change before GA.
* Bad, because it introduces a TypeScript/Bun runtime dependency into a Go/bash/markdown project.
* Bad, because it requires a persistent long-running session, which is a significant architectural shift from the current single-shot `-p` invocation model.
* Bad, because Apprise is still needed for non-channel targets (email, Slack, PagerDuty), so the communication architecture is not fully unified.
* Bad, because the plugin must track upstream API changes for Telegram, Discord, and Claude Code channels — three separate APIs.

### Adopt Official Telegram/Discord Plugins Directly

Use the official Telegram and Discord channel plugins from claude-plugins-official as-is. No custom plugin development; configure the official plugins and let them bridge operator chat into the session.

* Good, because no custom plugin code to write or maintain — use official implementations.
* Good, because official plugins are more likely to track channel protocol changes.
* Good, because it provides the same bidirectional operator chat capability for Telegram/Discord.
* Bad, because official plugins do not include webhook ingestion — the custom webhook endpoint (ADR-0024) would still be needed.
* Bad, because configuration cannot be unified — each official plugin has its own configuration model, separate from Claude-Ops' env var pattern.
* Bad, because sender allowlist management is per-plugin, not centralized.
* Bad, because the same `claude.ai` auth blocker applies — official plugins use channels, which require login.
* Bad, because there is no mechanism to prefer channel replies over Apprise for Telegram/Discord — the agent would need prompt instructions to choose between two paths for the same platform.
* Bad, because official plugins may not exist for all desired platforms, or may not support the specific features Claude-Ops needs (e.g., structured webhook metadata in channel events).

### Keep Current Architecture

Maintain the three separate communication mechanisms: Apprise for outbound notifications, OpenAI-compatible chat endpoint for mobile inbound, webhook endpoint for alert ingestion.

* Good, because no new dependencies or architectural changes — everything works today.
* Good, because no auth blocker — the current architecture uses API key auth throughout.
* Good, because each mechanism is independently proven and tested.
* Good, because no risk from channel protocol instability — channels are not used.
* Bad, because three separate communication mechanisms means three separate configuration surfaces, three sets of credentials, and three code paths to maintain.
* Bad, because operator communication is one-way for notifications (Apprise) and context-free for inbound (OpenAI chat is stateless, webhook synthesis loses context). No mechanism supports bidirectional conversation with full session context.
* Bad, because the OpenAI chat endpoint requires operators to install and configure a specialized client app.
* Bad, because webhook ingestion requires an LLM synthesis step that adds latency, can fail, and produces lower-quality prompts than direct interpretation would.
* Bad, because as Claude Code's channel ecosystem matures, maintaining custom Go endpoints for chat and webhooks becomes technical debt.

### Webhook-Only Channel

Build a channel plugin that only handles webhook ingestion (HTTP POST events as channel events). Keep Apprise for outbound notifications and the OpenAI chat endpoint for operator inbound.

* Good, because it addresses the weakest part of the current architecture (webhook LLM synthesis) with the smallest change.
* Good, because it avoids the complexity of Telegram/Discord bot integrations in the plugin.
* Good, because Apprise and the OpenAI chat endpoint continue unchanged — minimal disruption.
* Good, because the plugin is simpler: HTTP listener only, no chat platform APIs.
* Bad, because it does not enable bidirectional operator communication — the primary benefit of channels.
* Bad, because the OpenAI chat endpoint and its statelessness limitations remain.
* Bad, because the same `claude.ai` auth blocker applies for channels.
* Bad, because it captures the least value from the channel architecture — webhooks are the simplest of the three communication paths and benefit least from being a channel (the current LLM synthesis approach works adequately).
* Bad, because it still requires maintaining three separate systems for what is fundamentally one operator communication problem.

## More Information

* **Channels docs**: https://code.claude.com/docs/en/channels
* **Channels reference**: https://code.claude.com/docs/en/channels-reference
* **Blocked by**: `claude.ai` auth requirement — channels do not support `ANTHROPIC_API_KEY` auth
* **Partially supersedes**: [ADR-0020](ADR-0020-openai-compatible-chat-endpoint.md) (OpenAI chat endpoint), [ADR-0024](ADR-0024-inbound-webhook-alert-ingestion.md) (Webhook ingestion)
* **Supplements**: [ADR-0004](ADR-0004-apprise-notification-abstraction.md) (Apprise), [ADR-0013](ADR-0013-manual-ad-hoc-session-runs.md) (Ad-hoc sessions)
* **Related**: [ADR-0010](ADR-0010-claude-code-cli-subprocess.md) (CLI subprocess invocation — persistent session model is an architectural shift from single-shot `-p`)
* **SPEC-0033**: The formal specification for this channel plugin lives in `docs/openspec/specs/channel-operator-interface/spec.md`
