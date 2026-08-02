package deploy

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
	return string(out)
}

// gitRepo creates a temp git repo with one committed file and returns its path.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// EvalSymlinks: macOS TempDir is /var -> /private/var, and git reports the
	// resolved path. Keeps assertions on paths comparable.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0o600))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

func svcCfg(dir string, requireClean bool, preDeploy ...string) *config.Config {
	return &config.Config{
		Name:         "web",
		Context:      dir,
		RequireClean: &requireClean,
		PreDeploy:    preDeploy,
	}
}

func TestPreflight_DirtyTrackedFile_RequireClean_Aborts(t *testing.T) {
	dir := gitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o600))

	err := preflight(context.Background(), ui.Discard(), svcCfg(dir, true))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tracked.txt")
	assert.Contains(t, err.Error(), "require_clean")
}

func TestPreflight_StagedFile_RequireClean_Aborts(t *testing.T) {
	dir := gitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o600))
	git(t, dir, "add", "tracked.txt")

	err := preflight(context.Background(), ui.Discard(), svcCfg(dir, true))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tracked.txt")
}

func TestPreflight_UntrackedOnly_Proceeds(t *testing.T) {
	dir := gitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o600))

	require.NoError(t, preflight(context.Background(), ui.Discard(), svcCfg(dir, true)))
}

func TestPreflight_DirtySubmodulePointer_Aborts(t *testing.T) {
	parent := gitRepo(t)
	sub := gitRepo(t)

	// Newer git refuses the file:// transport for submodules unless allowed.
	add := exec.Command("git", "-c", "protocol.file.allow=always", "submodule", "add", sub, "vendor/sub")
	add.Dir = parent
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("git submodule add unsupported here: %s", out)
	}
	git(t, parent, "commit", "-qm", "add submodule")

	// Move the submodule pointer without committing it in the parent. The
	// checkout is a fresh clone, so it carries none of sub's config — give it
	// an identity of its own or the commit fails wherever git has no global
	// user set (CI runners).
	subCheckout := filepath.Join(parent, "vendor", "sub")
	git(t, subCheckout, "config", "user.email", "test@example.com")
	git(t, subCheckout, "config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(subCheckout, "tracked.txt"), []byte("v2\n"), 0o600))
	git(t, subCheckout, "commit", "-qam", "move pointer")

	err := preflight(context.Background(), ui.Discard(), svcCfg(parent, true))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vendor/sub")
}

func TestPreflight_DirtyOutsideContext_Proceeds(t *testing.T) {
	repo := gitRepo(t)
	app := filepath.Join(repo, "app")
	require.NoError(t, os.Mkdir(app, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(app, "main.go"), []byte("package main\n"), 0o600))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "app")

	// Dirty file lives outside the build context.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("v2\n"), 0o600))

	require.NoError(t, preflight(context.Background(), ui.Discard(), svcCfg(app, true)))
}

func TestPreflight_DirtyWithoutRequireClean_WarnsAndProceeds(t *testing.T) {
	dir := gitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o600))

	var buf bytes.Buffer
	cfg := &config.Config{Name: "web", Context: dir} // require_clean unset

	require.NoError(t, preflight(context.Background(), ui.NewPlain(&buf), cfg))
	assert.Contains(t, buf.String(), "tracked.txt")
	assert.Contains(t, buf.String(), "NOT be deployed")
}

func TestPreflight_NotAGitRepo_Proceeds(t *testing.T) {
	dir := t.TempDir()

	// Rsync reports the missing repo with a better message; preflight stays quiet.
	require.NoError(t, preflight(context.Background(), ui.Discard(), &config.Config{Name: "web", Context: dir}))
}

func TestPreDeploy_RunsInOrderInContextDir(t *testing.T) {
	dir := gitRepo(t)

	err := preflight(context.Background(), ui.Discard(), svcCfg(dir, false,
		"echo one >> log.txt",
		"echo two >> log.txt",
	))
	require.NoError(t, err)

	out, readErr := os.ReadFile(filepath.Join(dir, "log.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "one\ntwo\n", string(out))
}

func TestPreDeploy_FailureAbortsWithOutput(t *testing.T) {
	dir := gitRepo(t)

	err := preflight(context.Background(), ui.Discard(), svcCfg(dir, false,
		"echo boom >&2; exit 3",
		"touch never-ran",
	))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "echo boom")
	assert.Contains(t, err.Error(), "boom")
	assert.NoFileExists(t, filepath.Join(dir, "never-ran"))
}

// The ordering is the whole point of the pair: pre_deploy regenerates
// artifacts, require_clean then catches "regenerated and not committed".
func TestPreflight_PreDeployDirtiesTree_RequireClean_Aborts(t *testing.T) {
	dir := gitRepo(t)

	err := preflight(context.Background(), ui.Discard(), svcCfg(dir, true, "echo regenerated > tracked.txt"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tracked.txt")
}

func TestDeployWithClient_RequireClean_AbortsBeforeTouchingServer(t *testing.T) {
	dir := gitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o600))

	client := new(MockDeployer)
	requireClean := true
	cfg := &config.Config{
		Name:         "web",
		Server:       "srv",
		Stack:        "/stacks/test-preflight",
		Context:      dir,
		RequireClean: &requireClean,
	}

	err := DeployWithClient(cfg, client, &Options{Reporter: ui.Discard()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tracked.txt")
	client.AssertExpectations(t) // no calls were set up, so any call fails the test
}
