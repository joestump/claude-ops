#!/usr/bin/env bash
# Governing: ADR-0029 (hooks lifecycle guardrails), SPEC-0030 REQ-5
# SessionStart hook — injects dynamic context (cooldown state, recent events,
# host connectivity) into the Claude session at startup. Output goes to stdout
# and is added to Claude's context automatically.
set -uo pipefail

STATE_DIR="${CLAUDEOPS_STATE_DIR:-/state}"
DB_PATH="${STATE_DIR}/claudeops.db"
COOLDOWN_FILE="${STATE_DIR}/cooldown.json"

echo "=== Claude Ops Session Context (auto-injected) ==="
echo ""

# --- Cooldown State ---
echo "Cooldown State:"
if [ -f "$COOLDOWN_FILE" ] && command -v jq &>/dev/null; then
  jq -r '
    .services // {} | to_entries[] |
    "\(.key): " +
    ((.value.restart_timestamps // []) | length | tostring) + "/2 restarts (4h), " +
    ((.value.redeployment_timestamps // []) | length | tostring) + "/1 redeployments (24h)"
  ' "$COOLDOWN_FILE" 2>/dev/null | while read -r line; do
    echo "  $line"
  done
  # If no services, say so.
  SERVICE_COUNT=$(jq '.services // {} | length' "$COOLDOWN_FILE" 2>/dev/null || echo "0")
  if [ "$SERVICE_COUNT" = "0" ]; then
    echo "  No services tracked yet."
  fi
else
  echo "  No cooldown data available."
fi
echo ""

# --- Recent Events ---
echo "Recent Events (last 10):"
if [ -f "$DB_PATH" ] && command -v sqlite3 &>/dev/null; then
  EVENTS=$(sqlite3 -separator ' | ' "$DB_PATH" \
    "SELECT created_at, level, COALESCE(service, '-'), message
     FROM events ORDER BY created_at DESC LIMIT 10;" 2>/dev/null)
  if [ -n "$EVENTS" ]; then
    echo "$EVENTS" | while read -r line; do
      echo "  $line"
    done
  else
    echo "  No events recorded yet."
  fi
else
  echo "  No event data available."
fi
echo ""

# --- Host Connectivity ---
echo "Host Connectivity:"
declare -A HOSTS=(
  ["ie01"]="192.168.100.210"
  ["pie01"]="192.168.100.220"
  ["pie02"]="192.168.100.221"
  ["int01"]="192.168.5.30"
)

for host in ie01 pie01 pie02 int01; do
  ip="${HOSTS[$host]}"
  if ping -c1 -W2 "$ip" &>/dev/null; then
    echo "  $host ($ip): reachable"
  else
    echo "  $host ($ip): unreachable"
  fi
done
echo ""

echo "=== End Session Context ==="
