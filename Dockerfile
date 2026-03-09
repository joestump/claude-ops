# Governing: SPEC-0008 REQ-1 (Single Binary Entrypoint — static binary replaces entrypoint.sh)
# This Dockerfile uses the pre-built base image (ghcr.io/joestump/claude-ops:base-latest)
# which contains all slow-moving CLI tool layers. See Dockerfile.base for details.
# The base image is rebuilt infrequently via .github/workflows/build-base.yml.

# Build stage: compile Go binary
FROM golang:1.24-alpine AS builder

ARG VERSION=dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY api/ api/
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/joestump/claude-ops/internal/config.Version=${VERSION}" -o /claudeops ./cmd/claudeops

# Runtime stage — inherits all CLI tools from pre-built base image
FROM ghcr.io/joestump/claude-ops:base-latest

# Governing: ADR-0028 "Headless Permission Bypass" — Claude Code CLI refuses
# --dangerously-skip-permissions when running as root. Create a non-root user
# so the flag works in headless containers while --allowedTools/--disallowedTools
# still enforce tier restrictions at the CLI boundary.
RUN groupadd -r claudeops && useradd -r -g claudeops -m -s /bin/bash claudeops \
    && chown -R claudeops:claudeops /app /state /results /repos

# Copy Go binary from build stage
COPY --from=builder /claudeops /claudeops

# Copy project files (prompts, checks, playbooks, etc.)
COPY --chown=claudeops:claudeops . .

# The root CLAUDE.md is the developer guide for the claude-ops codebase.
# In the container the agent must read only the monitoring runbook, so we
# overwrite it with prompts/agent.md after the broad COPY above.
COPY --chown=claudeops:claudeops prompts/agent.md /app/CLAUDE.md

USER claudeops

# Governing: SPEC-0009 REQ "Dockerfile Structure" — container entrypoint
ENTRYPOINT ["/claudeops"]
