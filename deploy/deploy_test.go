package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/byteink/ssd/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// MockDeployer is a mock implementation of the Deployer interface
type MockDeployer struct {
	mock.Mock
}

func (m *MockDeployer) GetCurrentVersion() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

func (m *MockDeployer) MakeTempDir() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockDeployer) Rsync(localPath, remotePath string) error {
	args := m.Called(localPath, remotePath)
	return args.Error(0)
}

func (m *MockDeployer) BuildImage(buildDir string, version int) error {
	args := m.Called(buildDir, version)
	return args.Error(0)
}

func (m *MockDeployer) UpdateCompose(version int) error {
	args := m.Called(version)
	return args.Error(0)
}

func (m *MockDeployer) RestartStack() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDeployer) Cleanup(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

func newTestConfig() *config.Config {
	return &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
}

func TestDeploy_Success(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Setup expectations in order
	mockClient.On("GetCurrentVersion").Return(4, nil)
	mockClient.On("MakeTempDir").Return("/tmp/ssd-build-123", nil)
	mockClient.On("Rsync", mock.AnythingOfType("string"), "/tmp/ssd-build-123").Return(nil)
	mockClient.On("BuildImage", "/tmp/ssd-build-123", 5).Return(nil)
	mockClient.On("UpdateCompose", 5).Return(nil)
	mockClient.On("RestartStack").Return(nil)
	mockClient.On("Cleanup", "/tmp/ssd-build-123").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestDeploy_FirstDeploy(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// First deploy - version starts at 0
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil) // Version 1
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("RestartStack").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestDeploy_GetVersionError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(0, errors.New("SSH connection failed"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get current version")
	assert.Contains(t, err.Error(), "SSH connection failed")
}

func TestDeploy_MakeTempDirError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("", errors.New("disk full"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create temp directory")
	assert.Contains(t, err.Error(), "disk full")
}

func TestDeploy_RsyncError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(errors.New("connection reset"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil) // Cleanup still called in defer

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to sync code")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build") // Verify cleanup was called
}

func TestDeploy_BuildError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 2).Return(errors.New("docker build failed"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to build image")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestDeploy_UpdateComposeError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 2).Return(nil)
	mockClient.On("UpdateCompose", 2).Return(errors.New("permission denied"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update compose.yaml")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestDeploy_RestartError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 2).Return(nil)
	mockClient.On("UpdateCompose", 2).Return(nil)
	mockClient.On("RestartStack").Return(errors.New("compose up failed"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to restart stack")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestDeploy_CleanupCalledEvenOnSuccess(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("RestartStack").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestDeploy_VersionIncrement(t *testing.T) {
	tests := []struct {
		name            string
		currentVersion  int
		expectedVersion int
	}{
		{"first deploy", 0, 1},
		{"second deploy", 1, 2},
		{"large version", 99, 100},
		{"triple digits", 999, 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockDeployer)
			cfg := newTestConfig()

			mockClient.On("GetCurrentVersion").Return(tt.currentVersion, nil)
			mockClient.On("MakeTempDir").Return("/tmp/build", nil)
			mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
			mockClient.On("BuildImage", "/tmp/build", tt.expectedVersion).Return(nil)
			mockClient.On("UpdateCompose", tt.expectedVersion).Return(nil)
			mockClient.On("RestartStack").Return(nil)
			mockClient.On("Cleanup", "/tmp/build").Return(nil)

			err := DeployWithClient(cfg, mockClient, nil)

			require.NoError(t, err)
			mockClient.AssertCalled(t, "BuildImage", "/tmp/build", tt.expectedVersion)
			mockClient.AssertCalled(t, "UpdateCompose", tt.expectedVersion)
		})
	}
}

func TestDeploy_CleanupErrorIgnored(t *testing.T) {
	// Cleanup errors should not fail the deployment
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("RestartStack").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(errors.New("cleanup failed")) // Error here

	err := DeployWithClient(cfg, mockClient, nil)

	// Deployment should still succeed even if cleanup fails
	require.NoError(t, err)
}

func TestDeploy_UsesCorrectTempDir(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	customTempDir := "/var/tmp/ssd-custom-abc123"

	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return(customTempDir, nil)
	mockClient.On("Rsync", mock.Anything, customTempDir).Return(nil) // Must use custom dir
	mockClient.On("BuildImage", customTempDir, 1).Return(nil)        // Must use custom dir
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("RestartStack").Return(nil)
	mockClient.On("Cleanup", customTempDir).Return(nil) // Must clean up custom dir

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}
