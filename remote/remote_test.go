package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestConfig() *config.Config {
	return &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
}

func TestNewClient(t *testing.T) {
	cfg := newTestConfig()
	client := NewClient(cfg)

	assert.NotNil(t, client)
	assert.Equal(t, "testserver", client.server)
	assert.NotNil(t, client.executor)
}

func TestNewClientWithExecutor(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	assert.NotNil(t, client)
	assert.Equal(t, "testserver", client.server)
	assert.Equal(t, mockExec, client.executor)
}

func TestClient_SSH_Success(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", []string{"testserver", "echo hello"}).Return("hello\n", nil)

	output, err := client.SSH(context.Background(), "echo hello")

	require.NoError(t, err)
	assert.Equal(t, "hello\n", output)
	mockExec.AssertExpectations(t)
}

func TestClient_SSH_Error(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("connection refused"))

	_, err := client.SSH(context.Background(), "echo hello")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestClient_SSHInteractive_Success(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", []string{"testserver", "docker ps"}).Return(nil)

	err := client.SSHInteractive(context.Background(), "docker ps")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_SSHInteractive_Error(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.Anything).Return(errors.New("command failed"))

	err := client.SSHInteractive(context.Background(), "docker ps")

	require.Error(t, err)
}

func TestClient_Rsync(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)
	client.findGitRoot = func(dir string) (string, error) {
		return dir, nil // pretend dir is the git root
	}

	mockExec.On("RunInteractive", "bash", mock.MatchedBy(func(args []string) bool {
		if len(args) != 2 || args[0] != "-c" {
			return false
		}
		pipeline := args[1]
		return strings.Contains(pipeline, "git") &&
			strings.Contains(pipeline, "archive --format=tar HEAD") &&
			strings.Contains(pipeline, "ssh testserver") &&
			strings.Contains(pipeline, "tar xf - -C") &&
			strings.Contains(pipeline, "/remote/path") &&
			// Root context should NOT have --strip-components or -- path
			!strings.Contains(pipeline, "--strip-components") &&
			!strings.Contains(pipeline, " -- ")
	})).Return(nil)

	err := client.Rsync(context.Background(), "/local/path", "/remote/path")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_Rsync_Subdirectory(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)
	client.findGitRoot = func(dir string) (string, error) {
		return "/project", nil // git root is the parent
	}

	mockExec.On("RunInteractive", "bash", mock.MatchedBy(func(args []string) bool {
		if len(args) != 2 || args[0] != "-c" {
			return false
		}
		pipeline := args[1]
		// Should archive only the subdirectory
		return strings.Contains(pipeline, "-- apps/api") &&
			// Should strip 2 components (apps/api)
			strings.Contains(pipeline, "--strip-components=2")
	})).Return(nil)

	err := client.Rsync(context.Background(), "/project/apps/api", "/remote/path")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_Rsync_GitRootError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)
	client.findGitRoot = func(dir string) (string, error) {
		return "", fmt.Errorf("not a git repository")
	}

	err := client.Rsync(context.Background(), "/local/path", "/remote/path")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find git root")
}

func TestClient_GetCurrentVersion_NewFormat(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	composeContent := `services:
  app:
    image: ssd-myapp-myapp:5
    ports:
      - "8080:8080"`

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		return strings.Contains(args[1], "cat") && strings.Contains(args[1], "compose.yaml")
	})).Return(composeContent, nil)

	version, err := client.GetCurrentVersion(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 5, version)
}

func TestClient_GetCurrentVersion_NoMatch(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	composeContent := `services:
  app:
    image: nginx:latest`

	mockExec.On("Run", "ssh", mock.Anything).Return(composeContent, nil)

	version, err := client.GetCurrentVersion(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 0, version)
}

func TestClient_GetCurrentVersion_EmptyFile(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", nil)

	version, err := client.GetCurrentVersion(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 0, version)
}

