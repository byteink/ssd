# ssd - SSH Deploy

Agentless remote deployment tool for Docker Compose stacks.

## What is ssd?

`ssd` is a lightweight CLI tool that simplifies deploying Docker applications to remote servers via SSH. No agents, no complex setup—just SSH access and Docker Compose.

## Features

- **Simple**: Convention-over-configuration approach
- **Flexible**: Works with monorepos and simple projects
- **Agentless**: Only requires SSH access and Docker on the server
- **Smart**: Auto-increments build numbers
- **Fast**: Builds on the server, no image registry needed

## Installation

```bash
# Homebrew
brew tap byteink/tap
brew install ssd

# Go install
go install github.com/byteink/ssd@latest
```

## Quick Start

1. Add `ssd.yaml` to your project:
```yaml
server: myserver
```

2. Deploy:
```bash
ssd deploy
```

That's it! `ssd` will:
- Sync your code to the server via rsync
- Build the Docker image on the server
- Auto-increment the version number
- Update compose.yaml and restart the stack

## Configuration

### Minimal (uses smart defaults):
```yaml
# ssd.yaml
server: myserver
```

Defaults:
- `name`: Current directory name
- `stack`: `/stacks/{name}`
- `image`: `ssd-{name}:{version}`
- `dockerfile`: `./Dockerfile`
- `context`: `.`

### Custom configuration:
```yaml
# ssd.yaml
server: myserver
name: my-app
stack: /custom/stacks/my-app
```

### Monorepo support:
```yaml
# ssd.yaml
server: myserver
stack: /stacks/myproject

services:
  web:
    name: myproject-web
    context: ./apps/web
  api:
    name: myproject-api
    context: ./apps/api
```

Deploy specific service:
```bash
ssd deploy web
```

## Commands

```bash
ssd deploy [service]     # Deploy application (build + restart)
ssd restart [service]    # Restart stack without rebuilding
ssd rollback [service]   # Rollback to previous version
ssd status [service]     # Check deployment status
ssd logs [service] [-f]  # View logs (-f to follow)
ssd config [service]     # Show current configuration
ssd version              # Show version
ssd help                 # Show help
```

## How It Works

1. Reads `ssd.yaml` from current directory
2. SSHs into the configured server (uses `~/.ssh/config`)
3. Rsyncs code to a temp directory (excludes .git, node_modules, .next)
4. Builds Docker image on the server
5. Parses current version from compose.yaml, increments it
6. Updates compose.yaml with new image tag
7. Runs `docker compose up -d` to restart the stack
8. Cleans up temp directory

## Requirements

- SSH access to target server (configured in `~/.ssh/config`)
- Docker and Docker Compose on the server
- A `compose.yaml` already set up in the stack directory
- rsync installed locally

## License

MIT

## Author

Built by [ByteInk](https://github.com/byteink)
