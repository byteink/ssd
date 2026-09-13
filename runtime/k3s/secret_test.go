package k3s

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/byteink/ssd/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSetSecret_PatchJSON_SafeEscaping(t *testing.T) {
	// Verify that the JSON patch structure is safe for keys with special characters
	key := `my"key`
	encoded := "dGVzdA==" // base64("test")
	patch := map[string]map[string]string{
		"data": {key: encoded},
	}
	patchJSON, err := json.Marshal(patch)
	assert.NoError(t, err)
	assert.Contains(t, string(patchJSON), `"my\"key"`)
}

func TestRemoveSecret_JSONPointerEscaping(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{name: "simple key", key: "API_KEY", expected: "/data/API_KEY"},
		{name: "key with slash", key: "my/key", expected: "/data/my~1key"},
		{name: "key with tilde", key: "my~key", expected: "/data/my~0key"},
		{name: "key with both", key: "a/b~c", expected: "/data/a~1b~0c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the escaping logic from RemoveSecret
			escaped := tt.key
			escaped = replaceAll(escaped, "~", "~0")
			escaped = replaceAll(escaped, "/", "~1")

			patch := []map[string]string{
				{"op": "remove", "path": "/data/" + escaped},
			}
			patchJSON, err := json.Marshal(patch)
			assert.NoError(t, err)
			assert.Contains(t, string(patchJSON), tt.expected)
		})
	}
}

// replaceAll mirrors strings.ReplaceAll for test clarity
func replaceAll(s, old, new string) string {
	result := ""
	for i := 0; i < len(s); {
		if len(s)-i >= len(old) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}

// testClient builds a k3s client wired to a recording executor whose SSH
// calls return the given stub in order of registration.
func testClient(exec remote.CommandExecutor) *Client {
	return NewClientWithExecutor(&config.Config{
		Name:   "backend",
		Server: "testserver",
		Stack:  "/stacks/expensia",
	}, exec)
}

// A never-deployed stack has no namespace. kubectl's --ignore-not-found makes
// that exit 0 with empty output, so listing must report "no secrets", not fail.
func TestListSecrets_MissingNamespace_IsEmptyNotError(t *testing.T) {
	exec := new(testhelpers.MockExecutor)
	exec.On("Run", "ssh", mock.Anything).Return("", nil)

	out, err := testClient(exec).ListSecrets(context.Background(), "backend")

	require.NoError(t, err)
	assert.Empty(t, out)
}

// The old command ended in `2>/dev/null`, which threw away the one piece of
// information needed to debug a failure.
func TestListSecrets_CommandIgnoresNotFoundAndKeepsStderr(t *testing.T) {
	exec := &recordingExecutor{}
	exec.On("Run", "ssh", mock.Anything).Return("", nil)

	_, err := testClient(exec).ListSecrets(context.Background(), "backend")
	require.NoError(t, err)

	require.Len(t, exec.cmds, 1)
	assert.Contains(t, exec.cmds[0], "--ignore-not-found")
	assert.NotContains(t, exec.cmds[0], "2>/dev/null")
}

// A genuine failure (ssh down, broken kubeconfig, RBAC) must name the command
// target and carry the remote stderr through.
func TestListSecrets_RealFailure_ErrorNamesTarget(t *testing.T) {
	exec := new(testhelpers.MockExecutor)
	exec.On("Run", "ssh", mock.Anything).
		Return("", errors.New("ssh command failed: command failed: exit status 1\nThe connection to the server was refused"))

	_, err := testClient(exec).ListSecrets(context.Background(), "backend")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend-secret")
	assert.Contains(t, err.Error(), "expensia")
	assert.Contains(t, err.Error(), "connection to the server was refused")
}

func TestNamespaceExists(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		want   bool
	}{
		{name: "present", stdout: "namespace/expensia\n", want: true},
		{name: "absent", stdout: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := new(testhelpers.MockExecutor)
			exec.On("Run", "ssh", mock.Anything).Return(tt.stdout, nil)

			got, err := testClient(exec).NamespaceExists(context.Background())

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNamespaceExists_FailurePropagates(t *testing.T) {
	exec := new(testhelpers.MockExecutor)
	exec.On("Run", "ssh", mock.Anything).Return("", errors.New("permission denied"))

	_, err := testClient(exec).NamespaceExists(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "expensia")
	assert.Contains(t, err.Error(), "permission denied")
}

// build_secrets reference secrets during the build, so the secret must be
// settable before the first deploy — which means before the namespace exists.
func TestSetSecret_CreatesNamespaceBeforeSecret(t *testing.T) {
	exec := &recordingExecutor{}
	exec.On("Run", "ssh", mock.Anything).Return("", nil)

	err := testClient(exec).SetSecret(context.Background(), "backend", "API_KEY", "v")
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(exec.cmds), 3)
	assert.Contains(t, exec.cmds[0], "create namespace")
	assert.Contains(t, exec.cmds[0], "apply -f -")
	assert.Contains(t, exec.cmds[len(exec.cmds)-1], "create secret generic")
}

// The existence probe used to swallow SSH errors and fall through to "create",
// turning an unreachable server into a confusing create failure.
func TestSetSecret_ProbeFailureAborts(t *testing.T) {
	exec := &recordingExecutor{}
	exec.On("Run", "ssh", mock.Anything).Return("", nil).Once()
	exec.On("Run", "ssh", mock.Anything).Return("", errors.New("connection refused"))

	err := testClient(exec).SetSecret(context.Background(), "backend", "API_KEY", "v")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
	for _, cmd := range exec.cmds {
		assert.NotContains(t, cmd, "create secret generic")
	}
}

func TestRemoveSecret_NoSecret_ReportsPlainly(t *testing.T) {
	exec := new(testhelpers.MockExecutor)
	exec.On("Run", "ssh", mock.Anything).Return("", nil)

	err := testClient(exec).RemoveSecret(context.Background(), "backend", "API_KEY")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no secrets set")
	assert.Contains(t, err.Error(), "backend")
}
