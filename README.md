# ssd - SSH Deploy

Agentless remote deployment tool for Docker Compose stacks.

## What is ssd?

`ssd` is a lightweight CLI tool that simplifies deploying Docker applications to remote servers via SSH. No agents, no complex setup—just SSH access and Docker Compose.

## Features

- 🚀 **Simple**: Convention-over-configuration approach
- 🔧 **Flexible**: Works with monorepos and simple projects
- 📦 **Agentless**: Only requires SSH access
- 🎯 **Smart**: Auto-increments build numbers
- 🔄 **Fast**: Builds on the server, no image pushing needed

## Installation

```bash
# Homebrew (coming soon)
brew install ssd

# Go install
go install github.com/byteink/ssd@latest

# Binary download
# Coming soon...
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
- Sync your code to the server
- Build the Docker image
- Auto-increment version number
- Update and restart your stack

## Configuration

### Minimal (uses smart defaults):
```yaml
# ssd.yaml
server: myserver
```

Defaults:
- `name`: Current directory name
- `stack`: `/stacks/{name}`
- `container`: `{name}-app-1`
- `image`: `ssd-{name}`
- `dockerfile`: `./Dockerfile`
- `context`: `.`

### Custom configuration:
```yaml
# ssd.yaml
name: my-app
server: myserver
stack: /custom/path
container: custom-container-1
image: custom-image-name
dockerfile: ./custom/Dockerfile
context: ./apps/web
```

### Monorepo support:
```yaml
# ssd.yaml
services:
  web:
    server: myserver
    dockerfile: ./apps/web/Dockerfile
    context: ./apps/web
  api:
    server: myserver
    dockerfile: ./apps/api/Dockerfile
    context: ./apps/api
```

## Commands

```bash
ssd deploy [service]     # Deploy application
ssd status [service]     # Check deployment status
ssd logs [service]       # View logs
ssd config               # Show current configuration
```

## Development Status

🚧 **Work in Progress** - Core features being implemented.

## License

MIT

## Author

Built with ❤️ by [ByteInk](https://github.com/byteink)
