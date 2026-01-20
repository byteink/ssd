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
)

func TestRsync_BasicSync(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file1.txt"), []byte("content1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file2.txt"), []byte("content2"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(localDir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "subdir", "file3.txt"), []byte("content3"), 0644))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	output, err := client.SSH(ctx, "ls -1 "+remoteDir)
	require.NoError(t, err)
	assert.Contains(t, output, "file1.txt")
	assert.Contains(t, output, "file2.txt")
	assert.Contains(t, output, "subdir")

	content, err := client.SSH(ctx, "cat "+remoteDir+"/file1.txt")
	require.NoError(t, err)
	assert.Equal(t, "content1", strings.TrimSpace(content))

	subdirContent, err := client.SSH(ctx, "cat "+remoteDir+"/subdir/file3.txt")
	require.NoError(t, err)
	assert.Equal(t, "content3", strings.TrimSpace(subdirContent))
}

func TestRsync_ExcludesGit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file.txt"), []byte("content"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(localDir, ".git"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, ".git", "config"), []byte("git config"), 0644))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	output, err := client.SSH(ctx, "ls -1a "+remoteDir)
	require.NoError(t, err)
	assert.Contains(t, output, "file.txt")
	assert.NotContains(t, output, ".git")

	checkGit, err := client.SSH(ctx, "test -d "+remoteDir+"/.git && echo 'EXISTS' || echo 'MISSING'")
	require.NoError(t, err)
	assert.Contains(t, checkGit, "MISSING")
}

func TestRsync_ExcludesNodeModules(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	require.NoError(t, os.WriteFile(filepath.Join(localDir, "package.json"), []byte("{}"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(localDir, "node_modules"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(localDir, "node_modules", "lodash"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "node_modules", "lodash", "index.js"), []byte("module.exports = {}"), 0644))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	output, err := client.SSH(ctx, "ls -1 "+remoteDir)
	require.NoError(t, err)
	assert.Contains(t, output, "package.json")
	assert.NotContains(t, output, "node_modules")

	checkNodeModules, err := client.SSH(ctx, "test -d "+remoteDir+"/node_modules && echo 'EXISTS' || echo 'MISSING'")
	require.NoError(t, err)
	assert.Contains(t, checkNodeModules, "MISSING")
}

func TestRsync_PreservesPermissions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	executableFile := filepath.Join(localDir, "script.sh")
	require.NoError(t, os.WriteFile(executableFile, []byte("#!/bin/bash\necho hello"), 0755))

	readonlyFile := filepath.Join(localDir, "readonly.txt")
	require.NoError(t, os.WriteFile(readonlyFile, []byte("readonly"), 0444))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	execPerms, err := client.SSH(ctx, "stat -c '%a' "+remoteDir+"/script.sh")
	require.NoError(t, err)
	assert.Equal(t, "755", strings.TrimSpace(execPerms))

	readonlyPerms, err := client.SSH(ctx, "stat -c '%a' "+remoteDir+"/readonly.txt")
	require.NoError(t, err)
	assert.Equal(t, "444", strings.TrimSpace(readonlyPerms))
}

func TestRsync_DeletesRemoved(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file1.txt"), []byte("content1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file2.txt"), []byte("content2"), 0644))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	output, err := client.SSH(ctx, "ls -1 "+remoteDir)
	require.NoError(t, err)
	assert.Contains(t, output, "file1.txt")
	assert.Contains(t, output, "file2.txt")

	require.NoError(t, os.Remove(filepath.Join(localDir, "file2.txt")))

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	output, err = client.SSH(ctx, "ls -1 "+remoteDir)
	require.NoError(t, err)
	assert.Contains(t, output, "file1.txt")
	assert.NotContains(t, output, "file2.txt")

	checkDeleted, err := client.SSH(ctx, "test -f "+remoteDir+"/file2.txt && echo 'EXISTS' || echo 'DELETED'")
	require.NoError(t, err)
	assert.Contains(t, checkDeleted, "DELETED")
}

func TestRsync_FilenameWithSpaces(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	testContent := "content with spaces"
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "my file.txt"), []byte(testContent), 0644))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	output, err := client.SSH(ctx, "ls -1 "+remoteDir)
	require.NoError(t, err)
	assert.Contains(t, output, "my file.txt")

	content, err := client.SSH(ctx, "cat '"+remoteDir+"/my file.txt'")
	require.NoError(t, err)
	assert.Equal(t, testContent, strings.TrimSpace(content))
}

