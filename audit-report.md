## Design Audit Report

Scope: Full project
Analyzed: 20 ADRs, 20 specs, 42 source files (8 packages)
Total findings: 18 (1 critical, 10 warning, 7 info)

---

### Code vs. Specification Drift

| Severity | Finding | Spec | Location |
|----------|---------|------|----------|
| [CRITICAL] | Handoff struct missing `schema_version` field. SPEC-0016-REQ-1 requires `schema_version` set to `1` in every handoff file. The `Handoff` struct has no such field, and `ValidateHandoff()` does not check for it. Handoff files written by agents will lack this field, and unrecognized versions cannot be rejected as required. | SPEC-0016 | internal/session/handoff.go:14-21 |
| [WARNING] | `create_pr` tool gated behind `CLAUDEOPS_PR_ENABLED` env var. SPEC-0019-REQ-2 requires `tools/list` to return exactly three tools (`create_pr`, `list_prs`, `get_pr_status`). The implementation conditionally omits `create_pr` when `CLAUDEOPS_PR_ENABLED` is not `"true"`, meaning tool discovery returns only 2 tools by default. The spec says server-side tier enforcement is sufficient; this additional gate is not specified. | SPEC-0019 | internal/mcpserver/server.go:60-67 |

### Code vs. ADR Drift

No findings. All accepted ADRs (ADR-0001 through ADR-0010) have implementations consistent with their decisions.

### ADR vs. Spec Inconsistencies

No findings. Reviewed all ADR-spec pairings (ADR-0011/SPEC-0011 through ADR-0019/SPEC-0019). Decisions in ADRs align with requirements in their corresponding specs.

### Coverage Gaps

| Severity | Area | Description |
|----------|------|-------------|
| [INFO] | internal/hub/ | SSE (Server-Sent Events) hub package for real-time streaming. Referenced in SPEC-0013 scenarios but the hub itself is not specified -- it is an implementation detail without formal requirements. |
| [INFO] | internal/config/ | Runtime configuration loading package. No spec governs config loading, validation, or env var precedence. |
| [INFO] | internal/mcp/merge.go | MCP config merging from mounted repos. Described in CLAUDE.md and ADR-0006, but the merge algorithm has no formal spec with testable scenarios. |
| [INFO] | cmd/claudeops/ | Main entrypoint, Cobra commands, and CLI argument parsing. No spec governs the CLI interface, subcommands, or flags. |
| [INFO] | checks/*.md, playbooks/*.md, skills/*.md | All markdown-based checks, playbooks, and skills lack formal specs. They are the core operational content but are governed only by CLAUDE.md prose, not testable SPEC requirements. |
| [INFO] | prompts/tier*.md | Tier prompt files. Referenced by multiple specs (SPEC-0013, SPEC-0015) for integration requirements but have no dedicated spec governing their structure or required sections. |

### Stale Artifacts

| Severity | Artifact | Issue |
|----------|----------|-------|
| [WARNING] | ADR-0011 | Status is `proposed` but session CLI output is fully implemented in `internal/session/manager.go` (stream-json parsing, HTMX streaming). Should be `accepted`. |
| [WARNING] | ADR-0013 | Status is `proposed` but manual ad-hoc sessions are fully implemented (`POST /sessions/trigger`, API endpoint, dashboard trigger button). Should be `accepted`. |
| [WARNING] | ADR-0014 | Status is `proposed` but realtime dashboard events system is fully implemented (events table, SSE hub, HTMX polling, event marker parsing). Should be `accepted`. |
| [WARNING] | ADR-0015 | Status is `proposed` but persistent agent memory is fully implemented (memories table, marker parsing, `buildMemoryContext()`, dashboard CRUD, reinforcement logic). Should be `accepted`. |
| [WARNING] | ADR-0016 | Status is `proposed` but session-based escalation with structured handoff is fully implemented (`internal/session/handoff.go`, `runEscalationChain` in manager.go, DB schema with `parent_session_id`). Should be `accepted`. |
| [WARNING] | ADR-0017 | Status is `proposed` but REST API is fully implemented (`/api/v1/*` routes in `internal/web/server.go`, `api/openapi.yaml`, Swagger UI). Should be `accepted`. |
| [WARNING] | ADR-0018 | Status is `proposed` but PR-based config changes are implemented (`internal/gitprovider/` package with provider interface, scope validation, tier gating, GitHub and Gitea providers). Should be `accepted`. |
| [WARNING] | ADR-0012 | Status is `proposed` but browser automation is fully implemented in `internal/session/browser.go` and `internal/session/redaction.go`, governed by SPEC-0014. Should be `accepted`. |
| [WARNING] | ADR-0019 | Status is `proposed` but MCP gitprovider is fully implemented in `internal/mcpserver/server.go` and `internal/mcpserver/tools.go`. Should be `accepted`. |

### Policy Violations

| Severity | Finding | Source | Location |
|----------|---------|--------|----------|
| [INFO] | CLAUDE.md states spec numbering is `RFC-XXXX` but all actual specs use `SPEC-XXXX`. The CLAUDE.md instruction contradicts the design plugin's spec template and every existing spec file. Future spec creation guided by CLAUDE.md would use the wrong prefix. | CLAUDE.md | CLAUDE.md:265 |

---

### Summary

| Category | Critical | Warning | Info | Total |
|----------|----------|---------|------|-------|
| Code vs. Spec | 1 | 1 | 0 | 2 |
| Code vs. ADR | 0 | 0 | 0 | 0 |
| ADR vs. Spec | 0 | 0 | 0 | 0 |
| Coverage Gaps | 0 | 0 | 6 | 6 |
| Stale Artifacts | 0 | 9 | 0 | 9 |
| Policy Violations | 0 | 0 | 1 | 1 |
| **Total** | **1** | **10** | **7** | **18** |

### Recommended Actions

1. [CRITICAL] Add `schema_version` field to the `Handoff` struct in `internal/session/handoff.go` and add validation in `ValidateHandoff()` to reject unrecognized versions, per SPEC-0016-REQ-1.
2. [WARNING] Decide whether `CLAUDEOPS_PR_ENABLED` gate is intentional. If so, update SPEC-0019-REQ-2 to document the env var gate on `create_pr` and change the "exactly three tools" requirement. If not, remove the gate and always expose `create_pr` with server-side tier enforcement as the spec requires.
3. [WARNING] Update status of ADR-0011, ADR-0012, ADR-0013, ADR-0014, ADR-0015, ADR-0016, ADR-0017, ADR-0018, ADR-0019 from `proposed` to `accepted`. Use `/design:status` to update each.
4. [INFO] Fix CLAUDE.md line 265: change `RFC-XXXX` to `SPEC-XXXX` to match actual spec numbering convention.
5. [INFO] Consider creating a spec for the CLI interface (`cmd/claudeops/` subcommands and flags). Use `/design:spec cli-interface`.
