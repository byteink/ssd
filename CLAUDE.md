# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Rules

- **TDD is mandatory — red/green, every tier**: Write a failing test first, watch
  it fail (red), then write the minimum code to make it pass (green). This is a
  hard requirement and it applies to **all** test tiers — unit, integration,
  **and e2e**. No production behaviour change lands without a test that failed
  before it and passes after.
- **Release gate — the whole suite must be green**: Nothing is released until
  unit **and** integration **and** e2e all pass. Run `make test-all` locally
  before tagging a release (it runs unit + integration + full-fidelity e2e). A
  red test anywhere blocks the release, no exceptions.
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
make test              # Run unit tests
make lint              # Run linter (lints unit + integration + e2e sources)
go run .               # Run directly
./ssd version          # Test the binary
```

## Testing

Three tiers, each red/green TDD (see Development Rules). Lint runs over all
three (`make lint` passes `--build-tags 'integration e2e'`).

```bash
make test              # Unit — fast, no Docker. Runs in CI on every push/PR.
make test-integration  # Integration — real SSH/Docker in throwaway containers.
make test-e2e          # E2E, fast path — full deploy in a DinD sandbox.
make test-e2e-full     # E2E, full-fidelity path — local pre-release gate.
make test-all          # Unit + integration + full-fidelity e2e (release gate).
```

- **Unit** (`./...`, no tags): pure-Go, mocked executors. CI required check.
- **Integration** (`//go:build integration`): spins up real SSH / docker:dind
  containers via testcontainers and exercises `remote` against them (SSH,
  rsync-over-`git archive`, docker build). CI required check.
- **E2E** (`//go:build e2e`): runs the real `DeployWithClient` path against an
  **isolated docker-in-docker sandbox** — a privileged container running its
  own Docker daemon plus sshd. ssd SSHes in and does real `docker build` /
  `docker compose` / `docker rollout` entirely inside that container, so the
  **host Docker is never touched** and everything dies with the sandbox.
  Two modes:
  - **fast** (default, CI): recreate strategy, compose plugin only. ~4 min.
  - **full-fidelity** (`SSD_E2E_FULL=1`, local pre-release): provisions
    `docker-rollout` exactly as `ssd provision` does and drives the real
    zero-downtime rollout deploy. This is the gate to run before tagging.

Test infrastructure lives in `internal/testhelpers/` (DinD/SSH containers,
chaos executor, mocks, fixtures). The `SSHConfigExecutor` there must satisfy
`remote.CommandExecutor`; a compile-time guard in `containers_test.go` keeps the
gated suites from silently rotting when that interface changes.

CI (`.github/workflows/test.yml`): `unit-tests`, `integration-tests`, and
`e2e-tests` (fast path) all run on every push/PR, plus `lint` and the
cross-compile `build` matrix.

## Release

Uses goreleaser. Version is injected via ldflags (`-X main.version={{.Version}}`).

**Gate**: run `make test-all` first — unit + integration + full-fidelity e2e
must all be green before tagging. A red test anywhere blocks the release.

```bash
make test-all                           # Release gate — must be fully green
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
├── completion/
│   └── completion.go # Shell completion scripts + __complete dispatcher
├── ui/
│   └── reporter.go   # Deploy progress renderer (plain + pretty/tty)
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

## Deploy Output (ui package)

`ui.Reporter` renders deploy progress in two modes, picked by `ui.New(w)`
based on whether `w` is a tty:

- **Plain** (non-tty: CI, pipes, redirected logs): line-by-line transcript.
  Steps render as ` · name` on start and ` ✓ name  1.2s` / ` ✗ name  …`
  on end. Details are indented under the active step.
- **Pretty** (tty): Docker-style live block. The current step's header
  is repainted at 10Hz with a spinner and ticking elapsed time; on
  Done/Fail the line is frozen as ` ✓ name  1.2s` / ` ✗ name  …` and the
  next step starts. Completed steps and their details become permanent
  transcript above the live area. The live block is painted with **no
  trailing newline** so the cursor parks on its last line; the next repaint
  walks back with `\r` + `CSI nA` (up) and `CSI 0J` (erase to end of screen).
  Emitting a trailing newline while the block sits on the bottom screen row
  would scroll the terminal every repaint and dump each animation frame into
  scrollback. No `uilive`/`tcell` dep.

  **Lines are clamped to the terminal width** before painting
  (`ansi.Truncate` to `width-1`). The repaint walks the cursor up by
  `liveLines` *logical* lines, but the terminal lays out *physical*
  rows — any line wider than the screen wraps onto extra rows, the
  cursor-up count under-shoots, and every frame drifts down into
  scrollback. A single long buildkit line (`#15 go: downloading …`) is
  enough to corrupt the count; after that even the bare spinner header
  floods. Clamping guarantees one logical line == one physical row, so
  the up/erase math stays exact. Width is read live per paint via
  `term.GetSize` (resize-safe); `width 0` (non-sized tty) skips the clamp.

