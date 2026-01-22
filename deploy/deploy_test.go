package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func (m *MockDeployer) GetCurrentVersion(ctx context.Context) (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

func (m *MockDeployer) MakeTempDir(ctx context.Context) (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockDeployer) Rsync(ctx context.Context, localPath, remotePath string) error {
	args := m.Called(localPath, remotePath)
	return args.Error(0)
}

func (m *MockDeployer) BuildImage(ctx context.Context, buildDir string, version int) error {
	args := m.Called(buildDir, version)
	return args.Error(0)
}

func (m *MockDeployer) UpdateCompose(ctx context.Context, version int) error {
	args := m.Called(version)
	return args.Error(0)
}

func (m *MockDeployer) RestartStack(ctx context.Context) error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDeployer) Cleanup(ctx context.Context, path string) error {
	args := m.Called(path)
	return args.Error(0)
}

func (m *MockDeployer) StackExists(ctx context.Context) (bool, error) {
	args := m.Called()
	return args.Bool(0), args.Error(1)
}

func (m *MockDeployer) CreateStack(ctx context.Context, composeContent string) error {
	args := m.Called(composeContent)
	return args.Error(0)
}

func (m *MockDeployer) EnsureNetwork(ctx context.Context, name string) error {
	args := m.Called(name)
	return args.Error(0)
}

func (m *MockDeployer) CreateEnvFile(ctx context.Context, serviceName string) error {
	args := m.Called(serviceName)
	return args.Error(0)
}

func (m *MockDeployer) IsServiceRunning(ctx context.Context, serviceName string) (bool, error) {
	args := m.Called(serviceName)
	return args.Bool(0), args.Error(1)
}

func (m *MockDeployer) PullImage(ctx context.Context, image string) error {
	args := m.Called(image)
	return args.Error(0)
}

func (m *MockDeployer) StartService(ctx context.Context, serviceName string) error {
	args := m.Called(serviceName)
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
	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(4, nil)
	mockClient.On("MakeTempDir").Return("/tmp/ssd-build-123", nil)
	mockClient.On("Rsync", mock.AnythingOfType("string"), "/tmp/ssd-build-123").Return(nil)
	mockClient.On("BuildImage", "/tmp/ssd-build-123", 5).Return(nil)
	mockClient.On("UpdateCompose", 5).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/ssd-build-123").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestDeploy_FirstDeploy(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// First deploy - version starts at 0
	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil) // Version 1
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestDeploy_GetVersionError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, errors.New("SSH connection failed"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get current version")
	assert.Contains(t, err.Error(), "SSH connection failed")
}

