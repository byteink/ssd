//go:build integration

package remote

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SSHIntegrationSuite struct {
	suite.Suite
	ctx       context.Context
	cancel    context.CancelFunc
	container *testhelpers.SSHContainer
	sshConfig string
}

func (s *SSHIntegrationSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)

	container, err := testhelpers.StartSSHContainer(s.ctx, s.T())
	require.NoError(s.T(), err, "Failed to start SSH container")
	s.container = container

	// Write SSH config file
	sshConfig, err := container.WriteSSHConfig("testserver")
	require.NoError(s.T(), err, "Failed to write SSH config")
	s.sshConfig = sshConfig
}

func (s *SSHIntegrationSuite) TearDownSuite() {
	if s.container != nil {
		s.container.Cleanup(s.ctx)
	}
	s.cancel()
}

func (s *SSHIntegrationSuite) newClient() *Client {
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

func (s *SSHIntegrationSuite) TestSSH_BasicCommand() {
	client := s.newClient()

	output, err := client.SSH(context.Background(), "echo hello")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "hello\n", output)
}

func (s *SSHIntegrationSuite) TestSSH_MultipleCommands() {
	client := s.newClient()

	output, err := client.SSH(context.Background(), "echo one && echo two")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "one")
	assert.Contains(s.T(), output, "two")
}

func (s *SSHIntegrationSuite) TestMakeTempDir() {
	client := s.newClient()

	dir, err := client.MakeTempDir(context.Background())
	require.NoError(s.T(), err)
	assert.True(s.T(), strings.HasPrefix(dir, "/tmp/"))

	// Verify directory exists
	output, err := client.SSH(context.Background(), "ls -d " + dir)
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, dir)

	// Cleanup
	err = client.Cleanup(context.Background(), dir)
	require.NoError(s.T(), err)
}

func (s *SSHIntegrationSuite) TestCleanup() {
	client := s.newClient()

	// Create a directory and file
	dir, err := client.MakeTempDir(context.Background())
	require.NoError(s.T(), err)

	_, err = client.SSH(context.Background(), "touch " + dir + "/testfile && mkdir " + dir + "/subdir")
	require.NoError(s.T(), err)

	// Cleanup
	err = client.Cleanup(context.Background(), dir)
	require.NoError(s.T(), err)

	// Verify it's gone
	output, _ := client.SSH(context.Background(), "ls " + dir + " 2>&1 || echo 'DELETED'")
	assert.Contains(s.T(), output, "DELETED")
}

func (s *SSHIntegrationSuite) TestGetCurrentVersion_NoComposeFile() {
	client := s.newClient()

	// Ensure no compose.yaml exists
	client.SSH(context.Background(), "rm -rf /home/testuser/stacks/testapp")

	version, err := client.GetCurrentVersion(context.Background())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 0, version)
}

func (s *SSHIntegrationSuite) TestGetCurrentVersion_WithComposeFile() {
	client := s.newClient()

	// Create stack directory and compose file
	client.SSH(context.Background(), "mkdir -p /home/testuser/stacks/testapp")
	client.SSH(context.Background(), `echo 'services:
  app:
    image: ssd-testapp:7
    ports:
      - "8080:8080"' > /home/testuser/stacks/testapp/compose.yaml`)

	version, err := client.GetCurrentVersion(context.Background())
	require.NoError(s.T(), err)
	assert.Equal(s.T(), 7, version)

	// Cleanup
	client.SSH(context.Background(), "rm -rf /home/testuser/stacks/testapp")
}

func (s *SSHIntegrationSuite) TestUpdateCompose() {
	client := s.newClient()

	// Create stack directory and compose file
	client.SSH(context.Background(), "mkdir -p /home/testuser/stacks/testapp")
	client.SSH(context.Background(), `echo 'services:
  app:
    image: ssd-testapp:1
    ports:
      - "8080:8080"' > /home/testuser/stacks/testapp/compose.yaml`)

	// Update to version 2
	err := client.UpdateCompose(context.Background(), 2)
	require.NoError(s.T(), err)

	// Verify
	output, err := client.SSH(context.Background(), "cat /home/testuser/stacks/testapp/compose.yaml")
	require.NoError(s.T(), err)
	assert.Contains(s.T(), output, "ssd-testapp:2")
	assert.NotContains(s.T(), output, "ssd-testapp:1")

	// Cleanup
	client.SSH(context.Background(), "rm -rf /home/testuser/stacks/testapp")
}

func TestSSHIntegrationSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}
	suite.Run(t, new(SSHIntegrationSuite))
}
