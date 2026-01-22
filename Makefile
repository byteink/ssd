.PHONY: all build test lint setup hooks

# Auto-setup hooks on any make command
-include .make.state

all: hooks build

build:
	go build -o ssd .

test: hooks
	go test ./...

lint: hooks
	golangci-lint run

# Setup git hooks (runs automatically via .make.state)
hooks:
	@if [ "$$(git config core.hooksPath)" != ".githooks" ]; then \
		echo "Setting up git hooks..."; \
		git config core.hooksPath .githooks; \
		echo "✅ Git hooks configured"; \
	fi

setup: hooks
	@echo "Setup complete"
