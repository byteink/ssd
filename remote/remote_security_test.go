package remote

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestShellInjection_StackPathWithSpaces verifies paths with spaces are properly escaped
func TestShellInjection_StackPathWithSpaces(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/my app",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Verify GetCurrentVersion properly escapes the path
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// Path should be quoted to handle spaces safely
		return strings.Contains(cmd, "cat '/stacks/my app/compose.yaml'") ||
			strings.Contains(cmd, `cat "/stacks/my app/compose.yaml"`)
	})).Return("image: ssd-myapp:1", nil)

	_, err := client.GetCurrentVersion(context.Background())
	require.NoError(t, err)

	// Verify RestartStack properly escapes the path
	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// Path should be quoted
		return strings.Contains(cmd, "cd '/stacks/my app'") ||
			strings.Contains(cmd, `cd "/stacks/my app"`)
	})).Return(nil)

	err = client.RestartStack(context.Background())
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

// TestShellInjection_StackPathWithSemicolon verifies semicolons in paths are escaped
func TestShellInjection_StackPathWithSemicolon(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/app;rm -rf /",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Verify the semicolon is escaped and doesn't cause command injection
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// The path should be quoted, preventing the semicolon from being interpreted
		// as a command separator
		hasQuotedPath := strings.Contains(cmd, "'/stacks/app;rm -rf /") ||
			strings.Contains(cmd, `"/stacks/app;rm -rf /`)
		// Ensure the malicious command is not executed separately
		isNotSeparateCommand := !strings.Contains(cmd, "cat '/stacks/app' ; rm -rf /")
		return hasQuotedPath && isNotSeparateCommand
	})).Return("", nil)

	_, err := client.GetCurrentVersion(context.Background())
	require.NoError(t, err)

	// Verify RestartStack also properly escapes
	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		hasQuotedPath := strings.Contains(cmd, "cd '/stacks/app;rm -rf /") ||
			strings.Contains(cmd, `cd "/stacks/app;rm -rf /`)
		isNotSeparateCommand := !strings.Contains(cmd, "cd '/stacks/app' ; rm -rf /")
		return hasQuotedPath && isNotSeparateCommand
	})).Return(nil)

	err = client.RestartStack(context.Background())
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

// TestShellInjection_StackPathWithBackticks verifies backticks don't execute commands
func TestShellInjection_StackPathWithBackticks(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/app`whoami`",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Verify backticks are escaped and don't cause command execution
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// The backticks should be inside quotes, preventing execution
		return (strings.Contains(cmd, "'/stacks/app`whoami`") ||
			strings.Contains(cmd, `"/stacks/app`+"`"+`whoami`+"`"+`"`)) &&
			// Ensure it's not executed as a subshell
			!strings.Contains(cmd, "cat /stacks/app$(whoami)")
	})).Return("", nil)

	_, err := client.GetCurrentVersion(context.Background())
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

// TestShellInjection_StackPathWithDollarSign verifies $VAR doesn't expand
func TestShellInjection_StackPathWithDollarSign(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/$HOME",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Verify $HOME is not expanded as a variable
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// The dollar sign should be escaped or inside single quotes
		// Single quotes prevent variable expansion
		return strings.Contains(cmd, "'/stacks/$HOME") ||
			strings.Contains(cmd, `"/stacks/\$HOME`) ||
			strings.Contains(cmd, `"/stacks/$HOME"`)
	})).Return("", nil)

	_, err := client.GetCurrentVersion(context.Background())
	require.NoError(t, err)

	// Verify UpdateCompose also properly escapes
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		// First call reads compose.yaml
		return strings.Contains(args[1], "cat")
	})).Return("image: ssd-myapp:1", nil)

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		// Second call writes compose.yaml
		cmd := args[1]
		return strings.Contains(cmd, "echo") &&
			(strings.Contains(cmd, "> '/stacks/$HOME/compose.yaml'") ||
				strings.Contains(cmd, `> "/stacks/\$HOME/compose.yaml"`))
	})).Return("", nil)

	err = client.UpdateCompose(context.Background(), 2)
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

// TestShellInjection_ImageNameWithSpecialChars verifies image names with special chars are rejected or escaped
func TestShellInjection_ImageNameWithSpecialChars(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		shouldError bool
	}{
		{
			name:        "semicolon in name",
			appName:     "myapp;rm -rf /",
			shouldError: false, // Escaped by shellescape.Quote
		},
		{
			name:        "backticks in name",
			appName:     "myapp`whoami`",
			shouldError: false, // Escaped by shellescape.Quote
		},
		{
			name:        "dollar sign in name",
			appName:     "myapp$HOME",
			shouldError: false, // Escaped by shellescape.Quote
		},
		{
			name:        "pipe in name",
			appName:     "myapp|cat /etc/passwd",
			shouldError: false, // Escaped by shellescape.Quote
		},
		{
			name:        "ampersand in name",
			appName:     "myapp&whoami",
			shouldError: false, // Escaped by shellescape.Quote
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Name:       tt.appName,
				Server:     "testserver",
				Stack:      "/stacks/myapp",
				Dockerfile: "./Dockerfile",
				Context:    ".",
			}
			mockExec := new(testhelpers.MockExecutor)
			client := NewClientWithExecutor(cfg, mockExec)

			// Verify BuildImage properly escapes the image name
			mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
				cmd := args[1]
				// The image tag should be quoted to prevent injection
				return strings.Contains(cmd, "docker build") &&
					(strings.Contains(cmd, fmt.Sprintf("'ssd-%s:1'", tt.appName)) ||
						strings.Contains(cmd, fmt.Sprintf(`"ssd-%s:1"`, tt.appName)))
			})).Return(nil)

			err := client.BuildImage(context.Background(), "/tmp/build", 1)

			if tt.shouldError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				mockExec.AssertExpectations(t)
			}
		})
	}
}

