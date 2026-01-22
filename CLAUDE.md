# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Rules

- **TDD**: Write tests first, then implementation
- **Never weaken tests**: Fix code, not tests
- **Never relax linting**: Fix errors, don't disable rules or use `_ =`
- **Keep docs updated**: When adding/changing features, update both `README.md` and `CLAUDE.md`

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
- **Image naming**: `ssd-{project}-{name}:{version}` where project is extracted from stack path
- **Version tracking**: Parsed from compose.yaml image tag, auto-incremented on deploy
- **Config inheritance**: Root-level `server` and `stack` are inherited by services
- **Services-only mode**: All configs must use `services:` map (single-service mode removed)

## ssd.yaml Patterns

### Minimal (single service)
```yaml
server: myserver
services:
  app:
    # Inherits server from root
    # name defaults to service key ("app")
    # stack defaults to /stacks/app
```

### Custom stack path
```yaml
server: myserver
stack: /custom/stacks/myapp   # Shared by all services

services:
  web:
    # stack inherited from root
```

### Monorepo with shared stack
```yaml
server: myserver
stack: /stacks/myproject      # All services share this stack

services:
  web:
    context: ./apps/web
    dockerfile: ./apps/web/Dockerfile
  api:
    context: ./apps/api
    dockerfile: ./apps/api/Dockerfile
```

### Full-featured service
```yaml
server: myserver

services:
  web:
    name: myapp-web
    stack: /stacks/myapp
    context: ./apps/web
    dockerfile: ./apps/web/Dockerfile
    domain: example.com         # Enable Traefik routing
    https: true                 # Default true, set false to disable
    port: 3000                  # Container port, default 80
    depends_on:
      - db
      - redis
    volumes:
      postgres-data: /var/lib/postgresql/data
      redis-data: /data
    healthcheck:
      cmd: "curl -f http://localhost:3000/health || exit 1"
      interval: 30s
      timeout: 10s
      retries: 3
```

### Pre-built image (skip build)
```yaml
server: myserver

services:
  nginx:
    image: nginx:latest        # Use pre-built image, skip build step
    domain: example.com
```

## Commands

### Deployment
```bash
ssd deploy <service>          # Build and deploy service
ssd restart <service>         # Restart without rebuilding
ssd rollback <service>        # Rollback to previous version
ssd status <service>          # Check container status
ssd logs <service> [-f]       # View logs, -f to follow
```

### Configuration
```bash
ssd config                    # Show all services config
ssd config <service>          # Show specific service config
```

### Environment variables
```bash
ssd env <service> set KEY=VALUE      # Set environment variable
ssd env <service> list               # List all environment variables
ssd env <service> rm KEY             # Remove environment variable
```

**Note**: `env` command is currently a stub (not yet implemented).

### Provision (future)
Server provisioning with Docker and Traefik is planned but not yet available. Tests exist in `provision/provision_test.go`.