API: `Reporter` exposes `Header`, `Step(name) Step`, `Info`, `Warn`,
`Close`. `Step` exposes `Detail`, `Quiet`, `Done`, `Fail(err)`. Only
one step may be active at a time — starting a new step or calling
`Header`/`Info`/`Warn`/`Close` finalises any pending live area. All
methods are concurrency-safe.

**Stream tail window for noisy subprocesses.** Pretty mode redraws the
active step header in-place via cursor-up + clear-to-end. That collides
with any subprocess that writes its own lines to the same terminal
(docker buildkit's `#1`, `#2`, ... progress; `docker rollout`; `kubectl
rollout status`) — the spinner repaints and the subprocess output keeps
shoving it down, leaving duplicate headers.

`step.Stream(n) io.Writer` is the docker-build-style fix: subprocess
output goes through the returned writer, lines are stripped of ANSI
codes and pushed into a ring buffer of the last `n` lines, and the live
area paints as **spinner header + last n lines** indented beneath. On
Done/Fail the tail window vanishes, leaving only the final ✓/✗ line —
just like `[+] Building (15/15) FINISHED` collapsing in docker.

Plumbing: `Deployer.SetOutput(stdout, stderr io.Writer)` redirects the
client's `SSHInteractive` (and everything built on it: `BuildImage`,
`PullImage`, `StartService`, `RolloutService`) into the Stream writer.
`nil` restores `os.Stdout`/`os.Stderr`. The helper `withStreamedOutput`
in deploy.go wraps the set/restore + step lifecycle so callers stay
terse. Tail height is `streamTailLines = 4`.

Used in `deploy/deploy.go` for `BuildImage`, `PullImage` (prebuilt +
dependency), and `StartService`/`RolloutService` (main + dependency).
`Rsync` doesn't need it — `git archive | tar` is silent on success.

`step.Quiet()` still exists as a coarser alternative: it freezes the
header and lets subprocess output flow inline (no tail window, no
clipping). Use it when the subprocess output is short enough to want
fully visible.

`deploy.Options.Reporter` is the wiring seam. When nil, `DeployWithClient`
builds a plain reporter from `Options.Output` (kept for backwards
compatibility with existing tests that pass `io.Discard`). `main.go`
constructs `ui.New(os.Stdout)` for `ssd deploy` and `deployServiceBuildOnly`.

Restart/Rollback still use the simple `logf` writer path — they have
two or three output lines each and don't justify the reporter machinery.

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

## Config Layout

ssd resolves its config in this order (first match wins):

1. `--config <path>` — explicit override, no fallback
2. `.ssd/ssd.yaml` — preferred layout, keeps repo root clean
3. `ssd.yaml` — legacy layout, kept for back-compat

`ssd init` writes to `.ssd/ssd.yaml` and creates `.ssd/.gitignore`
(content: `.cache/`) on a fresh project. If `./ssd.yaml` already
exists, init keeps writing to that legacy path so existing projects
are not silently restructured.

### Environment overlays (`--env` / `-e`)

Optional sibling files alongside the base config provide per-env
overrides:

```
.ssd/
├── ssd.yaml          # base
├── ssd.dev.yaml      # ssd deploy --env dev
└── ssd.prod.yaml     # ssd deploy -e prod
```

The overlay is deep-merged onto the base at the YAML node level —
mappings recurse, scalars/sequences in the overlay replace the base.
A missing overlay file when `--env` is set is an error (typo guard).

### Generated artifacts

ssd writes generated/temporary files (build metadata, future k8s
runtime manifests, etc.) under a per-project cache directory, never
in the repo root or alongside the configs:

- `.ssd/.cache/` — when using the new layout
- `.ssd-cache/`  — when using the legacy `./ssd.yaml`

Today the compose and k3s runtimes don't materialise local artifacts
(manifests live on the server), so the cache dir is reserved for the
upcoming `runtime: k8s`. The path is exposed via
`config.CacheDir(configPath)`.

### Global flags

Accepted on every command (stripped before per-command parsers run):

- `--config <path>` — explicit config file path
- `--env <name>` / `-e <name>` — overlay name to apply

### Layout warnings and migration

When `--config` is not given, ssd prints a single nudge line to
stderr based on `config.DetectLayout()`:

- only `./ssd.yaml` exists → "using legacy ./ssd.yaml. Run `ssd migrate`…"
- both files exist → "both …exist; .ssd/ssd.yaml is being used. Delete ./ssd.yaml…"

`ssd migrate` moves `./ssd.yaml` into `.ssd/ssd.yaml` and seeds
`.ssd/.gitignore` (`.cache/`). Refuses to run when `.ssd/ssd.yaml`
already exists or there is no legacy file — the user must reconcile
manually so we never silently clobber a config.

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

### Replicas
```yaml
services:
  web:
    deploy:
      replicas: 3             # default 1
```

- **k3s**: written into the Deployment `spec.replicas` at
  `k8s/manifest.go`.
- **compose**: written into `services.<svc>.deploy.replicas` in the
  generated compose.yaml. Honored in non-swarm mode only when deploying
  with `docker compose --compatibility`; ssd does not add this flag
  automatically. For live scaling without persistence, use `ssd scale`.

### `ssd scale`
```bash
ssd scale <service> <count>
```

Live-scale a running service. Does NOT edit ssd.yaml (matches
`kubectl scale`). To persist, edit `deploy.replicas` in ssd.yaml.
- k3s: `k3s kubectl scale deployment/<svc> --replicas=<n>`
- compose: `docker compose up -d --scale <svc>=<n> --no-recreate`

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
- k3s: a Traefik `Middleware` CRD (`kind: Middleware`, `apiVersion: traefik.io/v1alpha1`) named `{service}-redirect` is emitted in the same namespace and referenced by the Ingress via `traefik.ingress.kubernetes.io/router.middlewares: {namespace}-{service}-redirect@kubernetescrd`
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

### Env file (overwrite-on-deploy)
```yaml
server: myserver

services:
  web:
    env_file: ./.env        # local path, relative to project root
```

On every deploy, the local file is uploaded (mode 600) to
`{stack}/{service}.env`, OVERWRITING any values previously set via
`ssd env set`. Validation: path must exist, must be a file (not a
directory), no `..` traversal, no shell metacharacters.

Works identically for both runtimes:
- **compose**: `compose.yaml` already points at `./{service}.env` via
  `env_file:` — the fresh content takes effect on container start.
- **k3s**: the `applyEnvConfigMap` step reads the uploaded file and
  syncs it into the `{service}-env` ConfigMap. The ConfigMap is owned
  exclusively by this step — it is intentionally NOT emitted in
  `manifests.yaml` to avoid an empty placeholder wiping the populated
  data via the last-applied-configuration diff on every deploy.

To manage env vars via CLI only, remove `env_file` from ssd.yaml.

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
ssd scale <service> <count>   # Live-scale a service (does not edit ssd.yaml)
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

For K3s runtime, env vars are translated to a ConfigMap on every deploy via
`k3s kubectl create configmap {service}-env --from-env-file={stack}/{service}.env --dry-run=client -o yaml | k3s kubectl apply -f -`,
issued before `kubectl apply` in both StartService and RolloutService.

If `env_file` is set in ssd.yaml, it OVERWRITES any values set via
`ssd env set` on every deploy. To manage env vars via CLI only, remove
`env_file` from ssd.yaml first.

### Secrets (k3s only)
```bash
ssd secret <service> set KEY=VALUE    # Set a K8s Secret
ssd secret <service> list             # List all secrets
ssd secret <service> rm KEY           # Remove a secret
```

K8s Secrets are injected as env vars alongside ConfigMap vars. Only available with `runtime: k3s`. Running `ssd secret` with compose runtime errors out.

### Prune
```bash
ssd prune                             # Remove orphaned services from server (default)
ssd prune --images                    # Remove old image tags beyond per-service retention
ssd prune --build-cache               # Prune build cache entries older than 168h
ssd prune --dangling                  # Remove unreferenced (dangling) images
ssd prune --all                       # Everything above
ssd prune --keep N                    # Override per-service retention for --images/--all
ssd prune --dry-run                   # Preview, combinable with any flag
```

No-flag `ssd prune` prunes orphans only (historical behavior preserved).
Compares ssd.yaml services against what's deployed; removes any not in config. Works with both runtimes. Deploy-all (`ssd deploy`) warns about orphans after deployment.

Runtime-specific commands:
- **compose**: `docker rmi`, `docker builder prune -af --filter until=168h`, `docker image prune -f`
- **k3s**: `nerdctl --namespace k8s.io rmi`, `sudo buildctl --addr unix:///run/buildkit/buildkitd.sock prune --keep-duration 168h`, `nerdctl --namespace k8s.io image prune -f`

Build cache pruning is **opt-in only** — never runs automatically on deploy.

### Cleanup / retention

```yaml
cleanup:
  retention: 2              # keep last N image tags per service (root default)

services:
  web:
    cleanup:
      retention: 5          # per-service override
```

- Default retention: **2** (current + rollback target)
- Minimum: **1** — negative values rejected
- `0` disables auto cleanup on deploy (explicit opt-out)
- Service-level value wins over root (even when 0)

After every successful deploy, ssd prunes image tags older than the configured retention for that service. Tag cleanup is **warn-only** — it never fails a deploy. Pre-built images (`image:` field) are never pruned — ssd doesn't manage those tags.

Build cache threshold is hard-coded to **168h** (7 days). Recent cache stays warm; only cold layers are reclaimed.

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

**Compose provision**: Installs Docker, Docker Compose, docker-rollout plugin, creates `traefik_web` network, starts Traefik with HTTPS via Let's Encrypt. Traefik is deployed with `--ping=true` and a Docker healthcheck (`traefik healthcheck --ping`).

**K3s provision**: Installs K3s, nerdctl + buildkit, configures nerdctl for K3s containerd socket (`/run/k3s/containerd/containerd.sock`, namespace `k8s.io`), installs buildkitd as systemd service, configures Traefik ACME via HelmChartConfig CRD.

All steps are idempotent.

### Skill
```bash
ssd skill                             # Interactive agent selection
ssd skill --path <dir>                # Symlink skill dir to custom path
```

Symlinks the bundled skill directory into your coding agent's skills folder. After `brew install ssd`, the skill lives at `$(brew --prefix)/share/ssd/skill/`. The symlink ensures the skill auto-updates on `brew upgrade`.

### Completion
```bash
ssd completion install                # auto-detects $SHELL
ssd completion install --shell zsh    # override detection
ssd completion bash|zsh|fish          # print script to stdout
```

Installed paths:
- bash → `~/.local/share/bash-completion/completions/ssd`
- zsh  → `~/.zsh/completions/_ssd` (user must add the dir to `fpath` before `compinit`)
- fish → `~/.config/fish/completions/ssd.fish` (auto-loaded)

The scripts are intentionally thin shims that call the hidden helper
`ssd __complete <prev-tokens>` on every TAB. The Go side
(`completion/completion.go`) holds the single source of truth for the
command layout and emits one candidate per line; the shell does
prefix filtering. Dynamic completions sourced from ssd.yaml today:
service names for `deploy/up/down/rm/restart/rollback/status/logs/config/scale/env/secret`.
Honors global `--config` / `--env` so completion in a multi-env repo
reflects the right overlay.
