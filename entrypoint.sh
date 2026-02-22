#!/bin/bash
set -euo pipefail

INTERVAL="${CLAUDEOPS_INTERVAL:-3600}"
# Governing: SPEC-0010 REQ-4 — Prompt Loading via --prompt-file
PROMPT_FILE="${CLAUDEOPS_PROMPT:-/app/prompts/tier1-observe.md}"
# Governing: SPEC-0001 REQ-1 (Three-Tier Model Hierarchy), REQ-2 (Configurable Model Selection)
# Governing: SPEC-0010 REQ-3 — Model Selection via --model Flag
MODEL="${CLAUDEOPS_TIER1_MODEL:-haiku}"
STATE_DIR="${CLAUDEOPS_STATE_DIR:-/state}"
RESULTS_DIR="${CLAUDEOPS_RESULTS_DIR:-/results}"
REPOS_DIR="${CLAUDEOPS_REPOS_DIR:-/repos}"
# Governing: SPEC-0003 REQ-6 (Tool-Level Enforcement via --allowedTools),
#            SPEC-0003 REQ-11 (Permission Modification Without Rebuild),
#            SPEC-0010 REQ-5 "Tool filtering via --allowedTools",
#            ADR-0023 (AllowedTools-Based Tier Enforcement)
# — restricts available tools at the CLI runtime level, providing defense-in-depth
# for the permission tier model. Configurable via env var — changes take effect on next container restart (REQ-11).
ALLOWED_TOOLS="${CLAUDEOPS_ALLOWED_TOOLS:-Bash,Read,Grep,Glob,Task,WebFetch}"
# Governing: ADR-0023 (AllowedTools-Based Tier Enforcement), SPEC-0010 REQ-5
# Default to Tier 1 blocklist (most restrictive)
DISALLOWED_TOOLS="${CLAUDEOPS_DISALLOWED_TOOLS:-Bash(docker restart:*),Bash(docker stop:*),Bash(docker start:*),Bash(docker rm:*),Bash(docker compose:*),Bash(ansible:*),Bash(ansible-playbook:*),Bash(helm:*),Bash(gh pr create:*),Bash(gh pr merge:*),Bash(tea pr create:*),Bash(git push:*),Bash(git commit:*),Bash(systemctl restart:*),Bash(systemctl stop:*),Bash(systemctl start:*),Bash(apprise:*)}"
CLAUDEOPS_TIER="${CLAUDEOPS_TIER:-1}"
DRY_RUN="${CLAUDEOPS_DRY_RUN:-false}"
MCP_CONFIG="/app/.claude/mcp.json"

echo "Claude Ops starting"
echo "  Tier 1 model: ${MODEL}"
echo "  Interval: ${INTERVAL}s"
echo "  Prompt: ${PROMPT_FILE}"
echo "  State: ${STATE_DIR}"
echo "  Results: ${RESULTS_DIR}"
echo "  Repos: ${REPOS_DIR}"
echo "  Tier: ${CLAUDEOPS_TIER}"
echo "  Dry run: ${DRY_RUN}"
echo ""

# Governing: SPEC-0007 REQ-1 (state file at $CLAUDEOPS_STATE_DIR/cooldown.json),
#            SPEC-0007 REQ-2 (initialize if missing, never overwrite existing)
# Ensure state file exists
if [ ! -f "${STATE_DIR}/cooldown.json" ]; then
    echo '{"services":{},"last_run":null,"last_daily_digest":null}' > "${STATE_DIR}/cooldown.json"
fi

