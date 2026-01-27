package remote

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// CommandExecutor abstracts command execution for testing
type CommandExecutor interface {
	// Run executes a command with a 5 minute timeout and returns stdout
	Run(ctx context.Context, name string, args ...string) (string, error)
	// RunInteractive executes a command with a 30 minute timeout and stdout/stderr connected to terminal
	RunInteractive(ctx context.Context, name string, args ...string) error
}

// RealExecutor implements CommandExecutor using real exec.Command
type RealExecutor struct{}

// NewRealExecutor creates a new RealExecutor
func NewRealExecutor() *RealExecutor {
	return &RealExecutor{}
}

// Run executes a command with a 5 minute timeout and returns the output
func (e *RealExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command failed: %s\n%s", err, stderr.String())
	}
	return stdout.String(), nil
}

// RunInteractive executes a command with a 30 minute timeout and output streamed to terminal
func (e *RealExecutor) RunInteractive(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
