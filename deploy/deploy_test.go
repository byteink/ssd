package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func (m *MockDeployer) ReadCompose(ctx context.Context) (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
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

func (m *MockDeployer) CreateEnvFiles(ctx context.Context, serviceNames []string) error {
	args := m.Called(serviceNames)
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

func (m *MockDeployer) StopService(ctx context.Context, serviceName string) error {
	args := m.Called(serviceName)
	return args.Error(0)
}

func (m *MockDeployer) WaitForHealthy(ctx context.Context, serviceName string, timeout time.Duration) error {
	args := m.Called(serviceName, timeout)
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
	mockClient.On("CreateEnvFiles", []string{"myapp"}).Return(nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myapp_internal").Return(nil)

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
	mockClient.AssertCalled(t, "CreateEnvFiles", []string{"myapp"})
	mockClient.AssertCalled(t, "CreateStack", mock.AnythingOfType("string"))
	mockClient.AssertCalled(t, "EnsureNetwork", "traefik_web")
	mockClient.AssertCalled(t, "EnsureNetwork", "myapp_internal")
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
	mockClient.AssertNotCalled(t, "CreateEnvFiles")
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

	// Stack doesn't exist, env files succeed, but creation fails
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateEnvFiles", []string{"myapp"}).Return(nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(errors.New("permission denied"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create stack")
	mockClient.AssertCalled(t, "CreateEnvFiles", []string{"myapp"})
	mockClient.AssertCalled(t, "CreateStack", mock.AnythingOfType("string"))
	mockClient.AssertNotCalled(t, "EnsureNetwork")
}

func TestDeploy_AutoCreateStack_EnsureNetworkError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Env files and stack creation succeed, but network creation fails
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateEnvFiles", []string{"myapp"}).Return(nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(errors.New("network error"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to ensure network")
	mockClient.AssertCalled(t, "CreateEnvFiles", []string{"myapp"})
	mockClient.AssertCalled(t, "EnsureNetwork", "traefik_web")
}

func TestDeploy_AutoCreateStack_CreateEnvFilesError(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	// Env file creation fails before CreateStack is reached
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateEnvFiles", []string{"myapp"}).Return(errors.New("permission denied"))

	err := DeployWithClient(cfg, mockClient, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create env files")
	mockClient.AssertCalled(t, "CreateEnvFiles", []string{"myapp"})
	mockClient.AssertNotCalled(t, "CreateStack")
	mockClient.AssertNotCalled(t, "EnsureNetwork")
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
	mockClient.On("CreateEnvFiles", []string{"web"}).Return(nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myapp_internal").Return(nil)

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

// Integration tests for comprehensive deploy scenarios

func TestDeploy_IntegrationFirstDeployCreatesEverything(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		Domain:     "example.com",
	}

	// First deploy: stack doesn't exist
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateEnvFiles", []string{"web"}).Return(nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myapp_internal").Return(nil)

	// Normal build and deploy
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "StackExists")
	mockClient.AssertCalled(t, "CreateEnvFiles", []string{"web"})
	mockClient.AssertCalled(t, "CreateStack", mock.AnythingOfType("string"))
	mockClient.AssertCalled(t, "EnsureNetwork", "traefik_web")
	mockClient.AssertCalled(t, "EnsureNetwork", "myapp_internal")
}

func TestDeploy_IntegrationSecondDeploySkipsCreation(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	// Second deploy: stack already exists
	mockClient.On("StackExists").Return(true, nil)

	// Only normal deployment steps, no creation steps
	mockClient.On("GetCurrentVersion").Return(1, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 2).Return(nil)
	mockClient.On("UpdateCompose", 2).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "StackExists")
	mockClient.AssertNotCalled(t, "CreateStack")
	mockClient.AssertNotCalled(t, "EnsureNetwork")
	mockClient.AssertNotCalled(t, "CreateEnvFiles")
}

