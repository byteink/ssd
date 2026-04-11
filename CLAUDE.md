# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Rules

- **TDD**: Write tests first, then implementation
- **Never weaken tests**: Fix code, not tests
- **Never relax linting**: Fix errors, don't disable rules or use `_ =`
- **CLAUDE.md is the source of truth**: This file must always reflect the current state of the app. Every feature addition, removal, or change must include a CLAUDE.md update in the same changeset. If the code and CLAUDE.md disagree, the code is wrong or CLAUDE.md is stale — fix whichever is behind. Never merge a change that leaves CLAUDE.md out of sync.
- **Help text is the user interface**: Every command, subcommand, and flag must have accurate, complete `-h`/`--help` output. When adding or changing any feature, update the help text (descriptions, usage strings, examples) in the same commit. Help text is not optional documentation — it is the primary way users discover and understand the tool. Outdated or missing help text is a bug.
- **README.md stays current**: When adding/changing features, update `README.md` alongside code and CLAUDE.md

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
├── compose/
│   └── compose.go    # Docker Compose YAML generation
├── k8s/
│   └── manifest.go   # K8s manifest YAML generation (for k3s runtime)
├── runtime/
│   ├── runtime.go    # Runtime factory (compose or k3s)
│   └── k3s/
│       ├── client.go # K3s remote client (nerdctl/kubectl)
│       └── secret.go # K8s secret management
├── provision/
│   └── provision.go  # Server provisioning (Docker or K3s)
├── scaffold/
│   └── scaffold.go   # ssd init command (generate ssd.yaml)
├── skill/
│   └── SKILL.md      # Claude Code skill file (installed via ssd skill)
└── .goreleaser.yaml  # Release config (bundles skill/ in brew install)
```

## Runtime

ssd supports two runtimes, selected via `runtime` field in ssd.yaml:

- **compose** (default): Docker Compose + Traefik. Builds with `docker build`, deploys with `docker compose`.
- **k3s**: Kubernetes via K3s. Builds with `nerdctl build`, deploys with `kubectl apply`. K3s ships Traefik as ingress controller.

The runtime factory (`runtime/runtime.go`) selects the right client implementation. All ssd commands work identically across runtimes — the user experience doesn't change.

## Core Workflow

### Compose runtime
1. Read `ssd.yaml` config from current directory
2. SSH into configured server (uses `~/.ssh/config` hosts)
3. Create temp directory on server
4. Rsync code to temp dir (via git archive)
5. Build Docker image on server: `ssd-{name}:{version}`
6. Parse current version from compose.yaml, increment it
7. Start service using configured strategy (`docker rollout` or `--force-recreate`)
8. Clean up temp directory

### K3s runtime
1. Read `ssd.yaml` config from current directory
2. SSH into configured server
3. Create temp directory on server
4. Rsync code to temp dir (via git archive)
5. Ensure buildkitd is running, build image with `nerdctl --namespace k8s.io build`
6. Parse current version from manifests.yaml, increment it
7. Generate K8s manifests, apply with `kubectl apply`
8. Wait for rollout: `kubectl rollout status`
9. Clean up temp directory

## Deployment Strategy

Configurable via `deploy.strategy` in ssd.yaml. Two strategies:
- **rollout** (default): Zero-downtime. Compose: `docker rollout` plugin. K3s: native K8s `RollingUpdate`.
- **recreate**: In-place replacement. Compose: `docker compose up --force-recreate`. K3s: K8s `Recreate` strategy.

Strategy is set at root level and inherited by services. Per-service override supported.
Deploy-all (`ssd deploy` with no args) builds all images first, then deploys each service using its configured strategy.

## Conventions

- **Stack path**: Full path to stack directory containing compose.yaml (default: `/stacks/{name}`)
- **Image naming**: `ssd-{project}-{name}:{version}` where project is extracted from stack path
- **Version tracking**: Parsed from compose.yaml image tag, auto-incremented on deploy
- **Config inheritance**: Root-level `server` and `stack` are inherited by services
- **Services-only mode**: All configs must use `services:` map (single-service mode removed)
- **Runtime**: `compose` (default) or `k3s`, set via `runtime:` field in ssd.yaml
- **K3s namespace**: One namespace per stack, derived from stack path basename (`/stacks/myapp` → `myapp`)
- **K3s manifests**: Single `manifests.yaml` in stack dir, all K8s resources separated by `---`
- **K3s builds**: `nerdctl --namespace k8s.io build` (images land directly in K3s containerd)

## ssd.yaml Patterns

### K3s runtime
```yaml
runtime: k3s
server: myserver

services:
  web:
    domain: example.com
    port: 3000
```

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
    ports:                          # Host:container port mappings (optional)
      - "3000:3000"
      - "8080:80"
    depends_on:                     # Simple list or map with conditions
      - db
      - redis
    files:
      ./config.yaml: /app/config.yaml  # Local file -> container path
    volumes:
      postgres-data: /var/lib/postgresql/data
      redis-data: /data
    healthcheck:
      cmd: "curl -f http://localhost:3000/health || exit 1"
      interval: 30s
      timeout: 10s
      retries: 3
```

