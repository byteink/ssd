# ssd - SSH Deploy

Agentless remote deployment tool for Docker Compose and K3s.

## What is ssd?

`ssd` is a lightweight CLI tool that simplifies deploying containerized applications to remote servers via SSH. Supports both Docker Compose and K3s (Kubernetes) runtimes. No agents, no complex setup—just SSH access.

## Features

- **Simple**: Convention-over-configuration approach
- **Flexible**: Works with monorepos and simple projects
- **Agentless**: Only requires SSH access
- **Dual runtime**: Docker Compose or K3s — same ssd.yaml, same commands
- **Smart**: Auto-increments build numbers
- **Fast**: Builds on the server, no image registry needed
- **Reliable**: Zero-downtime deployments with automatic version tracking

## Installation

**Quick install (Linux/macOS)**
```bash
curl -sSL https://raw.githubusercontent.com/byteink/ssd/main/install.sh | sh
```

**Homebrew (macOS/Linux)**
```bash
brew install byteink/tap/ssd
```

**Go**
```bash
go install github.com/byteink/ssd@latest
```

**Linux packages**

Download from [Releases](https://github.com/byteink/ssd/releases/latest):
- Debian/Ubuntu: `ssd_*_linux_amd64.deb`
- RHEL/Fedora: `ssd_*_linux_amd64.rpm`

**Windows**

Download `ssd_Windows_x86_64.zip` from [Releases](https://github.com/byteink/ssd/releases/latest), extract, and add to PATH.

## Quick Start

1. Initialize your project:
```bash
# Interactive mode
ssd init

# Or with flags
ssd init -s myserver -d myapp.example.com -p 3000
```

2. Deploy:
```bash
ssd deploy app
```

That's it! `ssd` will:
- Sync your code to the server via rsync
- Build the container image on the server
- Auto-increment the version number
- Update compose.yaml (or K8s manifests for k3s) and restart the stack

### K3s Quick Start

```bash
# Initialize with K3s runtime
ssd init -s myserver -r k3s -d myapp.example.com -p 3000

# Provision the server (installs K3s, nerdctl, buildkit, configures Traefik)
ssd provision

# Deploy
ssd deploy app
```

## Configuration

### Minimal (single service):
```yaml
# ssd.yaml
server: myserver
services:
  app:
    # name defaults to service key ("app")
    # stack defaults to /stacks/app
```

### Custom configuration:
```yaml
# ssd.yaml
server: myserver
stack: /custom/stacks/myapp   # Shared by all services

services:
  web:
    name: myapp-web
    context: ./apps/web
    dockerfile: ./apps/web/Dockerfile
```

### Monorepo with multiple services:
```yaml
# ssd.yaml
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

Deploy specific service:
```bash
ssd deploy web
```

### Full-featured service with all options:
```yaml
# ssd.yaml
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

### Config files

```yaml
server: myserver

services:
  api:
    files:
      ./config.yaml: /app/config.yaml             # relative to project
      /opt/shared/ca.pem: /etc/ssl/ca.pem          # absolute path outside project
```

Copy local files to the stack directory and bind-mount into the container. Files are transferred via SSH on every deploy, independent of git tracking (works with `.gitignore`d files). Relative paths resolve from the working directory where `ssd` is run. Absolute paths work for files outside the project. Basenames must be unique per service.

### Dependency health conditions:
```yaml
# ssd.yaml
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

### Internal-only service (no Traefik):
```yaml
# ssd.yaml
server: myserver

services:
  app:
    ports:
      - "3000:3000"             # Expose on host for Tailscale/CF tunnel
```

When no `domain` or `domains` is set, the service is deployed without Traefik labels or the `traefik_web` network. Use `ports` to map host:container ports for access via Tailscale, Cloudflare tunnels, or direct host access.

### Using pre-built images (skip build):
```yaml
# ssd.yaml
server: myserver

services:
  nginx:
    image: nginx:latest        # Use pre-built image, skip build step
    domain: example.com
```

### Multi-domain configuration (no redirects):
```yaml
# ssd.yaml
server: myserver

services:
  web:
    domains:
      - example.com
      - www.example.com
      - api.example.com
    port: 3000
```

All domains work independently, no redirects. Useful for multi-brand apps, different locales, or A/B testing.

### Multi-domain with automatic redirects:
```yaml
# ssd.yaml
server: myserver

services:
  web:
    domains:
      - example.com
      - www.example.com
      - old-domain.com
    redirect_to: example.com    # All other domains redirect to this
    port: 3000
```

When `redirect_to` is set, all other domains automatically redirect to it with a 302 temporary redirect. Common use cases:
- **www redirect**: Redirect www to non-www (or vice versa)
- **Domain migration**: Redirect old domains to new primary domain
- **Multi-TLD consolidation**: Redirect .net, .org to primary .com

### Full stack example (API + Database):
```yaml
# ssd.yaml
server: myserver
stack: /stacks/myapp

services:
  api:
    context: ./apps/api
    dockerfile: ./apps/api/Dockerfile
    domain: api.example.com
    port: 8080
    depends_on:
      - db
    healthcheck:
      cmd: "curl -f http://localhost:8080/health || exit 1"
      interval: 30s
      timeout: 10s
      retries: 3

  db:
    image: postgres:16-alpine
    volumes:
      postgres-data: /var/lib/postgresql/data
    healthcheck:
      cmd: "pg_isready -U postgres"
      interval: 10s
      timeout: 5s
      retries: 5
```

### Configuration Fields

**Service-level fields:**
- `name`: Service name (defaults to service key)
- `stack`: Path to stack directory on server (defaults to `/stacks/{name}`)
- `context`: Build context path (defaults to `.`)
- `dockerfile`: Dockerfile path (defaults to `./Dockerfile`)
- `image`: Pre-built image to use (skips build step if specified)
- `target`: Docker build target stage for multi-stage builds (e.g., `production`)
- `domain`: Single domain for Traefik routing
- `domains`: Multiple domains for Traefik routing. Cannot use both `domain` and `domains`
- `redirect_to`: When set, all domains except this one redirect to it (302 temporary). Must be one of the domains in `domains` array
- `path`: Path prefix for routing (e.g., `/api`). Requires `domain` or `domains`. Generates `PathPrefix` rule with `StripPrefix` middleware
- `https`: Enable HTTPS (default: `true`)
- `port`: Container port (default: `80`)
- `ports`: Host:container port mappings (e.g., `["3000:3000"]`). Maps directly to Docker Compose `ports:`
- `depends_on`: Service dependencies (list or map with conditions)
- `volumes`: Map of volume names to mount paths
- `files`: Map of local file paths to container mount paths. Copied to stack directory and bind-mounted on every deploy. Works with `.gitignore`d files
- `healthcheck`: Health check configuration
  - `cmd`: Health check command
  - `interval`: Check interval (e.g., `30s`)
  - `timeout`: Command timeout (e.g., `10s`)
  - `retries`: Number of retries before unhealthy

**Root-level fields:**
- `server`: SSH server name (from `~/.ssh/config`)
- `stack`: Default stack path for all services

## Commands

### Initialize
```bash
ssd init                      # Interactive mode
ssd init -s myserver          # Non-interactive with flags
```

**Flags:**
- `-s, --server` - SSH host name (required in non-interactive mode)
- `--stack` - Stack path (e.g., `/dockge/stacks/myapp`)
- `--service` - Service name (default: `app`)
- `-d, --domain` - Domain for Traefik routing
- `--path` - Path prefix for routing (e.g., `/api`)
- `-p, --port` - Container port
- `-f, --force` - Overwrite existing `ssd.yaml`

### Deployment
```bash
ssd deploy|up [service]       # Deploy service (or all if omitted)
ssd down [service]            # Stop services (or all if omitted)
ssd rm [service]              # Permanently remove services (or entire stack)
ssd restart <service>         # Restart without rebuilding
ssd rollback <service>        # Rollback to previous version
ssd status <service>          # Check container status
ssd logs <service> [-f]       # View logs, -f to follow
ssd scale <service> <count>   # Live-scale a service (does not edit ssd.yaml)
```

### Replicas & scaling

Set a persistent replica count in ssd.yaml:

```yaml
services:
  web:
    deploy:
      replicas: 3    # default 1
```

- **k3s**: written to Deployment `spec.replicas` and applied on deploy.
- **compose**: written to `services.<svc>.deploy.replicas`. Docker
  Compose v2 honors this in non-swarm mode only when deploying with
  `docker compose --compatibility`. ssd does NOT add this flag;
  document it in your own deploy wrapper if you need >1 replica
  persisted across restarts. For ephemeral scaling, use `ssd scale`.

Live-scale without editing ssd.yaml (matches `kubectl scale`):

```bash
ssd scale web 3
ssd scale worker 0    # scale down to zero
```

**Deploy behavior:**
- With no argument, deploys all services in alphabetical order
- With a service name, deploys that single service
- Dependencies are started first (respects `depends_on`)
- Example: `ssd deploy api` will also start `db` if `api` depends on it

### Configuration
```bash
ssd config                    # Show all services config
ssd config <service>          # Show specific service config
```

### Environment Variables
```bash
ssd env <service> set KEY=VALUE      # Set environment variable
ssd env <service> list               # List all environment variables
ssd env <service> rm KEY             # Remove environment variable
```

**Note**: Environment variables are stored in `{service}.env` files in the stack directory on the server. For k3s, they are synced into a `{service}-env` ConfigMap on every deploy.

#### env_file (overwrite-on-deploy)

```yaml
services:
  web:
    env_file: ./.env    # local path, relative to project root
```

When `env_file` is set, the local file is uploaded to
`{stack}/{service}.env` (mode 600) on every deploy. This **overwrites**
any values set via `ssd env set`. To manage env vars via CLI only, remove
`env_file` from ssd.yaml first.

### Server Provisioning
```bash
ssd provision                                         # Provision server from ssd.yaml
ssd provision --server myserver --email admin@x.com   # Explicit server and email
ssd provision check                                   # Verify server readiness
ssd provision check --server myserver                 # Check a specific server
```

Provisions the target server with:
- Docker and Docker Compose installation
- docker-rollout plugin for zero-downtime deploys
- Traefik reverse proxy with automatic HTTPS (Let's Encrypt), `--ping` endpoint, and Docker healthcheck
- `traefik_web` Docker network for service discovery

All steps are idempotent and safe to run multiple times.

`provision check` verifies that Docker, Docker Compose, docker-rollout, the traefik_web network, and Traefik are all present and running.

### Other
```bash
ssd version              # Show version
ssd help                 # Show help
```

## How It Works

1. Reads `ssd.yaml` from current directory
2. SSHs into the configured server (uses `~/.ssh/config`)
3. Rsyncs code to a temp directory (excludes .git, node_modules, .next)
4. Builds Docker image on the server (or skips if using pre-built `image`)
5. Parses current version from compose.yaml, increments it
6. Recreates the service with `docker compose up -d --force-recreate`
7. Cleans up temp directory

## Requirements

- SSH access to target server (configured in `~/.ssh/config`)
- Docker and Docker Compose on the server
- A `compose.yaml` already set up in the stack directory
- rsync installed locally

## Development

```bash
# Clone and setup
git clone https://github.com/byteink/ssd.git
cd ssd
make setup    # Configures git hooks for linting

# Build and test
make build    # Build binary
make test     # Run tests
make lint     # Run linter
```

## Claude Code Skill

ssd ships with a [Claude Code](https://docs.anthropic.com/en/docs/claude-code) skill that lets Claude deploy and manage your services via the `/ssd` slash command.

### Setup

```bash
ssd skill
```

This symlinks the bundled skill directory into `~/.claude/skills/ssd`. The skill auto-updates whenever you run `brew upgrade ssd`.

### Usage

```
/ssd deploy web
/ssd status api
/ssd logs web -f
/ssd rollback api
```

Or ask Claude naturally and it will use `ssd` when appropriate.

## License

MIT

## Author

Built by [ByteInk](https://github.com/byteink)
