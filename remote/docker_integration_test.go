//go:build integration

package remote

import (
	"context"
	"crypto/sha256"
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

	err = client.BuildImage(ctx, remoteDir, 1, nil, nil)
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

	err = client.BuildImage(ctx, remoteDir, 1, nil, nil)
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

	err = client.BuildImage(ctx, remoteDir, 1, nil, nil)
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
		err = client.BuildImage(ctx, remoteDir, version, nil, nil)
		require.NoError(t, err)

		imageTag := fmt.Sprintf("%s:%d", cfg.ImageName(), version)
		output, err := client.SSH(ctx, fmt.Sprintf("docker images %s --format '{{.Repository}}:{{.Tag}}'", imageTag))
		require.NoError(t, err)
		assert.Contains(t, output, imageTag)

		_, err = client.SSH(ctx, fmt.Sprintf("docker rmi %s", imageTag))
		require.NoError(t, err)
	}
}

// Build args must reach docker intact — including a value with a space, which
// is only correct if the flag is shell-escaped on the remote side.
// (TestDocker_BuildWithBuildArgs above covers the no-flag ARG default.)
func TestDocker_BuildWithBuildArgFlags(t *testing.T) {
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

	localDir, err := os.MkdirTemp("", "docker-buildargs-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(localDir) }()

	dockerfileContent := `FROM alpine:latest
ARG LICENSE_KEY
ARG BUILD_CHANNEL
RUN echo "key=$LICENSE_KEY channel=$BUILD_CHANNEL" > /baked.txt
CMD ["cat", "/baked.txt"]
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

	require.NoError(t, client.Rsync(ctx, localDir, remoteDir))
	require.NoError(t, client.BuildImage(ctx, remoteDir, 1, map[string]string{
		"LICENSE_KEY":   "abc 123",
		"BUILD_CHANNEL": "stable",
	}, nil))

	imageTag := fmt.Sprintf("%s:1", cfg.ImageName())
	baked, err := client.SSH(ctx, fmt.Sprintf("docker run --rm %s", imageTag))
	require.NoError(t, err)
	assert.Contains(t, baked, "key=abc 123", "build arg value must arrive verbatim")
	assert.Contains(t, baked, "channel=stable")

	_, err = client.SSH(ctx, fmt.Sprintf("docker rmi %s", imageTag))
	require.NoError(t, err)
}

// The point of build_secrets: the value is usable during the build but never
// recorded in the image. Asserted against a real docker daemon, because that
// guarantee lives in BuildKit, not in ssd.
func TestDocker_BuildWithBuildSecrets(t *testing.T) {
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

	localDir, err := os.MkdirTemp("", "docker-buildsecrets-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(localDir) }()

	// The build proves it saw the value by hashing it — the Dockerfile itself
	// must never contain the value, or the history assertion below would be
	// matching the Dockerfile text instead of the secret.
	const licence = "s3cr3t-licence"
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(licence)))
	dockerfileContent := `FROM alpine:latest
RUN --mount=type=secret,id=LICENSE_KEY \
    sha256sum /run/secrets/LICENSE_KEY | cut -d" " -f1 > /marker.txt
CMD ["cat", "/marker.txt"]
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

	require.NoError(t, client.Rsync(ctx, localDir, remoteDir))
	require.NoError(t, client.BuildImage(ctx, remoteDir, 1, nil,
		map[string]string{"LICENSE_KEY": licence}))

	imageTag := fmt.Sprintf("%s:1", cfg.ImageName())
	marker, err := client.SSH(ctx, fmt.Sprintf("docker run --rm %s", imageTag))
	require.NoError(t, err)
	assert.Equal(t, want, strings.TrimSpace(marker), "the build must have seen the secret value")

	history, err := client.SSH(ctx, fmt.Sprintf("docker history --no-trunc %s", imageTag))
	require.NoError(t, err)
	assert.NotContains(t, history, licence, "the secret must not appear in image history")

	inspect, err := client.SSH(ctx, fmt.Sprintf("docker image inspect %s", imageTag))
	require.NoError(t, err)
	assert.NotContains(t, inspect, licence, "the secret must not appear in image metadata")

	// The file holding the secret is removed when the build command exits.
	leftovers, err := client.SSH(ctx, "find /tmp -name LICENSE_KEY 2>/dev/null | wc -l")
	require.NoError(t, err)
	assert.Equal(t, "0", strings.TrimSpace(leftovers), "the secret file must not survive the build")

	_, err = client.SSH(ctx, fmt.Sprintf("docker rmi %s", imageTag))
	require.NoError(t, err)
}
