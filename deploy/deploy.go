package deploy

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"

	"github.com/byteink/ssd/compose"
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
	StackExists(ctx context.Context) (bool, error)
	CreateStack(ctx context.Context, composeContent string) error
	EnsureNetwork(ctx context.Context, name string) error
	CreateEnvFile(ctx context.Context, serviceName string) error
	IsServiceRunning(ctx context.Context, serviceName string) (bool, error)
	PullImage(ctx context.Context, image string) error
	StartService(ctx context.Context, serviceName string) error
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

	// Check if stack exists, create if needed
	stackExists, err := client.StackExists(ctx)
	if err != nil {
		return fmt.Errorf("failed to check stack existence: %w", err)
	}

	if !stackExists {
		logln(output, "Stack does not exist, creating...")

		// Generate compose file
		services := map[string]*config.Config{
			cfg.Name: cfg,
		}
		composeContent, err := compose.GenerateCompose(services, cfg.StackPath(), 0)
		if err != nil {
			return fmt.Errorf("failed to generate compose file: %w", err)
		}

		// Create stack directory and compose.yaml
		if err := client.CreateStack(ctx, composeContent); err != nil {
			return fmt.Errorf("failed to create stack: %w", err)
		}

		// Ensure traefik_web network exists
		if err := client.EnsureNetwork(ctx, "traefik_web"); err != nil {
			return fmt.Errorf("failed to ensure network traefik_web: %w", err)
		}

		// Ensure project internal network exists
		project := filepath.Base(cfg.StackPath())
		internalNetwork := project + "_internal"
		if err := client.EnsureNetwork(ctx, internalNetwork); err != nil {
			return fmt.Errorf("failed to ensure network %s: %w", internalNetwork, err)
		}

		// Create env file for the service
		if err := client.CreateEnvFile(ctx, cfg.Name); err != nil {
			return fmt.Errorf("failed to create env file for %s: %w", cfg.Name, err)
		}

		logln(output, "Stack created successfully")
	}

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

// Restart restarts the stack without building a new image
func Restart(cfg *config.Config) error {
	client := remote.NewClient(cfg)
	return RestartWithClient(cfg, client, nil)
}

// RestartWithClient restarts with a custom client (for testing)
func RestartWithClient(cfg *config.Config, client Deployer, opts *Options) error {
	ctx := context.Background()

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

	// Restart stack
	logln(output, "Restarting stack...")
	if err := client.RestartStack(ctx); err != nil {
		return fmt.Errorf("failed to restart stack: %w", err)
	}

	logf(output, "\nRestarted %s successfully!\n", cfg.Name)
	return nil
}

// Rollback rolls back to the previous version
func Rollback(cfg *config.Config) error {
	client := remote.NewClient(cfg)
	return RollbackWithClient(cfg, client, nil)
}

// RollbackWithClient rolls back with a custom client (for testing)
func RollbackWithClient(cfg *config.Config, client Deployer, opts *Options) error {
	ctx := context.Background()

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

	// Check if this is a pre-built service
	if cfg.IsPrebuilt() {
		logf(output, "Skipping %s: pre-built images don't have versions to rollback\n", cfg.Name)
		return nil
	}

	// Get current version
	logf(output, "Checking current version on %s...\n", cfg.Server)
	currentVersion, err := client.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	if currentVersion <= 1 {
		return fmt.Errorf("cannot rollback: no previous version (current: %d)", currentVersion)
	}

	previousVersion := currentVersion - 1
	logf(output, "Current version: %d, rolling back to: %d\n", currentVersion, previousVersion)

	// Update compose.yaml to previous version
	logln(output, "Updating compose.yaml...")
	if err := client.UpdateCompose(ctx, previousVersion); err != nil {
		return fmt.Errorf("failed to update compose.yaml: %w", err)
	}

	// Restart stack
	logln(output, "Restarting stack...")
	if err := client.RestartStack(ctx); err != nil {
		return fmt.Errorf("failed to restart stack: %w", err)
	}

	logf(output, "\nRolled back %s to version %d successfully!\n", cfg.Name, previousVersion)
	return nil
}
