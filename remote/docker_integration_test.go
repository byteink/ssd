//go:build integration

package remote

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
	ctx           context.Context
	cancel        context.CancelFunc
	sshContainer  *testhelpers.SSHContainer
	dindContainer *testhelpers.DinDContainer
	sshConfig     string
	tempDir       string
}

func (s *DockerIntegrationSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 10*time.Minute)

	// Start DinD container first
	dindContainer, err := testhelpers.StartDinDContainer(s.ctx, s.T())
	require.NoError(s.T(), err, "Failed to start DinD container")
	s.dindContainer = dindContainer

	// Start SSH container
	sshContainer, err := testhelpers.StartSSHContainer(s.ctx, s.T())
	require.NoError(s.T(), err, "Failed to start SSH container")
	s.sshContainer = sshContainer

	// Write SSH config
	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(s.T(), err, "Failed to write SSH config")
	s.sshConfig = sshConfig

	// Install Docker CLI in SSH container and configure it to use DinD
	dockerHost := dindContainer.DockerHost()
	installCmd := fmt.Sprintf(`
		set -e
		apk add --no-cache docker-cli
		echo 'export DOCKER_HOST=%s' >> ~/.profile
		echo 'export DOCKER_HOST=%s' >> ~/.bashrc
		export DOCKER_HOST=%s
		docker version
	`, dockerHost, dockerHost, dockerHost)

	output, err := sshContainer.RunSSH(installCmd)
	require.NoError(s.T(), err, "Failed to install Docker in SSH container: %s", output)

	// Create temp directory for test files
	tempDir, err := os.MkdirTemp("", "ssd-docker-test-*")
	require.NoError(s.T(), err, "Failed to create temp dir")
	s.tempDir = tempDir
}

func (s *DockerIntegrationSuite) TearDownSuite() {
	if s.tempDir != "" {
		os.RemoveAll(s.tempDir)
	}
	if s.sshContainer != nil {
		s.sshContainer.Cleanup(s.ctx)
	}
	if s.dindContainer != nil {
		s.dindContainer.Cleanup(s.ctx)
	}
	s.cancel()
}

func (s *DockerIntegrationSuite) newClient(name string) *Client {
	cfg := &config.Config{
		Name:       name,
		Server:     "testserver",
		Stack:      fmt.Sprintf("/home/testuser/stacks/%s", name),
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	executor := &dockerSSHExecutor{
		configPath: s.sshConfig,
		dockerHost: s.dindContainer.DockerHost(),
	}
	return NewClientWithExecutor(cfg, executor)
}

// dockerSSHExecutor wraps SSHConfigExecutor and injects DOCKER_HOST environment variable
type dockerSSHExecutor struct {
	configPath string
	dockerHost string
}

func (e *dockerSSHExecutor) Run(name string, args ...string) (string, error) {
	if name == "ssh" && len(args) >= 2 {
		// Inject DOCKER_HOST into SSH command
		command := args[len(args)-1]
		args[len(args)-1] = fmt.Sprintf("export DOCKER_HOST=%s && %s", e.dockerHost, command)
		newArgs := []string{"-F", e.configPath}
		newArgs = append(newArgs, args...)
		args = newArgs
	} else if name == "ssh" {
		newArgs := []string{"-F", e.configPath}
		newArgs = append(newArgs, args...)
		args = newArgs
	}
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command failed: %s\n%s", err, stderr.String())
	}
	return stdout.String(), nil
}

func (e *dockerSSHExecutor) RunInteractive(name string, args ...string) error {
	if name == "ssh" && len(args) >= 2 {
		command := args[len(args)-1]
		args[len(args)-1] = fmt.Sprintf("export DOCKER_HOST=%s && %s", e.dockerHost, command)
		newArgs := []string{"-F", e.configPath}
		newArgs = append(newArgs, args...)
		args = newArgs
	} else if name == "ssh" {
		newArgs := []string{"-F", e.configPath}
		newArgs = append(newArgs, args...)
		args = newArgs
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *DockerIntegrationSuite) TestDocker_SimpleBuild() {
	client := s.newClient("simple-build")

	// Create a simple Dockerfile
	buildDir := filepath.Join(s.tempDir, "simple-build")
	err := os.MkdirAll(buildDir, 0755)
	require.NoError(s.T(), err)

	dockerfile := `FROM alpine:latest
RUN echo "Hello from simple build" > /hello.txt
CMD ["cat", "/hello.txt"]
`
	err = os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644)
	require.NoError(s.T(), err)

	// Rsync to remote
	remoteDir, err := client.MakeTempDir(context.Background())
	require.NoError(s.T(), err)
	defer client.Cleanup(context.Background(), remoteDir)

	err = client.Rsync(context.Background(), buildDir, remoteDir)
	require.NoError(s.T(), err)

	// Build image
	version := 1
	err = client.BuildImage(context.Background(), remoteDir, version)
	require.NoError(s.T(), err)

	// Verify image exists
	imageName := fmt.Sprintf("%s:%d", client.cfg.ImageName(), version)
	output, err := client.SSH(context.Background(), fmt.Sprintf("docker images %s --format '{{.Repository}}:{{.Tag}}'", imageName))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, imageName)

	// Verify image runs correctly
	output, err = client.SSH(context.Background(), fmt.Sprintf("docker run --rm %s", imageName))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "Hello from simple build")
}

