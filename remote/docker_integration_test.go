//go:build integration

package remote

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DockerIntegrationSuite struct {
	suite.Suite
	ctx          context.Context
	cancel       context.CancelFunc
	sshContainer *testhelpers.SSHContainer
	dindHost     string
	sshConfig    string
}

func (s *DockerIntegrationSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 10*time.Minute)

	// Start SSH container with Docker installed
	sshContainer, err := testhelpers.StartSSHContainer(s.ctx, s.T())
	require.NoError(s.T(), err, "Failed to start SSH container")
	s.sshContainer = sshContainer

	// Write SSH config file
	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(s.T(), err, "Failed to write SSH config")
	s.sshConfig = sshConfig

	// Install Docker in SSH container
	s.installDockerInSSH()
}

func (s *DockerIntegrationSuite) TearDownSuite() {
	if s.sshContainer != nil {
		s.sshContainer.Cleanup(s.ctx)
	}
	s.cancel()
}

func (s *DockerIntegrationSuite) installDockerInSSH() {
	s.T().Log("Installing Docker in SSH container...")

	commands := []string{
		"sudo apk update",
		"sudo apk add docker",
		"sudo rc-update add docker boot",
		"sudo service docker start",
		"sleep 3",
	}

	for _, cmd := range commands {
		output, err := s.sshContainer.RunSSH(cmd)
		if err != nil {
			s.T().Logf("Command output: %s", output)
		}
		require.NoError(s.T(), err, "Failed to run: %s", cmd)
	}

	// Verify Docker is running
	output, err := s.sshContainer.RunSSH("sudo docker version")
	require.NoError(s.T(), err, "Docker not properly installed")
	s.T().Logf("Docker installed: %s", strings.Split(output, "\n")[0])
}

func (s *DockerIntegrationSuite) newClient(cfg *config.Config) *Client {
	executor := &testhelpers.SSHConfigExecutor{ConfigPath: s.sshConfig}
	return NewClientWithExecutor(cfg, executor)
}

func (s *DockerIntegrationSuite) createTestDockerfile(dir, content string) {
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	err := os.WriteFile(dockerfilePath, []byte(content), 0644)
	require.NoError(s.T(), err, "Failed to create Dockerfile")
}

func (s *DockerIntegrationSuite) TestDocker_SimpleBuild() {
	// Create temp directory for test files
	tmpDir, err := os.MkdirTemp("", "ssd-docker-test-*")
	require.NoError(s.T(), err)
	defer os.RemoveAll(tmpDir)

	// Create simple Dockerfile
	dockerfile := `FROM alpine:latest
RUN echo "Hello from SSD test"
CMD ["echo", "test"]
`
	s.createTestDockerfile(tmpDir, dockerfile)

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/home/testuser/stacks/testapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	client := s.newClient(cfg)

	// Create temp dir on remote
	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	// Rsync files to remote
	err = client.Rsync(tmpDir, remoteDir)
	require.NoError(s.T(), err)

	// Build image
	version := 1
	err = client.BuildImage(remoteDir, version)
	require.NoError(s.T(), err)

	// Verify image exists
	imageTag := fmt.Sprintf("ssd-%s:%d", cfg.Name, version)
	output, err := client.SSH(fmt.Sprintf("sudo docker images %s --format '{{.Repository}}:{{.Tag}}'", imageTag))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, imageTag, "Image should be tagged correctly")
}

func (s *DockerIntegrationSuite) TestDocker_CustomDockerfilePath() {
	tmpDir, err := os.MkdirTemp("", "ssd-docker-test-*")
	require.NoError(s.T(), err)
	defer os.RemoveAll(tmpDir)

	// Create docker subdirectory
	dockerDir := filepath.Join(tmpDir, "docker")
	err = os.Mkdir(dockerDir, 0755)
	require.NoError(s.T(), err)

	// Create Dockerfile in custom location
	dockerfile := `FROM alpine:latest
RUN echo "Custom Dockerfile location"
CMD ["echo", "custom"]
`
	dockerfilePath := filepath.Join(dockerDir, "Dockerfile.prod")
	err = os.WriteFile(dockerfilePath, []byte(dockerfile), 0644)
	require.NoError(s.T(), err)

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/home/testuser/stacks/testapp",
		Dockerfile: "./docker/Dockerfile.prod",
		Context:    ".",
	}

	client := s.newClient(cfg)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(tmpDir, remoteDir)
	require.NoError(s.T(), err)

	// Build with custom Dockerfile
	version := 1
	err = client.BuildImage(remoteDir, version)
	require.NoError(s.T(), err)

	// Verify image exists
	imageTag := fmt.Sprintf("ssd-%s:%d", cfg.Name, version)
	output, err := client.SSH(fmt.Sprintf("sudo docker images %s --format '{{.Repository}}:{{.Tag}}'", imageTag))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, imageTag)
}

