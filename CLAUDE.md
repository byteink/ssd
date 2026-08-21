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
│   ├── deploy.go     # Deploy orchestration
│   ├── preflight.go  # Local pre-flight: pre_deploy hooks + require_clean check
│   ├── buildargs.go  # build_args/build_secrets resolution (${secret:}/${env:})
│   └── redact.go     # Masks resolved build-arg/secret values in build output
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
1b. Local pre-flight: `pre_deploy` hooks, then the `require_clean` check
2. SSH into configured server (uses `~/.ssh/config` hosts)
3. Create temp directory on server
4. Rsync code to temp dir (via git archive)
4b. Resolve `build_args` / `build_secrets` — missing key aborts here
5. Build Docker image on server: `ssd-{name}:{version}` (with `--build-arg`)
6. Parse current version from compose.yaml, increment it
7. Start service using configured strategy (`docker rollout` or `--force-recreate`)
8. Clean up temp directory

### K3s runtime
1. Read `ssd.yaml` config from current directory
1b. Local pre-flight: `pre_deploy` hooks, then the `require_clean` check
2. SSH into configured server
3. Create temp directory on server
4. Rsync code to temp dir (via git archive)
4b. Resolve `build_args` / `build_secrets` — missing key aborts here
5. Ensure buildkitd is running, build image with `nerdctl --namespace k8s.io build`
   (plus `--build-arg` flags)
6. Parse current version from manifests.yaml, increment it
7. Generate K8s manifests, apply with `kubectl apply`
8. Wait for rollout: `kubectl rollout status`
9. Clean up temp directory

## Local Pre-flight (`deploy/preflight.go`)

`Rsync` ships the build context as `git archive HEAD`, so **uncommitted tracked
changes are never deployed** — and without a check that failure mode is silent:
the server rebuilds the previous commit and the deploy exits 0. Two config
fields close that hole. Both are per-service and settable at root for
inheritance (same shape as `deploy:` / `cleanup:`).

```yaml
services:
  website:
    require_clean: true
    pre_deploy:
      - sh advisories.sh
      - make gen
```

- **`pre_deploy`** — shell commands run **locally** (`sh -c`), sequentially,
  with `cmd.Dir` set to the resolved `context`. First non-zero exit aborts;
  the error carries the command and the tail of its output
  (`preDeployErrTail`, 2000 runes). Output streams live into the step's tail
  window and is buffered in parallel, because `Fail()` collapses the window.
- **`require_clean`** — `true` aborts when `git status --porcelain -- .` (run
  with `cmd.Dir` = context, so the pathspec scopes the check to the context,
  not the whole repo) reports tracked changes. `??` lines are skipped:
  untracked files were never in the archive. A moved submodule pointer shows
  as a modified path, so it aborts — that is the bit-website case, where the
  `vendor/bit` pointer decides which docs get published. Default is `false`
  (`config.CleanRequired()`), which **warns** instead — silence was the actual
  defect. A non-git context is not an error here; `Rsync` reports it with a
  better message moments later.

**Ordering is load-bearing**: `pre_deploy` runs *before* `require_clean`. Hooks
regenerate committed artifacts; the check then catches "you regenerated and did
not commit". Reversed, the pair is useless.
`TestPreflight_PreDeployDirtiesTree_RequireClean_Aborts` guards it.

There is deliberately **no opt-out** for files a hook is expected to modify: if
`pre_deploy` dirties a tracked file, the deploy *should* stop and make you
commit it. Don't add an escape hatch until someone proves they need one.

Called from `DeployWithClient` right after the header, before `StackExists` —
a failing hook or dirty tree leaves the server untouched. Skipped for pre-built
(`image:`) services, which sync no context.

## Build Args and Build Secrets (`deploy/buildargs.go`, `deploy/redact.go`)

`build_args` is a per-service map passed to the builder as `--build-arg K=V`
(`docker build` on compose, `nerdctl --namespace k8s.io build` on k3s, both via
`remote.BuildArgFlags`, sorted by key so the command is stable across deploys).
It exists because instaplayers-api downloads the MaxMind GeoLite2 DB in a build
stage using two credentials; without it that app cannot deploy with ssd at all.