func (s *DockerIntegrationSuite) TestDocker_CustomDockerfilePath() {
	client := s.newClient("custom-dockerfile")
	client.cfg.Dockerfile = "./docker/custom.Dockerfile"

	// Create directory structure
	buildDir := filepath.Join(s.tempDir, "custom-dockerfile")
	dockerDir := filepath.Join(buildDir, "docker")
	err := os.MkdirAll(dockerDir, 0755)
	require.NoError(s.T(), err)

	dockerfile := `FROM alpine:latest
RUN echo "Custom dockerfile path" > /custom.txt
CMD ["cat", "/custom.txt"]
`
	err = os.WriteFile(filepath.Join(dockerDir, "custom.Dockerfile"), []byte(dockerfile), 0644)
	require.NoError(s.T(), err)

	// Rsync to remote
	remoteDir, err := client.MakeTempDir(context.Background())
	require.NoError(s.T(), err)
	defer client.Cleanup(context.Background(), remoteDir)

	err = client.Rsync(context.Background(), buildDir, remoteDir)
	require.NoError(s.T(), err)

	// Build image with custom Dockerfile path
	version := 1
	err = client.BuildImage(context.Background(), remoteDir, version)
	require.NoError(s.T(), err)

	// Verify image exists
	imageName := fmt.Sprintf("%s:%d", client.cfg.ImageName(), version)
	output, err := client.SSH(context.Background(), fmt.Sprintf("docker images %s --format '{{.Repository}}:{{.Tag}}'", imageName))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, imageName)

	// Verify image runs correctly
	output, err = client.SSH(context.Background(), fmt.Sprintf("docker run --rm %s", imageName))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "Custom dockerfile path")
}

func (s *DockerIntegrationSuite) TestDocker_BuildWithBuildArgs() {
	client := s.newClient("build-args")

	// Create Dockerfile with ARG instructions
	buildDir := filepath.Join(s.tempDir, "build-args")
	err := os.MkdirAll(buildDir, 0755)
	require.NoError(s.T(), err)

	dockerfile := `FROM alpine:latest
ARG BUILD_VERSION=unknown
ARG BUILD_ENV=development
RUN echo "Version: ${BUILD_VERSION}" > /version.txt
RUN echo "Environment: ${BUILD_ENV}" >> /version.txt
CMD ["cat", "/version.txt"]
`
	err = os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644)
	require.NoError(s.T(), err)

	// Rsync to remote
	remoteDir, err := client.MakeTempDir(context.Background())
	require.NoError(s.T(), err)
	defer client.Cleanup(context.Background(), remoteDir)

	err = client.Rsync(context.Background(), buildDir, remoteDir)
	require.NoError(s.T(), err)

	// Build image with build args
	version := 1
	imageName := fmt.Sprintf("%s:%d", client.cfg.ImageName(), version)

	// Build with custom build args
	buildCmd := fmt.Sprintf(
		"cd %s && docker build -t %s --build-arg BUILD_VERSION=1.2.3 --build-arg BUILD_ENV=production -f Dockerfile .",
		remoteDir, imageName,
	)
	err = client.SSHInteractive(context.Background(), buildCmd)
	require.NoError(s.T(), err)

	// Verify build args were applied
	output, err := client.SSH(context.Background(), fmt.Sprintf("docker run --rm %s", imageName))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "Version: 1.2.3")
	assert.Contains(s.T(), output, "Environment: production")
}

func (s *DockerIntegrationSuite) TestDocker_ImageTagging() {
	client := s.newClient("image-tagging")

	// Create Dockerfile
	buildDir := filepath.Join(s.tempDir, "image-tagging")
	err := os.MkdirAll(buildDir, 0755)
	require.NoError(s.T(), err)

	dockerfile := `FROM alpine:latest
CMD ["echo", "Image tagging test"]
`
	err = os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(dockerfile), 0644)
	require.NoError(s.T(), err)

	// Rsync to remote
	remoteDir, err := client.MakeTempDir(context.Background())
	require.NoError(s.T(), err)
	defer client.Cleanup(context.Background(), remoteDir)

	err = client.Rsync(context.Background(), buildDir, remoteDir)
	require.NoError(s.T(), err)

	// Build multiple versions
	for version := 1; version <= 3; version++ {
		err = client.BuildImage(context.Background(), remoteDir, version)
		require.NoError(s.T(), err, "Failed to build version %d", version)
	}

	// Verify all three versions exist
	output, err := client.SSH(context.Background(), fmt.Sprintf("docker images %s --format '{{.Tag}}'", client.cfg.ImageName()))
	require.NoError(s.T(), err)

	// Parse tags from output
	tags := strings.Split(strings.TrimSpace(output), "\n")
	require.Len(s.T(), tags, 3, "Expected 3 image tags")

	// Verify each version exists
	for version := 1; version <= 3; version++ {
		assert.Contains(s.T(), tags, fmt.Sprintf("%d", version), "Version %d not found", version)
	}

	// Verify image naming convention
	expectedPrefix := "ssd-image-tagging"
	for _, version := range []int{1, 2, 3} {
		imageName := fmt.Sprintf("%s:%d", client.cfg.ImageName(), version)
		assert.Contains(s.T(), imageName, expectedPrefix, "Image name doesn't follow naming convention")
	}
}

func TestDockerIntegrationSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	suite.Run(t, new(DockerIntegrationSuite))
}
