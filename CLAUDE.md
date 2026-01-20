# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Rules

- **TDD**: Write tests first, then implementation
- **Never weaken tests**: Fix code, not tests
- **Never relax linting**: Fix errors, don't disable rules or use `_ =`

## Build & Run

```bash
go build -o ssd .      # Build binary
go run .               # Run directly
./ssd version          # Test the binary
```

## Release

Uses goreleaser. Version is injected via ldflags (`-X main.version={{.Version}}`).

```bash
goreleaser release --snapshot --clean   # Test release locally
```

## Project Structure

```
├── main.go           # CLI entry point and commands
├── config/
│   └── config.go     # ssd.yaml parsing and defaults
├── remote/
│   └── remote.go     # SSH, rsync, docker operations
└── deploy/
    └── deploy.go     # Deploy orchestration
```

## Core Workflow

1. Read `ssd.yaml` config from current directory
2. SSH into configured server (uses `~/.ssh/config` hosts)
3. Create temp directory on server
4. Rsync code to temp dir (excludes .git, node_modules, .next)
5. Build Docker image on server: `ssd-{name}:{version}`
6. Parse current version from compose.yaml, increment it
7. Update compose.yaml with new image tag
8. Run `docker compose up -d` to restart
9. Clean up temp directory

## Conventions

- **Stack path**: Full path to stack directory containing compose.yaml (default: `/stacks/{name}`)
- **Image naming**: `ssd-{name}:{version}` (auto-incremented)
- **Version tracking**: Parsed from compose.yaml image tag (`ssd-{name}:{version}`)
- **Config inheritance**: Root-level `server` and `stack` are inherited by services in monorepo mode

## ssd.yaml Patterns

### Simple project
```yaml
server: myserver        # SSH host from ~/.ssh/config
name: myapp             # Optional, defaults to directory name
# stack defaults to /stacks/{name}
```

### Custom stack path
```yaml
server: myserver
name: myapp
stack: /custom/stacks/myapp   # Full path to stack directory
```

### Monorepo (root-level defaults)
```yaml
server: myserver
stack: /stacks/myproject      # Shared stack for all services

services:
  web:
    name: myproject-web       # Image will be ssd-myproject-web:{version}
    context: ./apps/web
  api:
    name: myproject-api
    context: ./apps/api
```
