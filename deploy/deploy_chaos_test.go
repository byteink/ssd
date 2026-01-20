package deploy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestChaos_ComposeUpdatedRestartFails(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(3, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 4).Return(nil)
	mockClient.On("UpdateCompose", 4).Return(nil)
	mockClient.On("RestartStack").Return(errors.New("docker compose up failed"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to restart stack")
	assert.Contains(t, err.Error(), "docker compose up failed")
	mockClient.AssertCalled(t, "UpdateCompose", 4)
	mockClient.AssertCalled(t, "RestartStack")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestChaos_BuildSucceededUpdateFails(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 6).Return(nil)
	mockClient.On("UpdateCompose", 6).Return(errors.New("permission denied on compose.yaml"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update compose.yaml")
	assert.Contains(t, err.Error(), "permission denied on compose.yaml")
	mockClient.AssertCalled(t, "BuildImage", "/tmp/build", 6)
	mockClient.AssertCalled(t, "UpdateCompose", 6)
	mockClient.AssertNotCalled(t, "RestartStack")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestChaos_CleanupFailsAfterSuccess(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(2, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 3).Return(nil)
	mockClient.On("UpdateCompose", 3).Return(nil)
	mockClient.On("RestartStack").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(errors.New("failed to remove temp directory"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "RestartStack")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestChaos_MkTempFailsDiskFull(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	diskFullErr := errors.New("no space left on device")

	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("", diskFullErr)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create temp directory")
	assert.Contains(t, err.Error(), "no space left on device")
	mockClient.AssertCalled(t, "GetCurrentVersion")
	mockClient.AssertCalled(t, "MakeTempDir")
	mockClient.AssertNotCalled(t, "Rsync", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "BuildImage", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "UpdateCompose", mock.Anything)
	mockClient.AssertNotCalled(t, "RestartStack")
	mockClient.AssertNotCalled(t, "Cleanup", mock.Anything)
}
