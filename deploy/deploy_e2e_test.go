//go:build e2e

package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/byteink/ssd/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupE2EEnvironment creates a complete test environment with SSH and Docker
func setupE2EEnvironment(t *testing.T) (*testhelpers.SSHContainer, *config.Config, string, func()) {
	t.Helper()

	ctx := context.Background()

	// Start SSH container
	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err, "failed to start SSH container")

	// Generate SSH config
	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err, "failed to write SSH config")

	// Install Docker on the SSH container
	t.Log("Installing Docker on SSH container...")
	installDocker := `
		apk add --no-cache docker docker-compose
		rc-update add docker boot
		service docker start
		sleep 2
	`
	_, err = sshContainer.RunSSH(installDocker)
	require.NoError(t, err, "failed to install Docker")

	// Create test project directory
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "testproject")
	err = os.Mkdir(projectDir, 0755)
	require.NoError(t, err)

	// Create a minimal Dockerfile
	dockerfile := `FROM alpine:latest
CMD ["sh", "-c", "echo 'Test app running' && sleep 3600"]
`
	err = os.WriteFile(filepath.Join(projectDir, "Dockerfile"), []byte(dockerfile), 0644)
	require.NoError(t, err)

	// Create stack directory on remote server
	stackPath := "/stacks/testapp"
	_, err = sshContainer.RunSSH(fmt.Sprintf("mkdir -p %s", stackPath))
	require.NoError(t, err)

	// Create test config
	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      stackPath,
		Dockerfile: "./Dockerfile",
		Context:    projectDir,
	}

	// Set SSH config environment variable
	oldSSHConfig := os.Getenv("SSH_CONFIG")
	os.Setenv("SSH_CONFIG", sshConfig)

	cleanup := func() {
		os.Setenv("SSH_CONFIG", oldSSHConfig)
		sshContainer.Cleanup(ctx)
	}

	return sshContainer, cfg, projectDir, cleanup
}

func TestE2E_FirstDeploy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sshContainer, cfg, projectDir, cleanup := setupE2EEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// Create a basic compose.yaml (empty, no services yet)
	initialCompose := `services:
  app:
    image: placeholder:0
    ports:
      - "8080:8080"
`
	_, err := sshContainer.RunSSH(fmt.Sprintf("echo '%s' > %s/compose.yaml", initialCompose, cfg.Stack))
	require.NoError(t, err)

	// Create client with SSH config executor
	sshConfigPath := filepath.Join(filepath.Dir(sshContainer.KeyPath), "ssh_config")
	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfigPath}
	client := remote.NewClientWithExecutor(cfg, executor)

	// Perform deployment
	output := new(strings.Builder)
	opts := &Options{Output: output}
	err = DeployWithClient(cfg, client, opts)
	require.NoError(t, err, "first deploy should succeed")

	// Verify version was incremented to 1
	version, err := client.GetCurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, version, "version should be 1 after first deploy")

	// Verify compose.yaml was updated
	composeContent, err := sshContainer.RunSSH(fmt.Sprintf("cat %s/compose.yaml", cfg.Stack))
	require.NoError(t, err)
	assert.Contains(t, composeContent, "ssd-testapp:1", "compose.yaml should contain new image tag")

	// Verify Docker image exists
	imageList, err := sshContainer.RunSSH("docker images --format '{{.Repository}}:{{.Tag}}'")
	require.NoError(t, err)
	assert.Contains(t, imageList, "ssd-testapp:1", "Docker image should exist")

	// Verify deployment output contains expected messages
	outputStr := output.String()
	assert.Contains(t, outputStr, "Checking current version", "output should mention version check")
	assert.Contains(t, outputStr, "deploying version: 1", "output should mention deploying version 1")
	assert.Contains(t, outputStr, "Deployed testapp version 1 successfully", "output should confirm success")

	// Save project directory for next test
	t.Setenv("E2E_PROJECT_DIR", projectDir)
}