# Governing: SPEC-0010 REQ-8 (MCP Server Configuration — merge repo configs into baseline before each CLI invocation)
# Merge MCP configs from mounted repos into the baseline config.
# Each repo can provide .claude-ops/mcp.json with additional MCP servers.
# These are merged (repo configs added to baseline) before each run.
merge_mcp_configs() {
    local baseline="/app/.claude/mcp.json.baseline"

    # Save baseline on first run
    if [ ! -f "$baseline" ]; then
        cp "$MCP_CONFIG" "$baseline"
    fi

    # Start from baseline
    cp "$baseline" "$MCP_CONFIG"

    # Find and merge all repo-level MCP configs
    for repo_mcp in "${REPOS_DIR}"/*/.claude-ops/mcp.json; do
        [ -f "$repo_mcp" ] || continue
        repo_name=$(basename "$(dirname "$(dirname "$repo_mcp")")")
        echo "  Merging MCP config from ${repo_name}"

        # Merge mcpServers objects: repo configs are added to baseline.
        # If a repo defines a server with the same name as a baseline server,
        # the repo version wins (allows overriding).
        merged=$(jq -s '.[0].mcpServers as $base |
            .[1].mcpServers as $repo |
            .[0] | .mcpServers = ($base * $repo)' \
            "$MCP_CONFIG" "$repo_mcp")
        echo "$merged" > "$MCP_CONFIG"
    done
}

while true; do
    RUN_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    LOG_FILE="${RESULTS_DIR}/run-$(date +%Y%m%d-%H%M%S).log"

    # Governing: SPEC-0003 REQ-10 — Post-Hoc Auditability
    # All agent output is logged to timestamped files in $RESULTS_DIR.
    # This captures tool calls, check results, remediation actions, and
    # cooldown state changes for post-hoc compliance and incident review.
    echo "[${RUN_START}] Starting health check run..."
    echo "--- Run metadata ---" >> "${LOG_FILE}"
    echo "timestamp: ${RUN_START}" >> "${LOG_FILE}"
    echo "tier: ${CLAUDEOPS_TIER}" >> "${LOG_FILE}"
    echo "model: ${MODEL}" >> "${LOG_FILE}"
    echo "dry_run: ${DRY_RUN}" >> "${LOG_FILE}"
    echo "---" >> "${LOG_FILE}"

    # Merge repo MCP configs before each run
    echo "Merging MCP configurations..."
    merge_mcp_configs

    # Build environment context for Claude
    ENV_CONTEXT="CLAUDEOPS_TIER=${CLAUDEOPS_TIER}"
    ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_DRY_RUN=${DRY_RUN}"
    ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_STATE_DIR=${STATE_DIR}"
    ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_RESULTS_DIR=${RESULTS_DIR}"
    ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_REPOS_DIR=${REPOS_DIR}"
    # Governing: SPEC-0001 REQ-1 (Three-Tier Model Hierarchy), REQ-2 (Configurable Model Selection)
    # Governing: SPEC-0010 REQ-3 — Tier model defaults passed to agent for subagent spawning
    ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_TIER2_MODEL=${CLAUDEOPS_TIER2_MODEL:-sonnet}"
    ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_TIER3_MODEL=${CLAUDEOPS_TIER3_MODEL:-opus}"

    # Governing: SPEC-0004 REQ-1 (single env var config),
    #            SPEC-0004 REQ-2 (graceful degradation — only pass when set),
    #            SPEC-0004 REQ-4 (env var passthrough to agent context)
    # Pass Apprise URLs if configured; skip silently when unset (REQ-2).
    if [ -n "${CLAUDEOPS_APPRISE_URLS:-}" ]; then
        ENV_CONTEXT="${ENV_CONTEXT} CLAUDEOPS_APPRISE_URLS=${CLAUDEOPS_APPRISE_URLS}"
    fi

    # Governing: SPEC-0003 REQ-6 (--allowedTools hard boundary),
    #            SPEC-0003 REQ-11 (prompt read at runtime — changes take effect next cycle),
    #            SPEC-0010 REQ-2 (Subprocess Invocation from Bash — CLI flags for all config, non-interactive, piped output)
    # Run Claude with tier 1 prompt
    # Governing: SPEC-0010 REQ-3 (--model), REQ-4 (--prompt-file)
    # Governing: SPEC-0010 REQ-5 — --allowedTools and --disallowedTools enforce
    # tool filtering at CLI runtime, independent of prompt-level instructions.
    claude \
        --model "${MODEL}" \
        -p "$(cat "${PROMPT_FILE}")" \
        --allowedTools "${ALLOWED_TOOLS}" \
        --disallowedTools "${DISALLOWED_TOOLS}" \
        --append-system-prompt "Environment: ${ENV_CONTEXT}" \
        2>&1 | tee -a "${LOG_FILE}" || true  # Governing: SPEC-0010 REQ-10 (Error Handling — || true prevents set -e from terminating loop)

    RUN_END=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    echo "[${RUN_END}] Run complete. Log: ${LOG_FILE}" | tee -a "${LOG_FILE}"
    echo "[${RUN_END}] Sleeping ${INTERVAL}s..."
    sleep "${INTERVAL}"
done
