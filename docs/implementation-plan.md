# Implementation Roadmap: SPEC-0014, SPEC-0015, SPEC-0016

## Executive Summary

This document defines the implementation order, migration coordination, shared change plan, phased rollout, testing strategy, and risk matrix for three specifications:

- **SPEC-0016**: Session-Based Escalation with Structured Handoff
- **SPEC-0015**: Persistent Agent Memory System
- **SPEC-0014**: Browser Automation for Authenticated Web UIs

All three specs modify `internal/session/manager.go`, `internal/config/config.go`, and prompt files. Two specs require database migrations. The plan sequences work to minimize merge conflicts, maximize early value, and maintain a deployable system at each phase boundary.

---

## 1. Implementation Order

### Recommended sequence: SPEC-0016 -> SPEC-0015 -> SPEC-0014

| Order | Spec | Rationale |
|-------|------|-----------|
| **1st** | SPEC-0016 (Session-Based Escalation) | Restructures the core session lifecycle (`runOnce` -> `runEscalationChain`). All other specs build on this refactored manager. Provides the `parent_session_id` column that links escalated sessions. |
| **2nd** | SPEC-0015 (Persistent Agent Memory) | Adds the memory system that hooks into the session startup sequence and stream parser. Depends on the refactored `runTier` method from SPEC-0016 (memory injection needs to happen per-tier, not per-cycle). |
| **3rd** | SPEC-0014 (Browser Automation) | Most isolated spec -- primarily adds middleware (credential resolver, redaction filter, URL allowlist) around the existing Chrome DevTools MCP integration. Does not modify session lifecycle structure. Can layer onto the SPEC-0015/0016-enhanced manager. |

### Dependency analysis

```
SPEC-0016 (escalation)
  |
  +--> SPEC-0015 (memory)  -- memories injected per-tier via buildMemoryContext()
  |                           in the refactored runTier() method
  |
  +--> SPEC-0014 (browser)  -- redaction filter wraps stream output pipeline
                               already modified by SPEC-0015 memory parsing
```

SPEC-0015 depends on SPEC-0016 because:
- Memory context injection (`buildMemoryContext()`) must happen inside the new per-tier `runTier()` method, not the old monolithic `runOnce()`
- Memory markers are parsed in the stream goroutine; SPEC-0016 refactors how sessions are spawned, so memory parsing must align with the new structure
- The `tier` column on memories needs to be set per-session, which requires the per-tier session model from SPEC-0016

SPEC-0014 depends on SPEC-0015 because:
- The redaction filter wraps the same stream output pipeline where memory markers are parsed
- Both modify the stream scanner goroutine in `runTier()` (memory marker parsing + credential redaction)
- The redaction filter should run *before* memory parsing to prevent credential values from being stored as memory observations

SPEC-0014 is otherwise independent -- it does not require escalation or memory to function. But implementing it last avoids merge conflicts in the shared output pipeline code.

---

## 2. Migration Coordination

Current state: migrations 001-004 exist in `internal/db/db.go`.

| Migration | Spec | Description |
|-----------|------|-------------|
| **005** | SPEC-0016 | `ALTER TABLE sessions ADD COLUMN parent_session_id INTEGER REFERENCES sessions(id)` + index |
| **006** | SPEC-0015 | `CREATE TABLE memories (...)` with 3 indexes |

### Why separate migrations (not shared)

- SPEC-0016 alters an existing table; SPEC-0015 creates a new table. These are logically distinct operations.
- Separate migrations allow deploying SPEC-0016 independently and validating the escalation chain before adding the memory table.
- If migration 005 fails for any reason, it does not block the unrelated memories table creation in a future deployment.

### Migration implementation pattern

Both migrations follow the established pattern in `db.go`:

```go
var migrations = []migration{
    {version: 1, fn: migrate001},
    {version: 2, fn: migrate002},
    {version: 3, fn: migrate003},
    {version: 4, fn: migrate004},
    {version: 5, fn: migrate005},  // SPEC-0016: parent_session_id
    {version: 6, fn: migrate006},  // SPEC-0015: memories table
}
```

SPEC-0014 requires no migrations -- it uses environment variables and middleware, not new database tables.

---

## 3. Shared Changes

### 3.1 `internal/session/manager.go` (most contended file)

All three specs modify this file. The merge order matters.

