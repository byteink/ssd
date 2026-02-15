# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Rules

- **TDD**: Write tests first, then implementation
- **Never weaken tests**: Fix code, not tests
- **Never relax linting**: Fix errors, don't disable rules or use `_ =`
- **Keep docs updated**: When adding/changing features, update both `README.md` and `CLAUDE.md`

## Setup

```bash
make setup             # Configure git hooks for linting (run once after clone)
```

## Build & Run

```bash
make build             # Build binary (also runs make setup if needed)
make test              # Run tests
make lint              # Run linter
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
├── deploy/
│   └── deploy.go     # Deploy orchestration
└── scaffold/
    └── scaffold.go   # ssd init command (generate ssd.yaml)
```

## Core Workflow

1. Read `ssd.yaml` config from current directory
2. SSH into configured server (uses `~/.ssh/config` hosts)
3. Create temp directory on server
4. Rsync code to temp dir (excludes .git, node_modules, .next)
5. Build Docker image on server: `ssd-{name}:{version}`
6. Parse current version from compose.yaml, increment it
7. Canary deploy (if service already running) or direct start (first deploy)
8. Clean up temp directory

## Canary Zero-Downtime Deployment

Single-service deploys (`ssd deploy <service>`) use a canary strategy to avoid downtime.
Deploy-all (`ssd deploy` with no args) builds all images first, then does a single `docker compose up -d`.

**How it works:**
1. Start a temporary canary container (`{name}-canary`) with the new image alongside the old one
2. Both containers share the same Traefik labels, so Traefik load-balances between them
3. Wait for canary to pass health check (or reach `running` state if no healthcheck configured)
4. Regenerate compose.yaml with the new version (no canary entry)
5. Recreate the main service — canary covers traffic during the brief restart gap
6. Stop and remove canary

**Canary eligibility** (all must be true):
- Service is already running (first deploy uses direct start)
- Not in `BuildOnly` mode (deploy-all builds first, starts later)
- `AllServices` map is available (needed for compose regeneration)

**Health timeout**: Computed from healthcheck config (`retries * interval + 30s buffer`, max 5 min). Falls back to 30s if no healthcheck configured.

**Rollback on failure**: If canary fails health check, it is stopped and compose.yaml is restored to the pre-canary state. The old service is never touched.

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
    target: production          # Docker build target stage (optional)
    domain: example.com         # Enable Traefik routing
    path: /api                  # Path prefix routing (optional)
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

### Multi-domain configuration
```yaml
server: myserver

services:
  web:
    # Multiple working domains (no redirects)
    domains:
      - example.com
      - www.example.com
      - api.example.com
    port: 3000
```

### Multi-domain with redirects
```yaml
server: myserver

services:
  web:
    # All domains except redirect_to will redirect to it
    domains:
      - example.com
      - www.example.com
      - old.example.com
    redirect_to: example.com    # Optional: enables redirects to this domain
    port: 3000
```

Common redirect use cases:
- **www redirect**: `redirect_to: example.com` with domains `[example.com, www.example.com]`
- **Reverse www redirect**: `redirect_to: www.example.com` with domains `[www.example.com, example.com]`
- **Domain migration**: `redirect_to: new.com` with domains `[new.com, old.com, legacy.com]`
- **Multi-TLD consolidation**: `redirect_to: example.com` with domains `[example.com, example.net, example.org]`

Notes:
- `redirect_to` is optional - omit it to serve all domains without redirects
- When `redirect_to` is set, all other domains redirect to it with 302 temporary redirect (flexible, not cached)
- `redirect_to` must be one of the domains in the `domains` array
- Redirects preserve path and query parameters
- Works with both HTTPS and HTTP
- HTTPS redirects happen after TLS termination (certificates issued for all domains)
- Cannot use both `domain` and `domains` fields (mutually exclusive)

## Commands

### Initialize
```bash
ssd init                      # Interactive mode
ssd init -s myserver          # Non-interactive with flags
ssd init -s myserver --stack /dockge/stacks/myapp -d myapp.example.com -p 3000
```

### Deployment
```bash
ssd deploy [service]          # Deploy service (or all if omitted)
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