func TestDeploy_IntegrationWithStoppedDependency(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"db", "redis"},
	}

	dbCfg := &config.Config{
		Name:   "db",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "postgres:16",
	}

	redisCfg := &config.Config{
		Name:   "redis",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "redis:7",
	}

	opts := &Options{
		Dependencies: map[string]*config.Config{
			"db":    dbCfg,
			"redis": redisCfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// db stopped, redis stopped
	mockClient.On("IsServiceRunning", "db").Return(false, nil)
	mockClient.On("IsServiceRunning", "redis").Return(false, nil)

	// Both need to be pulled and started
	mockClient.On("PullImage", "postgres:16").Return(nil)
	mockClient.On("StartService", "db").Return(nil)
	mockClient.On("PullImage", "redis:7").Return(nil)
	mockClient.On("StartService", "redis").Return(nil)

	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "IsServiceRunning", "db")
	mockClient.AssertCalled(t, "IsServiceRunning", "redis")
	mockClient.AssertCalled(t, "PullImage", "postgres:16")
	mockClient.AssertCalled(t, "StartService", "db")
	mockClient.AssertCalled(t, "PullImage", "redis:7")
	mockClient.AssertCalled(t, "StartService", "redis")
}

func TestDeploy_IntegrationWithRunningDependency(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"db", "redis"},
	}

	dbCfg := &config.Config{
		Name:   "db",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "postgres:16",
	}

	redisCfg := &config.Config{
		Name:   "redis",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "redis:7",
	}

	opts := &Options{
		Dependencies: map[string]*config.Config{
			"db":    dbCfg,
			"redis": redisCfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)

	// Both already running
	mockClient.On("IsServiceRunning", "db").Return(true, nil)
	mockClient.On("IsServiceRunning", "redis").Return(true, nil)

	// Neither should be started or pulled
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("UpdateCompose", 1).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "IsServiceRunning", "db")
	mockClient.AssertCalled(t, "IsServiceRunning", "redis")
	mockClient.AssertNotCalled(t, "PullImage", "postgres:16")
	mockClient.AssertNotCalled(t, "StartService", "db")
	mockClient.AssertNotCalled(t, "PullImage", "redis:7")
	mockClient.AssertNotCalled(t, "StartService", "redis")
}

func TestDeploy_IntegrationPrebuiltServicePullsInsteadOfBuilds(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:   "nginx",
		Server: "testserver",
		Stack:  "/stacks/nginx",
		Image:  "nginx:alpine",
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("PullImage", "nginx:alpine").Return(nil)
	mockClient.On("StartService", "nginx").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "PullImage", "nginx:alpine")
	mockClient.AssertNotCalled(t, "Rsync")
	mockClient.AssertNotCalled(t, "BuildImage")
	mockClient.AssertNotCalled(t, "UpdateCompose")
}

func TestDeploy_AutoCreateStack_EnvFilesCreatedBeforeCreateStack(t *testing.T) {
	// Env files must be created BEFORE CreateStack, because docker compose config
	// validates that referenced env_file paths exist on disk.
	var callOrder []string

	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "api",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    "./api",
		DependsOn:  []string{"postgres"},
	}

	postgresCfg := &config.Config{
		Name:   "postgres",
		Server: "testserver",
		Stack:  "/stacks/myproject",
		Image:  "postgres:16",
	}

	opts := &Options{
		Dependencies: map[string]*config.Config{
			"postgres": postgresCfg,
		},
		AllServices: map[string]*config.Config{
			"api":      cfg,
			"postgres": postgresCfg,
		},
	}

	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		callOrder = append(callOrder, "CreateEnvFiles")
	})
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil).Run(func(args mock.Arguments) {
		callOrder = append(callOrder, "CreateStack")
	})
	mockClient.On("EnsureNetwork", mock.Anything).Return(nil)

	// Normal deploy flow (AllServices triggers regeneration instead of UpdateCompose)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("IsServiceRunning", "postgres").Return(false, nil)
	mockClient.On("IsServiceRunning", "api").Return(false, nil)
	mockClient.On("PullImage", "postgres:16").Return(nil)
	mockClient.On("StartService", "postgres").Return(nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	mockClient.On("ReadCompose").Return("", nil)
	mockClient.On("StartService", "api").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)
	require.NoError(t, err)

	// CreateEnvFiles must come before CreateStack in the initial phase
	envFilesIdx := -1
	createStackIdx := -1
	for i, call := range callOrder {
		if call == "CreateEnvFiles" && envFilesIdx == -1 {
			envFilesIdx = i
		}
		if call == "CreateStack" && createStackIdx == -1 {
			createStackIdx = i
		}
	}
	require.NotEqual(t, -1, envFilesIdx, "CreateEnvFiles should have been called")
	require.NotEqual(t, -1, createStackIdx, "CreateStack should have been called")
	assert.Less(t, envFilesIdx, createStackIdx,
		"CreateEnvFiles must be called before CreateStack")
}

