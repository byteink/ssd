.PHONY: all build test test-integration test-e2e test-e2e-full test-all lint setup hooks

# Auto-setup hooks on any make command
-include .make.state

all: hooks build

build:
	go build -o ssd .

# Unit tests (fast, no Docker required)
test: hooks
	go test ./...

# Integration tests: real SSH/Docker against throwaway containers (needs Docker)
test-integration: hooks
	go test -tags integration ./...

# E2E tests, CI/fast path: full deploy in a docker-in-docker sandbox using the
# recreate strategy (compose plugin only). Host Docker is never touched.
test-e2e: hooks
	go test -tags e2e ./...

# E2E tests, full-fidelity path: provisions docker-rollout in the sandbox and
# exercises the real zero-downtime rollout deploy. Run locally before release.
test-e2e-full: hooks
	SSD_E2E_FULL=1 go test -tags e2e ./...

# Everything that must pass before a release (see CLAUDE.md release gate).
test-all: test test-integration test-e2e-full

lint: hooks
	golangci-lint run --build-tags 'integration e2e'

# Setup git hooks (runs automatically via .make.state)
hooks:
	@if [ "$$(git config core.hooksPath)" != ".githooks" ]; then \
		echo "Setting up git hooks..."; \
		git config core.hooksPath .githooks; \
		echo "✅ Git hooks configured"; \
	fi

setup: hooks
	@echo "Setup complete"