```yaml
services:
  api:
    build_args:
      MAXMIND_ACCOUNT_ID: ${secret:MAXMIND_ACCOUNT_ID}
      MAXMIND_LICENSE_KEY: ${secret:MAXMIND_LICENSE_KEY}
      BUILD_CHANNEL: stable
```

`build_secrets` is the same map shape with a different transport: BuildKit
`--secret`, so the value never enters an image layer or `docker history`.
Both resolve identically and share their stores (`resolveBuildInputs` →
`resolveEntries`, one fetch per store per deploy). A key may not appear in
both maps.

A value is a literal or a **whole-value** reference (`config.ParseBuildArgRef`,
anchored regexp — `prefix${secret:K}` is a config error, not a template):

- `${secret:KEY}` — the service secret store. On k3s that is the
  `{svc}-secret` Secret, read through the optional `secretStore` interface
  (`ListSecrets`), which only `*k3s.Client` satisfies. On compose there is no
  secret store, so it falls back to `{svc}.env` — one ssd.yaml then works on
  both runtimes, and the error message says which store it read.
- `${env:KEY}` — `{svc}.env` via `GetEnvFile`.

Each store is fetched **once per deploy** (`buildArgStores`), and both parse as
`KEY=VALUE` lines (`parseKVLines`) — the shape of a .env file and of the
`kubectl get secret -o go-template` output alike. That makes stored values
single-line by construction: a multi-line secret (a PEM, say) would read back
as its first line only. Not worth handling — `--build-arg` is the wrong
transport for a certificate, and the truncated value fails the build loudly.

A missing **or empty** key is a hard error raised in `imageStep` *before* the
rsync, so a bad reference leaves the server untouched. Empty counts as missing:
building with a silently blank credential produces a broken image and a
successful-looking deploy.

**Secrecy is the load-bearing part** (`build_args` values are credentials):

- values resolved from a store are returned separately from
  `resolveBuildArgs` and fed to `withStreamedOutput`, which wraps the step's
  tail-window writer in a `redactWriter`. It holds output back to the last
  newline (so a value split across two writes is still matched), replaces each
  value with `***`, and bounds the hold buffer at `maxRedactHold` with a
  `carry` so no value straddles a forced flush. Values shorter than
  `minRedactLen` are ignored — masking them would smear the log.
  This is not theoretical: buildkit prints `RUN` step output, so a Dockerfile
  that echoes the arg (or a failing `curl` printing the MaxMind URL, which
  carries the licence key as a query parameter) leaks it into the deploy
  transcript. `TestE2E_BuildArgsResolvedAndMasked` fails without the masking.
- progress output logs key names only (`Build args: A, B`).
- no error in `config` or `deploy` ever contains a value — including
  validation errors, since a literal build arg may itself be a credential.
- `ssd config` prints the reference unresolved (`printConfig`).

Validation (`config.validateBuildArgs`): env-var-shaped keys, no control
characters in values, `maxBuildArgValue` cap, malformed `${...}` rejected, and
`build_args` on a pre-built (`image:`) service rejected outright — nothing is
built there, so it would be silently ignored.

Build args are per-service only (no root-level inheritance) and merge through
env overlays for free, since the overlay deep-merge is at the YAML node level:
an overlay that names one key replaces that key and keeps the rest.

### Transport

- `build_args` → `remote.BuildArgFlags`: shell-escaped `--build-arg K=V`,
  sorted keys. **Recorded in image history** — that is inherent to `--build-arg`,
  not an ssd choice, and it is why `build_secrets` exists.
  `TestE2E_BuildArgsDoLandInImageHistory` pins the behaviour so nobody
  "fixes" the docs to claim otherwise.