func TestE2E_UpgradeDeploy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sshContainer, cfg, _, cleanup := setupE2EEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// Create initial compose.yaml with version 5
	initialCompose := `services:
  app:
    image: ssd-testapp:5
    ports:
      - "8080:8080"
`
	_, err := sshContainer.RunSSH(fmt.Sprintf("echo '%s' > %s/compose.yaml", initialCompose, cfg.Stack))
	require.NoError(t, err)

	// Build version 5 image manually so it exists
	_, err = sshContainer.RunSSH("echo 'FROM alpine:latest\nCMD sleep 3600' | docker build -t ssd-testapp:5 -")
	require.NoError(t, err)

	// Create client
	sshConfigPath := filepath.Join(filepath.Dir(sshContainer.KeyPath), "ssh_config")
	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfigPath}
	client := remote.NewClientWithExecutor(cfg, executor)

	// Verify current version is 5
	currentVersion, err := client.GetCurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, currentVersion, "current version should be 5")

	// Perform upgrade deployment
	output := new(strings.Builder)
	opts := &Options{Output: output}
	err = DeployWithClient(cfg, client, opts)
	require.NoError(t, err, "upgrade deploy should succeed")

	// Verify version was incremented to 6
	newVersion, err := client.GetCurrentVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, 6, newVersion, "version should be incremented to 6")

	// Verify compose.yaml was updated
	composeContent, err := sshContainer.RunSSH(fmt.Sprintf("cat %s/compose.yaml", cfg.Stack))
	require.NoError(t, err)
	assert.Contains(t, composeContent, "ssd-testapp:6", "compose.yaml should contain new version")
	assert.NotContains(t, composeContent, "ssd-testapp:5", "compose.yaml should not contain old version")

	// Verify new Docker image exists
	imageList, err := sshContainer.RunSSH("docker images --format '{{.Repository}}:{{.Tag}}'")
	require.NoError(t, err)
	assert.Contains(t, imageList, "ssd-testapp:6", "new Docker image should exist")

	// Verify output mentions version increment
	outputStr := output.String()
	assert.Contains(t, outputStr, "Current version: 5", "output should show current version")
	assert.Contains(t, outputStr, "deploying version: 6", "output should show new version")
}

func TestE2E_VerifyContainerRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sshContainer, cfg, _, cleanup := setupE2EEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// Create compose.yaml with a simple service
	composeContent := `services:
  app:
    image: ssd-testapp:1
    container_name: testapp-container
    command: sh -c "echo 'Container started' && sleep 3600"
`
	_, err := sshContainer.RunSSH(fmt.Sprintf("echo '%s' > %s/compose.yaml", composeContent, cfg.Stack))
	require.NoError(t, err)

	// Create client
	sshConfigPath := filepath.Join(filepath.Dir(sshContainer.KeyPath), "ssh_config")
	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfigPath}
	client := remote.NewClientWithExecutor(cfg, executor)

	// Deploy
	err = DeployWithClient(cfg, client, nil)
	require.NoError(t, err, "deployment should succeed")

	// Wait for container to start
	time.Sleep(2 * time.Second)

	// Verify container is running
	status, err := client.GetContainerStatus(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, status, "container status should not be empty")

	// Check container is actually running via docker ps
	containerList, err := sshContainer.RunSSH("docker ps --format '{{.Names}}\\t{{.Status}}'")
	require.NoError(t, err)
	assert.Contains(t, containerList, "testapp", "container should be running")
	assert.Contains(t, containerList, "Up", "container status should be Up")

	// Verify we can get logs
	logs, err := sshContainer.RunSSH(fmt.Sprintf("cd %s && docker compose logs --tail 10", cfg.Stack))
	require.NoError(t, err)
	assert.NotEmpty(t, logs, "logs should not be empty")
}

func TestE2E_VerifyVersionInCompose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sshContainer, cfg, _, cleanup := setupE2EEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	testCases := []struct {
		name            string
		initialVersion  int
		expectedVersion int
	}{
		{
			name:            "version 0 to 1",
			initialVersion:  0,
			expectedVersion: 1,
		},
		{
			name:            "version 10 to 11",
			initialVersion:  10,
			expectedVersion: 11,
		},
		{
			name:            "version 99 to 100",
			initialVersion:  99,
			expectedVersion: 100,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create compose.yaml with initial version
			var composeContent string
			if tc.initialVersion == 0 {
				composeContent = `services:
  app:
    image: placeholder:latest
    ports:
      - "8080:8080"
`
			} else {
				composeContent = fmt.Sprintf(`services:
  app:
    image: ssd-testapp:%d
    ports:
      - "8080:8080"
`, tc.initialVersion)
			}

			_, err := sshContainer.RunSSH(fmt.Sprintf("echo '%s' > %s/compose.yaml", composeContent, cfg.Stack))
			require.NoError(t, err)

			// If initial version > 0, create the image
			if tc.initialVersion > 0 {
				_, err = sshContainer.RunSSH(fmt.Sprintf("echo 'FROM alpine:latest' | docker build -t ssd-testapp:%d -", tc.initialVersion))
				require.NoError(t, err)
			}

			// Create client
			sshConfigPath := filepath.Join(filepath.Dir(sshContainer.KeyPath), "ssh_config")
			executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfigPath}
			client := remote.NewClientWithExecutor(cfg, executor)

			// Deploy
			err = DeployWithClient(cfg, client, nil)
			require.NoError(t, err)

			// Read compose.yaml and verify version
			composeResult, err := sshContainer.RunSSH(fmt.Sprintf("cat %s/compose.yaml", cfg.Stack))
			require.NoError(t, err)

			expectedImageTag := fmt.Sprintf("ssd-testapp:%d", tc.expectedVersion)
			assert.Contains(t, composeResult, expectedImageTag, "compose.yaml should contain correct version")

			// Verify version via client
			version, err := client.GetCurrentVersion(ctx)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedVersion, version, "GetCurrentVersion should return correct version")

			// Cleanup for next iteration
			_, _ = sshContainer.RunSSH(fmt.Sprintf("docker compose -f %s/compose.yaml down 2>/dev/null || true", cfg.Stack))
			_, _ = sshContainer.RunSSH("docker image prune -f")
		})
	}
}

