package testhelpers

import (
	"github.com/stretchr/testify/mock"
)

// MockExecutor is a mock implementation of CommandExecutor
type MockExecutor struct {
	mock.Mock
}

// Run mocks command execution
func (m *MockExecutor) Run(name string, args ...string) (string, error) {
	callArgs := m.Called(name, args)
	return callArgs.String(0), callArgs.Error(1)
}

// RunInteractive mocks interactive command execution
func (m *MockExecutor) RunInteractive(name string, args ...string) error {
	callArgs := m.Called(name, args)
	return callArgs.Error(0)
}

// MockRemoteClient is a mock implementation of the remote client interface
type MockRemoteClient struct {
	mock.Mock
}

// SSH mocks SSH execution
func (m *MockRemoteClient) SSH(command string) (string, error) {
	args := m.Called(command)
	return args.String(0), args.Error(1)
}

// SSHInteractive mocks interactive SSH execution
func (m *MockRemoteClient) SSHInteractive(command string) error {
	args := m.Called(command)
	return args.Error(0)
}

// Rsync mocks file synchronization
func (m *MockRemoteClient) Rsync(localPath, remotePath string) error {
	args := m.Called(localPath, remotePath)
	return args.Error(0)
}

// GetCurrentVersion mocks version retrieval
func (m *MockRemoteClient) GetCurrentVersion() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

// BuildImage mocks image building
func (m *MockRemoteClient) BuildImage(buildDir string, version int) error {
	args := m.Called(buildDir, version)
	return args.Error(0)
}

// UpdateCompose mocks compose file updates
func (m *MockRemoteClient) UpdateCompose(version int) error {
	args := m.Called(version)
	return args.Error(0)
}

// RestartStack mocks stack restart
func (m *MockRemoteClient) RestartStack() error {
	args := m.Called()
	return args.Error(0)
}

// GetContainerStatus mocks container status retrieval
func (m *MockRemoteClient) GetContainerStatus() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

// GetLogs mocks log retrieval
func (m *MockRemoteClient) GetLogs(follow bool, tail int) error {
	args := m.Called(follow, tail)
	return args.Error(0)
}

// Cleanup mocks cleanup operations
func (m *MockRemoteClient) Cleanup(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

// MakeTempDir mocks temp directory creation
func (m *MockRemoteClient) MakeTempDir() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}
