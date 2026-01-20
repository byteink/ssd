# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

## Project Overview

`ssd` (SSH Deploy) is an opinionated CLI tool for deploying Docker Compose applications to remote servers via SSH. No agents, no image registries—just rsync code to server, build there, restart stack.

### Core Workflow
1. Read `ssd.yaml` config from current directory
2. SSH into configured server
3. Rsync code to temp directory on server
4. Build Docker image on server
5. Auto-increment build number
6. Update compose.yaml with new image tag
7. Restart the stack

### Conventions
- **Stack path**: `{stack}/{name}/` where `stack` defaults to `/stacks` (legacy servers may use `/dockge/stacks`)
- **Stack structure**: `compose.yaml` + `data/` directory for persistent data
- **Image naming**: `ssd-{name}:{version}`
- **SSH config**: Uses `~/.ssh/config` hosts directly
- **Config file**: `ssd.yaml` in project root

### Expected ssd.yaml Structure
```yaml
# Simple project
server: myserver        # SSH host from ~/.ssh/config
name: myapp             # Optional, defaults to directory name
stack: /stacks          # Optional, defaults to /stacks

# Monorepo
services:
  api:
    server: myserver
    dockerfile: ./api/Dockerfile
    context: ./api
  web:
    server: myserver
    dockerfile: ./web/Dockerfile
    context: ./web
```

### Current State
Skeleton CLI with command structure in place. Core deployment logic not yet implemented.