func TestRsync_FilenameWithUnicode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	testContent := "unicode content"
	unicodeFilename := "file-❤.txt"
	require.NoError(t, os.WriteFile(filepath.Join(localDir, unicodeFilename), []byte(testContent), 0644))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	checkExists, err := client.SSH(ctx, "test -f '"+remoteDir+"/"+unicodeFilename+"' && echo 'EXISTS' || echo 'MISSING'")
	require.NoError(t, err)
	assert.Contains(t, checkExists, "EXISTS")

	content, err := client.SSH(ctx, "cat '"+remoteDir+"/"+unicodeFilename+"'")
	require.NoError(t, err)
	assert.Equal(t, testContent, strings.TrimSpace(content))
}

func TestRsync_DeepNestedPath(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	deepPath := filepath.Join(localDir, "a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	require.NoError(t, os.MkdirAll(deepPath, 0755))

	testContent := "deep content"
	require.NoError(t, os.WriteFile(filepath.Join(deepPath, "deep.txt"), []byte(testContent), 0644))

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	remotePath := remoteDir + "/a/b/c/d/e/f/g/h/i/j/deep.txt"
	checkExists, err := client.SSH(ctx, "test -f "+remotePath+" && echo 'EXISTS' || echo 'MISSING'")
	require.NoError(t, err)
	assert.Contains(t, checkExists, "EXISTS")

	content, err := client.SSH(ctx, "cat "+remotePath)
	require.NoError(t, err)
	assert.Equal(t, testContent, strings.TrimSpace(content))
}

func TestRsync_LargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	largeFilePath := filepath.Join(localDir, "large.bin")
	largeFile, err := os.Create(largeFilePath)
	require.NoError(t, err)

	const fileSize = 100 * 1024 * 1024
	chunk := make([]byte, 1024*1024)
	for i := range chunk {
		chunk[i] = byte(i % 256)
	}

	written := 0
	for written < fileSize {
		n, err := largeFile.Write(chunk)
		require.NoError(t, err)
		written += n
	}
	require.NoError(t, largeFile.Close())

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	sizeOutput, err := client.SSH(ctx, "stat -c '%s' "+remoteDir+"/large.bin")
	require.NoError(t, err)
	assert.Equal(t, "104857600", strings.TrimSpace(sizeOutput))
}

func TestRsync_ManySmallFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sshContainer, err := testhelpers.StartSSHContainer(ctx, t)
	require.NoError(t, err)
	defer sshContainer.Cleanup(ctx)

	sshConfig, err := sshContainer.WriteSSHConfig("testserver")
	require.NoError(t, err)

	localDir, err := os.MkdirTemp("", "rsync-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(localDir)

	const numFiles = 1000
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(localDir, fmt.Sprintf("file%04d.txt", i))
		content := fmt.Sprintf("content%d", i)
		require.NoError(t, os.WriteFile(filename, []byte(content), 0644))
	}

	cfg := &config.Config{
		Name:    "testapp",
		Server:  "testserver",
		Stack:   "/stacks/testapp",
		Context: ".",
	}

	executor := &testhelpers.SSHConfigExecutor{ConfigPath: sshConfig}
	client := NewClientWithExecutor(cfg, executor)

	remoteDir, err := client.MakeTempDir(ctx)
	require.NoError(t, err)
	defer client.Cleanup(ctx, remoteDir)

	err = client.Rsync(ctx, localDir, remoteDir)
	require.NoError(t, err)

	countOutput, err := client.SSH(ctx, "find "+remoteDir+" -type f | wc -l")
	require.NoError(t, err)
	assert.Equal(t, "1000", strings.TrimSpace(countOutput))

	sampleContent, err := client.SSH(ctx, "cat "+remoteDir+"/file0000.txt")
	require.NoError(t, err)
	assert.Equal(t, "content0", strings.TrimSpace(sampleContent))
}