func TestE2E_RsyncExclusions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sshContainer, cfg, projectDir, cleanup := setupE2EEnvironment(t)
	defer cleanup()

	// Create files that should be excluded
	excludedDirs := []string{".git", "node_modules", ".next"}
	for _, dir := range excludedDirs {
		dirPath := filepath.Join(projectDir, dir)
		err := os.Mkdir(dirPath, 0755)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dirPath, "test.txt"), []byte("excluded"), 0644)
		require.NoError(t, err)
	}

	// Create files that should be included
	err := os.WriteFile(filepath.Join(projectDir, "app.go"), []byte("package main"), 0644)
	require.NoError(t, err)

	// Create client
	sshConfigPath := filepath.Join(filepath.Dir(sshContainer.KeyPath), "ssh_config")
	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfigPath}
	client := remote.NewClientWithExecutor(cfg, executor)

	// Create initial compose
	initialCompose := `services:
  app:
    image: ssd-testapp:1
`
	_, err = sshContainer.RunSSH(fmt.Sprintf("echo '%s' > %s/compose.yaml", initialCompose, cfg.Stack))
	require.NoError(t, err)

	// Deploy
	err = DeployWithClient(cfg, client, nil)
	require.NoError(t, err)

	// Verify excluded directories don't exist on remote
	tempDirs, err := sshContainer.RunSSH("find /tmp -maxdepth 1 -name 'tmp.*' -type d 2>/dev/null | head -1")
	require.NoError(t, err)
	tempDir := strings.TrimSpace(tempDirs)

	if tempDir != "" {
		for _, excludedDir := range excludedDirs {
			output, _ := sshContainer.RunSSH(fmt.Sprintf("ls -la %s/%s 2>&1", tempDir, excludedDir))
			assert.Contains(t, output, "No such file", "excluded directory %s should not be synced", excludedDir)
		}

		// Verify included files exist
		output, err := sshContainer.RunSSH(fmt.Sprintf("ls %s/app.go 2>&1", tempDir))
		assert.NoError(t, err, "included file should be synced")
		assert.NotContains(t, output, "No such file", "app.go should exist")
	}
}

func TestE2E_DeploymentLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	sshContainer, cfg, _, cleanup := setupE2EEnvironment(t)
	defer cleanup()

	// Create compose.yaml
	composeContent := `services:
  app:
    image: ssd-testapp:1
`
	_, err := sshContainer.RunSSH(fmt.Sprintf("echo '%s' > %s/compose.yaml", composeContent, cfg.Stack))
	require.NoError(t, err)

	// Create client
	sshConfigPath := filepath.Join(filepath.Dir(sshContainer.KeyPath), "ssh_config")
	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfigPath}
	client := remote.NewClientWithExecutor(cfg, executor)

	// Acquire lock manually
	unlock, err := acquireLockWithTimeout(cfg.StackPath(), 2*time.Second)
	require.NoError(t, err)

	// Try to deploy while lock is held (should timeout)
	errChan := make(chan error, 1)
	go func() {
		errChan <- DeployWithClient(cfg, client, nil)
	}()

	select {
	case err := <-errChan:
		assert.Error(t, err, "deployment should fail when lock is held")
		assert.Contains(t, err.Error(), "timeout", "error should mention timeout")
	case <-time.After(3 * time.Second):
		t.Fatal("deployment should have timed out")
	}

	// Release lock
	unlock()

	// Now deployment should succeed
	err = DeployWithClient(cfg, client, nil)
	assert.NoError(t, err, "deployment should succeed after lock is released")
}
