#!/usr/bin/env bash
# Governing: ADR-0029 (hooks lifecycle guardrails), SPEC-0030 REQ-6
# Notification hook — bridges Claude Code notification events to Apprise CLI.
# Gracefully skips if Apprise is not configured (CLAUDEOPS_APPRISE_URLS unset).
# Always exits 0 — notification failure must never block the session.
set -uo pipefail

# If Apprise URLs not configured, skip silently.
if [ -z "${CLAUDEOPS_APPRISE_URLS:-}" ]; then
  exit 0
fi

# If apprise CLI not available, skip.
if ! command -v apprise &>/dev/null; then
  echo "notify-apprise: apprise CLI not found" >&2
  exit 0
fi

# Parse notification event from stdin.
INPUT=$(cat)

# Extract notification details if available.
TITLE="Claude Ops Notification"
BODY=""

if command -v jq &>/dev/null; then
  BODY=$(echo "$INPUT" | jq -r '.message // .body // .text // "Claude Code needs your attention"' 2>/dev/null)
else
  BODY="Claude Code needs your attention"
fi

# Send notification via Apprise. Failure is non-fatal.
# Governing: ADR-0025 — use --input-format markdown for rich rendering.
apprise -t "$TITLE" -b "$BODY" --input-format markdown "$CLAUDEOPS_APPRISE_URLS" 2>/dev/null || {
  echo "notify-apprise: apprise delivery failed (non-fatal)" >&2
}

exit 0