- `build_secrets` → `remote.BuildSecretsScript`: a shell prelude that
  `umask 077`s, `mktemp -d`s a directory, base64-decodes each value into a
  file inside it, and traps `EXIT HUP INT TERM` to `rm -rf` it however the
  build ends (including a dropped SSH connection). The build then gets
  `--secret id=K,src="$SSD_SECRETS/K"`. The compose path also pins
  `DOCKER_BUILDKIT=1`, since `--secret` is a BuildKit flag and an older daemon
  would reject it.

  The temp dir is deliberately **outside the build context**: a file inside it
  would be visible to `COPY . .` and end up in the image — precisely what
  `--secret` prevents. `TestClient_BuildImage_SecretsLiveOutsideBuildContext`
  guards that.

  The Dockerfile must read the mount rather than an `ARG`:
  `RUN --mount=type=secret,id=KEY ... "$(cat /run/secrets/KEY)"`.

**Residual exposure, both mechanisms**: the value travels inside the SSH
command string (base64 for secrets — encoding, not protection), so it is
briefly visible in `ps` on the server. Closing that needs stdin transport
through the executor, which has no API for it today. The image-history leak
was the one worth fixing; `ps` requires concurrent root access on the box that
already holds the secret store.

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
`nil` restores `os.Stdout`/`os.Stderr`. Tail height is
`streamTailLines = 4`.

`withStreamedOutput(client, step, secrets, fn)` wraps the set/restore plus the
`redactWriter` that masks `secrets` (resolved build-arg values; nil for every
other step). Its `Flush()` runs on restore so a final line without a newline is
not lost.

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
Deploy-all (`ssd deploy` with no args) builds all images first, then deploys
each service using its configured strategy. Services start in **dependency
order** (`deploy.OrderByDependsOn`, a stable topological sort of `depends_on`)
so a dependency (e.g. a DB a readiness probe needs) is Ready before the
dependent starts. Alphabetical order would start the dependent first and
deadlock on its rollout deadline. Dangling `depends_on` names are ignored;
services in a cycle are appended in alphabetical order so none are dropped.

### Targeted deploy pulls in missing dependencies

`deploy`, `up`, and `update` are aliases. A **targeted** deploy — one or more
named services (`ssd update web,api`) — auto-includes each named service's
transitive `depends_on` dependencies that are **not already running** on the
server, so updating one service in a stack never fails because its DB was
never brought up. `deploy.TransitiveDeps` computes the closure;
`deployedServices` (reused by orphan detection) lists what's already deployed
via `docker compose ps` / `kubectl get deploy -l managed-by=ssd`;
`filterDeploySet` keeps every named service plus the not-yet-deployed deps.
Dependencies already running are left untouched. Auto-included deps are
printed before the deploy. A no-arg deploy already covers everything, so it
skips this expansion (and runs orphan detection instead — a subset can't,
since the un-deployed rest are intentional).

**Manifest is always regenerated from the full config, not the deploy subset.**
`updateManifestStep` rewrites the whole compose.yaml / manifests from
`Options.AllServices`, so `deployMany` (and `deployService`) pass the complete
service map via `loadAllServices` — only build/start is scoped to the subset.
Passing just the subset would shrink the manifest to those services, dropping
the rest and breaking any `depends_on` that points at an excluded service
(`docker compose config` fails with exit 15). This was the v0.20.0 →
v0.20.1 subset-deploy regression; `TestLoadAllServices_ReturnsFullConfig`
guards it.

## Conventions

- **Dockerfile path**: `dockerfile` is relative to `context`, never to the repo
  root. `Rsync` ships the *contents* of `context` (git archive +
  `--strip-components`), and `BuildImage` runs `cd <tempdir> && docker build -f
  <dockerfile>`, so `context: ./apps/web` + `dockerfile: ./apps/web/Dockerfile`
  resolves to `apps/web/apps/web/Dockerfile` and fails. On k3s the symptom is
  misleading: nerdctl falls back to `Containerfile` when `-f` is missing, so the
  error names a file the user never configured.
