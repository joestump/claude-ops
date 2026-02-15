.PHONY: build run run-dry clean test docker-build docker-up docker-down

BINARY    := claudeops
STATE     := /tmp/claudeops-state
RESULTS   := /tmp/claudeops-results
REPOS     := /tmp/claudeops-repos
INTERVAL  := 3600
MODEL     := haiku

build:
	go build -o $(BINARY) ./cmd/claudeops

test:
	go test ./internal/...

run: build
	@mkdir -p $(STATE) $(RESULTS) $(REPOS)
	CLAUDEOPS_STATE_DIR=$(STATE) \
	CLAUDEOPS_RESULTS_DIR=$(RESULTS) \
	CLAUDEOPS_REPOS_DIR=$(REPOS) \
	CLAUDEOPS_PROMPT=prompts/tier1-observe.md \
	CLAUDEOPS_INTERVAL=$(INTERVAL) \
	CLAUDEOPS_TIER1_MODEL=$(MODEL) \
	./$(BINARY)

run-dry: build
	@mkdir -p $(STATE) $(RESULTS) $(REPOS)
	CLAUDEOPS_STATE_DIR=$(STATE) \
	CLAUDEOPS_RESULTS_DIR=$(RESULTS) \
	CLAUDEOPS_REPOS_DIR=$(REPOS) \
	CLAUDEOPS_PROMPT=prompts/tier1-observe.md \
	CLAUDEOPS_INTERVAL=$(INTERVAL) \
	CLAUDEOPS_TIER1_MODEL=$(MODEL) \
	CLAUDEOPS_DRY_RUN=true \
	CLAUDEOPS_VERBOSE=true \
	./$(BINARY)

clean:
	rm -f $(BINARY)

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down
