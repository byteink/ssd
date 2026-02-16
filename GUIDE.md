# ssd - SSH Deploy Tool

**ssd** is a CLI tool that deploys Docker applications to remote servers over SSH. No agents, no registry, no complex setup — just SSH + Docker Compose.

---

## Prerequisites

- SSH access to your server (configured in `~/.ssh/config`)
- Docker and Docker Compose installed on the server
- `rsync` installed locally

---

## Quick Start

### 1. Install

```bash
curl -fsSL https://raw.githubusercontent.com/byteink/ssd/main/install.sh | bash
```

Or build from source:

```bash
git clone <repo> && cd ssd && make build
```

### 2. Initialize a project

Run this in your project directory (where your `Dockerfile` lives):

```bash
ssd init -s myserver -d myapp.example.com -p 3000
```

This creates an `ssd.yaml` file. You can also run `ssd init` interactively (no flags) and it will prompt you for each value.

### 3. Deploy

```bash
ssd deploy app
```

That's it. ssd will:
1. SSH into your server
2. Rsync your code over
3. Build the Docker image on the server
4. Auto-increment the version in `compose.yaml`
5. Run `docker compose up -d`
6. Clean up temp files

---

## Commands

| Command | Description |
|---|---|
| `ssd init` | Create `ssd.yaml` (interactive or with flags) |
| `ssd deploy [service]` | Build and deploy a service (or all if omitted) |
| `ssd restart <service>` | Restart without rebuilding |
| `ssd rollback <service>` | Roll back to the previous version |
| `ssd status <service>` | Check container status |
| `ssd logs <service> [-f]` | View logs (`-f` to follow/stream) |
| `ssd config [service]` | Show resolved configuration |
| `ssd env <service> set K=V` | Set an environment variable |
| `ssd env <service> list` | List environment variables |
| `ssd env <service> rm KEY` | Remove an environment variable |
| `ssd version` | Print version |

---

## Configuration (`ssd.yaml`)

### Minimal — single service

```yaml
server: myserver

services:
  app: {}
```

Defaults: name=`app`, stack=`/stacks/app`, context=`.`, dockerfile=`./Dockerfile`, port=`80`.

### With a domain and custom port

```yaml
server: myserver

services:
  app:
    domain: myapp.example.com
    port: 3000
```

This auto-configures Traefik routing with HTTPS (Let's Encrypt).

### Monorepo — multiple services sharing a stack

```yaml
server: myserver
stack: /stacks/myproject

services:
  web:
    context: ./apps/web
    dockerfile: ./apps/web/Dockerfile
    domain: example.com
    port: 3000

  api:
    context: ./apps/api
    dockerfile: ./apps/api/Dockerfile
    domain: api.example.com
    port: 8080
    depends_on:
      - db

  db:
    image: postgres:16-alpine
    volumes:
      postgres-data: /var/lib/postgresql/data
```

### Path-based routing (multiple services on one domain)

```yaml
server: myserver
stack: /stacks/myapp

services:
  api:
    context: ./apps/api
    domain: example.com
    path: /api
    port: 8080

  dashboard:
    context: ./apps/dashboard
    domain: example.com
    path: /dashboard
    port: 3000
```

Requests to `example.com/api/*` route to the api service, `example.com/dashboard/*` to the dashboard service. The path prefix is stripped before reaching the container.

### Pre-built image (no build step)

```yaml
server: myserver

services:
  nginx:
    image: nginx:latest
    domain: example.com
```

When `image` is set, ssd pulls it instead of building.

---

## Config Reference

### Root-level (inherited by all services)

| Field | Description |
|---|---|
| `server` | SSH host name (from `~/.ssh/config`) |
| `stack` | Default stack directory on server |

### Service-level

| Field | Default | Description |
|---|---|---|
| `name` | service key | Service name |
| `stack` | `/stacks/{name}` | Stack directory on server |
| `context` | `.` | Docker build context |
| `dockerfile` | `./Dockerfile` | Path to Dockerfile |
| `image` | — | Pre-built image (skips build) |
| `domain` | — | Domain for Traefik routing |
| `path` | — | Path prefix for routing (e.g., `/api`). Requires `domain` |
| `https` | `true` | Enable HTTPS via Let's Encrypt |
| `port` | `80` | Container port |
| `depends_on` | — | Service dependencies (list or map with conditions) |
| `volumes` | — | Named volumes (`name: mount_path`) |
| `healthcheck` | — | Health check (cmd, interval, timeout, retries) |

---

## How Deploy Works

```
Local machine                        Remote server
─────────────                        ─────────────
ssd deploy app
  │
  ├─ Read ssd.yaml
  ├─ SSH connect ──────────────────► Create temp dir
  ├─ rsync code ───────────────────► /tmp/ssd-xxxxx/
  │                                  docker build → ssd-project-app:4
  │                                  Update compose.yaml (v3 → v4)
  │                                  docker compose up -d
  │                                  Remove temp dir
  └─ Done ✓
```

- Versions auto-increment (parsed from `compose.yaml`)
- Dependencies start first if not already running
- A lock file prevents concurrent deploys to the same stack

---

## Common Workflows

**First deploy:**
```bash
ssd init -s myserver -d myapp.com -p 3000
ssd deploy app
```

**Update after code changes:**
```bash
ssd deploy app
```

**Something broke — roll back:**
```bash
ssd rollback app
```

**Check what's running:**
```bash
ssd status app
ssd logs app -f
```

**Set an env var:**
```bash
ssd env app set DATABASE_URL=postgres://...
ssd deploy app   # redeploy to pick it up
```
