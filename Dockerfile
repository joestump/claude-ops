# Governing: SPEC-0008 REQ-1 (Single Binary Entrypoint — static binary replaces entrypoint.sh)
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

# Runtime stage
# Governing: SPEC-0009 REQ "Dockerfile Structure" — node:22-slim base image
FROM node:22-slim

# Governing: SPEC-0009 REQ "Dockerfile Structure" — system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-client \
    curl \
    git \
    dnsutils \
    jq \
    python3 \
    python3-pip \
    python3-venv \
    gnupg \
    lsb-release \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI, Docker CLI, HashiCorp Vault — add apt repos then install together
RUN mkdir -p /usr/share/keyrings \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
       | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
       > /etc/apt/sources.list.d/github-cli.list \
    && curl -fsSL https://download.docker.com/linux/debian/gpg \
       | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian $(lsb_release -cs) stable" \
       > /etc/apt/sources.list.d/docker.list \
    && curl -fsSL https://apt.releases.hashicorp.com/gpg \
       | gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
       > /etc/apt/sources.list.d/hashicorp.list \
    && apt-get update && apt-get install -y --no-install-recommends \
       gh \
       docker-ce-cli \
       vault \
    && rm -rf /var/lib/apt/lists/*

# Gitea CLI (tea) — no apt repo; install binary for current arch
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSL "https://gitea.com/gitea/tea/releases/download/v0.9.2/tea-0.9.2-linux-${ARCH}" \
       -o /usr/local/bin/tea \
    && chmod +x /usr/local/bin/tea

# Governing: SPEC-0004 REQ-8 (Docker Image Installation — apprise via pip3)
# Governing: SPEC-0009 REQ "Dockerfile Structure" — apprise for notifications
# Apprise for notifications (supports 80+ services)
# Governing: SPEC-0004 REQ-3 — Installed as CLI tool, invoked via `apprise` command in agent Bash commands
RUN pip3 install --break-system-packages --retries 3 --timeout 120 apprise

# Governing: SPEC-0010 REQ-1 (CLI Installation via npm), SPEC-0009 REQ "Dockerfile Structure" — Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Governing: SPEC-0009 REQ "Dockerfile Structure" — working directory /app
WORKDIR /app

# Copy Go binary from build stage
COPY --from=builder /claudeops /claudeops

# Copy project files (prompts, checks, playbooks, etc.)
COPY . .

# Governing: SPEC-0009 REQ "Dockerfile Structure" — /state, /results, /repos directories
RUN mkdir -p /state /results /repos

# Governing: SPEC-0009 REQ "Dockerfile Structure" — container entrypoint
# NOTE: The spec (REQ-9) references entrypoint.sh, which was the original entrypoint.
# The Go binary /claudeops has since replaced entrypoint.sh entirely — it handles config
# loading, MCP config merging, session scheduling, signal handling, and the web dashboard.
# See ADR-0009 (Go rewrite) and cmd/claudeops/main.go. entrypoint.sh is retained for
# reference but is not used.
ENTRYPOINT ["/claudeops"]
