---
sidebar_position: 8
---

# Browser Automation

Claude Ops can interact with web-based admin UIs through headless browser automation. This enables operations like credential rotation, configuration changes, and API key extraction on services that lack REST APIs for those functions.

## Overview

Browser automation uses a headless Chromium sidecar (`browserless/chromium`) connected via the Chrome DevTools MCP. The agent controls the browser through standard DevTools Protocol actions: navigating pages, taking snapshots, filling forms, clicking elements.

Browser automation is gated to **Tier 2 and above** — the Tier 1 observe agent cannot perform authenticated browser actions. Tier 1 may check that a login page loads (unauthenticated navigation only).

### When Browser Automation Is Used

Browser automation is invoked during Tier 2 (Sonnet) and Tier 3 (Opus) sessions when:

- A service requires API key rotation and the only way to obtain or reset the key is through the provider's web dashboard
- A health check indicates a configuration issue that can only be corrected via the service's web UI
- A playbook (e.g., `playbooks/rotate-api-key.md`) calls for browser-based login and interaction

## Prerequisites

1. Docker Compose with the `browser` profile enabled
2. At least one `BROWSER_CRED_*` environment variable for each service
3. `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS` set with the service origins
4. Chrome DevTools MCP server configured (included in the base MCP config)

## Setup

### 1. Start the Chrome sidecar

The Chrome sidecar runs under the `browser` Docker Compose profile. Start it alongside the main service:

```bash
docker compose --profile browser up -d
```

This launches two containers:
- `claude-ops` — the main watchdog
- `claude-ops-chrome` — headless Chromium on port 9222

### 2. Configure allowed origins

Set `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS` to a comma-separated list of origins the agent is permitted to visit. Only these origins will be accessible through browser automation.

```env
# .env
CLAUDEOPS_BROWSER_ALLOWED_ORIGINS=https://sonarr.example.com,https://prowlarr.example.com,https://radarr.example.com
```

Origins must be in the format `scheme://hostname[:port]`. If this variable is empty or unset, browser automation is disabled entirely.

### 3. Configure service credentials

Credentials follow the naming convention `BROWSER_CRED_{SERVICE}_{FIELD}`, where:
- `{SERVICE}` is an uppercase identifier for the service (e.g., `SONARR`, `PROWLARR`)
- `{FIELD}` is the credential type: `USER`, `PASS`, `TOKEN`, or `API_KEY`

```env
# .env
BROWSER_CRED_SONARR_USER=admin
BROWSER_CRED_SONARR_PASS=your-sonarr-password
BROWSER_CRED_PROWLARR_USER=admin
BROWSER_CRED_PROWLARR_PASS=your-prowlarr-password
```

### Chrome Sidecar Settings

The Chrome sidecar container accepts configuration via environment variables in `docker-compose.yaml`:

| Variable | Default | Description |
|----------|---------|-------------|
| `CONNECTION_TIMEOUT` | `120000` | Maximum time (ms) for a browser connection before timeout |
| `CHROME_FLAGS` | `--incognito` | Chrome launch flags. `--incognito` ensures session isolation |

### 4. Pass credentials through Docker Compose

Add each credential variable to the `watchdog` service environment in `docker-compose.yaml`:

```yaml
services:
  watchdog:
    environment:
      # ... existing vars ...
      - BROWSER_CRED_SONARR_USER=${BROWSER_CRED_SONARR_USER}
      - BROWSER_CRED_SONARR_PASS=${BROWSER_CRED_SONARR_PASS}
      - BROWSER_CRED_PROWLARR_USER=${BROWSER_CRED_PROWLARR_USER}
      - BROWSER_CRED_PROWLARR_PASS=${BROWSER_CRED_PROWLARR_PASS}
```

## How It Works

### Credential injection

The agent never sees raw credential values. When it needs to log into a service, it references credentials by their environment variable names:

```
fill(uid="login-user", value="$BROWSER_CRED_SONARR_USER")
fill(uid="login-pass", value="$BROWSER_CRED_SONARR_PASS")
```

The credential resolver intercepts these references and:

1. Verifies the session is Tier 2 or higher (denies Tier 1 requests)
2. Validates the key starts with `BROWSER_CRED_`
3. Looks up the actual value from the environment
4. Returns the resolved value to the Chrome DevTools MCP, which injects it into the form field

The LLM's prompt, reasoning, and output never contain the actual credential value. Resolution happens at the MCP tool execution layer, below the LLM token generation layer.

### URL allowlist

Before each page navigation, a JavaScript init script is generated from `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS` and injected into every page. The init script checks `window.location.origin` against the allowlist. If the origin is not allowed, it replaces the page content with a "Navigation Blocked" message and calls `window.stop()`. This also catches client-side redirects to disallowed origins.