// TestShellInjection_BuildDirWithSpecialChars verifies build directory paths are escaped
func TestShellInjection_BuildDirWithSpecialChars(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Build directory with spaces and special chars
	buildDir := "/tmp/build dir`whoami`"

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// Build dir should be quoted
		return (strings.Contains(cmd, "cd '/tmp/build dir`whoami`'") ||
			strings.Contains(cmd, `cd "/tmp/build dir`+"`"+`whoami`+"`"+`"`)) &&
			strings.Contains(cmd, "docker build")
	})).Return(nil)

	err := client.BuildImage(context.Background(), buildDir, 1)
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

// TestShellInjection_DockerfilePathWithSpecialChars verifies dockerfile paths are escaped
func TestShellInjection_DockerfilePathWithSpecialChars(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./docker/Dockerfile`whoami`",
		Context:    ".",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	mockExec.On("RunInteractive", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// Dockerfile path should be quoted
		return strings.Contains(cmd, "docker build") &&
			(strings.Contains(cmd, "-f 'docker/Dockerfile`whoami`'") ||
				strings.Contains(cmd, `-f "docker/Dockerfile`+"`"+`whoami`+"`"+`"`))
	})).Return(nil)

	err := client.BuildImage(context.Background(), "/tmp/build", 1)
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

// TestShellInjection_ComposeContentWithSpecialChars verifies compose content is properly escaped
func TestShellInjection_ComposeContentWithSpecialChars(t *testing.T) {
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      "/stacks/myapp",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// Compose content with single quotes that could break escaping
	composeContent := "services:\n  app:\n    image: ssd-myapp:1\n    environment:\n      - KEY='value with 'quotes'"

	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		return strings.Contains(args[1], "cat")
	})).Return(composeContent, nil)

	// The echo command should properly escape single quotes in the content
	mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
		cmd := args[1]
		// Single quotes in content should be escaped as '\''
		return strings.Contains(cmd, "echo") &&
			strings.Contains(cmd, "ssd-myapp:2") &&
			!strings.Contains(cmd, "echo 'image: ssd-myapp:2' > /stacks/myapp/compose.yaml")
	})).Return("", nil)

	err := client.UpdateCompose(context.Background(), 2)
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}

// TestShellInjection_CleanupPathValidation verifies cleanup only accepts safe paths
func TestShellInjection_CleanupPathValidation(t *testing.T) {
	cfg := &config.Config{
		Name:   "myapp",
		Server: "testserver",
		Stack:  "/stacks/myapp",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	// These paths should be rejected by validation
	rejectedPaths := []string{
		"/tmp/../etc/passwd",
		"/var/lib/docker",
		"/home/user/.ssh",
		"../../etc/passwd",
	}

	for _, path := range rejectedPaths {
		err := client.Cleanup(context.Background(), path)
		assert.Error(t, err, "Path %s should be rejected", path)
	}

	// These paths pass validation but are safely escaped by shellescape.Quote
	escapedPaths := []string{
		"/tmp/;rm -rf /",
		"/tmp/`whoami`",
		"/tmp/$HOME",
	}

	for _, path := range escapedPaths {
		mockExec.On("Run", "ssh", mock.MatchedBy(func(args []string) bool {
			cmd := args[1]
			// Verify the path is quoted to prevent injection
			return strings.Contains(cmd, "rm -rf") &&
				(strings.Contains(cmd, fmt.Sprintf("'%s'", path)) ||
					strings.Contains(cmd, fmt.Sprintf(`"%s"`, path)))
		})).Return("", nil).Once()

		err := client.Cleanup(context.Background(), path)
		assert.NoError(t, err, "Path %s should be safely escaped", path)
	}

	mockExec.AssertExpectations(t)
}

// TestShellInjection_RsyncPathsWithSpecialChars verifies rsync handles special chars safely
func TestShellInjection_RsyncPathsWithSpecialChars(t *testing.T) {
	cfg := &config.Config{
		Name:   "myapp",
		Server: "testserver",
		Stack:  "/stacks/myapp",
	}
	mockExec := new(testhelpers.MockExecutor)
	client := NewClientWithExecutor(cfg, mockExec)

	localPath := "./my app`whoami`"
	remotePath := "/tmp/remote dir;rm -rf /"

	mockExec.On("RunInteractive", "rsync", mock.MatchedBy(func(args []string) bool {
		// Verify paths are passed as separate arguments (not concatenated in shell)
		// rsync handles its own path safety when args are properly separated
		source := args[len(args)-2]
		dest := args[len(args)-1]

		// Source should end with /
		hasSourceSlash := strings.HasSuffix(source, "/")

		// Destination should be in format server:path
		hasServerPrefix := strings.HasPrefix(dest, "testserver:")

		return hasSourceSlash && hasServerPrefix
	})).Return(nil)

	err := client.Rsync(context.Background(), localPath, remotePath)
	require.NoError(t, err)

	mockExec.AssertExpectations(t)
}
