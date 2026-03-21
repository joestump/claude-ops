#!/usr/bin/env bash
# Governing: ADR-0029 (hooks lifecycle guardrails), SPEC-0030 REQ-2
# PreToolUse hook — enforces cooldown limits before restart/redeployment commands.
# Reads cooldown.json and blocks commands that exceed limits:
#   - 2 restarts per service per 4 hours
#   - 1 redeployment per service per 24 hours
# Exit 0 = allow, Exit 2 = deny with reason on stderr.
set -euo pipefail

# Fail-open: if jq is missing, allow the command.
if ! command -v jq &>/dev/null; then
  echo "cooldown-check: jq not found, allowing command" >&2
  exit 0
fi

INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)

# If we can't parse the command, fail-open.
if [ -z "$COMMAND" ]; then
  exit 0
fi

# Pattern match against restart/redeployment commands.
ACTION_TYPE=""
SERVICE=""

case "$COMMAND" in
  docker\ restart\ *)
    ACTION_TYPE="restart"
    SERVICE=$(echo "$COMMAND" | awk '{print $3}')
    ;;
  docker\ stop\ *)
    ACTION_TYPE="restart"
    SERVICE=$(echo "$COMMAND" | awk '{print $3}')
    ;;
  docker\ start\ *)
    ACTION_TYPE="restart"
    SERVICE=$(echo "$COMMAND" | awk '{print $3}')
    ;;
  docker\ compose\ restart\ *)
    ACTION_TYPE="restart"
    SERVICE=$(echo "$COMMAND" | awk '{print $4}')
    ;;
  docker\ compose\ up\ *)
    ACTION_TYPE="restart"
    # Try to extract service name from the end of the command.
    SERVICE=$(echo "$COMMAND" | sed -n 's/.*docker compose up[^[:alnum:]]*//p' | awk '{print $NF}')
    ;;
  ansible-playbook\ *)
    ACTION_TYPE="redeployment"
    # Extract service name from playbook filename (e.g., redeploy-jellyfin.yml → jellyfin).
    SERVICE=$(echo "$COMMAND" | sed -n 's/.*redeploy-\([^.]*\).*/\1/p')
    ;;
  helm\ upgrade\ *)
    ACTION_TYPE="redeployment"
    SERVICE=$(echo "$COMMAND" | awk '{print $3}')
    ;;
  *)
    # No pattern match — not a cooldown-relevant command. Allow.
    exit 0
    ;;
esac

# If we couldn't determine the service, fail-open.
if [ -z "$SERVICE" ]; then
  exit 0
fi

# Read cooldown state.
STATE_DIR="${CLAUDEOPS_STATE_DIR:-/state}"
COOLDOWN_FILE="${STATE_DIR}/cooldown.json"

if [ ! -f "$COOLDOWN_FILE" ]; then
  echo "cooldown-check: cooldown.json not found at $COOLDOWN_FILE, allowing" >&2
  exit 0
fi

NOW=$(date -u +%s)

if [ "$ACTION_TYPE" = "restart" ]; then
  # Check restart limit: 2 per service per 4 hours (14400 seconds).
  WINDOW_START=$((NOW - 14400))
  COUNT=$(jq -r --arg svc "$SERVICE" --argjson ws "$WINDOW_START" '
    .services[$svc].restart_timestamps // [] |
    map(. | sub("\\.[0-9]+"; "") | strptime("%Y-%m-%dT%H:%M:%SZ") | mktime) |
    map(select(. >= $ws)) |
    length
  ' "$COOLDOWN_FILE" 2>/dev/null || echo "0")

  if [ "$COUNT" -ge 2 ]; then
    EARLIEST=$(jq -r --arg svc "$SERVICE" '
      .services[$svc].restart_timestamps // [] | sort | first // "unknown"
    ' "$COOLDOWN_FILE" 2>/dev/null)
    echo "Cooldown limit exceeded for $SERVICE: $COUNT/2 restarts in last 4h. Earliest restart: $EARLIEST" >&2
    exit 2
  fi

elif [ "$ACTION_TYPE" = "redeployment" ]; then
  # Check redeployment limit: 1 per service per 24 hours (86400 seconds).
  WINDOW_START=$((NOW - 86400))
  COUNT=$(jq -r --arg svc "$SERVICE" --argjson ws "$WINDOW_START" '
    .services[$svc].redeployment_timestamps // [] |
    map(. | sub("\\.[0-9]+"; "") | strptime("%Y-%m-%dT%H:%M:%SZ") | mktime) |
    map(select(. >= $ws)) |
    length
  ' "$COOLDOWN_FILE" 2>/dev/null || echo "0")

  if [ "$COUNT" -ge 1 ]; then
    EARLIEST=$(jq -r --arg svc "$SERVICE" '
      .services[$svc].redeployment_timestamps // [] | sort | first // "unknown"
    ' "$COOLDOWN_FILE" 2>/dev/null)
    echo "Cooldown limit exceeded for $SERVICE: $COUNT/1 redeployments in last 24h. Earliest redeployment: $EARLIEST" >&2
    exit 2
  fi
fi

# Within limits — allow.
exit 0