:::note
This is JavaScript-level enforcement, not network-level. For stronger enforcement, consider Docker network policies.
:::

### Log redaction

The redaction filter scans all session output for known credential values and replaces them with `[REDACTED:BROWSER_CRED_{SERVICE}_{FIELD}]` placeholders. Redaction applies to:

- **Session logs** — every line of the Claude CLI's NDJSON stream output, before parsing
- **SSE activity stream** — real-time dashboard output
- **Session response** — the final markdown summary stored in the database
- **Apprise notifications** — notification message bodies

Both raw values and URL-encoded variants are redacted.

### Browser context isolation

Each browser automation task uses Chromium's incognito mode (`--incognito` flag on the sidecar). The agent opens a new page for each service and closes it when done. Cookies, local storage, and session tokens from one service do not leak to another.

## Security Model

Browser automation has four independent security layers:

| Layer | Purpose | Mechanism |
|-------|---------|-----------|
| Credential injection | Agent never sees raw values | Env var resolution at MCP tool level |
| URL allowlist | Agent can only visit pre-approved origins | JavaScript init script blocks disallowed origins |
| Log redaction | Credential values stripped from all output | Pattern replacement on all output channels |
| Context isolation | No cross-service session leakage | Incognito mode, page-per-service lifecycle |

### Tier gating

| Tier | Allowed |
|------|---------|
| Tier 1 (Haiku) | Unauthenticated navigation only. Must NOT fill forms or use credential references. |
| Tier 2 (Sonnet) | Full authenticated browser automation against allowed origins. |
| Tier 3 (Opus) | Same browser permissions as Tier 2. |

Tier gating is enforced at both the prompt level and in code — both must pass for credential injection to proceed.

## Complete Example

### .env

```env
ANTHROPIC_API_KEY=sk-ant-...

CLAUDEOPS_INTERVAL=3600

CLAUDEOPS_BROWSER_ALLOWED_ORIGINS=https://sonarr.example.com,https://prowlarr.example.com

BROWSER_CRED_SONARR_USER=admin
BROWSER_CRED_SONARR_PASS=your-sonarr-password

BROWSER_CRED_PROWLARR_USER=admin
BROWSER_CRED_PROWLARR_PASS=your-prowlarr-password
```

### docker-compose.yaml additions

```yaml
services:
  watchdog:
    environment:
      # ... standard vars ...
      - CLAUDEOPS_BROWSER_ALLOWED_ORIGINS=${CLAUDEOPS_BROWSER_ALLOWED_ORIGINS:-}
      - BROWSER_CRED_SONARR_USER=${BROWSER_CRED_SONARR_USER}
      - BROWSER_CRED_SONARR_PASS=${BROWSER_CRED_SONARR_PASS}
      - BROWSER_CRED_PROWLARR_USER=${BROWSER_CRED_PROWLARR_USER}
      - BROWSER_CRED_PROWLARR_PASS=${BROWSER_CRED_PROWLARR_PASS}
```

Then start with the browser profile:

```bash
docker compose --profile browser up -d
```

## Troubleshooting

### "Navigation Blocked" errors

**Cause**: The target URL's origin is not in `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS`.

**Fix**: Add the origin (with scheme and any non-standard port) to your `.env` file and restart the container.

### "credential not set" errors

**Cause**: The referenced `BROWSER_CRED_*` env var is not set or is empty.

**Fix**: Verify the variable is in `.env` with the correct naming convention (`BROWSER_CRED_{SERVICE}_{FIELD}`) and is passed through in `docker-compose.yaml`. Restart the container after updating.

### "browser credential injection requires Tier 2+" errors

**Cause**: Expected behavior — Tier 1 cannot perform authenticated browser actions. If a service requires browser-based investigation, Tier 1 should escalate to Tier 2 via the handoff file.

**Fix**: Ensure `CLAUDEOPS_MAX_TIER` is set to at least `2` so escalation can proceed.

### Chrome sidecar connection issues

1. **Chrome sidecar not running**: Start with `docker compose --profile browser up -d` and verify with `docker compose --profile browser ps`.
2. **Network connectivity**: The watchdog and chrome containers must be on the same Docker network (handled automatically by the default compose file).
3. **Connection timeout**: Increase `CONNECTION_TIMEOUT` in the chrome service environment (default: `120000` ms).
4. **Port conflict**: If port 9222 is already in use, change the host port mapping in `docker-compose.yaml`.

### Browser automation silently disabled

**Cause**: `CLAUDEOPS_BROWSER_ALLOWED_ORIGINS` is empty or unset.

**Fix**: Set it to a non-empty comma-separated list of origins in your `.env` file.