func TestDeploy_MakeTempDirError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StackExists").Return(true, nil)
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

	mockClient.On("StackExists").Return(true, nil)
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

	mockClient.On("StackExists").Return(true, nil)
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

	mockClient.On("StackExists").Return(true, nil)
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

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 2).Return(nil)
	mockClient.On("UpdateCompose", 2).Return(nil)
	mockClient.On("StartService", "myapp").Return(errors.New("compose up failed"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start service")
	mockClient.AssertCalled(t, "Cleanup", "/tmp/build")
}

func TestDeploy_CleanupCalledEvenOnSuccess(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
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

			mockClient.On("StackExists").Return(true, nil)
			mockClient.On("GetCurrentVersion").Return(tt.currentVersion, nil)
			mockClient.On("MakeTempDir").Return("/tmp/build", nil)
			mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
			mockClient.On("BuildImage", "/tmp/build", tt.expectedVersion).Return(nil)
			mockClient.On("UpdateCompose", tt.expectedVersion).Return(nil)
			mockClient.On("StartService", "myapp").Return(nil)
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

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(errors.New("cleanup failed")) // Error here

	err := DeployWithClient(cfg, mockClient, nil)

	// Deployment should still succeed even if cleanup fails
	require.NoError(t, err)
}

func TestDeploy_UsesCorrectTempDir(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	customTempDir := "/var/tmp/ssd-custom-abc123"

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return(customTempDir, nil)
	mockClient.On("Rsync", mock.Anything, customTempDir).Return(nil) // Must use custom dir
	mockClient.On("BuildImage", customTempDir, 1).Return(nil)        // Must use custom dir
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", customTempDir).Return(nil) // Must clean up custom dir

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestAcquireLock_Success(t *testing.T) {
	stackPath := "/stacks/test-app"
	unlock, err := acquireLock(stackPath)
	require.NoError(t, err)
	require.NotNil(t, unlock)

	unlock()
}

func TestAcquireLock_SamePathTwice(t *testing.T) {
	stackPath := "/stacks/test-concurrent"

	unlock1, err := acquireLockWithTimeout(stackPath, 2*time.Second)
	require.NoError(t, err)
	defer unlock1()

	done := make(chan error, 1)
	go func() {
		_, err := acquireLockWithTimeout(stackPath, 500*time.Millisecond)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timeout waiting for deployment lock")
	case <-time.After(2 * time.Second):
		t.Fatal("expected timeout error but goroutine did not complete")
	}
}

func TestAcquireLock_ConcurrentDeploys(t *testing.T) {
	stackPath := "/stacks/concurrent-test"

	var wg sync.WaitGroup
	results := make(chan error, 2)
	ready := make(chan struct{})

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			<-ready

			unlock, err := acquireLockWithTimeout(stackPath, 300*time.Millisecond)
			if err != nil {
				results <- fmt.Errorf("goroutine %d: %w", id, err)
				return
			}

			time.Sleep(400 * time.Millisecond)
			unlock()
			results <- nil
		}(i)
	}

	time.Sleep(10 * time.Millisecond)
	close(ready)

	wg.Wait()
	close(results)

	successCount := 0
	timeoutCount := 0

	for err := range results {
		if err == nil {
			successCount++
		} else if err != nil && err.Error() != "" {
			timeoutCount++
		}
	}

	assert.Equal(t, 1, successCount, "exactly one deployment should succeed")
	assert.Equal(t, 1, timeoutCount, "exactly one deployment should timeout")
}

func TestAcquireLock_DifferentPaths(t *testing.T) {
	unlock1, err := acquireLock("/stacks/app1")
	require.NoError(t, err)
	defer unlock1()

	unlock2, err := acquireLock("/stacks/app2")
	require.NoError(t, err)
	defer unlock2()
}

func TestAcquireLock_UnlockReleases(t *testing.T) {
	stackPath := "/stacks/unlock-test"

	unlock1, err := acquireLock(stackPath)
	require.NoError(t, err)
	unlock1()

	unlock2, err := acquireLock(stackPath)
	require.NoError(t, err)
	defer unlock2()
}

func TestAcquireLock_LockFileCreated(t *testing.T) {
	stackPath := "/stacks/lockfile-test"
	unlock, err := acquireLock(stackPath)
	require.NoError(t, err)
	defer unlock()

	expectedPath := filepath.Join(os.TempDir(), "ssd-lock-")
	files, err := filepath.Glob(expectedPath + "*")
	require.NoError(t, err)
	assert.NotEmpty(t, files, "lock file should exist")
}

func TestDeploy_WithLocking(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 2).Return(nil)
	mockClient.On("UpdateCompose", 2).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestDeploy_LockReleasedOnError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, errors.New("connection failed"))

	err := DeployWithClient(cfg, mockClient, nil)
	require.Error(t, err)

	unlock, err := acquireLock(cfg.StackPath())
	require.NoError(t, err, "lock should be released after deployment error")
	unlock()
}

func TestAcquireLock_InvalidFile(t *testing.T) {
	t.Skip("Test requires monkey patching os.TempDir which is not supported")
}