func TestClient_GetCurrentVersion_MultiDigit(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	composeContent := `services:
  app:
    image: ssd-myapp-myapp:123`

	mockExec.On("Run", "ssh", mock.Anything).Return(composeContent, nil)

	version, err := client.GetCurrentVersion(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 123, version)
}

func TestClient_BuildImage(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "cd /tmp/build123") &&
			strings.Contains(cmd, "docker build") &&
			strings.Contains(cmd, "-t ssd-myapp-myapp:5") &&
			strings.Contains(cmd, "-f Dockerfile")
	})).Return(nil)

	err := client.BuildImage(context.Background(), "/tmp/build123", 5)

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_BuildImage_CustomDockerfile(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "docker/Dockerfile.prod",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "-f docker/Dockerfile.prod")
	})).Return(nil)

	err := client.BuildImage(context.Background(), "/tmp/build", 1)

	require.NoError(t, err)
}

func TestClient_BuildImage_WithTarget(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Target:     "production",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "docker build") &&
			strings.Contains(cmd, "-t ssd-myapp-myapp:3") &&
			strings.Contains(cmd, "--target production")
	})).Return(nil)

	err := client.BuildImage(context.Background(), "/tmp/build", 3)

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_BuildImage_NoTarget(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "docker build") &&
			!strings.Contains(cmd, "--target")
	})).Return(nil)

	err := client.BuildImage(context.Background(), "/tmp/build", 1)

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_UpdateCompose(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Single sed call to update image tag in-place
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "sed -i") &&
			strings.Contains(cmd, "ssd-myapp-myapp") &&
			strings.Contains(cmd, ":5") &&
			strings.Contains(cmd, "/stacks/myapp/compose.yaml")
	})).Return("", nil)

	err := client.UpdateCompose(context.Background(), 5)

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_UpdateCompose_SedError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("sed failed"))

	err := client.UpdateCompose(context.Background(), 5)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update compose.yaml")
}

func TestClient_RestartStack(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "cd /stacks/myapp") &&
			strings.Contains(cmd, "docker compose up -d")
	})).Return(nil)

	err := client.RestartStack(context.Background())

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_GetContainerStatus(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	expectedOutput := "myapp-app-1\tUp 5 minutes"
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cd /stacks/myapp") &&
			strings.Contains(cmd, "docker compose ps")
	})).Return(expectedOutput, nil)

	status, err := client.GetContainerStatus(context.Background())

	require.NoError(t, err)
	assert.Contains(t, status, "Up 5 minutes")
}

func TestClient_GetLogs_NoFollow(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "docker compose logs") &&
			!strings.Contains(cmd, "-f") &&
			strings.Contains(cmd, "--tail 100")
	})).Return(nil)

	err := client.GetLogs(context.Background(), false, 100)

	require.NoError(t, err)
}

func TestClient_GetLogs_WithFollow(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "docker compose logs") &&
			strings.Contains(cmd, "-f")
	})).Return(nil)

	err := client.GetLogs(context.Background(), true, 0)

	require.NoError(t, err)
}

