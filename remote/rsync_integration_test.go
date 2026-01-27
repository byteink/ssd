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

// initGitRepo initializes a git repository in the given directory
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
	}
	for _, cmd := range cmds {
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", cmd, string(out))
	}
}

// gitAddCommit stages all files and commits in the given directory
func gitAddCommit(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "add", "-A"},
		{"git", "-C", dir, "commit", "-m", "test"},
	}
	for _, cmd := range cmds {
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", cmd, string(out))
	}
}

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

	initGitRepo(t, localDir)
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file1.txt"), []byte("content1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file2.txt"), []byte("content2"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(localDir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "subdir", "file3.txt"), []byte("content3"), 0644))
	gitAddCommit(t, localDir)

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

func TestRsync_ExcludesGitignored(t *testing.T) {
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

	initGitRepo(t, localDir)

	// Create .gitignore that excludes node_modules, .next, and *.log
	require.NoError(t, os.WriteFile(filepath.Join(localDir, ".gitignore"), []byte("node_modules/\n.next/\n*.log\n"), 0644))

	// Create tracked file
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "file.txt"), []byte("content"), 0644))

	// Create gitignored directories and files
	require.NoError(t, os.Mkdir(filepath.Join(localDir, "node_modules"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(localDir, "node_modules", "lodash"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "node_modules", "lodash", "index.js"), []byte("module.exports = {}"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(localDir, ".next"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, ".next", "cache.json"), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "debug.log"), []byte("log data"), 0644))

	gitAddCommit(t, localDir)

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
	assert.Contains(t, output, ".gitignore")
	assert.NotContains(t, output, "node_modules")
	assert.NotContains(t, output, ".next")
	assert.NotContains(t, output, "debug.log")

	// .git directory is never included in git archive
	checkGit, err := client.SSH(ctx, "test -d "+remoteDir+"/.git && echo 'EXISTS' || echo 'MISSING'")
	require.NoError(t, err)
	assert.Contains(t, checkGit, "MISSING")
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

	initGitRepo(t, localDir)

	deepPath := filepath.Join(localDir, "a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	require.NoError(t, os.MkdirAll(deepPath, 0755))

	testContent := "deep content"
	require.NoError(t, os.WriteFile(filepath.Join(deepPath, "deep.txt"), []byte(testContent), 0644))
	gitAddCommit(t, localDir)

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

	initGitRepo(t, localDir)

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
	gitAddCommit(t, localDir)

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

	initGitRepo(t, localDir)

	const numFiles = 1000
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(localDir, fmt.Sprintf("file%04d.txt", i))
		content := fmt.Sprintf("content%d", i)
		require.NoError(t, os.WriteFile(filename, []byte(content), 0644))
	}
	gitAddCommit(t, localDir)

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

	initGitRepo(t, localDir)
	testContent := "content with spaces"
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "my file.txt"), []byte(testContent), 0644))
	gitAddCommit(t, localDir)

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

	initGitRepo(t, localDir)
	testContent := "unicode content"
	unicodeFilename := "file-❤.txt"
	require.NoError(t, os.WriteFile(filepath.Join(localDir, unicodeFilename), []byte(testContent), 0644))
	gitAddCommit(t, localDir)

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
