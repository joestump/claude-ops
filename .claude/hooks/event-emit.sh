#!/usr/bin/env bash
# Governing: ADR-0029 (hooks lifecycle guardrails), SPEC-0030 REQ-3
# PostToolUse hook — emits events to the SQLite events table after significant
# infrastructure actions. Detects action type from the Bash command and inserts
# a structured event with appropriate level and service tag.
# Always exits 0 — PostToolUse hooks must never block.
set -uo pipefail

# Fail-open: if jq or sqlite3 are missing, skip silently.
if ! command -v jq &>/dev/null || ! command -v sqlite3 &>/dev/null; then
  exit 0
fi

INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // empty' 2>/dev/null)

if [ -z "$COMMAND" ]; then
  exit 0
fi

# Pattern match against significant action categories.
LEVEL=""
SERVICE=""
MESSAGE=""

case "$COMMAND" in
  docker\ restart\ *)
    LEVEL="warning"
    SERVICE=$(echo "$COMMAND" | awk '{print $3}')
    MESSAGE="Container restarted: $COMMAND"
    ;;
  docker\ compose\ up\ *)
    LEVEL="warning"
    SERVICE=$(echo "$COMMAND" | sed -n 's/.*docker compose up[^[:alnum:]]*//p' | awk '{print $NF}')
    MESSAGE="Compose service started: $COMMAND"
    ;;
  docker\ compose\ restart\ *)
    LEVEL="warning"
    SERVICE=$(echo "$COMMAND" | awk '{print $4}')
    MESSAGE="Compose service restarted: $COMMAND"
    ;;
  docker\ compose\ down\ *)
    LEVEL="critical"
    SERVICE=$(echo "$COMMAND" | awk '{print $4}')
    MESSAGE="Compose service stopped: $COMMAND"
    ;;
  ansible-playbook\ *)
    LEVEL="warning"
    SERVICE=$(echo "$COMMAND" | sed -n 's/.*redeploy-\([^.]*\).*/\1/p')
    MESSAGE="Ansible playbook executed: $COMMAND"
    ;;
  helm\ upgrade\ *)
    LEVEL="warning"
    SERVICE=$(echo "$COMMAND" | awk '{print $3}')
    MESSAGE="Helm upgrade: $COMMAND"
    ;;
  gh\ pr\ create*|tea\ pr\ create*)
    LEVEL="info"
    MESSAGE="Pull request created: $COMMAND"
    ;;
  apprise\ *)
    LEVEL="info"
    MESSAGE="Notification sent via Apprise"
    ;;
  *)
    # Not a significant action — skip.
    exit 0
    ;;
esac

# Insert event into SQLite database.
STATE_DIR="${CLAUDEOPS_STATE_DIR:-/state}"
DB_PATH="${STATE_DIR}/claudeops.db"

if [ ! -f "$DB_PATH" ]; then
  echo "event-emit: database not found at $DB_PATH" >&2
  exit 0
fi

# Escape single quotes in message for SQL.
SAFE_MESSAGE=$(echo "$MESSAGE" | sed "s/'/''/g")
SAFE_SERVICE="NULL"
if [ -n "$SERVICE" ]; then
  SAFE_SERVICE="'$(echo "$SERVICE" | sed "s/'/''/g")'"
fi

# Use the internal session ID if available, otherwise NULL.
SESSION_REF="NULL"
if [ -n "$SESSION_ID" ]; then
  SESSION_REF="'$SESSION_ID'"
fi

sqlite3 "$DB_PATH" "INSERT INTO events (session_id, level, service, message, created_at)
  VALUES ($SESSION_REF, '$LEVEL', $SAFE_SERVICE, '$SAFE_MESSAGE', datetime('now'));" 2>/dev/null || true

exit 0