func TestClient_Cleanup(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", []string{"testserver", "rm -rf /tmp/build123"}).Return("", nil)

	err := client.Cleanup(context.Background(), "/tmp/build123")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_Cleanup_InvalidPath(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	err := client.Cleanup(context.Background(), "/var/lib/something")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path must start with /tmp/")
	mockExec.AssertNotCalled(t, "Run")
}

func TestClient_Cleanup_EmptyPath(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	err := client.Cleanup(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path cannot be empty")
	mockExec.AssertNotCalled(t, "Run")
}

func TestClient_Cleanup_PathTraversal(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	err := client.Cleanup(context.Background(), "/tmp/../etc/passwd")

	require.Error(t, err)
	// Path gets normalized to /etc/passwd by filepath.Clean, which fails the /tmp/ prefix check
	assert.Contains(t, err.Error(), "path must start with /tmp/")
	mockExec.AssertNotCalled(t, "Run")
}

func TestClient_MakeTempDir(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", []string{"testserver", "mktemp -d"}).Return("/tmp/tmp.abc123\n", nil)

	dir, err := client.MakeTempDir(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "/tmp/tmp.abc123", dir) // Trimmed
	mockExec.AssertExpectations(t)
}

func TestClient_MakeTempDir_Error(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("disk full"))

	_, err := client.MakeTempDir(context.Background())

	require.Error(t, err)
}

// Test that Client implements RemoteClient interface
func TestClient_ImplementsRemoteClient(t *testing.T) {
	cfg := newTestConfig()
	var _ RemoteClient = NewClient(cfg)
}

func TestValidateTempPath_Success(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"simple temp path", "/tmp/ssd-build-123"},
		{"nested temp path", "/tmp/ssd/build/abc"},
		{"with hyphens", "/tmp/my-temp-dir"},
		{"with underscores", "/tmp/my_temp_dir"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTempPath(tt.path)
			assert.NoError(t, err)
		})
	}
}

func TestValidateTempPath_EmptyPath(t *testing.T) {
	err := ValidateTempPath("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path cannot be empty")
}

func TestValidateTempPath_NotInTmp(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"root path", "/var/lib/something"},
		{"home path", "/home/user/temp"},
		{"relative path", "tmp/something"},
		{"current dir", "./tmp/something"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTempPath(tt.path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "path must start with /tmp/")
		})
	}
}

func TestValidateTempPath_ContainsDotDot(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"direct parent ref", "/tmp/../etc/passwd"},
		{"nested parent ref", "/tmp/foo/../../../etc"},
		{"normalized to parent", "/tmp/foo/bar/../../.."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTempPath(tt.path)
			require.Error(t, err)
			// These paths get normalized by filepath.Clean to paths outside /tmp/
			// So they fail the prefix check, not the ".." check
			assert.Contains(t, err.Error(), "path must start with /tmp/")
		})
	}
}

func TestValidateTempPath_NormalizedPath(t *testing.T) {
	// Path with redundant slashes should still validate if it's in /tmp
	err := ValidateTempPath("/tmp//ssd-build//123")
	assert.NoError(t, err)
}

func TestClient_StackExists_True(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Mock the SSH command that checks directory and compose.yaml existence
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "test -d /stacks/myapp") &&
			strings.Contains(cmd, "test -f /stacks/myapp/compose.yaml") &&
			strings.Contains(cmd, "echo yes") &&
			strings.Contains(cmd, "echo no")
	})).Return("yes\n", nil)

	exists, err := client.StackExists(context.Background())

	require.NoError(t, err)
	assert.True(t, exists)
	mockExec.AssertExpectations(t)
}

func TestClient_StackExists_False_NoDirectory(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Stack directory doesn't exist
	mockExec.On("Run", "ssh", mock.Anything).Return("no\n", nil)

	exists, err := client.StackExists(context.Background())

	require.NoError(t, err)
	assert.False(t, exists)
}

func TestClient_StackExists_False_NoComposeFile(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Directory exists but compose.yaml doesn't
	mockExec.On("Run", "ssh", mock.Anything).Return("no\n", nil)

	exists, err := client.StackExists(context.Background())

	require.NoError(t, err)
	assert.False(t, exists)
}

func TestClient_StackExists_SSHError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// SSH command fails
	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("connection refused"))

	_, err := client.StackExists(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
}

func TestClient_StackExists_EmptyOutput(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Empty output should be treated as false
	mockExec.On("Run", "ssh", mock.Anything).Return("", nil)

	exists, err := client.StackExists(context.Background())

	require.NoError(t, err)
	assert.False(t, exists)
}

func TestClient_StackExists_UnexpectedOutput(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Unexpected output should be treated as false
	mockExec.On("Run", "ssh", mock.Anything).Return("maybe\n", nil)

	exists, err := client.StackExists(context.Background())

	require.NoError(t, err)
	assert.False(t, exists)
}

