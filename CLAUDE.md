# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
# Local dev setup
cp .env.example .env                                          # add API key
cp docker-compose.override.yaml.example docker-compose.override.yaml  # edit repo mounts

# Development (Docker Compose — full environment with Chrome sidecar)
make dev          # build + start (foreground)
make dev-up       # build + start (background)
make dev-down     # stop
make dev-logs     # tail watchdog logs
make dev-rebuild  # full rebuild (no cache)

# Production
docker compose up -d                        # without browser automation
docker compose --profile browser up -d      # with Chrome sidecar

# Go-only (no Docker)
make build        # compile binary
make test         # run tests
```

Requires a `.env` file with at minimum `ANTHROPIC_API_KEY=sk-ant-...`. See README.md for all env vars.

CI/CD: GitHub Actions (`.github/workflows/ci.yaml`) runs lint → test → build sequentially, then pushes the Docker image to GHCR on push to `main` or version tags.

## Pre-Push Requirements

You MUST pass lint and tests locally before pushing. Do NOT waste GitHub Actions credits by pushing code that fails basic checks.

```bash
# 1. Lint (must pass)
go vet ./...
golangci-lint run

# 2. Test (must pass)
go test ./... -count=1 -race
```

If either step fails, fix the issues before pushing. No exceptions.

## Releases

Use the `/release` skill (`.claude/skills/release.md`) to create tagged releases. Releases are created with `gh release create` — never through the GitHub UI. The skill handles version bumping, release note generation, and pre-flight checks.

<!-- Governing: SPEC-0010 REQ-11 (Zero Application Code Constraint — no src/, no compiled artifacts, CLI-provided features only) -->
## Architecture

This is not a traditional codebase — there is no application code to compile or test. Claude Ops is an **AI agent runbook**: markdown documents that the Claude Code CLI reads and executes at runtime.

### Execution flow

<!-- Governing: SPEC-0010 REQ-3 (model selection), REQ-4 (prompt loading) -->

`entrypoint.sh` runs an infinite loop:
1. Merges MCP configs from mounted repos into `.claude/mcp.json`
2. Invokes `claude --model haiku --prompt-file prompts/tier1-observe.md`
3. The Tier 1 agent discovers repos under `/repos/`, runs health checks from `checks/`, and evaluates results
4. If issues found → spawns a Tier 2 subagent (`sonnet`) with `prompts/tier2-investigate.md` + failure context
5. If Tier 2 can't fix → spawns Tier 3 (`opus`) with `prompts/tier3-remediate.md` + investigation findings
6. Each tier has escalating permissions (observe → safe remediation → full remediation)
7. Sleeps `$CLAUDEOPS_INTERVAL` seconds, repeats

### Key design decisions

- **Checks and playbooks are markdown, not scripts.** Claude reads `checks/*.md` and `playbooks/*.md` as instructions and executes the appropriate commands itself. This means "adding a check" = writing a markdown file describing what to check and how.
- **Mounted repos extend the agent.** Infrastructure repos mounted under `/repos/` can include `CLAUDE-OPS.md` (manifest) and `.claude-ops/` (custom checks, playbooks, skills, MCP configs). See `docs/repo-mounting.md` for the full spec.
- **MCP configs are merged at startup.** The entrypoint merges `.claude-ops/mcp.json` from all mounted repos into the baseline `.claude/mcp.json` before each run. Repo configs override baseline on name collision. Repos are processed in alphabetical order; later repos override earlier ones. <!-- Governing: SPEC-0005 REQ-9 -->
- **Cooldown state persists in `$CLAUDEOPS_STATE_DIR/cooldown.json`.** Max 2 restarts/service/4h, max 1 redeployment/service/24h.

### Skills

- `/adr` — Creates an Architecture Decision Record using a Claude Team (drafter + architect agents). Output goes to `docs/adrs/ADR-XXXX-*.md`.
- `/openspec` — Creates an OpenSpec specification (spec.md + design.md) using a Claude Team. Output goes to `docs/openspec/specs/{capability}/`.

Both skills use `TeamCreate` to spawn drafter/writer and architect review agents.

---

> **Note**: The agent runbook (identity, permission tiers, cooldown rules, PR workflow, etc.) lives in
> `prompts/agent.md`. The Dockerfile copies it to `/app/CLAUDE.md` in the container image so the
> monitoring agent reads it as its system context. This file is for developers working on the
> claude-ops codebase — not for the agent itself.

## Architecture Decision Records (ADRs)

This project uses [MADR](https://adr.github.io/madr/) (Markdown Architectural Decision Records) to document significant architectural decisions.

### Location

ADRs live in `docs/adrs/` and are named `ADR-XXXX-short-title.md` (e.g., `ADR-0001-tiered-model-escalation.md`).

### Numbering

ADR numbers are sequential and zero-padded to 4 digits: `ADR-0001`, `ADR-0002`, etc. Always scan `docs/adrs/` for the highest existing number before creating a new one.

### Creating an ADR

Use the `/adr` skill or shorthand like "We need an ADR for X" or "Create an ADR for Y".

**Every ADR MUST be created using a Claude Team:**
1. A **drafter** agent writes the ADR from the user's description
2. An **architect** agent reviews the ADR for completeness, realistic trade-offs, and correct MADR structure
3. The architect must approve before the ADR is finalized

ADRs start with `status: proposed`. The user decides when to mark them `accepted`.

### MADR Format

Every ADR MUST include YAML frontmatter (`status`, `date`) and these sections:
- **Context and Problem Statement** (required)
- **Decision Drivers** (optional but recommended)
- **Considered Options** (required)
- **Decision Outcome** with Consequences (required)
- **Pros and Cons of the Options** (required)

## OpenSpec Specifications

This project uses [OpenSpec](https://github.com/Fission-AI/OpenSpec) for formal specifications. The schema is spec-driven: proposal → specs → design → tasks.

### Location

Specs live in `docs/openspec/specs/` organized by capability:
```
docs/openspec/specs/
└── {capability-name}/
    ├── spec.md       # Requirements (RFC 2119 language)
    └── design.md     # Technical design and architecture
```

### Rules

- **ALWAYS write BOTH `spec.md` AND `design.md`** — never one without the other
- **spec.md** uses SPEC numbering: `SPEC-XXXX` (sequential, zero-padded to 4 digits)
- **spec.md** MUST express requirements using [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119) keywords: MUST, MUST NOT, SHALL, SHALL NOT, SHOULD, SHOULD NOT, REQUIRED, MAY, OPTIONAL
- **Scenarios** in spec.md MUST use exactly 4 hashtags (`####`) — using 3 or bullets will fail silently
- Every requirement MUST have at least one scenario
- **design.md** focuses on architecture, rationale, and trade-offs — not line-by-line implementation

### Creating an OpenSpec

Use the `/openspec` skill or shorthand like "Convert the ADR to a spec" or "Write a spec for X".

**Every OpenSpec MUST be created using a Claude Team:**
1. A **spec-writer** agent writes both spec.md and design.md
2. An **architect** agent reviews both documents for RFC 2119 compliance, scenario format, and alignment between spec and design
3. The architect must approve both documents before they are finalized

### Workflow: ADR → OpenSpec

The typical flow is:
1. Create an ADR to decide on an approach (`/adr`)
2. User reviews and accepts the ADR (`status: accepted`)
3. Convert the accepted ADR into an OpenSpec (`/openspec`)
4. Implement from the spec

## Architecture Context

This project uses the [design plugin](https://github.com/joestump/claude-plugin-design) for architecture governance.

- Architecture Decision Records are in `docs/adrs/`
- Specifications are in `docs/openspec/specs/`

### Design Plugin Skills

| Skill | Purpose |
|-------|---------|
| `/design:adr` | Create a new Architecture Decision Record |
| `/design:spec` | Create a new specification |
| `/design:list` | List all ADRs and specs with status |
| `/design:status` | Update the status of an ADR or spec |
| `/design:docs` | Generate a documentation site |
| `/design:init` | Set up CLAUDE.md with architecture context |
| `/design:prime` | Load architecture context into session |
| `/design:check` | Quick-check code against ADRs and specs for drift |
| `/design:audit` | Comprehensive design artifact alignment audit |

Run `/design:prime [topic]` at the start of a session to load relevant ADRs and specs into context.