- **Stack path**: Full path to stack directory containing compose.yaml (default: `/stacks/{name}`)
- **Image naming**: `ssd-{project}-{name}:{version}` where project is extracted from stack path
- **Version tracking**: Parsed from compose.yaml image tag, auto-incremented on deploy
- **Config inheritance**: Root-level `server` and `stack` are inherited by services
- **Services-only mode**: All configs must use `services:` map (single-service mode removed)
- **Runtime**: `compose` (default) or `k3s`, set via `runtime:` field in ssd.yaml
- **K3s namespace**: One namespace per stack, derived from stack path basename (`/stacks/myapp` → `myapp`)
- **K3s manifests**: Single `manifests.yaml` in stack dir, all K8s resources separated by `---`
- **K3s per-service labels**: every generated resource (Deployment, Service,
  Ingress, Middleware, **PVC**) carries `app: <svc>` so the per-service
  `kubectl apply -f manifests.yaml -l app=<svc>` includes it. A PVC missing this
  label is filtered out of the apply and the pod hangs on `FailedScheduling`.
- **K3s env/secret wiring**: each Deployment's `envFrom` references both the
  `{svc}-env` ConfigMap and the `{svc}-secret` Secret, both `optional: true`.
  `optional` keeps the pod schedulable when a backing object is absent (a
  service with no env vars or no secrets) — no `CreateContainerConfigError`.
  Because the `secretRef` is always in the generated manifest, a plain
  `ssd deploy` wires any secret set via `ssd secret set` into the pod; no
  post-rollout patch is needed. The `{svc}-env` ConfigMap is populated by
  `applyEnvConfigMap`; the `{svc}-secret` Secret is created by `ssd secret set`.
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
    dockerfile: ./Dockerfile    # relative to context, not to the repo root
  api:
    context: ./apps/api
    dockerfile: ./Dockerfile
```

### Full-featured service
```yaml
server: myserver

services:
  web:
    name: myapp-web
    stack: /stacks/myapp
    context: ./apps/web
    dockerfile: ./Dockerfile    # relative to context
    target: production          # Docker build target stage (optional)
    build_args:                 # --build-arg KEY=VALUE (optional)
      BUILD_CHANNEL: stable
    build_secrets:              # BuildKit --secret mounts (optional)
      MAXMIND_LICENSE_KEY: ${secret:MAXMIND_LICENSE_KEY}
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

### Build args
```yaml
server: myserver

services:
  api:
    dockerfile: ./Dockerfile
    build_args:
      MAXMIND_ACCOUNT_ID: ${secret:MAXMIND_ACCOUNT_ID}   # k3s Secret / compose env file
      MAXMIND_LICENSE_KEY: ${secret:MAXMIND_LICENSE_KEY}
      API_URL: ${env:API_URL}                            # {svc}.env
      BUILD_CHANNEL: stable                              # literal
```

### Build secrets
```yaml
services:
  api:
    build_secrets:
      MAXMIND_LICENSE_KEY: ${secret:MAXMIND_LICENSE_KEY}
```

```dockerfile
RUN --mount=type=secret,id=MAXMIND_LICENSE_KEY \
    curl "...license_key=$(cat /run/secrets/MAXMIND_LICENSE_KEY)" -o db.tar.gz
```

See "Build Args and Build Secrets" above. Missing/empty reference aborts before
the build; resolved values are never printed. Neither is valid with `image:`.
Use `build_secrets` for credentials — `build_args` land in image history.

### Pre-deploy hooks / clean tree
```yaml
server: myserver
require_clean: true           # root default, inherited by every service

services:
  website:
    pre_deploy:               # run locally in the context, in order
      - sh advisories.sh
      - make gen
  worker:
    require_clean: false      # per-service override (warn instead of abort)
```