func TestClient_IsServiceRunning_Running(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Docker compose ps returns JSON with State "running"
	composeJSON := `{"Name":"myapp-web-1","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":8080,"PublishedPort":8080,"Protocol":"tcp"}]}`

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cd /stacks/myapp") &&
			strings.Contains(cmd, "docker compose ps --format json") &&
			strings.Contains(cmd, "web")
	})).Return(composeJSON, nil)

	isRunning, err := client.IsServiceRunning(context.Background(), "web")

	require.NoError(t, err)
	assert.True(t, isRunning)
	mockExec.AssertExpectations(t)
}

func TestClient_IsServiceRunning_Stopped(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Docker compose ps returns JSON with State "exited"
	composeJSON := `{"Name":"myapp-web-1","State":"exited","ExitCode":0}`

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "docker compose ps --format json")
	})).Return(composeJSON, nil)

	isRunning, err := client.IsServiceRunning(context.Background(), "web")

	require.NoError(t, err)
	assert.False(t, isRunning)
}

func TestClient_IsServiceRunning_Missing(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Docker compose ps returns empty when service doesn't exist
	mockExec.On("Run", "ssh", mock.Anything).Return("", nil)

	isRunning, err := client.IsServiceRunning(context.Background(), "nonexistent")

	require.NoError(t, err)
	assert.False(t, isRunning)
}

func TestClient_IsServiceRunning_SSHError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// SSH command fails
	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("connection refused"))

	_, err := client.IsServiceRunning(context.Background(), "web")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
}

func TestClient_IsServiceRunning_InvalidJSON(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Docker compose ps returns invalid JSON
	mockExec.On("Run", "ssh", mock.Anything).Return("not json", nil)

	isRunning, err := client.IsServiceRunning(context.Background(), "web")

	require.NoError(t, err)
	assert.False(t, isRunning) // Invalid JSON treated as not running
}

func TestClient_IsServiceRunning_EmptyState(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// JSON with no State field
	composeJSON := `{"Name":"myapp-web-1"}`

	mockExec.On("Run", "ssh", mock.Anything).Return(composeJSON, nil)

	isRunning, err := client.IsServiceRunning(context.Background(), "web")

	require.NoError(t, err)
	assert.False(t, isRunning) // No state means not running
}

func TestClient_EnsureNetwork_CreateSuccess(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "docker network create mynetwork 2>/dev/null || true")
	})).Return("", nil)

	err := client.EnsureNetwork(context.Background(), "mynetwork")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_EnsureNetwork_AlreadyExists(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Even if network exists, the command should succeed (idempotent)
	mockExec.On("Run", "ssh", mock.Anything).Return("", nil)

	err := client.EnsureNetwork(context.Background(), "existing-network")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_EnsureNetwork_SSHError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("connection refused"))

	err := client.EnsureNetwork(context.Background(), "mynetwork")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
}

func TestClient_EnsureNetwork_EmptyName(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Empty network name should still call docker (docker will handle validation)
	mockExec.On("Run", "ssh", mock.Anything).Return("", nil)

	err := client.EnsureNetwork(context.Background(), "")

	require.NoError(t, err)
}

func TestClient_CreateEnvFile(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "/stacks/myapp") &&
			strings.Contains(cmd, "test -f") &&
			strings.Contains(cmd, "install -m 600 /dev/null /stacks/myapp/myservice.env")
	})).Return("", nil)

	err := client.CreateEnvFile(context.Background(), "myservice")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_CreateEnvFile_SSHError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("permission denied"))

	err := client.CreateEnvFile(context.Background(), "myservice")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
}

