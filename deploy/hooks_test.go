package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPostDeploy_RunsInOrderInContextDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Name: "web", Context: dir, PostDeploy: []string{
		"echo one >> log.txt",
		"echo two >> log.txt",
	}}

	require.NoError(t, PostDeploy(context.Background(), ui.Discard(), cfg))

	out, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(out))
}

// The service is live by the time a hook runs; the error must say so, or the
// operator reads "deploy failed" over a rollout that succeeded.
func TestPostDeploy_FailureSaysServiceIsLive(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Name: "web", Context: dir, PostDeploy: []string{
		"echo boom >&2; exit 3",
		"touch never-ran",
	}}

	err := PostDeploy(context.Background(), ui.Discard(), cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "web is deployed and live")
	assert.Contains(t, err.Error(), "post_deploy")
	assert.Contains(t, err.Error(), "boom")
	assert.NoFileExists(t, filepath.Join(dir, "never-ran"))
}

// prebuiltCfg returns a pre-built service whose post_deploy hook drops a
// marker file in its context. Pre-built so the mock needs no build steps.
func prebuiltCfg(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "hook-ran")
	cfg := newTestConfig()
	cfg.Image = "nginx:latest"
	cfg.Context = dir
	cfg.PostDeploy = []string{"touch hook-ran"}
	return cfg, marker
}

func prebuiltMock() *MockDeployer {
	m := new(MockDeployer)
	m.On("StackExists").Return(true, nil)
	m.On("GetCurrentVersion").Return(0, nil)
	m.On("MakeTempDir").Return("/tmp/build", nil)
	m.On("PullImage", "nginx:latest").Return(nil)
	m.On("UpdateManifest", mock.Anything).Maybe().Return(nil)
	m.On("Cleanup", "/tmp/build").Return(nil)
	return m
}

// Order is the entire feature: a hook that fires before the rollout (a CDN
// purge, say) is repopulated by the old pod and worse than no hook at all.
func TestDeployWithClient_PostDeployRunsAfterStart(t *testing.T) {
	cfg, marker := prebuiltCfg(t)
	client := prebuiltMock()
	client.On("RolloutService", "myapp").Run(func(mock.Arguments) {
		assert.NoFileExists(t, marker, "post_deploy ran before the service was rolled out")
	}).Return(nil)

	err := DeployWithClient(cfg, client, &Options{Reporter: ui.Discard()})

	require.NoError(t, err)
	assert.FileExists(t, marker)
	client.AssertExpectations(t)
}

func TestDeployWithClient_PostDeployFailureFailsDeployAfterStart(t *testing.T) {
	cfg, _ := prebuiltCfg(t)
	cfg.PostDeploy = []string{"exit 7"}
	client := prebuiltMock()
	client.On("RolloutService", "myapp").Return(nil)

	err := DeployWithClient(cfg, client, &Options{Reporter: ui.Discard()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "myapp is deployed and live")
	client.AssertExpectations(t) // the rollout did happen
}

func TestDeployWithClient_PostDeploySkippedWhenStartFails(t *testing.T) {
	cfg, marker := prebuiltCfg(t)
	client := prebuiltMock()
	client.On("RolloutService", "myapp").Return(assert.AnError)

	err := DeployWithClient(cfg, client, &Options{Reporter: ui.Discard()})

	require.Error(t, err)
	assert.NoFileExists(t, marker, "post_deploy must not run for a service that did not start")
}

// BuildOnly deploys never start the service — the caller (deploy-all) starts
// it later and owns the hook. Running it here would fire before the rollout.
func TestDeployWithClient_PostDeploySkippedInBuildOnly(t *testing.T) {
	cfg, marker := prebuiltCfg(t)
	client := prebuiltMock()

	err := DeployWithClient(cfg, client, &Options{Reporter: ui.Discard(), BuildOnly: true})

	require.NoError(t, err)
	assert.NoFileExists(t, marker)
}
