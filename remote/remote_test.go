package remote

import (
	"context"
	"errors"
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

	mockExec.On("RunInteractive", "rsync", mock.MatchedBy(func(args []string) bool {
		// Verify essential args
		hasAvz := false
		hasDelete := false
		hasGitExclude := false
		hasNodeModulesExclude := false
		endsWithSlash := false
		hasRemoteDest := false

		for i, arg := range args {
			if arg == "-avz" {
				hasAvz = true
			}
			if arg == "--delete" {
				hasDelete = true
			}
			if arg == "--exclude" && i+1 < len(args) {
				if args[i+1] == ".git" {
					hasGitExclude = true
				}
				if args[i+1] == "node_modules" {
					hasNodeModulesExclude = true
				}
			}
		}

		// Check source path ends with /
		if len(args) >= 2 {
			source := args[len(args)-2]
			endsWithSlash = strings.HasSuffix(source, "/")
		}

		// Check destination format
		if len(args) >= 1 {
			dest := args[len(args)-1]
			hasRemoteDest = strings.Contains(dest, "testserver:") && strings.Contains(dest, "/remote/path")
		}

		return hasAvz && hasDelete && hasGitExclude && hasNodeModulesExclude && endsWithSlash && hasRemoteDest
	})).Return(nil)

	err := client.Rsync(context.Background(), "/local/path", "/remote/path")

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_Rsync_AlreadyHasSlash(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "rsync", mock.MatchedBy(func(args []string) bool {
		// Verify no double slash
		source := args[len(args)-2]
		return strings.HasSuffix(source, "/") && !strings.HasSuffix(source, "//")
	})).Return(nil)

	err := client.Rsync(context.Background(), "/local/path/", "/remote/path")

	require.NoError(t, err)
}

func TestClient_GetCurrentVersion_NewFormat(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	composeContent := `services:
  app:
    image: ssd-myapp:5
    ports:
      - "8080:8080"`

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		return strings.Contains(args[1], "cat") && strings.Contains(args[1], "compose.yaml")
	})).Return(composeContent, nil)

	version, err := client.GetCurrentVersion(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 5, version)
}

func TestClient_GetCurrentVersion_(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	composeContent := `services:
  app:
    image: ssd-myapp:3
    ports:
      - "8080:8080"`

	mockExec.On("Run", "ssh", mock.Anything).Return(composeContent, nil)

	version, err := client.GetCurrentVersion(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 3, version)
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
    image: ssd-myapp:123`

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
		cmd := args[1]
		return strings.Contains(cmd, "cd /tmp/build123") &&
			strings.Contains(cmd, "docker build") &&
			strings.Contains(cmd, "-t ssd-myapp:5") &&
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
		cmd := args[1]
		return strings.Contains(cmd, "-f docker/Dockerfile.prod")
	})).Return(nil)

	err := client.BuildImage(context.Background(), "/tmp/build", 1)

	require.NoError(t, err)
}

func TestClient_UpdateCompose(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// First call reads the compose file
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		return strings.Contains(args[1], "cat /stacks/myapp/compose.yaml")
	})).Return("services:\n  app:\n    image: ssd-myapp:4", nil)

	// Second call writes the updated compose file
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		return strings.Contains(cmd, "echo") &&
			strings.Contains(cmd, "ssd-myapp:5") &&
			strings.Contains(cmd, "> /stacks/myapp/compose.yaml")
	})).Return("", nil)

	err := client.UpdateCompose(context.Background(), 5)

	require.NoError(t, err)
	mockExec.AssertExpectations(t)
}

func TestClient_UpdateCompose_(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Read 
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		return strings.Contains(args[1], "cat")
	})).Return("services:\n  app:\n    image: ssd-myapp:3", nil)

	// Write with new format
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// Should replace with new ssd- format
		return strings.Contains(cmd, "ssd-myapp:5") && !strings.Contains(cmd, "ssd")
	})).Return("", nil)

	err := client.UpdateCompose(context.Background(), 5)

	require.NoError(t, err)
}

func TestClient_UpdateCompose_ReadError(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("Run", "ssh", mock.Anything).Return("", errors.New("file not found"))

	err := client.UpdateCompose(context.Background(), 5)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read compose.yaml")
}

func TestClient_RestartStack(t *testing.T) {
	cfg := newTestConfig()
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
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
		cmd := args[1]
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
		cmd := args[1]
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