| Phase | Spec | Changes to manager.go |
|-------|------|----------------------|
| **Phase 1** | SPEC-0016 | **Major refactor**: `runOnce()` -> `runTier()` + `runEscalationChain()`. New methods: `readHandoff()`, `deleteHandoff()`, `validateHandoff()`, `buildHandoffContext()`. Modified: CLI args construction (remove Task from allowedTools, add handoff context to `--append-system-prompt`). Modified: `Run()` loop calls `runEscalationChain()` instead of `runSession()`. Session insertion now includes `parent_session_id`. |
| **Phase 2** | SPEC-0015 | **Additive**: New methods: `buildMemoryContext()`, `parseMemoryMarkers()`, `upsertMemory()`, `decayStaleMemories()`. Modified: `runTier()` to call `decayStaleMemories()` + `buildMemoryContext()` at session start. Modified: stream scanner goroutine to parse `[MEMORY:...]` markers alongside `[EVENT:...]` markers. New regex: `memoryMarkerRe`. |
| **Phase 3** | SPEC-0014 | **Wrapping**: New type: `RedactionFilter`. New method: `resolveCredentials()`. Modified: stream scanner goroutine to apply redaction before logging/SSE/parsing. Modified: CLI args to include browser-related env context. New: `buildBrowserInitScript()`. |

The key insight: **SPEC-0016 refactors the structure, SPEC-0015 adds to it, SPEC-0014 wraps it.** This order produces the cleanest diffs.

### 3.2 `internal/config/config.go`

| Phase | Spec | New fields |
|-------|------|-----------|
| **Phase 1** | SPEC-0016 | `MaxTier int`, `Tier2Prompt string`, `Tier3Prompt string` |
| **Phase 2** | SPEC-0015 | `MemoryBudget int` |
| **Phase 3** | SPEC-0014 | `BrowserAllowedOrigins string`, `BrowserCredPrefix string` (implicit via env) |

These are purely additive -- no conflicts expected.

### 3.3 `internal/db/db.go`

| Phase | Spec | Changes |
|-------|------|---------|
| **Phase 1** | SPEC-0016 | Add `ParentSessionID *int64` to `Session` struct. Update `sessionColumns`, `scanSession`, `InsertSession`. New method: `GetEscalationChain()`. Migration 005. |
| **Phase 2** | SPEC-0015 | New `Memory` struct and type `MemoryFilter`. 8 new methods (InsertMemory, UpdateMemory, DeleteMemory, etc.). Migration 006. |
| **Phase 3** | SPEC-0014 | No db.go changes. |

### 3.4 Prompt files (`prompts/tier1-observe.md`, `tier2-investigate.md`, `tier3-remediate.md`)

| Phase | Spec | Changes |
|-------|------|---------|
| **Phase 1** | SPEC-0016 | Replace Task tool escalation instructions with handoff file writing instructions. Remove Task from allowed tools guidance. |
| **Phase 2** | SPEC-0015 | Add "Memory Recording" section to all three prompts with `[MEMORY:...]` marker format, valid categories, and examples. |
| **Phase 3** | SPEC-0014 | Add browser automation security instructions to Tier 2 and Tier 3 prompts. Add untrusted-data warning. Add credential reference pattern instructions. |

### 3.5 Dashboard (`internal/web/`)

| Phase | Spec | Changes |
|-------|------|---------|
| **Phase 1** | SPEC-0016 | Session detail: parent/child links, chain cost rollup. Sessions list: chain indicator. New viewmodel fields. |
| **Phase 2** | SPEC-0015 | New `/memories` page with CRUD. Sidebar navigation update. Overview page memory count card. New handlers, routes, template. |
| **Phase 3** | SPEC-0014 | No dashboard changes (browser automation is invisible to the dashboard -- redacted output flows through existing log/SSE channels). |

### 3.6 `docker-compose.yaml`

| Phase | Spec | Changes |
|-------|------|---------|
| **Phase 1** | SPEC-0016 | New env vars: `CLAUDEOPS_MAX_TIER`, `CLAUDEOPS_TIER2_PROMPT`, `CLAUDEOPS_TIER3_PROMPT` |
| **Phase 2** | SPEC-0015 | New env var: `CLAUDEOPS_MEMORY_BUDGET` |
| **Phase 3** | SPEC-0014 | New env vars: `BROWSER_ALLOWED_ORIGINS`, `BROWSER_CRED_*`. Chrome service: add `--incognito` flag. |

---

## 4. Phased Rollout

### Phase 1: Session-Based Escalation (SPEC-0016)