See "Local Pre-flight" above. `pre_deploy` runs before the `require_clean`
check; neither applies to pre-built (`image:`) services.

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
ssd deploy|up|update [svc...] # Deploy service(s) (all if omitted; comma/space-separated subset supported)
ssd down [service]            # Stop services (or all if omitted)
ssd rm [service]              # Permanently remove services (or entire stack)
ssd restart <service>         # Restart without rebuilding
ssd rollback <service>        # Rollback to previous version
ssd status|ps [service]       # What's running (whole stack, or one service)
ssd logs <service> [-f]       # View logs, -f to follow
ssd scale <service> <count>   # Live-scale a service (does not edit ssd.yaml)
```

### `ssd status` / `ssd ps`

One row per running instance, same table for both runtimes:

```
arcline on hl-master

SERVICE     STATUS             UPTIME  VERSION  PORTS
backend     running            25m     7        8092→8090
kb          running (healthy)  7m      8        8102→8100
web         exited             -       8        -
```

- No argument = whole stack; a named service narrows the rows (compose:
  positional service arg; k3s: `-l app=<svc>`).
- `remote.ServiceStatus` is the shared row type. Compose fills it from
  `docker compose ps --all --format json` (`remote.ParseComposeStatus`,
  accepting both the JSON-array and NDJSON shapes compose emits across
  versions); k3s fills it from a `kubectl get pods -o jsonpath` row
  (`k3s.ParsePodStatus`).
- `GetStatus` deliberately lives on `runtime.StatusClient`, **not** on
  `remote.RemoteClient`: `internal/testhelpers` cannot import `remote` (the
  mocks are consumed by `remote`'s own in-package tests, so the import would
  cycle), and only `ssd status` needs the method.
- STATUS is the runtime state plus health when known — compose reports
  `healthy`/`unhealthy`/`starting`, k3s reports `not ready` for a pod whose
  probes have not passed.
- VERSION is the deployed image tag (`remote.ImageTag`, which ignores a
  registry port so `registry.local:5000/web` is untagged, not tag `5000`).
- The PORTS column is dropped entirely when no service publishes a port (the
  norm on k3s, where traffic arrives through the Ingress) to keep the table
  narrow. Per row, ports are capped at two with a `+N` overflow marker.
- Rendering is `renderStatus` in main.go (stdlib `text/tabwriter`) — the
  `ui.Reporter` machinery is for deploy progress and is not used here.

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
issued before `kubectl apply` in both StartService and RolloutService. The
Deployment references it as `configMapRef: {name: {service}-env, optional:
true}`; `optional` keeps a service with no env vars schedulable instead of
failing with `CreateContainerConfigError`.

If `env_file` is set in ssd.yaml, it OVERWRITES any values set via
`ssd env set` on every deploy. To manage env vars via CLI only, remove
`env_file` from ssd.yaml first.

### Secrets (k3s only)
```bash
ssd secret <service> set KEY=VALUE    # Set a K8s Secret
ssd secret <service> list             # List all secrets
ssd secret <service> rm KEY           # Remove a secret
```

A secret can also be fed to an image build by referencing it from a service's
`build_args` or `build_secrets` as `${secret:KEY}` (see "Build Args and Build
Secrets"). Prefer `build_secrets` — `build_args` end up in image history.

K8s Secrets are injected as env vars alongside ConfigMap vars. The generated
Deployment's `envFrom` always carries `secretRef: {name: {service}-secret,
optional: true}`, so a plain `ssd deploy` wires the secret into the pod on the
next manifest apply — no post-rollout patch. `optional: true` means a service
with no secret still schedules. Only available with `runtime: k3s`. Running
`ssd secret` with compose runtime errors out.

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
service names for `deploy/up/update/down/rm/restart/rollback/status/logs/config/scale/env/secret`.
Honors global `--config` / `--env` so completion in a multi-env repo
reflects the right overlay.

`deploy`/`up` accept a comma-separated subset (`ssd deploy web,api`), so
completion is comma-aware: after a comma the shell completes the next item
and re-attaches the already-typed `web,` prefix. The Go candidate set is
unchanged (all service names) — each shell handles the comma prefix in its
native filter layer (bash `compgen -P`, zsh `compset -P '*,'`, fish prefix
prepend).
