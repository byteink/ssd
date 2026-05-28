package remote

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// CommandExecutor abstracts command execution for testing.
type CommandExecutor interface {
	// Run executes a command with a 5 minute timeout and returns stdout
	Run(ctx context.Context, name string, args ...string) (string, error)
	// RunInteractive executes a command with a 30 minute timeout. By
	// default stdout/stderr go to the user's terminal; SetOutput
	// redirects them (used by ui.Reporter's Stream tail window).
	RunInteractive(ctx context.Context, name string, args ...string) error
	// SetOutput overrides the writers used by RunInteractive. Passing nil
	// for a writer restores the default (os.Stdout / os.Stderr).
	SetOutput(stdout, stderr io.Writer)
}

// RealExecutor implements CommandExecutor using real exec.Command
type RealExecutor struct {
	stdout io.Writer // nil → os.Stdout
	stderr io.Writer // nil → os.Stderr
}

// NewRealExecutor creates a new RealExecutor
func NewRealExecutor() *RealExecutor {
	return &RealExecutor{}
}

// SetOutput overrides the interactive-command writers. nil restores
// the default (os.Stdout / os.Stderr). Not safe for concurrent use —
// callers are expected to set, run, and restore from a single goroutine.
func (e *RealExecutor) SetOutput(stdout, stderr io.Writer) {
	e.stdout = stdout
	e.stderr = stderr
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

// RunInteractive executes a command with a 30 minute timeout and output
// streamed to the configured writers (defaults: os.Stdout / os.Stderr).
func (e *RealExecutor) RunInteractive(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	out := e.stdout
	if out == nil {
		out = os.Stdout
	}
	errW := e.stderr
	if errW == nil {
		errW = os.Stderr
	}
	cmd.Stdout = out
	cmd.Stderr = errW
	return cmd.Run()
}