func TestDeploy_AutoCreateStack_UsesAllServices(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "api",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    "./api",
		DependsOn:  []string{"postgres"},
	}

	postgresCfg := &config.Config{
		Name:   "postgres",
		Server: "testserver",
		Stack:  "/stacks/myproject",
		Image:  "postgres:16",
	}

	opts := &Options{
		Dependencies: map[string]*config.Config{
			"postgres": postgresCfg,
		},
		AllServices: map[string]*config.Config{
			"api":      cfg,
			"postgres": postgresCfg,
		},
	}

	// Stack doesn't exist - should create with ALL services
	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateEnvFiles", []string{"api", "postgres"}).Return(nil)
	mockClient.On("CreateStack", mock.MatchedBy(func(content string) bool {
		// Compose must contain both api AND postgres services
		return strings.Contains(content, "api:") && strings.Contains(content, "postgres:")
	})).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myproject_internal").Return(nil)

	// Normal deploy flow for api
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("IsServiceRunning", "postgres").Return(false, nil)
	mockClient.On("IsServiceRunning", "api").Return(false, nil)
	mockClient.On("PullImage", "postgres:16").Return(nil)
	mockClient.On("StartService", "postgres").Return(nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)
	// AllServices triggers compose regeneration instead of UpdateCompose
	mockClient.On("ReadCompose").Return("", nil)
	mockClient.On("StartService", "api").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	// Verify env files created for ALL services via batch call
	mockClient.AssertCalled(t, "CreateEnvFiles", []string{"api", "postgres"})
	mockClient.AssertNotCalled(t, "UpdateCompose")
}

func TestDeploy_AutoCreateStack_FallsBackToSingleService(t *testing.T) {
	// When AllServices is not provided, should still work with just the current service
	mockClient := new(MockDeployer)
	cfg := newTestConfig()

	mockClient.On("StackExists").Return(false, nil)
	mockClient.On("CreateEnvFiles", []string{"myapp"}).Return(nil)
	mockClient.On("CreateStack", mock.AnythingOfType("string")).Return(nil)
	mockClient.On("EnsureNetwork", "traefik_web").Return(nil)
	mockClient.On("EnsureNetwork", "myapp_internal").Return(nil)

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
	mockClient.AssertCalled(t, "CreateEnvFiles", []string{"myapp"})
}

func TestDeploy_BuildOnly_SkipsStartAndDeps(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "api",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		DependsOn:  []string{"postgres"},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(2, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 3).Return(nil)
	mockClient.On("UpdateCompose", 3).Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	opts := &Options{BuildOnly: true}
	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	// BuildOnly must NOT start the service or check dependencies
	mockClient.AssertNotCalled(t, "StartService")
	mockClient.AssertNotCalled(t, "IsServiceRunning")
}

func TestDeploy_BuildOnly_PrebuiltPullsButDoesNotStart(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:   "redis",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "redis:7",
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("PullImage", "redis:7").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	opts := &Options{BuildOnly: true}
	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "PullImage", "redis:7")
	mockClient.AssertNotCalled(t, "StartService")
}

func TestDeploy_IntegrationSingleServiceOnlyTargetRestarted(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "api",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    "./api",
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 6).Return(nil)
	mockClient.On("UpdateCompose", 6).Return(nil)
	mockClient.On("StartService", "api").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, nil)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertCalled(t, "StartService", "api")
	mockClient.AssertNotCalled(t, "RestartStack")
}

