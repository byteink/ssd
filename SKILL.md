---
name: ssd
description: Deploy and manage Docker services on remote servers using ssd (SSH Deploy). Use when the user asks to deploy, restart, rollback, check status, view logs, or configure services.
argument-hint: "[command] [service] [args...]"
---

`ssd` deploys Docker Compose stacks to remote servers via SSH. No agents — just rsync, build on server, and restart.

## Commands

```
ssd deploy [service]          # Deploy all or one service (rsync, build, version bump, restart)
ssd restart <service>         # Restart without rebuilding
ssd rollback <service>        # Rollback to previous version
ssd status <service>          # Container status
ssd logs <service> [-f]       # View/follow logs
ssd config [service]          # Show resolved config
ssd env <service> set K=V     # Set env var on server
ssd env <service> list        # List env vars
ssd env <service> rm KEY      # Remove env var
ssd init [-s host] [-d domain] [-p port]  # Generate ssd.yaml
```

## Config (ssd.yaml)

Read `ssd.yaml` in the project root before running commands. Key structure:

```yaml
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
    volumes:
      pg-data: /var/lib/postgresql/data
    healthcheck:
      cmd: "curl -f http://localhost:3000/health || exit 1"
      interval: 30s
      timeout: 10s
      retries: 3
    deploy:
      strategy: recreate      # Per-service override
```

Root-level `server`, `stack`, and `deploy.strategy` are inherited by all services.
Traefik is only included when a service has `domain` or `domains` set. Services without domains can use `ports` for host access (Tailscale, Cloudflare tunnels).

## Workflow

1. Read `ssd.yaml` to understand what services exist and their config
2. Run the appropriate `ssd` command
3. If deploying, confirm which service(s) unless the user was specific
4. Check output for errors; on failure suggest `ssd logs <service> -f`

If `$ARGUMENTS` is provided, run: `ssd $ARGUMENTS`
