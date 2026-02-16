# Build stage: compile Go binary
FROM golang:1.24-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY api/ api/
RUN CGO_ENABLED=0 go build -o /claudeops ./cmd/claudeops

# Runtime stage
FROM node:22-slim

# System dependencies for health checks and remediation
RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-client \
    curl \
    dnsutils \
    jq \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Apprise for notifications (supports 80+ services)
RUN pip3 install --break-system-packages --retries 3 --timeout 120 apprise

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Working directory — this repo gets copied in
WORKDIR /app

# Copy Go binary from build stage
COPY --from=builder /claudeops /claudeops

# Copy project files (prompts, checks, playbooks, etc.)
COPY . .

# State and results directories (mount volumes over these)
RUN mkdir -p /state /results /repos

ENTRYPOINT ["/claudeops"]