func TestAcquireLock_FlockBehavior(t *testing.T) {
	lockPath := filepath.Join(os.TempDir(), "ssd-test-flock")
	t.Cleanup(func() {
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			t.Logf("failed to remove lock file: %v", err)
		}
	})

	f1, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := f1.Close(); err != nil {
			t.Logf("failed to close f1: %v", err)
		}
	})

	err = unix.Flock(int(f1.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	require.NoError(t, err)

	f2, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := f2.Close(); err != nil {
			t.Logf("failed to close f2: %v", err)
		}
	})

	err = unix.Flock(int(f2.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	assert.Equal(t, unix.EWOULDBLOCK, err)

	err = unix.Flock(int(f1.Fd()), unix.LOCK_UN)
	require.NoError(t, err)

	err = unix.Flock(int(f2.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	require.NoError(t, err)
}

// Restart tests

func TestRestart_Success(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StartService", "myapp").Return(nil)

	err := RestartWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestRestart_Error(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StartService", "myapp").Return(errors.New("compose up failed"))

	err := RestartWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to restart service")
}

func TestRestart_LockReleasedOnError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StartService", "myapp").Return(errors.New("failed"))

	err := RestartWithClient(cfg, mockClient, nil)
	require.Error(t, err)

	// Lock should be released
	unlock, err := acquireLock(cfg.StackPath())
	require.NoError(t, err, "lock should be released after restart error")
	unlock()
}

// Rollback tests

func TestRollback_Success(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Current version is 5, rollback to 4
	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("UpdateCompose", 4).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)

	err := RollbackWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestRollback_GetVersionError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(0, errors.New("SSH failed"))

	err := RollbackWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get current version")
}

func TestRollback_CannotRollbackVersion0(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(0, nil)

	err := RollbackWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot rollback: no previous version")
}

func TestRollback_CannotRollbackVersion1(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(1, nil)

	err := RollbackWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot rollback: no previous version")
}

func TestRollback_UpdateComposeError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("UpdateCompose", 4).Return(errors.New("permission denied"))

	err := RollbackWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update compose.yaml")
}

func TestRollback_RestartError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("UpdateCompose", 4).Return(nil)
	mockClient.On("StartService", "myapp").Return(errors.New("compose up failed"))

	err := RollbackWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start service")
}

func TestRollback_VersionDecrement(t *testing.T) {
	tests := []struct {
		name            string
		currentVersion  int
		expectedVersion int
	}{
		{"version 2 to 1", 2, 1},
		{"version 10 to 9", 10, 9},
		{"version 100 to 99", 100, 99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockDeployer)
			cfg := newTestConfig()

			mockClient.On("GetCurrentVersion").Return(tt.currentVersion, nil)
			mockClient.On("UpdateCompose", tt.expectedVersion).Return(nil)
			mockClient.On("StartService", "myapp").Return(nil)

			err := RollbackWithClient(cfg, mockClient, nil)

			require.NoError(t, err)
			mockClient.AssertCalled(t, "UpdateCompose", tt.expectedVersion)
		})
	}
}

func TestRollback_LockReleasedOnError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("GetCurrentVersion").Return(0, errors.New("connection failed"))

	err := RollbackWithClient(cfg, mockClient, nil)
	require.Error(t, err)

	unlock, err := acquireLock(cfg.StackPath())
	require.NoError(t, err, "lock should be released after rollback error")
	unlock()
}

func TestRollback_PrebuiltService(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:   "nginx",
		Server: "testserver",
		Stack:  "/stacks/nginx",
		Image:  "nginx:latest", // Pre-built image
	}

	// Mock should not be called for pre-built services
	err := RollbackWithClient(cfg, mockClient, &Options{Output: io.Discard})

	require.NoError(t, err)
	mockClient.AssertNotCalled(t, "GetCurrentVersion")
	mockClient.AssertNotCalled(t, "UpdateCompose")
	mockClient.AssertNotCalled(t, "StartService")
}

// Auto-create stack tests

func TestDeploy_AutoCreateStack_FirstDeploy(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Stack doesn't exist yet
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myapp_internal").Return(nil)
	mockClient.On("CreateEnvFile", "myapp").Return(nil)

	// Normal deploy flow
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "StackExists")
	mockClient.AssertCalled(t, "CreateStack", mock.AnythingOfType("string"))
	mockClient.AssertCalled(t, "EnsureNetwork", "traefik_web")
	mockClient.AssertCalled(t, "EnsureNetwork", "myapp_internal")
	mockClient.AssertCalled(t, "CreateEnvFile", "myapp")
}

