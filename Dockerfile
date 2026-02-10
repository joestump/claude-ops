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
RUN pip3 install --break-system-packages apprise

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Working directory — this repo gets copied in
WORKDIR /app

# Copy project files
COPY . .

# Ensure entrypoint is executable
RUN chmod +x entrypoint.sh

# State and results directories (mount volumes over these)
RUN mkdir -p /state /results /repos

ENTRYPOINT ["./entrypoint.sh"]
