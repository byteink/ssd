//go:build e2e

package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/byteink/ssd/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E2E tests run the real DeployWithClient path against an isolated
// docker-in-docker sandbox — a privileged container that runs its own Docker
// daemon plus sshd. ssd SSHes in and does real `docker build` / `docker
// compose` / `docker rollout` entirely inside that container, so the host
// Docker is never touched and everything is torn down with the sandbox.
//
// Two modes, mirroring how the toolchain is installed by `ssd provision`:
//   - fast (default, CI): recreate strategy, compose plugin only.
//   - full (SSD_E2E_FULL set, run locally before release): rollout strategy,
//     docker-rollout plugin installed exactly as provision installs it.
func e2eFullMode() bool { return os.Getenv("SSD_E2E_FULL") != "" }

// gitInitCommitE2E makes dir a git repo with one commit. Client.Rsync ships the
// build context via `git archive HEAD`, so the context must be a committed repo.
func gitInitCommitE2E(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "add", "-A"},
		{"git", "-C", dir, "commit", "-m", "test"},
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", c, string(out))
	}
}

// setupE2EEnvironment starts the DinD sandbox, provisions it for the active
// mode, and returns a ready single-service config plus a cleanup func.
func setupE2EEnvironment(t *testing.T) (*testhelpers.SSHContainer, *config.Config, func()) {
	t.Helper()
	ctx := context.Background()

	sandbox, err := testhelpers.StartSSHDockerContainer(ctx, t)
	require.NoError(t, err, "failed to start dind sandbox")

	// Side effect: writes ssh_config next to the key; newE2EClient reads it back.
	_, err = sandbox.WriteSSHConfig("testserver")
	require.NoError(t, err)

	// Provision the sandbox like `ssd provision`: compose plugin always,
	// docker-rollout only for the full-fidelity local run.
	provision := []string{"apk add --no-cache docker-cli-compose git"}
	strategy := "recreate"
	if e2eFullMode() {
		provision = append(provision,
			"apk add --no-cache curl",
			"mkdir -p /root/.docker/cli-plugins",
			"curl -fsSL https://raw.githubusercontent.com/wowu/docker-rollout/main/docker-rollout "+
				"-o /root/.docker/cli-plugins/docker-rollout",
			"chmod +x /root/.docker/cli-plugins/docker-rollout")
		strategy = "rollout"
	}
	for _, cmd := range provision {
		_, err := sandbox.RunSSH(cmd)
		require.NoError(t, err, "provision step failed: %s", cmd)
	}

	// Stack root on the remote (sandbox user is root — no sudo needed).
	_, err = sandbox.RunSSH("mkdir -p /stacks")
	require.NoError(t, err)

	// Local build context: a committed git repo with a long-lived container.
	projectDir := t.TempDir()
	dockerfile := "FROM alpine:latest\nCMD [\"sh\", \"-c\", \"echo running && sleep 3600\"]\n"
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "Dockerfile"), []byte(dockerfile), 0644))
	gitInitCommitE2E(t, projectDir)

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/stacks/testapp",
		Dockerfile: "./Dockerfile",
		Context:    projectDir,
		Deploy:     &config.DeployConfig{Strategy: strategy},
	}
	t.Logf("e2e mode: strategy=%s full=%v", strategy, e2eFullMode())

	return sandbox, cfg, func() { sandbox.Cleanup(ctx) }
}

// newE2EClient wires a remote client that talks to the sandbox via its SSH config.
func newE2EClient(t *testing.T, sandbox *testhelpers.SSHContainer, cfg *config.Config) *remote.Client {
	t.Helper()
	sshConfigPath := filepath.Join(filepath.Dir(sandbox.KeyPath), "ssh_config")
	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfigPath}
	return remote.NewClientWithExecutor(cfg, executor)
}

// deployE2E runs one full deploy and returns the captured reporter output.
func deployE2E(t *testing.T, cfg *config.Config, client *remote.Client) string {
	t.Helper()
	out := new(strings.Builder)
	err := DeployWithClient(cfg, client, &Options{Output: out})
	require.NoError(t, err, "deploy failed; output:\n%s", out.String())
	return out.String()
}

func TestE2E_FirstDeploy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	ctx := context.Background()

	sandbox, cfg, cleanup := setupE2EEnvironment(t)
	defer cleanup()
	client := newE2EClient(t, sandbox, cfg)

	out := deployE2E(t, cfg, client)

	// Fresh stack starts at version 0 → first deploy is version 1.
	version, err := client.GetCurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, version, "version should be 1 after first deploy")

	imageTag := fmt.Sprintf("%s:1", cfg.ImageName())
	images, err := sandbox.RunSSH("docker images --format '{{.Repository}}:{{.Tag}}'")
	require.NoError(t, err)
	assert.Contains(t, images, imageTag, "built image should exist in the sandbox")

	compose, err := sandbox.RunSSH(fmt.Sprintf("cat %s/compose.yaml", cfg.Stack))
	require.NoError(t, err)
	assert.Contains(t, compose, imageTag, "compose.yaml should reference the new tag")

	assert.Contains(t, out, "Deployed testapp version 1 successfully")
}

