//go:build integration

package remote

import (
	"context"
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
	tempDir   string
}

func (s *RsyncIntegrationSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)

	container, err := testhelpers.StartSSHContainer(s.ctx, s.T())
	require.NoError(s.T(), err, "Failed to start SSH container")
	s.container = container

	sshConfig, err := container.WriteSSHConfig("testserver")
	require.NoError(s.T(), err, "Failed to write SSH config")
	s.sshConfig = sshConfig

	tempDir, err := os.MkdirTemp("", "ssd-rsync-test-*")
	require.NoError(s.T(), err, "Failed to create temp dir")
	s.tempDir = tempDir
}

func (s *RsyncIntegrationSuite) TearDownSuite() {
	if s.tempDir != "" {
		os.RemoveAll(s.tempDir)
	}
	if s.container != nil {
		s.container.Cleanup(s.ctx)
	}
	s.cancel()
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

	localDir := filepath.Join(s.tempDir, "basic-sync")
	err := os.MkdirAll(localDir, 0755)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(localDir, "file1.txt"), []byte("content1"), 0644)
	require.NoError(s.T(), err)
	err = os.WriteFile(filepath.Join(localDir, "file2.txt"), []byte("content2"), 0644)
	require.NoError(s.T(), err)

	err = os.MkdirAll(filepath.Join(localDir, "subdir"), 0755)
	require.NoError(s.T(), err)
	err = os.WriteFile(filepath.Join(localDir, "subdir", "file3.txt"), []byte("content3"), 0644)
	require.NoError(s.T(), err)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(localDir, remoteDir)
	require.NoError(s.T(), err)

	output, err := client.SSH("ls -1 " + remoteDir)
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "file1.txt")
	assert.Contains(s.T(), output, "file2.txt")
	assert.Contains(s.T(), output, "subdir")

	output, err = client.SSH("cat " + remoteDir + "/file1.txt")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "content1", output)

	output, err = client.SSH("cat " + remoteDir + "/subdir/file3.txt")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "content3", output)
}

func (s *RsyncIntegrationSuite) TestRsync_ExcludesGit() {
	client := s.newClient()

	localDir := filepath.Join(s.tempDir, "git-exclude")
	err := os.MkdirAll(localDir, 0755)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(localDir, "app.txt"), []byte("app content"), 0644)
	require.NoError(s.T(), err)

	gitDir := filepath.Join(localDir, ".git")
	err = os.MkdirAll(gitDir, 0755)
	require.NoError(s.T(), err)
	err = os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0644)
	require.NoError(s.T(), err)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(localDir, remoteDir)
	require.NoError(s.T(), err)

	output, err := client.SSH("ls -1 " + remoteDir)
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "app.txt")
	assert.NotContains(s.T(), output, ".git")

	output, err = client.SSH("test -d " + remoteDir + "/.git && echo 'EXISTS' || echo 'MISSING'")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "MISSING")
}

func (s *RsyncIntegrationSuite) TestRsync_ExcludesNodeModules() {
	client := s.newClient()

	localDir := filepath.Join(s.tempDir, "node-exclude")
	err := os.MkdirAll(localDir, 0755)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(localDir, "package.json"), []byte("{}"), 0644)
	require.NoError(s.T(), err)

	nodeModulesDir := filepath.Join(localDir, "node_modules")
	err = os.MkdirAll(filepath.Join(nodeModulesDir, "some-package"), 0755)
	require.NoError(s.T(), err)
	err = os.WriteFile(filepath.Join(nodeModulesDir, "some-package", "index.js"), []byte("module.exports = {};"), 0644)
	require.NoError(s.T(), err)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(localDir, remoteDir)
	require.NoError(s.T(), err)

	output, err := client.SSH("ls -1 " + remoteDir)
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "package.json")
	assert.NotContains(s.T(), output, "node_modules")

	output, err = client.SSH("test -d " + remoteDir + "/node_modules && echo 'EXISTS' || echo 'MISSING'")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "MISSING")
}

func (s *RsyncIntegrationSuite) TestRsync_PreservesPermissions() {
	client := s.newClient()

	localDir := filepath.Join(s.tempDir, "permissions")
	err := os.MkdirAll(localDir, 0755)
	require.NoError(s.T(), err)

	executablePath := filepath.Join(localDir, "script.sh")
	err = os.WriteFile(executablePath, []byte("#!/bin/bash\necho hello"), 0755)
	require.NoError(s.T(), err)

	readOnlyPath := filepath.Join(localDir, "readonly.txt")
	err = os.WriteFile(readOnlyPath, []byte("readonly"), 0444)
	require.NoError(s.T(), err)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(localDir, remoteDir)
	require.NoError(s.T(), err)

	output, err := client.SSH("stat -c '%a' " + remoteDir + "/script.sh")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "755")

	output, err = client.SSH("stat -c '%a' " + remoteDir + "/readonly.txt")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "444")
}

func (s *RsyncIntegrationSuite) TestRsync_DeletesRemoved() {
	client := s.newClient()

	localDir := filepath.Join(s.tempDir, "delete-test")
	err := os.MkdirAll(localDir, 0755)
	require.NoError(s.T(), err)

	err = os.WriteFile(filepath.Join(localDir, "keep.txt"), []byte("keep"), 0644)
	require.NoError(s.T(), err)
	err = os.WriteFile(filepath.Join(localDir, "remove.txt"), []byte("remove"), 0644)
	require.NoError(s.T(), err)

	remoteDir, err := client.MakeTempDir()
	require.NoError(s.T(), err)
	defer client.Cleanup(remoteDir)

	err = client.Rsync(localDir, remoteDir)
	require.NoError(s.T(), err)

	output, err := client.SSH("ls -1 " + remoteDir)
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "keep.txt")
	assert.Contains(s.T(), output, "remove.txt")

	err = os.Remove(filepath.Join(localDir, "remove.txt"))
	require.NoError(s.T(), err)

	err = client.Rsync(localDir, remoteDir)
	require.NoError(s.T(), err)

	output, err = client.SSH("ls -1 " + remoteDir)
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "keep.txt")
	assert.NotContains(s.T(), output, "remove.txt")

	output, err = client.SSH("test -f " + remoteDir + "/remove.txt && echo 'EXISTS' || echo 'DELETED'")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "DELETED")
}

func TestRsyncIntegrationSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	suite.Run(t, new(RsyncIntegrationSuite))
}