func TestDeploy_RegeneratesComposeWithAllServices(t *testing.T) {
	// When AllServices is provided, deploy should regenerate compose.yaml
	// from config instead of regex-replacing the image tag. This ensures
	// config changes (ports, labels, etc.) are always reflected.
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		Domain:     "example.com",
		Port:       3000,
	}

	dbCfg := &config.Config{
		Name:   "db",
		Server: "testserver",
		Stack:  "/stacks/myapp",
		Image:  "postgres:16",
	}

	opts := &Options{
		AllServices: map[string]*config.Config{
			"web": cfg,
			"db":  dbCfg,
		},
	}

	// Existing stack with version 2 for web
	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(2, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 3).Return(nil)

	// Regeneration: reads existing compose, generates new, writes
	mockClient.On("ReadCompose").Return("services:\n  web:\n    image: ssd-myapp-web:2\n  db:\n    image: postgres:16\n", nil)
	mockClient.On("IsServiceRunning", "web").Return(false, nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil)
	mockClient.On("CreateStack", mock.MatchedBy(func(content string) bool {
		return strings.Contains(content, "web:") &&
			strings.Contains(content, "db:") &&
			strings.Contains(content, "ssd-myapp-web:3") &&
			strings.Contains(content, "3000")
	})).Return(nil)

	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "ReadCompose")
	mockClient.AssertCalled(t, "CreateStack", mock.Anything)
	mockClient.AssertNotCalled(t, "UpdateCompose")
}

func TestDeploy_RegeneratesCompose_PreservesOtherVersions(t *testing.T) {
	// When regenerating compose, versions of other services must be
	// preserved from the existing compose.yaml, not reset to 0.
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "api",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    "./api",
	}

	webCfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    "./web",
	}

	opts := &Options{
		AllServices: map[string]*config.Config{
			"api": cfg,
			"web": webCfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 6).Return(nil)

	// Existing compose has web at version 10
	mockClient.On("ReadCompose").Return("services:\n  api:\n    image: ssd-myproject-api:5\n  web:\n    image: ssd-myproject-web:10\n", nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil)
	mockClient.On("CreateStack", mock.MatchedBy(func(content string) bool {
		// api bumped to 6, web stays at 10
		return strings.Contains(content, "ssd-myproject-api:6") &&
			strings.Contains(content, "ssd-myproject-web:10")
	})).Return(nil)

	mockClient.On("IsServiceRunning", "api").Return(false, nil)
	mockClient.On("StartService", "api").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "CreateStack", mock.Anything)
}

// --- Canary deployment tests ---

func TestCanaryDeploy_HappyPath(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	opts := &Options{
		AllServices: map[string]*config.Config{
			"web": cfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 6).Return(nil)
	mockClient.On("IsServiceRunning", "web").Return(true, nil)
	mockClient.On("ReadCompose").Return("services:\n  web:\n    image: ssd-myproject-web:5\n", nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil)

	// Track CreateStack calls to verify canary then final compose
	var createStackCalls []string
	mockClient.On("CreateStack", mock.Anything).Run(func(args mock.Arguments) {
		createStackCalls = append(createStackCalls, args.String(0))
	}).Return(nil)

	mockClient.On("StartService", "web-canary").Return(nil)
	mockClient.On("WaitForHealthy", "web-canary", defaultHealthTimeout).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("WaitForHealthy", "web", defaultHealthTimeout).Return(nil)
	mockClient.On("StopService", "web-canary").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)

	// First CreateStack: canary compose (web:5 + web-canary:6)
	require.Len(t, createStackCalls, 2)
	assert.Contains(t, createStackCalls[0], "web-canary")
	assert.Contains(t, createStackCalls[0], "ssd-myproject-web:5")
	assert.Contains(t, createStackCalls[0], "ssd-myproject-web:6")

	// Second CreateStack: final compose (web:6, no canary)
	assert.NotContains(t, createStackCalls[1], "web-canary")
	assert.Contains(t, createStackCalls[1], "ssd-myproject-web:6")

	mockClient.AssertCalled(t, "StartService", "web-canary")
	mockClient.AssertCalled(t, "WaitForHealthy", "web-canary", defaultHealthTimeout)
	mockClient.AssertCalled(t, "StartService", "web")
	mockClient.AssertCalled(t, "WaitForHealthy", "web", defaultHealthTimeout)
	mockClient.AssertCalled(t, "StopService", "web-canary")
}