func TestE2E_UpgradeDeploy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	ctx := context.Background()

	sandbox, cfg, cleanup := setupE2EEnvironment(t)
	defer cleanup()
	client := newE2EClient(t, sandbox, cfg)

	// First deploy establishes version 1; second must increment to 2.
	deployE2E(t, cfg, client)
	v1, err := client.GetCurrentVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, v1)

	out := deployE2E(t, cfg, client)

	v2, err := client.GetCurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, v2, "second deploy should increment to version 2")

	compose, err := sandbox.RunSSH(fmt.Sprintf("cat %s/compose.yaml", cfg.Stack))
	require.NoError(t, err)
	assert.Contains(t, compose, fmt.Sprintf("%s:2", cfg.ImageName()))
	assert.NotContains(t, compose, fmt.Sprintf("%s:1", cfg.ImageName()))
	assert.Contains(t, out, "Version: 1 → 2")
}

func TestE2E_VerifyContainerRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sandbox, cfg, cleanup := setupE2EEnvironment(t)
	defer cleanup()
	client := newE2EClient(t, sandbox, cfg)

	deployE2E(t, cfg, client)

	// The container sleeps 3600s, so it stays Up after deploy returns.
	ps, err := sandbox.RunSSH("docker ps --format '{{.Image}}\\t{{.Status}}'")
	require.NoError(t, err)
	assert.Contains(t, ps, cfg.ImageName(), "deployed container should be running")
	assert.Contains(t, ps, "Up", "container status should be Up")
}

func TestE2E_VersionIncrement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	ctx := context.Background()

	sandbox, cfg, cleanup := setupE2EEnvironment(t)
	defer cleanup()
	client := newE2EClient(t, sandbox, cfg)

	for expected := 1; expected <= 3; expected++ {
		deployE2E(t, cfg, client)
		got, err := client.GetCurrentVersion(ctx)
		require.NoError(t, err)
		assert.Equal(t, expected, got, "deploy %d should produce version %d", expected, expected)
	}
}

func TestE2E_RsyncExclusions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	ctx := context.Background()

	sandbox, cfg, cleanup := setupE2EEnvironment(t)
	defer cleanup()
	client := newE2EClient(t, sandbox, cfg)

	// git archive ships only tracked files. A tracked source file must arrive;
	// .git internals and a .gitignored dir must not.
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Context, "app.go"), []byte("package main"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Context, ".gitignore"), []byte("node_modules/\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(cfg.Context, "node_modules"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Context, "node_modules", "junk.txt"), []byte("junk"), 0644))
	gitInitCommitE2E(t, cfg.Context)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer func() { _ = client.Cleanup(ctx, remoteDir) }()

	require.NoError(t, client.Rsync(ctx, cfg.Context, remoteDir))

	listing, err := sandbox.RunSSH(fmt.Sprintf("ls -a %s", remoteDir))
	require.NoError(t, err)
	assert.Contains(t, listing, "app.go", "tracked file should be synced")
	assert.Contains(t, listing, "Dockerfile", "tracked file should be synced")
	assert.NotContains(t, listing, "node_modules", ".gitignored dir must not be synced")
	assert.NotContains(t, listing, ".git\n", ".git internals must not be synced")
}

func TestE2E_DeploymentLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sandbox, cfg, cleanup := setupE2EEnvironment(t)
	defer cleanup()
	client := newE2EClient(t, sandbox, cfg)

	// Hold the stack lock, then start a deploy: it must stay blocked while the
	// lock is held and complete once released. (The 5-minute acquire timeout
	// itself is covered by TestAcquireLock — re-proving it here would mean a
	// 5-minute wait.)
	unlock, err := acquireLockWithTimeout(cfg.StackPath(), 2*time.Second)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- DeployWithClient(cfg, client, nil) }()

	select {
	case err := <-done:
		t.Fatalf("deploy proceeded while the lock was held: %v", err)
	case <-time.After(3 * time.Second):
		// Still blocked on the lock, as required.
	}

	unlock()

	select {
	case err := <-done:
		require.NoError(t, err, "deploy should succeed after the lock is released")
	case <-time.After(3 * time.Minute):
		t.Fatal("deploy did not complete after the lock was released")
	}
}
