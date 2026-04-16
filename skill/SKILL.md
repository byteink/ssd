---
name: ssd
description: Deploy and manage services on remote servers using ssd (SSH Deploy). Supports Docker Compose and K3s runtimes. Use when the user asks to deploy, restart, rollback, check status, view logs, or configure services.
argument-hint: "[command] [service] [args...]"
---

`ssd` deploys containerized stacks to remote servers via SSH. Supports Docker Compose and K3s runtimes. No agents — just rsync, build on server, and restart.

## Commands

```
ssd deploy|up [service]       # Deploy all or one service (rsync, build, version bump, restart)
ssd down [service]            # Stop services (or all if omitted)
ssd rm [service]              # Permanently remove services (or entire stack)
ssd restart <service>         # Restart without rebuilding
ssd rollback <service>        # Rollback to previous version
ssd status <service>          # Container status
ssd logs <service> [-f]       # View/follow logs
ssd config [service]          # Show resolved config
ssd env <service> set K=V     # Set env var on server
ssd env <service> list        # List env vars
ssd env <service> rm KEY      # Remove env var
ssd secret <service> set K=V  # Set K8s secret (k3s only)
ssd secret <service> list     # List secrets (k3s only)
ssd secret <service> rm KEY   # Remove secret (k3s only)
ssd prune                     # Remove orphaned services (default)
ssd prune --images            # Remove old image tags beyond retention
ssd prune --build-cache       # Prune build cache older than 168h
ssd prune --dangling          # Remove unreferenced images
ssd prune --all               # All of the above
ssd prune --keep N            # Override retention for --images/--all
ssd prune --dry-run           # Preview, combinable with any flag
ssd scale <service> <count>   # Live-scale a service (does not edit ssd.yaml)
ssd init [-s host] [-r runtime] [-d domain] [-p port]  # Generate ssd.yaml
ssd provision [--server S] [--email E] [--runtime R]    # Provision server
ssd provision check [--server S] [--runtime R]          # Verify server readiness
```

## Config (ssd.yaml)

Read `ssd.yaml` in the project root before running commands. Key structure:

```yaml
runtime: k3s                  # "compose" (default) or "k3s"
server: myserver              # SSH host from ~/.ssh/config
stack: /stacks/myapp          # Stack dir on server (default: /stacks/{name})
deploy:
  strategy: rollout           # "rollout" (zero-downtime) or "recreate" (brief downtime)

services:
  web:
    name: myapp-web           # Defaults to service key
    context: ./apps/web       # Build context (default: .)
    dockerfile: ./Dockerfile  # Dockerfile path
    target: production        # Multi-stage build target
    image: nginx:latest       # Pre-built image (skips build)
    domain: example.com       # Traefik routing (single)
    domains: [a.com, b.com]   # Traefik routing (multi, mutually exclusive with domain)
    redirect_to: a.com        # Redirect other domains to this one
    path: /api                # Path prefix routing
    https: true               # Default true
    port: 3000                # Container port, default 80
    ports: ["3000:3000"]      # Host:container port mappings (optional)
    depends_on: [db, redis]   # Or map with conditions (service_healthy, service_started)
    env_file: ./.env          # Upload local .env to {stack}/{service}.env on every deploy (mode 600)
                              # OVERWRITES values set via `ssd env set`. Remove to manage vars via CLI only.
    files:
      ./config.yaml: /app/config.yaml  # Local file -> container path (works with .gitignored files)
    volumes:
      pg-data: /var/lib/postgresql/data
    healthcheck:
      cmd: "curl -f http://localhost:3000/health || exit 1"
      interval: 30s
      timeout: 10s
      retries: 3
    deploy:
      strategy: recreate      # Per-service override
      replicas: 3             # default 1 (compose: requires `docker compose --compatibility`)
```

Root-level `server`, `stack`, and `deploy.strategy` are inherited by all services.
Traefik is only included when a service has `domain` or `domains` set. Services without domains can use `ports` for host access (Tailscale, Cloudflare tunnels).

## Workflow

1. Read `ssd.yaml` to understand what services exist and their config
2. Run the appropriate `ssd` command
3. If deploying, confirm which service(s) unless the user was specific
4. Check output for errors; on failure suggest `ssd logs <service> -f`

If `$ARGUMENTS` is provided, run: `ssd $ARGUMENTS`
