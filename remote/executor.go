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
	// Run executes a command and returns stdout
	Run(name string, args ...string) (string, error)
	// RunInteractive executes a command with stdout/stderr connected to terminal
	RunInteractive(name string, args ...string) error
}

// RealExecutor implements CommandExecutor using real exec.Command
type RealExecutor struct{}

// NewRealExecutor creates a new RealExecutor
func NewRealExecutor() *RealExecutor {
	return &RealExecutor{}
}

// Run executes a command and returns the output
func (e *RealExecutor) Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command failed: %s\n%s", err, stderr.String())
	}
	return stdout.String(), nil
}

// RunInteractive executes a command with output streamed to terminal
func (e *RealExecutor) RunInteractive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
