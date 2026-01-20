package testhelpers

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockExecutor is a mock implementation of CommandExecutor
type MockExecutor struct {
	mock.Mock
}

// Run mocks command execution
func (m *MockExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	callArgs := m.Called(name, args)
	return callArgs.String(0), callArgs.Error(1)
}

// RunInteractive mocks interactive command execution
func (m *MockExecutor) RunInteractive(ctx context.Context, name string, args ...string) error {
	callArgs := m.Called(name, args)
	return callArgs.Error(0)
}

// MockRemoteClient is a mock implementation of the remote client interface
type MockRemoteClient struct {
	mock.Mock
}

// SSH mocks SSH execution
func (m *MockRemoteClient) SSH(ctx context.Context, command string) (string, error) {
	args := m.Called(command)
	return args.String(0), args.Error(1)
}

// SSHInteractive mocks interactive SSH execution
func (m *MockRemoteClient) SSHInteractive(ctx context.Context, command string) error {
	args := m.Called(command)
	return args.Error(0)
}

// Rsync mocks file synchronization
func (m *MockRemoteClient) Rsync(ctx context.Context, localPath, remotePath string) error {
	args := m.Called(localPath, remotePath)
	return args.Error(0)
}

// GetCurrentVersion mocks version retrieval
func (m *MockRemoteClient) GetCurrentVersion(ctx context.Context) (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

// BuildImage mocks image building
func (m *MockRemoteClient) BuildImage(ctx context.Context, buildDir string, version int) error {
	args := m.Called(buildDir, version)
	return args.Error(0)
}

// UpdateCompose mocks compose file updates
func (m *MockRemoteClient) UpdateCompose(ctx context.Context, version int) error {
	args := m.Called(version)
	return args.Error(0)
}

// RestartStack mocks stack restart
func (m *MockRemoteClient) RestartStack(ctx context.Context) error {
	args := m.Called()
	return args.Error(0)
}

// GetContainerStatus mocks container status retrieval
func (m *MockRemoteClient) GetContainerStatus(ctx context.Context) (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

// GetLogs mocks log retrieval
func (m *MockRemoteClient) GetLogs(ctx context.Context, follow bool, tail int) error {
	args := m.Called(follow, tail)
	return args.Error(0)
}

// Cleanup mocks cleanup operations
func (m *MockRemoteClient) Cleanup(ctx context.Context, path string) error {
	args := m.Called(path)
	return args.Error(0)
}

// MakeTempDir mocks temp directory creation
func (m *MockRemoteClient) MakeTempDir(ctx context.Context) (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}
