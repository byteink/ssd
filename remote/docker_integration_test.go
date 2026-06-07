//go:build integration

package remote

import (
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
)

// gitInitCommit initializes a git repo in dir and commits all files. Client.Rsync
// ships the build context via `git archive HEAD`, so the context must be a git
// repo with at least one commit.
func gitInitCommit(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "add", "-A"},
		{"git", "-C", dir, "commit", "-m", "test"},
	}
	for _, cmd := range cmds {
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", cmd, string(out))
	}
}

func TestDocker_SimpleBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHDockerContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "docker-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(localDir) }()

	dockerfileContent := `FROM alpine:latest
RUN echo "Hello from test container"
CMD ["echo", "test"]
`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "Dockerfile"), []byte(dockerfileContent), 0644))

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/stacks/testapp",
		Context:    ".",
		Dockerfile: "Dockerfile",
	}

	gitInitCommit(t, localDir)

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer func() { _ = client.Cleanup(ctx, remoteDir) }()

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	err = client.BuildImage(ctx, remoteDir, 1)
	require.NoError(t, err)

	imageTag := fmt.Sprintf("%s:1", cfg.ImageName())
	output, err := client.SSH(ctx, fmt.Sprintf("docker images %s --format '{{.Repository}}:{{.Tag}}'", imageTag))
	require.NoError(t, err)
	assert.Contains(t, output, imageTag)

	_, err = client.SSH(ctx, fmt.Sprintf("docker rmi %s", imageTag))
	require.NoError(t, err)
}

func TestDocker_CustomDockerfilePath(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHDockerContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "docker-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(localDir) }()

	require.NoError(t, os.Mkdir(filepath.Join(localDir, "docker"), 0755))

	dockerfileContent := `FROM alpine:latest
RUN echo "Custom Dockerfile location"
CMD ["echo", "custom"]
`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "docker", "Dockerfile.custom"), []byte(dockerfileContent), 0644))

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/stacks/testapp",
		Context:    ".",
		Dockerfile: "docker/Dockerfile.custom",
	}

	gitInitCommit(t, localDir)

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer func() { _ = client.Cleanup(ctx, remoteDir) }()

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	err = client.BuildImage(ctx, remoteDir, 1)
	require.NoError(t, err)

	imageTag := fmt.Sprintf("%s:1", cfg.ImageName())
	output, err := client.SSH(ctx, fmt.Sprintf("docker images %s --format '{{.Repository}}:{{.Tag}}'", imageTag))
	require.NoError(t, err)
	assert.Contains(t, output, imageTag)

	_, err = client.SSH(ctx, fmt.Sprintf("docker rmi %s", imageTag))
	require.NoError(t, err)
}

func TestDocker_BuildWithBuildArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHDockerContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "docker-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(localDir) }()

	dockerfileContent := `FROM alpine:latest
ARG TEST_ARG=default
RUN echo "Build arg value: $TEST_ARG" > /test.txt
CMD ["cat", "/test.txt"]
`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "Dockerfile"), []byte(dockerfileContent), 0644))

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/stacks/testapp",
		Context:    ".",
		Dockerfile: "Dockerfile",
	}

	gitInitCommit(t, localDir)

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer func() { _ = client.Cleanup(ctx, remoteDir) }()

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	err = client.BuildImage(ctx, remoteDir, 1)
	require.NoError(t, err)

	imageTag := fmt.Sprintf("%s:1", cfg.ImageName())
	output, err := client.SSH(ctx, fmt.Sprintf("docker run --rm %s", imageTag))
	require.NoError(t, err)
	assert.Contains(t, strings.TrimSpace(output), "Build arg value: default")

	_, err = client.SSH(ctx, fmt.Sprintf("docker rmi %s", imageTag))
	require.NoError(t, err)
}

func TestDocker_ImageTagging(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHDockerContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "docker-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(localDir) }()

	dockerfileContent := `FROM alpine:latest
RUN echo "Version tagging test"
CMD ["echo", "version"]
`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "Dockerfile"), []byte(dockerfileContent), 0644))

	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/stacks/testapp",
		Context:    ".",
		Dockerfile: "Dockerfile",
	}

	gitInitCommit(t, localDir)

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer func() { _ = client.Cleanup(ctx, remoteDir) }()

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	testVersions := []int{1, 2, 42, 100}
	for _, version := range testVersions {
		err = client.BuildImage(ctx, remoteDir, version)
		require.NoError(t, err)

		imageTag := fmt.Sprintf("%s:%d", cfg.ImageName(), version)
		output, err := client.SSH(ctx, fmt.Sprintf("docker images %s --format '{{.Repository}}:{{.Tag}}'", imageTag))
		require.NoError(t, err)
		assert.Contains(t, output, imageTag)

		_, err = client.SSH(ctx, fmt.Sprintf("docker rmi %s", imageTag))
		require.NoError(t, err)
	}
}