**Objective**: Replace in-process Task tool escalation with supervisor-controlled, per-tier CLI processes.

| File | Change description | Size |
|------|-------------------|------|
| `internal/db/db.go` | Migration 005 + Session struct changes + GetEscalationChain() | M |
| `internal/session/manager.go` | Major refactor: runOnce -> runTier + runEscalationChain + handoff I/O | L |
| `internal/config/config.go` | Add MaxTier, Tier2Prompt, Tier3Prompt fields | S |
| `prompts/tier1-observe.md` | Replace Task escalation with handoff file instructions | M |
| `prompts/tier2-investigate.md` | Replace Task escalation with handoff file instructions | M |
| `prompts/tier3-remediate.md` | Minor: note that no further escalation is possible | S |
| `internal/web/handlers.go` | Session detail: parent/child links, chain cost | M |
| `internal/web/viewmodel.go` | Add ParentSessionID, ChildSession fields to SessionView | S |
| `website/templates/session.html` | Render escalation chain links and cost breakdown | M |
| `website/templates/sessions.html` | Chain indicator column | S |
| `docker-compose.yaml` | New env vars | S |

**Estimated complexity**: L (Large)
**Risk level**: Medium-High -- this is the largest structural change, touching the core session lifecycle
**Testing focus**: Escalation chain creation, handoff file validation, policy enforcement (dry-run, max-tier), DB chain queries

### Phase 2: Persistent Agent Memory (SPEC-0015)

**Objective**: Give the agent persistent operational knowledge across sessions.

| File | Change description | Size |
|------|-------------------|------|
| `internal/db/db.go` | Migration 006 + Memory type + 8 CRUD methods + staleness decay | L |
| `internal/session/manager.go` | buildMemoryContext() + parseMemoryMarkers() + upsertMemory() + decayStaleMemories() + integrate into runTier() | M |
| `internal/config/config.go` | Add MemoryBudget field | S |
| `prompts/tier1-observe.md` | Add Memory Recording section | S |
| `prompts/tier2-investigate.md` | Add Memory Recording section | S |
| `prompts/tier3-remediate.md` | Add Memory Recording section | S |
| `internal/web/handlers.go` | New handleMemories, handleMemoryCRUD handlers | M |
| `internal/web/viewmodel.go` | New MemoryView, MemoryFilter types | S |
| `internal/web/server.go` | Register /memories routes | S |
| `website/templates/memories.html` | New page with table, filters, CRUD forms | M |
| `website/templates/nav.html` | Add Memories link between Events and Cooldowns | S |
| `website/templates/index.html` | Memory count summary card | S |
| `docker-compose.yaml` | New env var | S |

**Estimated complexity**: M (Medium)
**Risk level**: Medium -- additive changes, no structural refactoring
**Testing focus**: Memory marker parsing, reinforcement/contradiction logic, staleness decay, token budget enforcement, dashboard CRUD

### Phase 3: Browser Automation (SPEC-0014)

**Objective**: Enable safe authenticated browser interactions for web UI management.

| File | Change description | Size |
|------|-------------------|------|
| `internal/session/manager.go` | RedactionFilter type + resolveCredentials() + buildBrowserInitScript() + integrate redaction into stream pipeline | M |
| `internal/config/config.go` | Add BrowserAllowedOrigins field | S |
| `prompts/tier2-investigate.md` | Add browser automation security instructions + credential reference pattern | M |
| `prompts/tier3-remediate.md` | Add browser automation security instructions + credential reference pattern | M |
| `prompts/tier1-observe.md` | Add note that browser authentication is not permitted at Tier 1 | S |
| `docker-compose.yaml` | BROWSER_ALLOWED_ORIGINS, chrome service --incognito flag, BROWSER_CRED_* placeholders | S |
| `.claude/mcp.json` | Ensure Chrome DevTools MCP server is configured | S |

**Estimated complexity**: M (Medium)
**Risk level**: Medium -- security-sensitive (credential handling, redaction). Correctness is critical but blast radius is contained.
**Testing focus**: Credential resolution (including missing creds), URL allowlist enforcement (allowed/blocked/redirect), redaction coverage (raw, URL-encoded, all output channels), tier gating (Tier 1 denied, Tier 2 allowed), browser context isolation

---

## 5. Testing Strategy

### 5.1 Shared Test Infrastructure

All three specs need:

1. **Test database fixture** (`internal/db/db_test.go`): In-memory SQLite database with all migrations applied. Existing pattern can be extended.
2. **Mock CLI runner**: For testing escalation chains and session lifecycle without spawning actual `claude` processes. The `runTier()` method should accept an interface for the process runner to enable test doubles.
3. **Stream event fixtures**: Sample NDJSON lines with `[EVENT:...]` and `[MEMORY:...]` markers for parser testing.

### 5.2 Per-Spec Unit Tests

**SPEC-0016 (Escalation)**:
- `TestHandoffFileParsing` -- valid/invalid JSON, schema version mismatch, missing required fields
- `TestEscalationChainCreation` -- Tier 1 -> 2, Tier 1 -> 2 -> 3, Tier 1 only (all healthy)
- `TestDryRunSuppressesEscalation` -- handoff exists but dry-run prevents spawn
- `TestMaxTierEnforcement` -- handoff requests Tier 3 when max is 2
- `TestGetEscalationChain` -- DB query walks parent/child links correctly
- `TestHandoffContextSerialization` -- markdown output includes all required sections
- `TestStaleHandoffCleanup` -- pre-existing handoff deleted on cycle start

**SPEC-0015 (Memory)**:
- `TestMemoryMarkerParsing` -- valid categories, invalid categories, with/without service
- `TestMemoryReinforcement` -- same service+category increases confidence by 0.1
- `TestMemoryContradiction` -- same service+category with conflicting observation decreases confidence
- `TestStalenessDecay` -- 30-day grace, 0.1/week decay, deactivation at 0.3
- `TestBuildMemoryContext` -- token budget enforcement, service grouping, confidence ordering
- `TestMemoryDashboardCRUD` -- create, read, update, delete, bulk delete
- `TestConfidenceClamping` -- values above 1.0 clamped to 1.0, below 0.0 clamped to 0.0

**SPEC-0014 (Browser Automation)**:
- `TestRedactionFilter` -- raw credential, URL-encoded credential, short credential warning
- `TestCredentialResolver` -- valid env var, missing env var, Tier 1 denied, Tier 2 allowed
- `TestURLAllowlist` -- allowed origin, blocked origin, empty allowlist disables
- `TestInitScriptGeneration` -- correct JavaScript output from BROWSER_ALLOWED_ORIGINS
- `TestRedactionAllOutputChannels` -- log file, SSE stream, session response all redacted

### 5.3 Integration Tests

After all three specs are implemented, integration tests should verify the combined system:

1. **Full escalation with memory**: Tier 1 detects issue, writes memory + handoff. Tier 2 starts with memory context injected, attempts fix, writes another memory + handoff. Tier 3 receives all context. Verify all memories recorded with correct tier values.

2. **Browser automation with redaction in escalation chain**: Tier 2 browser task fills credentials, output is redacted in the session log. Memory marker emitted during browser task does not contain credential values (redaction runs before memory parsing).

3. **Memory reinforcement across escalation tiers**: Tier 1 emits `[MEMORY:timing:jellyfin] Takes 60s to start`. Tier 2 (same cycle) emits same observation. Verify reinforcement (confidence increase) not duplication.

4. **Dashboard end-to-end**: Escalation chain with memories visible. Session detail shows parent/child links. Memory page shows all memories with correct session links. Cooldowns page shows actions from escalated tiers.

### 5.4 Test Execution Plan

```bash
# Unit tests (run after each phase)
go test ./internal/db/... -v
go test ./internal/session/... -v
go test ./internal/web/... -v
go test ./internal/config/... -v

# Integration tests (run after Phase 3)
go test ./internal/... -tags=integration -v

# Full system test (manual, with docker compose)
docker compose build && CLAUDEOPS_DRY_RUN=true docker compose up
```

---

## 6. Risk Matrix

