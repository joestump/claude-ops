# Claude Ops — Architecture & Security Review Findings

**Date:** 2026-02-11
**Reviewers:** DevOps Engineer, System Architect, Security Researcher

---

## Executive Summary

The architecture is fundamentally sound — the tiered escalation model is a smart cost-optimization strategy, the extension model is elegant, and the prompt structure is well-organized. However, there are significant security and reliability gaps that need addressing before production use.

---

## Critical Findings

### 1. MCP Config Merging From Repos Allows Complete Takeover
**Severity:** CRITICAL | **Flagged by:** Security, Architecture

The `merge_mcp_configs()` function in `entrypoint.sh:32-58` merges `.claude-ops/mcp.json` from mounted repos into the baseline config. Repo configs override baseline servers (jq `$base * $repo`). A mounted repo can replace the `docker` MCP server with a malicious executable, point `postgres` at an attacker-controlled database, or add entirely new MCP servers that run arbitrary code.

**Fix:** Remove MCP config merging entirely, or at minimum prevent overriding baseline servers (use `$repo * $base` instead of `$base * $repo`). Add an allowlist for MCP server commands.

### 2. Tier Boundaries Are Prompt-Only — No Technical Enforcement
**Severity:** CRITICAL | **Flagged by:** Security, Architecture, DevOps

The `--allowedTools "Bash,Read,Grep,Glob,Task,WebFetch"` at `entrypoint.sh:88` gives Tier 1 (Haiku) unrestricted Bash access. It can run `docker restart`, `ansible-playbook`, `rm -rf /`, etc. The entire permission tier system is advisory text in prompt files, not enforced by tooling.

**Fix:** Restrict `--allowedTools` per tier. Tier 1 should not have `Bash` — use `Read,Grep,Glob,Task,WebFetch` plus specific MCP tools. If Tier 1 needs curl/dig, create a restricted wrapper or use the Fetch MCP.

### 3. Credentials Leak Into LLM Context and Logs
**Severity:** CRITICAL | **Flagged by:** Security, DevOps

`CLAUDEOPS_APPRISE_URLS` (which often contains SMTP passwords, API tokens) is passed through `--append-system-prompt "Environment: ${ENV_CONTEXT}"` at `entrypoint.sh:89`. This sends credentials to the Anthropic API on every run and writes them to log files via `tee -a`. The `ANTHROPIC_API_KEY` is also extractable from the process environment.

**Fix:** Do not pass credential-bearing env vars through the LLM system prompt. Use helper scripts that read secrets directly. Mount Apprise URLs as a file instead of env var.

### 4. Docker Socket + Root + Unrestricted Bash = Host Compromise
**Severity:** CRITICAL | **Flagged by:** Security, DevOps

The container runs as root (no `USER` directive in Dockerfile). Docker MCP requires socket access for container management. Combined with unrestricted Bash, a compromised agent can create privileged containers mounting `/`, effectively gaining root on the host. Blast radius includes all containers, all mounted volumes, and lateral movement via SSH keys.