func TestClient_CreateEnvFile_SkipsExistingFile(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// The command uses "test -f" to skip existing files,
	// so calling it on an existing file should not overwrite it.
	// Verify the command structure includes the existence check.
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "test -f /stacks/myapp/myservice.env") &&
			strings.Contains(cmd, "install -m 600 /dev/null /stacks/myapp/myservice.env")
	})).Return("", nil)

	err := client.CreateEnvFile(context.Background(), "myservice")
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

func TestClient_GetEnvFile(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	envContent := "DB_HOST=localhost\nDB_PORT=5432\n"
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat /stacks/myapp/myservice.env")
	})).Return(envContent, nil)

	content, err := client.GetEnvFile(context.Background(), "myservice")

	require.NoError(t, err)
	assert.Equal(t, envContent, content)
	mockExec.AssertExpectations(t)
}

func TestClient_GetEnvFile_Missing(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", nil)

	content, err := client.GetEnvFile(context.Background(), "myservice")

	require.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestClient_GetEnvFile_SSHError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("connection refused"))

	_, err := client.GetEnvFile(context.Background(), "myservice")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
}

func TestClient_SetEnvVar(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	existingContent := "OLD_VAR=old_value\n"

	// First call reads env file
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat /stacks/myapp/myservice.env")
	})).Return(existingContent, nil).Once()

	// Second call writes updated env file (with mkdir -p to ensure dir exists)
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "install -m 600 /dev/stdin /stacks/myapp/myservice.env") &&
			strings.Contains(cmd, "OLD_VAR=old_value") &&
			strings.Contains(cmd, "NEW_VAR=new_value")
	})).Return("", nil).Once()

	err := client.SetEnvVar(context.Background(), "myservice", "NEW_VAR", "new_value")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_SetEnvVar_UpdateExisting(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	existingContent := "DB_HOST=localhost\nDB_PORT=5432\n"

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat")
	})).Return(existingContent, nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "install") &&
			strings.Contains(cmd, "DB_HOST=newhost") &&
			!strings.Contains(cmd, "DB_HOST=localhost")
	})).Return("", nil).Once()

	err := client.SetEnvVar(context.Background(), "myservice", "DB_HOST", "newhost")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_SetEnvVar_EmptyFile(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "install") &&
			strings.Contains(cmd, "MY_VAR=value")
	})).Return("", nil).Once()

	err := client.SetEnvVar(context.Background(), "myservice", "MY_VAR", "value")

	require.NoError(t, err)
}

func TestClient_SetEnvVar_GetError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("permission denied"))

	err := client.SetEnvVar(context.Background(), "myservice", "KEY", "value")

	require.Error(t, err)
}

func TestClient_RemoveEnvVar(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	existingContent := "DB_HOST=localhost\nDB_PORT=5432\nDB_USER=admin\n"

	// First call reads env file
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat /stacks/myapp/myservice.env")
	})).Return(existingContent, nil).Once()

	// Second call writes filtered env file (with mkdir -p to ensure dir exists)
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "install -m 600 /dev/stdin /stacks/myapp/myservice.env") &&
			strings.Contains(cmd, "DB_HOST=localhost") &&
			!strings.Contains(cmd, "DB_PORT=5432") &&
			strings.Contains(cmd, "DB_USER=admin")
	})).Return("", nil).Once()

	err := client.RemoveEnvVar(context.Background(), "myservice", "DB_PORT")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_RemoveEnvVar_NotFound(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	existingContent := "DB_HOST=localhost\n"

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat")
	})).Return(existingContent, nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "install") &&
			strings.Contains(cmd, "DB_HOST=localhost")
	})).Return("", nil).Once()

	err := client.RemoveEnvVar(context.Background(), "myservice", "NONEXISTENT")

	require.NoError(t, err) // Should succeed even if var doesn't exist
	mockExec.AssertExpectations(t)
}

func TestClient_RemoveEnvVar_EmptyFile(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p") &&
			strings.Contains(cmd, "install")
	})).Return("", nil).Once()

	err := client.RemoveEnvVar(context.Background(), "myservice", "ANY_KEY")

	require.NoError(t, err) // Should succeed even with empty file
}