| Spec | Risk | Likelihood | Impact | Mitigation |
|------|------|-----------|--------|------------|
| **SPEC-0016** | Manager refactor introduces regression in session lifecycle | Medium | High | Comprehensive unit tests for runTier(). Maintain backward-compatible session record format. Deploy with dry-run first. |
| **SPEC-0016** | LLM writes malformed handoff JSON | Medium | Low | Supervisor validates and rejects. Fail-safe: no escalation on bad JSON (log + skip). |
| **SPEC-0016** | Handoff file left on disk after crash | Low | Low | Supervisor deletes stale handoff at cycle start. |
| **SPEC-0016** | Per-tier process startup latency (2-5s each) | High (certain) | Low | Acceptable for 60-min cycle intervals. |
| **SPEC-0015** | Single memory per (service, category) loses distinct observations | Medium | Low | Categories are specific enough for v1. Dashboard lets operator curate. |
| **SPEC-0015** | Token budget estimate (chars/4) inaccurate | Medium | Low | Budget is conservative (2K tokens out of 100K+ context). Overestimate is harmless. |
| **SPEC-0015** | Staleness decay too aggressive for evergreen knowledge | Medium | Medium | Operator can manually boost confidence. Future: add "persistent" flag. |
| **SPEC-0015** | Memory marker parsing interacts with redaction filter | Low | Medium | Phase ordering ensures redaction runs first. Test with combined fixtures. |
| **SPEC-0014** | Credential leaks through redaction gaps (base64, split values) | Low | High | Redaction covers raw + URL-encoded. Prompt instructs agent not to echo values. Log audit for missed redactions. |
| **SPEC-0014** | JavaScript URL allowlist bypassed via evaluate_script | Low | Medium | Known limitation per ADR-0012. Network-level enforcement deferred. Prompt warns against arbitrary JS execution. |
| **SPEC-0014** | Prompt injection from monitored service page content | Low | Medium | Prompt includes untrusted-data warning. URL allowlist limits exposure to known services. |
| **SPEC-0014** | Missing BROWSER_CRED_* env var at runtime | Medium | Low | Credential resolver returns clear error. Agent skips browser task. |

---

## 7. Milestones and Verification

### Milestone 1: Escalation Chain Working (end of Phase 1)

**Verification criteria**:
- [ ] Tier 1 writes valid handoff.json on unhealthy service
- [ ] Supervisor reads, validates, and deletes handoff
- [ ] Tier 2 spawned as separate process with handoff context
- [ ] Tier 2 -> Tier 3 escalation works end-to-end
- [ ] Dry-run mode suppresses escalation
- [ ] Dashboard shows escalation chains with parent/child links
- [ ] Per-tier cost attribution visible in session detail
- [ ] All healthy case: no handoff, no escalation

### Milestone 2: Memory System Operational (end of Phase 2)

**Verification criteria**:
- [ ] `[MEMORY:...]` markers parsed from LLM output
- [ ] Memories stored in SQLite with correct metadata
- [ ] Active memories injected into system prompt at session start
- [ ] Token budget enforced
- [ ] Reinforcement increases confidence for matching memories
- [ ] Staleness decay reduces confidence after 30 days
- [ ] Dashboard /memories page renders with CRUD
- [ ] Sidebar navigation includes Memories link

### Milestone 3: Browser Automation Secure (end of Phase 3)

**Verification criteria**:
- [ ] Credential resolver swaps env var references for real values
- [ ] Tier 1 credential resolution denied
- [ ] URL allowlist blocks navigation to non-approved origins
- [ ] Redaction filter strips credential values from all output channels
- [ ] Browser contexts isolated (incognito mode)
- [ ] Empty allowlist disables browser automation entirely
- [ ] Audit trail in session logs (with redacted values)

---

## 8. Files Changed Per Phase (Summary)

### Phase 1 (SPEC-0016): 11 files, ~800 LOC
### Phase 2 (SPEC-0015): 13 files, ~600 LOC
### Phase 3 (SPEC-0014): 7 files, ~400 LOC

**Total estimated**: ~1,800 LOC across 3 phases.

---

## 9. Decisions (Resolved)

1. **`runTier()` accepts an interface for process execution.** Yes — enables unit testing without spawning real `claude` processes. Define a `ProcessRunner` interface with `Run(ctx, args) (stdout, error)` and inject it into `Manager`.

2. **Memory staleness decay is configurable via env vars.** Yes — `CLAUDEOPS_MEMORY_GRACE_DAYS` (default: 30) and `CLAUDEOPS_MEMORY_DECAY_RATE` (default: 0.1 per week). Add to `Config` struct.

3. **Credential injection uses prompt-based references.** The agent references credentials by env var name (e.g., `$BROWSER_CRED_SONARR_PASS`) in its reasoning. The Chrome DevTools MCP `fill` tool receives the actual value from the environment. The redaction filter strips credential values from all output channels. No MCP middleware needed.

4. **All phases on main branch with commits between phases.** No separate PRs. Implement Phase 1 -> commit -> Phase 2 -> commit -> Phase 3 -> commit.
