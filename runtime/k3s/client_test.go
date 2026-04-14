package k3s

import (
	"context"
	"strings"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/byteink/ssd/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	cfg := &config.Config{
		Name:   "web",
		Server: "myserver",
		Stack:  "/stacks/myapp",
	}

	client := NewClient(cfg)
	assert.NotNil(t, client)
	assert.Equal(t, "myapp", client.namespace)
}

func TestClient_ImplementsRemoteClient(t *testing.T) {
	cfg := &config.Config{
		Name:   "web",
		Server: "myserver",
		Stack:  "/stacks/myapp",
	}

	client := NewClient(cfg)
	// Compile-time check that Client satisfies RemoteClient
	var _ remote.RemoteClient = client
}

// recordingExecutor captures the order of SSH commands issued so tests
// can assert the configmap is populated BEFORE kubectl apply.
type recordingExecutor struct {
	testhelpers.MockExecutor
	cmds []string
}

func (r *recordingExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "ssh" && len(args) > 0 {
		r.cmds = append(r.cmds, args[len(args)-1])
	}
	return r.MockExecutor.Run(ctx, name, args...)
}

func (r *recordingExecutor) RunInteractive(ctx context.Context, name string, args ...string) error {
	if name == "ssh" && len(args) > 0 {
		r.cmds = append(r.cmds, args[len(args)-1])
	}
	return r.MockExecutor.RunInteractive(ctx, name, args...)
}

func newRecordingClient(t *testing.T, cfg *config.Config) (*Client, *recordingExecutor) {
	t.Helper()
	rec := &recordingExecutor{}
	rec.On("Run", "ssh", mock.Anything).Return("", nil)
	rec.On("RunInteractive", "ssh", mock.Anything).Return(nil)
	return NewClientWithExecutor(cfg, rec), rec
}

func expectedConfigMapCmd(service, namespace, stack string) string {
	return "k3s kubectl create configmap " + service + "-env -n " + namespace +
		" --from-env-file=" + stack + "/" + service + ".env " +
		"--dry-run=client -o yaml | k3s kubectl apply -f -"
}

func TestClient_StartService_PopulatesConfigMapBeforeApply(t *testing.T) {
	cfg := &config.Config{Name: "web", Server: "srv", Stack: "/stacks/myapp"}
	client, rec := newRecordingClient(t, cfg)

	require.NoError(t, client.StartService(context.Background(), "web"))

	cmdIdx := -1
	applyIdx := -1
	want := expectedConfigMapCmd("web", "myapp", "/stacks/myapp")
	for i, c := range rec.cmds {
		if c == want && cmdIdx == -1 {
			cmdIdx = i
		}
		if applyIdx == -1 && strings.Contains(c, "kubectl apply -f") && strings.Contains(c, "manifests.yaml") {
			applyIdx = i
		}
	}
	require.NotEqual(t, -1, cmdIdx, "expected configmap creation command not found; cmds: %v", rec.cmds)
	require.NotEqual(t, -1, applyIdx, "expected kubectl apply command not found")
	assert.Less(t, cmdIdx, applyIdx, "configmap must be created before kubectl apply")
}

func TestClient_RolloutService_PopulatesConfigMapBeforeApply(t *testing.T) {
	cfg := &config.Config{Name: "api", Server: "srv", Stack: "/stacks/myapp"}
	client, rec := newRecordingClient(t, cfg)

	require.NoError(t, client.RolloutService(context.Background(), "api"))

	cmdIdx := -1
	applyIdx := -1
	want := expectedConfigMapCmd("api", "myapp", "/stacks/myapp")
	for i, c := range rec.cmds {
		if c == want && cmdIdx == -1 {
			cmdIdx = i
		}
		if applyIdx == -1 && strings.Contains(c, "kubectl apply -f") && strings.Contains(c, "manifests.yaml") {
			applyIdx = i
		}
	}
	require.NotEqual(t, -1, cmdIdx, "expected configmap creation command not found; cmds: %v", rec.cmds)
	require.NotEqual(t, -1, applyIdx, "expected kubectl apply command not found")
	assert.Less(t, cmdIdx, applyIdx, "configmap must be created before kubectl apply")
}

func TestClient_RestartStack_PopulatesConfigMapsBeforeApply(t *testing.T) {
	cfg := &config.Config{Name: "web", Server: "srv", Stack: "/stacks/myapp"}
	client, rec := newRecordingClient(t, cfg)

	require.NoError(t, client.RestartStack(context.Background()))

	want := expectedConfigMapCmd("web", "myapp", "/stacks/myapp")
	cmdIdx := -1
	applyIdx := -1
	for i, c := range rec.cmds {
		if c == want && cmdIdx == -1 {
			cmdIdx = i
		}
		if applyIdx == -1 && strings.Contains(c, "kubectl apply -f") && strings.Contains(c, "manifests.yaml") {
			applyIdx = i
		}
	}
	require.NotEqual(t, -1, cmdIdx)
	require.NotEqual(t, -1, applyIdx)
	assert.Less(t, cmdIdx, applyIdx)
}