func TestClient_RemoveEnvVar_GetError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("connection refused"))

	err := client.RemoveEnvVar(context.Background(), "myservice", "KEY")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
}

func TestClient_RemoveEnvVar_PreservesOtherVars(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	existingContent := "VAR1=value1\nVAR2=value2\nVAR3=value3\nVAR4=value4\n"

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cat")
	})).Return(existingContent, nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "install") &&
			strings.Contains(cmd, "VAR1=value1") &&
			!strings.Contains(cmd, "VAR2=value2") &&
			strings.Contains(cmd, "VAR3=value3") &&
			strings.Contains(cmd, "VAR4=value4")
	})).Return("", nil).Once()

	err := client.RemoveEnvVar(context.Background(), "myservice", "VAR2")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_CreateStack_Success(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	composeContent := `services:
  web:
    image: nginx:latest
    ports:
      - "80:80"
`

	// First call: mkdir -p
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir -p /stacks/myapp")
	})).Return("", nil).Once()

	// Second call: write temp file
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "compose.yaml.tmp") &&
			strings.Contains(cmd, "services:") &&
			strings.Contains(cmd, "nginx:latest")
	})).Return("", nil).Once()

	// Third call: validate with docker compose config
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "cd /stacks/myapp") &&
			strings.Contains(cmd, "docker compose -f compose.yaml.tmp config")
	})).Return("", nil).Once()

	// Fourth call: move tmp to final
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mv /stacks/myapp/compose.yaml.tmp /stacks/myapp/compose.yaml")
	})).Return("", nil).Once()

	err := client.CreateStack(context.Background(), composeContent)

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_CreateStack_MkdirFails(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir")
	})).Return("", errors.New("permission denied"))

	err := client.CreateStack(context.Background(), "services:\n  web:\n    image: nginx")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create stack directory")
}

func TestClient_CreateStack_WriteFileFails(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "compose.yaml.tmp")
	})).Return("", errors.New("disk full"))

	err := client.CreateStack(context.Background(), "services:\n  web:\n    image: nginx")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write compose.yaml.tmp")
}

func TestClient_CreateStack_ValidationFails(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	invalidCompose := `invalid yaml content: [}`

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "compose.yaml.tmp") && !strings.Contains(cmd, "docker compose")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "docker compose -f compose.yaml.tmp config")
	})).Return("", errors.New("yaml: invalid syntax"))

	err := client.CreateStack(context.Background(), invalidCompose)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "compose.yaml validation failed")
}

func TestClient_CreateStack_MoveFails(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mkdir")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "compose.yaml.tmp") && !strings.Contains(cmd, "docker compose")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "docker compose -f compose.yaml.tmp config")
	})).Return("", nil).Once()

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "mv") && strings.Contains(cmd, "compose.yaml.tmp")
	})).Return("", errors.New("operation not permitted"))

	err := client.CreateStack(context.Background(), "services:\n  web:\n    image: nginx")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to move compose.yaml.tmp to compose.yaml")
}

func TestClient_CreateStack_EmptyContent(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	err := client.CreateStack(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "compose content cannot be empty")
}

func TestClient_PullImage_Success(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "docker pull nginx:latest")
	})).Return(nil)

	err := client.PullImage(context.Background(), "nginx:latest")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_PullImage_SSHError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.Anything).Return(errors.New("connection refused"))

	err := client.PullImage(context.Background(), "nginx:latest")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestClient_StartService_Success(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[len(args)-1]
		return strings.Contains(cmd, "cd /stacks/myapp") &&
			strings.Contains(cmd, "docker compose up -d web")
	})).Return(nil)

	err := client.StartService(context.Background(), "web")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_StartService_SSHError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.Anything).Return(errors.New("connection refused"))

	err := client.StartService(context.Background(), "web")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}