### Deploy strategy
```yaml
server: myserver
deploy:
  strategy: rollout           # "rollout" (default) or "recreate"

services:
  web:
    # Inherits rollout from root
  worker:
    deploy:
      strategy: recreate      # Per-service override
```

### Dependency health conditions
```yaml
server: myserver

services:
  web:
    depends_on:
      db:
        condition: service_healthy
      redis:
        condition: service_started
```

Conditions: `service_started` (default), `service_healthy` (requires healthcheck), `service_completed_successfully`.

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

### Internal-only service (no Traefik)
```yaml
server: myserver

services:
  app:
    ports:
      - "3000:3000"             # Expose on host for Tailscale/CF tunnel
```

When no `domain` or `domains` is set, the service is deployed without Traefik labels or the `traefik_web` network. Use `ports` to map host:container ports for access via Tailscale, Cloudflare tunnels, or direct host access.

### Port mapping
```yaml
server: myserver

services:
  web:
    domain: example.com         # Traefik routing
    ports:
      - "9090:9090"             # Additional port exposure alongside Traefik
```

`ports` maps directly to Docker Compose `ports:`. Each entry is `host:container` format. Works independently of domain/Traefik configuration.

### Config files
```yaml
server: myserver

services:
  api:
    files:
      ./config.yaml: /app/config.yaml
      ./certs/ca.pem: /etc/ssl/ca.pem
```

`files` copies local files to the stack directory on the server and bind-mounts them into the container. Keys are local relative paths, values are absolute container mount paths.

- Files are transferred via SSH on every deploy, independent of git tracking (works with .gitignored files)
- Files are placed in the stack directory using their basename (e.g., `./config.yaml` becomes `/stacks/project/config.yaml`)
- Generates bind mounts in compose.yaml: `./config.yaml:/app/config.yaml`
- Local paths can be relative or absolute (for files outside the project directory)
- Relative local paths must not contain `..` traversal
- Basenames must be unique across all files in a service

## Commands

### Initialize
```bash
ssd init                      # Interactive mode (prompts for runtime, server, etc.)
ssd init -s myserver          # Non-interactive with flags
ssd init -s myserver -r k3s   # K3s runtime
ssd init -s myserver --stack /dockge/stacks/myapp -d myapp.example.com -p 3000
```

### Deployment
```bash
ssd deploy|up [service]       # Deploy service (or all if omitted)
ssd down [service]            # Stop services (or all if omitted)
ssd rm [service]              # Permanently remove services (or entire stack)
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

Environment variables are stored in `{service}.env` files on the server inside the stack directory (e.g., `/stacks/myapp/web.env`). Files are created automatically on first deploy with mode 600. Changes require `ssd restart <service>` to take effect.

For K3s runtime, env vars are translated to a ConfigMap on deploy.

### Secrets (k3s only)
```bash
ssd secret <service> set KEY=VALUE    # Set a K8s Secret
ssd secret <service> list             # List all secrets
ssd secret <service> rm KEY           # Remove a secret
```

K8s Secrets are injected as env vars alongside ConfigMap vars. Only available with `runtime: k3s`. Running `ssd secret` with compose runtime errors out.

### Prune
```bash
ssd prune                             # Remove orphaned services from server
ssd prune --dry-run                   # Preview without removing
```

Compares ssd.yaml services against what's deployed on the server. Removes orphans. Works with both runtimes. Deploy-all (`ssd deploy`) warns about orphans after deployment.

### Provision
```bash
ssd provision                         # Provision server (reads runtime from ssd.yaml)
ssd provision --server myserver       # Specify server explicitly
ssd provision --runtime k3s           # K3s provisioning
ssd provision --email admin@x.com     # Provide Let's Encrypt email via flag
ssd provision check                   # Verify server readiness
ssd provision check --server myserver # Check a specific server
ssd provision check --runtime k3s     # Check K3s readiness
```

**Compose provision**: Installs Docker, Docker Compose, docker-rollout plugin, creates `traefik_web` network, starts Traefik with HTTPS via Let's Encrypt.

**K3s provision**: Installs K3s, nerdctl + buildkit, configures nerdctl for K3s containerd socket (`/run/k3s/containerd/containerd.sock`, namespace `k8s.io`), installs buildkitd as systemd service, configures Traefik ACME via HelmChartConfig CRD.

All steps are idempotent.

### Skill
```bash
ssd skill                             # Interactive agent selection
ssd skill --path <dir>                # Symlink skill dir to custom path
```

Symlinks the bundled skill directory into your coding agent's skills folder. After `brew install ssd`, the skill lives at `$(brew --prefix)/share/ssd/skill/`. The symlink ensures the skill auto-updates on `brew upgrade`.
