package deploy

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/remote"
)

// logf writes formatted output, logging errors to stderr if write fails
func logf(w io.Writer, format string, args ...interface{}) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		log.Printf("failed to write output: %v", err)
	}
}

// logln writes a line to output, logging errors to stderr if write fails
func logln(w io.Writer, msg string) {
	if _, err := fmt.Fprintln(w, msg); err != nil {
		log.Printf("failed to write output: %v", err)
	}
}

// Deployer defines the interface for deployment operations
type Deployer interface {
	GetCurrentVersion(ctx context.Context) (int, error)
	MakeTempDir(ctx context.Context) (string, error)
	Rsync(ctx context.Context, localPath, remotePath string) error
	BuildImage(ctx context.Context, buildDir string, version int) error
	UpdateCompose(ctx context.Context, version int) error
	RestartStack(ctx context.Context) error
	Cleanup(ctx context.Context, path string) error
}

// Options holds configuration for the deployment
type Options struct {
	// Output is where to write progress messages (defaults to os.Stdout)
	Output io.Writer
}

// Deploy performs a full deployment using the default client
func Deploy(cfg *config.Config) error {
	client := remote.NewClient(cfg)
	return DeployWithClient(cfg, client, nil)
}

// DeployWithClient performs a deployment with a custom client (for testing)
func DeployWithClient(cfg *config.Config, client Deployer, opts *Options) error {
	ctx := context.Background()

	// Default output to discarding if nil (for cleaner test output)
	output := io.Discard
	if opts != nil && opts.Output != nil {
		output = opts.Output
	}

	// Acquire deployment lock
	unlock, err := acquireLock(cfg.StackPath())
	if err != nil {
		return fmt.Errorf("failed to acquire deployment lock: %w", err)
	}
	defer unlock()

	// Get current version
	logf(output, "Checking current version on %s...\n", cfg.Server)
	currentVersion, err := client.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	newVersion := currentVersion + 1
	logf(output, "Current version: %d, deploying version: %d\n", currentVersion, newVersion)

	// Create temp directory on server
	logln(output, "Creating temp build directory...")
	tempDir, err := client.MakeTempDir(ctx)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		logln(output, "Cleaning up temp directory...")
		if cleanupErr := client.Cleanup(ctx, tempDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp directory: %v", cleanupErr)
		}
	}()

	// Rsync code to server
	logf(output, "Syncing code to %s:%s...\n", cfg.Server, tempDir)
	localContext, err := filepath.Abs(cfg.Context)
	if err != nil {
		return fmt.Errorf("failed to resolve context path: %w", err)
	}
	if err := client.Rsync(ctx, localContext, tempDir); err != nil {
		return fmt.Errorf("failed to sync code: %w", err)
	}

	// Build image on server
	logf(output, "Building image %s:%d...\n", cfg.ImageName(), newVersion)
	if err := client.BuildImage(ctx, tempDir, newVersion); err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	// Update compose.yaml
	logln(output, "Updating compose.yaml...")
	if err := client.UpdateCompose(ctx, newVersion); err != nil {
		return fmt.Errorf("failed to update compose.yaml: %w", err)
	}

	// Restart stack
	logln(output, "Restarting stack...")
	if err := client.RestartStack(ctx); err != nil {
		return fmt.Errorf("failed to restart stack: %w", err)
	}

	logf(output, "\nDeployed %s version %d successfully!\n", cfg.Name, newVersion)
	return nil
}
