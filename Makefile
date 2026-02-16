.PHONY: build test clean dev dev-up dev-down dev-logs dev-rebuild

BINARY := claudeops
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags "-X github.com/joestump/claude-ops/internal/config.Version=$(VERSION)" -o $(BINARY) ./cmd/claudeops

test:
	go test ./internal/...

clean:
	rm -f $(BINARY)

# Docker Compose dev targets
dev:
	docker compose up --build

dev-up:
	docker compose up --build -d

dev-down:
	docker compose down

dev-logs:
	docker compose logs -f watchdog

dev-rebuild:
	docker compose build --no-cache
