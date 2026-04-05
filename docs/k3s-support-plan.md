# K3s Support Plan

## Config

New top-level `runtime` field in ssd.yaml:

```yaml
runtime: k3s          # "compose" (default) or "k3s"
server: myserver

services:
  web:
    domain: example.com
    port: 3000
```

Valid values: `compose` (default), `k3s`. Existing configs without `runtime` continue to work as-is.

## Stack Directory

Same layout as compose: `/stacks/{project}/` on the server. Instead of `compose.yaml`, ssd generates a single `manifests.yaml` containing all K8s resources (Deployment, Service, Ingress, etc.) separated by `---`.

```
/stacks/{project}/
├── manifests.yaml      # All K8s resources in one file
├── web.env             # Env files (same as compose)
└── ...
```

Deploy: `kubectl apply -f /stacks/{project}/manifests.yaml`

## Environment Variables and Secrets

### Env (both runtimes)

`ssd env <svc> set/list/rm` works identically on both runtimes:

- Writes to `/stacks/{project}/{svc}.env` on the server (same file format)
- **compose**: Referenced via `env_file` in compose.yaml (current behavior)
- **k3s**: ssd reads the `.env` file on deploy, creates a ConfigMap, injects via `envFrom` in the Deployment

User experience is 100% identical across runtimes.

### Secrets (k3s only)

New command: `ssd secret <svc> set/list/rm`

- Creates a K8s Secret (base64 encoded, restricted access)
- Injected as env vars in the container alongside ConfigMap env vars
- Only available when `runtime: k3s`

If a user runs `ssd secret` with `runtime: compose`, error out:

```
Error: secrets require runtime: k3s. Use "ssd env" for compose runtime.
```

## Domains and TLS

Same behavior as compose. Domain presence controls routing:

### With domain

- Generates an Ingress resource in manifests.yaml
- K3s ships Traefik, which picks up Ingress resources automatically
- TLS via Traefik's built-in ACME (Let's Encrypt), same approach as compose runtime
- `https`, `path`, `redirect_to`, multi-domain (`domains`) all map to Ingress rules and Traefik annotations

### Without domain

- No Ingress resource generated
- Use `ports` for direct access (maps to `hostPort` in pod spec)
- Same as compose: internal-only service accessible via Tailscale, CF tunnels, etc.

## Provisioning

`ssd provision` with `runtime: k3s`:

1. **Install K3s** — `curl -sfL https://get.k3s.io | sh -` (single binary: kubectl, containerd, Traefik all included)
2. **Install nerdctl + buildkit** — Two binaries (~80MB total). Configure nerdctl to use K3s's containerd socket (`/run/k3s/containerd/containerd.sock`). **Must use `--namespace k8s.io`** so images land in K3s's containerd namespace (otherwise K3s can't see them). No extra daemon running 24/7. buildkitd only runs during builds.
3. **Configure Traefik ACME** — K3s deploys Traefik via HelmChart CRD. Patch to enable Let's Encrypt with user's email.

Runtime context: If ssd.yaml exists, read runtime from it. If no ssd.yaml (provisioning before init), accept `--runtime` flag. If neither, prompt interactively:

```
Runtime (compose/k3s) [compose]:
```

`ssd provision check` verifies:

- K3s running
- kubectl available
- nerdctl + buildkit available
- Traefik ingress controller running
- Traefik ACME configured

## Build Strategy

**nerdctl + buildkit** — talks directly to K3s's existing containerd. No duplicate runtimes, no extra daemons, images land where K3s expects them.

- `nerdctl build` is Docker-compatible (same Dockerfile support, same CLI flags)
- **Critical**: All nerdctl commands must use `--namespace k8s.io` to target K3s's containerd namespace. Without this, images are invisible to K3s.
- Images go straight into containerd's image store (no save/import step)
- Build code path stays nearly identical to compose, just swap `docker` for `nerdctl --namespace k8s.io`

### Operational gotchas (all handled by ssd)

| Gotcha | How ssd handles it |
|---|---|
| `--namespace k8s.io` | Hardcoded in every nerdctl call. User never touches nerdctl. |
| buildkitd lifecycle | `ssd provision` installs a systemd service. Auto-starts on boot. |
| buildkitd health | Before every build, ssd verifies buildkitd is running: `systemctl is-active buildkitd \|\| systemctl start buildkitd` |
| Build cache growth | `ssd deploy` runs `buildctl prune` after each build in cleanup step. |
| Version compat | `ssd provision` installs pinned, tested versions of nerdctl + buildkit. |
| CNI conflicts | Builds use default network mode. No cluster network access during build. |

The user never sees any of this. All gotchas are abstracted away by ssd.

## Deploy Flow

### Single service: `ssd deploy <svc>`

1. SSH into server
2. Create temp dir, rsync code (git archive) — same as compose
3. `nerdctl build -t ssd-{project}-{svc}:{version}` — swap docker for nerdctl
4. Regenerate full manifests.yaml (all services) with new image tag for the target service
5. `kubectl apply -f manifests.yaml -l app={svc}` — selective apply via label (full file ensures no other services' manifests are lost)
6. `kubectl rollout status deployment/{svc}` — wait for rollout confirmation
7. Cleanup temp dir

### All services: `ssd deploy` (no args)

1. Build all images first (same as compose)
2. Regenerate full manifests.yaml
3. `kubectl apply -f manifests.yaml` — applies everything
4. Wait for all rollouts
5. Warn about orphaned services (see Prune section)

### Deploy strategies

- `rollout` (default) → K8s `RollingUpdate` — native, no plugin needed
- `recreate` → K8s `Recreate`

No docker-rollout plugin equivalent needed. K8s handles rolling updates out of the box.

## Prune

New command for both runtimes: `ssd prune`

Compares current ssd.yaml services against what's deployed on the server. Removes orphaned services.

| | compose | k3s |
|---|---|---|
| Detect orphans | Parse compose.yaml service names | `kubectl get -l managed-by=ssd` or parse manifests.yaml |
| Remove services | `docker compose rm -sf {svc}`, update compose.yaml | `kubectl delete deployment,service,ingress -l app={svc}` |
| Cleanup | Remove `{svc}.env` | Remove `{svc}.env`, regenerate manifests.yaml |

Supports `--dry-run` to preview without removing.

### Orphan warning on deploy-all

When `ssd deploy` (all services) detects services on the server not in ssd.yaml, warn after deploy:

```
Warning: 2 orphaned services detected on server (not in ssd.yaml):
  - worker
  - redis

Run "ssd prune" to remove them.
```

No auto-removal. User must explicitly run `ssd prune`.

## Command Mapping

Identical UX across runtimes. User sees no difference.

| Command | compose | k3s |
|---|---|---|
| `ssd deploy <svc>` | docker build, compose up | nerdctl build, kubectl apply |
| `ssd restart <svc>` | `docker compose restart` | `kubectl rollout restart deployment/{svc}` |
| `ssd rollback <svc>` | Decrement image tag, restart | `kubectl rollout undo deployment/{svc}` |
| `ssd status <svc>` | `docker compose ps` | `kubectl get pods -l app={svc}` |
| `ssd logs <svc> [-f]` | `docker compose logs [-f]` | `kubectl logs -l app={svc} [-f]` |
| `ssd env` | .env file | .env file → ConfigMap |
| `ssd secret` | Error (unsupported) | K8s Secret |
| `ssd prune` | Remove from compose.yaml | kubectl delete + regenerate manifests.yaml |
| `ssd provision` | Docker + Traefik | K3s + nerdctl + buildkit |

K3s rollback is native — K8s tracks revision history, no manual version decrementing needed. After rollback, ssd queries the actual running image tag from the cluster and updates manifests.yaml to match. Disk always reflects reality.

## Volumes

Same ssd.yaml syntax:

```yaml
volumes:
  postgres-data: /var/lib/postgresql/data
```

For K3s, generates a PersistentVolumeClaim + volumeMount in the Deployment.

- Storage class: `local-path` (K3s default)
- Size: 10Gi default (formality — `local-path` doesn't enforce limits)
- No size config needed for now

## Namespace Strategy

One namespace per stack. Derived from stack path:

- `/stacks/myapp` → namespace `myapp`
- `/stacks/myproject` → namespace `myproject`

All services in a stack share the namespace. Services reach each other via `{svc}:{port}` (K8s DNS).

- Clean isolation between projects
- Easy cleanup: `kubectl delete namespace myapp` wipes everything
- Mirrors the existing stack directory isolation
- Namespace created automatically on first deploy if it doesn't exist

## Pre-built Images

Same behavior as compose. When `image: nginx:latest` is set, skip build, pull instead.

- `nerdctl pull nginx:latest` (instead of `docker pull`)
- No build step, no rsync
- Deployment manifest uses `imagePullPolicy: Always` (to pull updates)
- Locally built ssd images use `imagePullPolicy: Never`

## Healthcheck

Same ssd.yaml config, no changes. ssd translates per runtime:

- **compose**: Maps directly to Docker healthcheck
- **k3s**: Maps to both liveness and readiness probes (exec-based, same cmd/interval/timeout/retries)

## depends_on

K8s has no native `depends_on`. ssd handles it at deploy time — same as what compose actually does.

When deploying a service with `depends_on`, ssd deploys dependencies first, waits for their pods to be ready, then deploys the dependent service. Ordering enforced by ssd, not by K8s manifests. No init containers, no extra config in manifests.

## Files

Same as compose. Files are copied to the stack dir on the server via SSH, then mounted into the pod using `hostPath`. No ConfigMaps needed.

```yaml
files:
  ./config.yaml: /app/config.yaml
```

- SSH transfer to `/stacks/{project}/config.yaml` (same as compose)
- Pod mounts via `hostPath` volume pointing to the stack dir file

## Code Architecture

Factory pattern. No if/else branching on runtime.

### Runtime interface

Generalize the current `Deployer` interface — rename compose-specific methods:

- `ReadCompose` → `ReadManifest`
- `UpdateCompose` → `UpdateManifest`
- `CreateStack(composeContent)` → `CreateStack(manifestContent)`

Both runtimes implement the same interface.

### Factory

```go
// runtime package resolves runtime by name
deployer := runtime.New(cfg.Runtime, remoteClient, cfg)
```

`runtime.New("compose", ...)` → compose deployer (existing code)
`runtime.New("k3s", ...)` → k3s deployer (new)

Adding a future runtime = register another implementation. Zero changes to main.go or deploy.go.

### Package structure

```
├── runtime/
│   ├── runtime.go        # Interface + factory
│   ├── compose/
│   │   └── compose.go    # Compose deployer (move from current compose/)
│   └── k3s/
│       ├── k3s.go        # K3s deployer
│       └── manifest.go   # K8s manifest generation
├── remote/
│   └── remote.go         # SSH client (shared, unchanged)
├── deploy/
│   └── deploy.go         # Orchestration (uses runtime interface)
├── config/
│   └── config.go         # Add runtime field
├── provision/
│   └── provision.go      # Branch via factory for provisioning
```

### What stays shared

- `remote/remote.go` — SSH transport, rsync, file copy (both runtimes use SSH)
- `deploy/deploy.go` — Orchestration logic (build, deploy, rollback flow)
- `config/config.go` — ssd.yaml parsing
- `main.go` — CLI commands (unchanged)

### What splits per runtime

- Manifest generation (compose.yaml vs K8s manifests.yaml)
- Remote commands (docker/docker-compose vs nerdctl/kubectl)
- Provisioning steps (Docker+Traefik vs K3s+nerdctl+buildkit)

## Scaffold (ssd init)

`ssd init` accepts runtime via `-r` flag:

```bash
ssd init -r k3s                    # Non-interactive
ssd init -s myserver -r k3s        # Combined with other flags
ssd init                           # Interactive, prompts for runtime
```

Interactive prompt when `-r` not passed:

```
Runtime (compose/k3s) [compose]:
```

Defaults to `compose`. Generated ssd.yaml includes `runtime:` field when k3s is selected.

## Development Approach

Strict red/green TDD. Every change follows:

1. **Red** — Write a failing test first
2. **Green** — Write the minimum code to make it pass
3. **Refactor** — Clean up, no behavior changes

No implementation without a failing test. No test weakening to make code pass.

## Implementation Order

Each step builds on the previous. Existing unit tests guard against regressions.

1. **Config** — Add `runtime` field + validation
2. **Code architecture** — Runtime interface + factory. Refactor existing compose code into `runtime/compose/`. No new features, just restructure. Existing tests catch regressions.
3. **Scaffold** — Add `-r` flag to `ssd init`
4. **Provision** — K3s install + nerdctl + buildkit
5. **Build** — `nerdctl build` via SSH
6. **Manifest generation** — K8s YAML from ssd.yaml (Deployment, Service)
7. **Deploy** — `kubectl apply`, rollout status
8. **Status/logs** — kubectl wrappers
9. **Restart/rollback** — kubectl rollout restart/undo
10. **Env** — ConfigMap from .env files
11. **Secrets** — New `ssd secret` command
12. **Domains/TLS** — Ingress + Traefik ACME
13. **Volumes** — PVC generation
14. **Files** — hostPath mounts
15. **Healthcheck** — Liveness/readiness probes
16. **depends_on** — Deploy ordering
17. **Prune** — Orphan detection + removal (both runtimes)
18. **Pre-built images** — `nerdctl pull` path

## Documentation

Every implementation step must include documentation updates in the same changeset. No code merges without docs.

- **CLAUDE.md** — Update with K3s runtime details, new commands (`secret`, `prune`), config patterns, provisioning changes
- **README.md** — Add K3s section: setup, config examples, provision flow, differences from compose
- **SKILL.md** — Update for Claude Code users to cover K3s commands and workflows
- **Help text** — Every new/changed command (`secret`, `prune`, `provision`, `init -r`) must have accurate `-h`/`--help` output
- **ssd.yaml examples** — Add K3s config examples alongside existing compose examples