func TestDeploy_AutoCreateStack_SecondDeploySkipsCreation(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Stack already exists
	mockClient.On("StackExists").Return(true, nil)
	// CreateStack, EnsureNetwork, CreateEnvFile should NOT be called

	// Normal deploy flow
	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 2).Return(nil)
	mockClient.On("UpdateCompose", 2).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "StackExists")
	mockClient.AssertNotCalled(t, "CreateStack")
	mockClient.AssertNotCalled(t, "EnsureNetwork")
	mockClient.AssertNotCalled(t, "CreateEnvFile")
}

func TestDeploy_AutoCreateStack_StackExistsCheckError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// StackExists returns an error
	mockClient.On("StackExists").Return(false, errors.New("SSH connection failed"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check stack existence")
	mockClient.AssertCalled(t, "StackExists")
	mockClient.AssertNotCalled(t, "CreateStack")
}

func TestDeploy_AutoCreateStack_CreateStackError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Stack doesn't exist, but creation fails
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(errors.New("permission denied"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create stack")
	mockClient.AssertCalled(t, "CreateStack", mock.AnythingOfType("string"))
	mockClient.AssertNotCalled(t, "EnsureNetwork")
}

func TestDeploy_AutoCreateStack_EnsureNetworkError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Stack creation succeeds, but network creation fails
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(errors.New("network error"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to ensure network")
	mockClient.AssertCalled(t, "EnsureNetwork", "traefik_web")
	mockClient.AssertNotCalled(t, "CreateEnvFile")
}

func TestDeploy_AutoCreateStack_CreateEnvFileError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Networks succeed, but env file creation fails
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myapp_internal").Return(nil)
	mockClient.On("CreateEnvFile", "myapp").Return(errors.New("permission denied"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create env file")
	mockClient.AssertCalled(t, "CreateEnvFile", "myapp")
}

func TestDeploy_AutoCreateStack_WithDomain(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		Domain:     "example.com",
	}

	// Stack creation with domain should still ensure traefik_web
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myapp_internal").Return(nil)
	mockClient.On("CreateEnvFile", "web").Return(nil)

	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "EnsureNetwork", "traefik_web")
	mockClient.AssertCalled(t, "EnsureNetwork", "myapp_internal")
}

// Pre-built image tests

func TestDeploy_PrebuiltService_PullsImage(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:   "nginx",
		Server: "testserver",
		Stack:  "/stacks/nginx",
		Image:  "nginx:latest", // Pre-built image
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("PullImage", "nginx:latest").Return(nil)
	mockClient.On("StartService", "nginx").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "PullImage", "nginx:latest")
	mockClient.AssertNotCalled(t, "Rsync")
	mockClient.AssertNotCalled(t, "BuildImage")
	mockClient.AssertNotCalled(t, "UpdateCompose")
}

func TestDeploy_PrebuiltService_PullError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:   "nginx",
		Server: "testserver",
		Stack:  "/stacks/nginx",
		Image:  "nginx:latest",
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("PullImage", "nginx:latest").Return(errors.New("image not found"))
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to pull image")
	mockClient.AssertCalled(t, "PullImage", "nginx:latest")
	mockClient.AssertNotCalled(t, "BuildImage")
	mockClient.AssertNotCalled(t, "UpdateCompose")
}

func TestDeploy_BuiltService_BuildsImage(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		// No Image field - custom build
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "myapp").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "Rsync", mock.Anything, "/tmp/build")
	mockClient.AssertCalled(t, "BuildImage", "/tmp/build", 1)
	mockClient.AssertCalled(t, "UpdateCompose", 1)
	mockClient.AssertNotCalled(t, "PullImage")
}

// Dependency tests

func TestDeploy_DependencyNotRunning_Started(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"db"},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// Dependency check: db not running
	mockClient.On("IsServiceRunning", "db").Return(false, nil)
	mockClient.On("StartService", "db").Return(nil)

	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "IsServiceRunning", "db")
	mockClient.AssertCalled(t, "StartService", "db")
}

func TestDeploy_DependencyRunning_NotRestarted(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"db"},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// Dependency check: db already running
	mockClient.On("IsServiceRunning", "db").Return(true, nil)

	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "IsServiceRunning", "db")
	mockClient.AssertNotCalled(t, "StartService", "db")
}

