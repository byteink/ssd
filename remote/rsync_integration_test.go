//go:build integration

package remote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RsyncIntegrationSuite struct {
	suite.Suite
	ctx       context.Context
	cancel    context.CancelFunc
	container *testhelpers.SSHContainer
	sshConfig string
	localDir  string
	remoteDir string
}

func (s *RsyncIntegrationSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)

	container, err := testhelpers.StartSSHContainer(s.ctx, s.T())
	require.NoError(s.T(), err, "Failed to start SSH container")
	s.container = container

	sshConfig, err := container.WriteSSHConfig("testserver")
	require.NoError(s.T(), err, "Failed to write SSH config")
	s.sshConfig = sshConfig
}

func (s *RsyncIntegrationSuite) TearDownSuite() {
	if s.container != nil {
		s.container.Cleanup(s.ctx)
	}
	s.cancel()
}

func (s *RsyncIntegrationSuite) SetupTest() {
	var err error
	s.localDir, err = os.MkdirTemp("", "ssd-rsync-local-*")
	require.NoError(s.T(), err, "Failed to create local temp dir")

	s.remoteDir = fmt.Sprintf("/tmp/ssd-rsync-remote-%d", time.Now().UnixNano())
	_, err = s.container.RunSSH(fmt.Sprintf("mkdir -p %s", s.remoteDir))
	require.NoError(s.T(), err, "Failed to create remote temp dir")
}

func (s *RsyncIntegrationSuite) TearDownTest() {
	if s.localDir != "" {
		os.RemoveAll(s.localDir)
	}
	if s.remoteDir != "" {
		s.container.RunSSH(fmt.Sprintf("rm -rf %s", s.remoteDir))
	}
}

func (s *RsyncIntegrationSuite) newClient() *Client {
	cfg := &config.Config{
		Name:       "testapp",
		Server:     "testserver",
		Stack:      "/home/testuser/stacks/testapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: s.sshConfig}
	return NewClientWithExecutor(cfg, executor)
}

func (s *RsyncIntegrationSuite) TestRsync_BasicSync() {
	client := s.newClient()

	err := os.WriteFile(filepath.Join(s.localDir, "test.txt"), []byte("hello world"), 0644)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(s.localDir, "test2.txt"), []byte("another file"), 0644)
	require.NoError(s.T(), err)

	err = client.Rsync(s.localDir, s.remoteDir)
	require.NoError(s.T(), err)

	output, err := s.container.RunSSH(fmt.Sprintf("cat %s/test.txt", s.remoteDir))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "hello world", output)

	output, err = s.container.RunSSH(fmt.Sprintf("cat %s/test2.txt", s.remoteDir))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "another file", output)
}

func (s *RsyncIntegrationSuite) TestRsync_ExcludesGit() {
	client := s.newClient()

	gitDir := filepath.Join(s.localDir, ".git")
	err := os.Mkdir(gitDir, 0755)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0644)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(s.localDir, "regular.txt"), []byte("regular file"), 0644)
	require.NoError(s.T(), err)

	err = client.Rsync(s.localDir, s.remoteDir)
	require.NoError(s.T(), err)

	output, err := s.container.RunSSH(fmt.Sprintf("ls -la %s", s.remoteDir))
	require.NoError(s.T(), err)
	assert.NotContains(s.T(), output, ".git")
	assert.Contains(s.T(), output, "regular.txt")

	_, err = s.container.RunSSH(fmt.Sprintf("test -d %s/.git", s.remoteDir))
	assert.Error(s.T(), err, ".git directory should not exist on remote")
}

func (s *RsyncIntegrationSuite) TestRsync_ExcludesNodeModules() {
	client := s.newClient()

	nodeModules := filepath.Join(s.localDir, "node_modules")
	err := os.MkdirAll(filepath.Join(nodeModules, "package"), 0755)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(nodeModules, "package", "index.js"), []byte("module.exports = {}"), 0644)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(s.localDir, "app.js"), []byte("console.log('app')"), 0644)
	require.NoError(s.T(), err)

	err = client.Rsync(s.localDir, s.remoteDir)
	require.NoError(s.T(), err)

	output, err := s.container.RunSSH(fmt.Sprintf("ls -la %s", s.remoteDir))
	require.NoError(s.T(), err)
	assert.NotContains(s.T(), output, "node_modules")
	assert.Contains(s.T(), output, "app.js")

	_, err = s.container.RunSSH(fmt.Sprintf("test -d %s/node_modules", s.remoteDir))
	assert.Error(s.T(), err, "node_modules directory should not exist on remote")
}

func (s *RsyncIntegrationSuite) TestRsync_PreservesPermissions() {
	client := s.newClient()

	scriptPath := filepath.Join(s.localDir, "script.sh")
	err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho test"), 0755)
	require.NoError(s.T(), err)

	regularPath := filepath.Join(s.localDir, "data.txt")
	err = os.WriteFile(regularPath, []byte("data"), 0644)
	require.NoError(s.T(), err)

	err = client.Rsync(s.localDir, s.remoteDir)
	require.NoError(s.T(), err)

	output, err := s.container.RunSSH(fmt.Sprintf("stat -c '%%a' %s/script.sh", s.remoteDir))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "755\n", output)

	output, err = s.container.RunSSH(fmt.Sprintf("stat -c '%%a' %s/data.txt", s.remoteDir))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "644\n", output)

	output, err = s.container.RunSSH(fmt.Sprintf("%s/script.sh", s.remoteDir))
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "test\n", output)
}

func (s *RsyncIntegrationSuite) TestRsync_DeletesRemoved() {
	client := s.newClient()

	err := os.WriteFile(filepath.Join(s.localDir, "keep.txt"), []byte("keep"), 0644)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(s.localDir, "remove.txt"), []byte("remove"), 0644)
	require.NoError(s.T(), err)

	err = client.Rsync(s.localDir, s.remoteDir)
	require.NoError(s.T(), err)

	output, err := s.container.RunSSH(fmt.Sprintf("ls %s", s.remoteDir))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "keep.txt")
	assert.Contains(s.T(), output, "remove.txt")

	err = os.Remove(filepath.Join(s.localDir, "remove.txt"))
	require.NoError(s.T(), err)

	err = client.Rsync(s.localDir, s.remoteDir)
	require.NoError(s.T(), err)

	output, err = s.container.RunSSH(fmt.Sprintf("ls %s", s.remoteDir))
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "keep.txt")
	assert.NotContains(s.T(), output, "remove.txt")

	_, err = s.container.RunSSH(fmt.Sprintf("test -f %s/remove.txt", s.remoteDir))
	assert.Error(s.T(), err, "remove.txt should not exist on remote")
}

func TestRsyncIntegrationSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	suite.Run(t, new(RsyncIntegrationSuite))
}