**Fix:** Use a Docker socket proxy (e.g., Tecnativa/docker-socket-proxy) that only allows specific API calls. Add `USER` directive, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`, and resource limits.

### 5. Nothing Watches the Watchdog
**Severity:** CRITICAL | **Flagged by:** Architecture, DevOps

If the entrypoint loop hangs, the API key expires, or Claude crashes mid-remediation, nothing detects it. The `|| true` at `entrypoint.sh:90` swallows all errors silently. No healthcheck, no external monitoring, no failure notifications from the entrypoint itself.

**Fix:** Add Docker healthcheck, signal handling (`trap`), timeout wrapper, circuit breaker (stop after N consecutive failures), and entrypoint-level Apprise notifications independent of the LLM.

---

## High Priority Findings

### 6. Prompt Injection via Mounted Repos
**Flagged by:** Security, Architecture

`CLAUDE-OPS.md` manifests and `.claude-ops/checks/` files are read directly by the LLM. A compromised repo can inject instructions via markdown comments, hidden text, or commands disguised as health checks. The LLM has no way to distinguish legitimate content from injected instructions.

**Fix:** Consider a schema-based manifest format (YAML/JSON) instead of free-form markdown. Add integrity checking (checksums) for repo extension files. Add a system-level preamble marking repo content as untrusted.

### 7. Runtime `npx` Installs Without Version Pinning
**Flagged by:** Security, DevOps

`.claude/mcp.json` uses `npx -y` to install MCP servers at runtime with no version pinning. A compromised npm package runs with root privileges inside the container. Also: Dockerfile has unpinned `node:22-slim`, `apprise`, `claude-code`, and `browserless/chromium`.

**Fix:** Pin all versions. Pre-install MCP servers at Docker build time. Pin base image by digest.

### 8. Chrome DevTools Port Exposed to Host
**Flagged by:** Security, DevOps

`docker-compose.yaml:38` exposes `9222:9222` on all interfaces. Chrome DevTools Protocol has no authentication. Anyone on the network can control the browser, execute JavaScript, and access credentials.

**Fix:** Remove host port mapping. The watchdog reaches Chrome via Docker internal networking (`chrome:9222`).

### 9. No Atomic State Management
**Flagged by:** Architecture, DevOps

`cooldown.json` has no file locking, no atomic writes, no schema validation, and no backup. Concurrent access during tier escalation could corrupt it. The LLM can also be tricked into resetting counters via prompt injection.

**Fix:** Add `flock`-based locking, write-to-temp-then-rename pattern, jq validation before/after each run, and rolling backups.

### 10. Context Loss Between Tiers
**Flagged by:** Architecture

Data passes between tiers only through Task tool prompt strings. No structured format. Token limits could truncate critical diagnostic info with no detection.

**Fix:** Define a structured handoff format. Write tier findings to state files (e.g., `/state/tier1-findings.json`) that subsequent tiers can read.

### 11. Entrypoint Loop Is Fragile
**Flagged by:** DevOps, Architecture

The `while true; do ... sleep $INTERVAL; done` loop has: no signal handling, no timeout on Claude runs, no clock drift compensation (sleep doesn't account for execution time), no circuit breaker for repeated failures.

**Fix:** Add `trap cleanup SIGTERM SIGINT`, `timeout` wrapper, elapsed-time-aware sleep, and a failure counter.

### 12. No Log Rotation
**Flagged by:** DevOps, Architecture

`results/run-TIMESTAMP.log` files accumulate forever. At 24 runs/day, that's 8,760 files/year.

**Fix:** Add `find /results -name "run-*.log" -mtime +30 -delete` to the entrypoint, or configure Docker logging driver.

---

## Medium Priority Findings

### 13. "Never Allowed" List Has No Technical Enforcement
The list in CLAUDE.md is purely prompt-based. Bypassed by any prompt injection or LLM reasoning failure. No command blocklist, no seccomp profile, no AppArmor rules.

### 14. No CI/CD Security Scanning
`.github/workflows/release.yaml` has no Trivy/Grype container scanning, no shellcheck, no hadolint, no SBOM generation.

### 15. No `.dockerignore`
`COPY . .` includes `.git/`, `state/`, `results/`, and other unnecessary files.

### 16. No Resource Limits on Container
No `mem_limit`, `cpus`, or `pids_limit` in `docker-compose.yaml`.

### 17. Extension Conflict Resolution Undefined
Multiple repos providing checks/playbooks for the same service have no documented precedence rules.

### 18. Daily Digest Has No Guaranteed Delivery
If every run escalates during a busy period, the digest is never sent because Tier 1 skips it when issues are found.

### 19. Docker Socket Not Actually Mounted
`docker-compose.yaml` doesn't mount `/var/run/docker.sock`. Docker MCP won't function without it. Needs to be added (ideally behind a socket proxy).

### 20. Cooldown Schema Missing `consecutive_healthy_checks` Field
CLAUDE.md says "reset counters when healthy for 2 consecutive checks" but there's no field tracking this. The LLM must infer it from timestamps.

### 21. No Network Segmentation
The watchdog has unrestricted outbound network access. Should be restricted to: Anthropic API, monitored services, and notification endpoints.

### 22. No Run-Once Mode
No way to trigger a single check for debugging without starting the full loop.

### 23. No Multi-Architecture Builds
CI/CD only builds for amd64. Won't work on ARM hosts.

### 24. MCP Config Merge Validation Missing
If a repo provides malformed JSON in `.claude-ops/mcp.json`, the merge could fail silently.

---

## Implementation Roadmap

### Phase 1 — Quick Wins (hours)
- [ ] Remove `ports: ["9222:9222"]` from Chrome sidecar
- [ ] Stop passing `CLAUDEOPS_APPRISE_URLS` through `--append-system-prompt`
- [ ] Add `.dockerignore`
- [ ] Add `USER` directive to Dockerfile
- [ ] Pin dependency versions in Dockerfile

### Phase 2 — Security Hardening (days)
- [ ] Remove or restrict MCP config merging from repos
- [ ] Restrict `--allowedTools` per tier
- [ ] Use Docker socket proxy instead of raw socket mount
- [ ] Add `cap_drop: [ALL]`, `security_opt`, resource limits
- [ ] Pre-install MCP servers at build time

### Phase 3 — Reliability Hardening (days)
- [ ] Add signal handling, timeout wrapper, circuit breaker to entrypoint
- [ ] Add cooldown.json validation, locking, and backup
- [ ] Add entrypoint-level Apprise notifications
- [ ] Add Docker healthcheck
- [ ] Add log rotation

### Phase 4 — Architecture Improvements (week)
- [ ] Structured inter-tier handoff via state files
- [ ] `consecutive_healthy_checks` field in cooldown schema
- [ ] Pending-action pattern for crash recovery
- [ ] Network segmentation
- [ ] Container image scanning in CI/CD
- [ ] Multi-architecture builds

---

## Failure Mode Catalog

| Failure Mode | Impact | Current Mitigation | Gap |
|---|---|---|---|
| Claude CLI crashes | Run skipped | `\|\| true` catches it | No notification sent |
| API key invalid/expired | All runs fail | None | No detection, burns loop iterations |
| cooldown.json corrupted | Cooldown bypassed | Init on missing | No recovery from malformed JSON |
| State dir disk full | Writes fail | None | No disk space monitoring |
| Overlapping runs | State corruption | None | No lock file |
| MCP server fails to start | Reduced capability | None | No fallback or notification |
| Network partition | All checks fail | None | Could trigger unnecessary escalation |
| Prompt injection via repo | Arbitrary code execution | `:ro` mount | No content validation |
| Mid-remediation crash | Inconsistent state | None | No pending-action tracking |
| Chrome sidecar down | Browser automation fails | None | No health check on sidecar |
