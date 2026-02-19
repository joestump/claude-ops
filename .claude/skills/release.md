---
name: release
description: Create a new tagged release with structured release notes
user_invocable: true
---

# Create a Release

Create a new tagged release for claude-ops using `gh release create`.

## Pre-Flight Checks

Before creating a release, you MUST verify the code is clean:

1. **Lint**: Run `go vet ./...` and `golangci-lint run`. Fix any issues before proceeding.
2. **Test**: Run `go test ./... -count=1 -race`. All tests must pass.
3. **Clean tree**: Run `git status` to confirm no uncommitted changes.
4. **CI green**: If there are recent pushes, verify the CI workflow passed with `gh run list --limit 1`.

Do NOT skip these steps. Do NOT create a release if any check fails.

## Determine Version

1. Run `git tag --sort=-v:refname | head -1` to find the latest tag.
2. Increment the patch version (e.g., `v0.0.5` → `v0.0.6`) unless the user specifies a different version.
3. If the user says "minor" or "major", bump accordingly.

## Build Release Notes

1. Run `git log <previous-tag>..HEAD --oneline` to see all commits since the last release.
2. Group commits into sections using this template:

```markdown
## What's New

### {Feature Title} ({SPEC/ADR reference if applicable})
- Bullet points describing the feature
- Focus on what changed and why it matters

### {Another Feature}
- Bullets

## Improvements

- One-line summaries of non-feature changes (bug fixes, refactors, CI, docs)
```

### Grouping Rules

- **What's New**: Features, new specs/ADRs, new capabilities. Use `###` sub-headers for each distinct feature. Reference SPEC or ADR numbers when applicable.
- **Improvements**: Bug fixes, refactors, CI changes, doc fixes, UI tweaks. One bullet per commit, concise.
- Omit sections that have no entries (e.g., skip "Improvements" if there are only features).
- Write from the user's perspective — what changed for them, not internal implementation details.

## Create the Release

```bash
gh release create <tag> --target main --title "<tag>" --notes "$(cat <<'EOF'
<release notes here>
EOF
)"
```

## Example

Here is v0.0.5 as a reference for tone and structure:

```markdown
## What's New

### SSH Access Discovery & Fallback (SPEC-0020)
- Dynamic SSH probing replaces hardcoded `root@<host>` — tries root, manifest user, then common defaults (ubuntu, debian, pi, admin)
- Builds a host access map with method (root/sudo/limited/unreachable) and Docker capability flags
- Map flows through tier handoffs so Tier 2/3 reuse it without re-probing
- Limited-access hosts get read-only inspection with PR-based or human-reported fallback for write operations
- New `skills/ssh-discovery.md` skill file with full procedure
- All tier prompts and playbooks updated to reference the access map

### Cooldown Marker Parsing
- `[COOLDOWN:restart|redeployment:service] success|failure — message` markers parsed from session output
- DRY `parseMarkers()` generic replaces per-marker-type parsers (events, memories, cooldowns)
- Teal badge rendering in dashboard (red for failures)
- Tier 2/3 prompts and cooldowns skill updated with marker docs

### ADR-0020: Bidirectional Notification Gateway
- Architecture decision record for notification gateway design

## Improvements

- Fix pipe race condition losing session results; improve escalation chain UI
- Tighten memory skill guidance — reject routine status observations and baseline performance confirmations
- Replace manual ADR/spec copies with build-time sync plugin
- Fix all broken links and deprecation warnings in docs site
- Increase text contrast on docs site light theme
- Remove API link from dashboard sidebar nav
```