func TestCanaryDeploy_HealthFailure_Rollback(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	opts := &Options{
		AllServices: map[string]*config.Config{
			"web": cfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 6).Return(nil)
	mockClient.On("IsServiceRunning", "web").Return(true, nil)
	mockClient.On("ReadCompose").Return("services:\n  web:\n    image: ssd-myproject-web:5\n", nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil)
	mockClient.On("CreateStack", mock.Anything).Return(nil)
	mockClient.On("StartService", "web-canary").Return(nil)

	// Health check fails
	mockClient.On("WaitForHealthy", "web-canary", defaultHealthTimeout).Return(fmt.Errorf("timeout"))

	// Rollback: stop canary, restore clean compose
	mockClient.On("StopService", "web-canary").Return(nil)

	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.Error(t, err)
	require.Contains(t, err.Error(), "canary health check failed")
	mockClient.AssertCalled(t, "StopService", "web-canary")
	// Main service should never have been restarted
	mockClient.AssertNotCalled(t, "StartService", "web")
}

func TestCanaryDeploy_SkipWhenServiceNotRunning(t *testing.T) {
	// First deploy: service not running, should use non-canary path
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	opts := &Options{
		AllServices: map[string]*config.Config{
			"web": cfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(0, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 1).Return(nil)

	// Service NOT running — skip canary
	mockClient.On("IsServiceRunning", "web").Return(false, nil)

	mockClient.On("ReadCompose").Return("", nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil)
	mockClient.On("CreateStack", mock.Anything).Return(nil)
	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	// Should use direct path, no canary interaction
	mockClient.AssertNotCalled(t, "StartService", "web-canary")
	mockClient.AssertNotCalled(t, "WaitForHealthy", mock.Anything, mock.Anything)
	mockClient.AssertCalled(t, "StartService", "web")
}

func TestCanaryDeploy_SkipForBuildOnly(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	opts := &Options{
		AllServices: map[string]*config.Config{
			"web": cfg,
		},
		BuildOnly: true,
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 6).Return(nil)
	mockClient.On("ReadCompose").Return("services:\n  web:\n    image: ssd-myproject-web:5\n", nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil)
	mockClient.On("CreateStack", mock.Anything).Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	// BuildOnly skips canary entirely — no service start, no health check
	mockClient.AssertNotCalled(t, "IsServiceRunning", mock.Anything)
	mockClient.AssertNotCalled(t, "StartService", mock.Anything)
	mockClient.AssertNotCalled(t, "WaitForHealthy", mock.Anything, mock.Anything)
}

func TestCanaryDeploy_CustomHealthTimeout(t *testing.T) {
	mockClient := new(MockDeployer)
	cfg := &config.Config{
		Name:       "web",
		Server:     "testserver",
		Stack:      "/stacks/myproject",
		Dockerfile: "./Dockerfile",
		Context:    ".",
		HealthCheck: &config.HealthCheck{
			Cmd:      "curl -f http://localhost:3000/health || exit 1",
			Interval: "10s",
			Timeout:  "5s",
			Retries:  5,
		},
	}

	opts := &Options{
		AllServices: map[string]*config.Config{
			"web": cfg,
		},
	}

	mockClient.On("StackExists").Return(true, nil)
	mockClient.On("GetCurrentVersion").Return(5, nil)
	mockClient.On("MakeTempDir").Return("/tmp/build", nil)
	mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
	mockClient.On("BuildImage", "/tmp/build", 6).Return(nil)
	mockClient.On("IsServiceRunning", "web").Return(true, nil)
	mockClient.On("ReadCompose").Return("services:\n  web:\n    image: ssd-myproject-web:5\n", nil)
	mockClient.On("CreateEnvFiles", mock.Anything).Return(nil)
	mockClient.On("CreateStack", mock.Anything).Return(nil)
	mockClient.On("StartService", "web-canary").Return(nil)

	// Expected: 5 retries * 10s + 30s buffer = 80s
	expectedTimeout := 80 * time.Second
	mockClient.On("WaitForHealthy", "web-canary", expectedTimeout).Return(nil)

	mockClient.On("StartService", "web").Return(nil)
	mockClient.On("WaitForHealthy", "web", expectedTimeout).Return(nil)
	mockClient.On("StopService", "web-canary").Return(nil)
	mockClient.On("Cleanup", "/tmp/build").Return(nil)

	err := DeployWithClient(cfg, mockClient, opts)

	require.NoError(t, err)
	mockClient.AssertCalled(t, "WaitForHealthy", "web-canary", expectedTimeout)
	mockClient.AssertCalled(t, "WaitForHealthy", "web", expectedTimeout)
}