func (s *DockerIntegrationSuite) TestDocker_BuildWithBuildArgs() {
	tmpDir, err := os.MkdirTemp("", "ssd-docker-test-*")
	require.NoError(s.T(), err)
	defer os.RemoveAll(tmpDir)

	// Create Dockerfile with ARG instruction
	dockerfile := `FROM alpine:latest
ARG BUILD_VERSION=unknown
ARG BUILD_DATE=unknown
RUN echo "Build Version: ${BUILD_VERSION}"
RUN echo "Build Date: ${BUILD_DATE}"
LABEL version="${BUILD_VERSION}"
LABEL date="${BUILD_DATE}"
CMD ["echo", "build-args-test"]
`
	s.createTestDockerfile(tmpDir, dockerfile)

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/home/testuser/stacks/testapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	client := s.newClient(cfg)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(tmpDir, remoteDir)
	require.NoError(s.T(), err)

	// Build image (build args would need to be supported in config)
	version := 1
	err = client.BuildImage(remoteDir, version)
	require.NoError(s.T(), err)

	// Verify image has correct labels
	imageTag := fmt.Sprintf("ssd-%s:%d", cfg.Name, version)
	output, err := client.SSH(fmt.Sprintf("sudo docker inspect %s --format '{{.Config.Labels}}'", imageTag))
	require.NoError(s.T(), err)

	// Image should exist with default ARG values
	assert.NotEmpty(s.T(), output, "Image should have labels")
}

func (s *DockerIntegrationSuite) TestDocker_ImageTagging() {
	tmpDir, err := os.MkdirTemp("", "ssd-docker-test-*")
	require.NoError(s.T(), err)
	defer os.RemoveAll(tmpDir)

	dockerfile := `FROM alpine:latest
CMD ["echo", "tagging-test"]
`
	s.createTestDockerfile(tmpDir, dockerfile)

	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/home/testuser/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	client := s.newClient(cfg)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(tmpDir, remoteDir)
	require.NoError(s.T(), err)

	// Build multiple versions
	for version := 1; version <= 3; version++ {
		err = client.BuildImage(remoteDir, version)
		require.NoError(s.T(), err, "Failed to build version %d", version)

		// Verify each version is tagged correctly
		expectedTag := fmt.Sprintf("ssd-myapp:%d", version)
		output, err := client.SSH(fmt.Sprintf("sudo docker images ssd-myapp --format '{{.Repository}}:{{.Tag}}'"))
		require.NoError(s.T(), err)
		assert.Contains(s.T(), output, expectedTag, "Version %d should be tagged", version)
	}

	// Verify all three versions exist
	output, err := client.SSH("sudo docker images ssd-myapp --format '{{.Repository}}:{{.Tag}}'")
	require.NoError(s.T(), err)

	assert.Contains(s.T(), output, "ssd-myapp:1")
	assert.Contains(s.T(), output, "ssd-myapp:2")
	assert.Contains(s.T(), output, "ssd-myapp:3")
}

func (s *DockerIntegrationSuite) TestDocker_BuildFailsWithInvalidDockerfile() {
	tmpDir, err := os.MkdirTemp("", "ssd-docker-test-*")
	require.NoError(s.T(), err)
	defer os.RemoveAll(tmpDir)

	// Create invalid Dockerfile
	dockerfile := `FROM nonexistent-image-that-does-not-exist:latest
RUN echo "This should fail"
`
	s.createTestDockerfile(tmpDir, dockerfile)

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/home/testuser/stacks/testapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	client := s.newClient(cfg)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(tmpDir, remoteDir)
	require.NoError(s.T(), err)

	// Build should fail
	err = client.BuildImage(remoteDir, 1)
	require.Error(s.T(), err, "Build should fail with invalid base image")
}

func (s *DockerIntegrationSuite) TestDocker_BuildWithContext() {
	tmpDir, err := os.MkdirTemp("", "ssd-docker-test-*")
	require.NoError(s.T(), err)
	defer os.RemoveAll(tmpDir)

	// Create a file that will be copied during build
	testFile := filepath.Join(tmpDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(s.T(), err)

	// Dockerfile that uses the context
	dockerfile := `FROM alpine:latest
COPY test.txt /app/test.txt
RUN cat /app/test.txt
CMD ["cat", "/app/test.txt"]
`
	s.createTestDockerfile(tmpDir, dockerfile)

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/home/testuser/stacks/testapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	client := s.newClient(cfg)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(tmpDir, remoteDir)
	require.NoError(s.T(), err)

	// Build should succeed with context file
	err = client.BuildImage(remoteDir, 1)
	require.NoError(s.T(), err)

	// Verify image exists
	imageTag := fmt.Sprintf("ssd-%s:1", cfg.Name)
	output, err := client.SSH(fmt.Sprintf("sudo docker images %s --format '{{.Repository}}:{{.Tag}}'", imageTag))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, imageTag)
}

func TestDockerIntegrationSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	suite.Run(t, new(DockerIntegrationSuite))
}