func TestDeploy_MultipleDependencies_AllHandled(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"db", "redis", "cache"},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// db not running, redis running, cache not running
	mockClient.On("IsServiceRunning", "db").Return(false, nil)
	mockClient.On("IsServiceRunning", "redis").Return(true, nil)
	mockClient.On("IsServiceRunning", "cache").Return(false, nil)

	mockClient.On("StartService", "db").Return(nil)
	mockClient.On("StartService", "cache").Return(nil)

	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "IsServiceRunning", "db")
	mockClient.AssertCalled(t, "IsServiceRunning", "redis")
	mockClient.AssertCalled(t, "IsServiceRunning", "cache")
	mockClient.AssertCalled(t, "StartService", "db")
	mockClient.AssertNotCalled(t, "StartService", "redis")
	mockClient.AssertCalled(t, "StartService", "cache")
}

func TestDeploy_DependencyCheckError_FailsDeployment(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"db"},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// IsServiceRunning returns error
	mockClient.On("IsServiceRunning", "db").Return(false, errors.New("docker ps failed"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check if dependency db is running")
	mockClient.AssertCalled(t, "IsServiceRunning", "db")
	mockClient.AssertNotCalled(t, "StartService", "db")
	mockClient.AssertNotCalled(t, "MakeTempDir")
}

func TestDeploy_DependencyStartError_FailsDeployment(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"db"},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("IsServiceRunning", "db").Return(false, nil)

	// StartService returns error
	mockClient.On("StartService", "db").Return(errors.New("failed to start container"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start dependency db")
	mockClient.AssertCalled(t, "StartService", "db")
	mockClient.AssertNotCalled(t, "MakeTempDir")
}

func TestDeploy_NoDependencies_SkipsDependencyChecks(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  nil, // No dependencies
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertNotCalled(t, "IsServiceRunning")
	mockClient.AssertCalled(t, "StartService", "web") // Main service is started
}

func TestDeploy_PrebuiltDependency_PullsImage(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"postgres"},
	}

	// Pre-built postgres dependency
	postgresCfg := &config.Config{
		Name:   "postgres",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "postgres:16",
	}

	opts := &Options{
		Dependencies: map[string]*config.Config{
			"postgres": postgresCfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// Dependency not running, needs pull
	mockClient.On("IsServiceRunning", "postgres").Return(false, nil)
	mockClient.On("PullImage", "postgres:16").Return(nil)
	mockClient.On("StartService", "postgres").Return(nil)

	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "IsServiceRunning", "postgres")
	mockClient.AssertCalled(t, "PullImage", "postgres:16")
	mockClient.AssertCalled(t, "StartService", "postgres")
}

func TestDeploy_PrebuiltDependency_PullError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"postgres"},
	}

	postgresCfg := &config.Config{
		Name:   "postgres",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "postgres:16",
	}

	opts := &Options{
		Dependencies: map[string]*config.Config{
			"postgres": postgresCfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("IsServiceRunning", "postgres").Return(false, nil)
	mockClient.On("PullImage", "postgres:16").Return(errors.New("image not found"))

	err := DeployWithClient(cfg, mockClient, opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to pull image for dependency postgres")
	mockClient.AssertCalled(t, "PullImage", "postgres:16")
	mockClient.AssertNotCalled(t, "StartService", "postgres")
	mockClient.AssertNotCalled(t, "MakeTempDir")
}

func TestDeploy_CustomBuildDependency_NoPull(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"api"},
	}

	// Custom-built API dependency (no Image field)
	apiCfg := &config.Config{
		Name:       "api",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    "./api",
	}

	opts := &Options{
		Dependencies: map[string]*config.Config{
			"api": apiCfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// Dependency not running, but no pull for custom builds
	mockClient.On("IsServiceRunning", "api").Return(false, nil)
	mockClient.On("StartService", "api").Return(nil)

	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "IsServiceRunning", "api")
	mockClient.AssertNotCalled(t, "PullImage")
	mockClient.AssertCalled(t, "StartService", "api")
}
