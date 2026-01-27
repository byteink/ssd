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
	// Dependencies maps dependency service names to their configs
	Dependencies map[string]*config.Config
	// AllServices maps all service names to their configs (used for initial stack creation)
	AllServices map[string]*config.Config
	// BuildOnly builds/pulls the image and updates compose.yaml but does not start the service.
	// Used by deploy-all: build everything first, then docker compose up -d once.
	BuildOnly bool
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
		logln(output, "==> Creating stack (first deploy)...")

		// Use all services for compose generation if available,
		// so depends_on references are valid in the compose file
		services := map[string]*config.Config{
			cfg.Name: cfg,
		}
		if opts != nil && len(opts.AllServices) > 0 {
			services = opts.AllServices
		}

		logln(output, "    Generating compose.yaml...")
		composeContent, err := compose.GenerateCompose(services, cfg.StackPath(), 0)
		if err != nil {
			return fmt.Errorf("failed to generate compose file: %w", err)
		}

		// Create env files BEFORE CreateStack, because docker compose config
		// validates that referenced env_file paths exist on disk
		for name := range services {
			logf(output, "    Creating env file for %s...\n", name)
			if err := client.CreateEnvFile(ctx, name); err != nil {
				return fmt.Errorf("failed to create env file for %s: %w", name, err)
			}
		}

		logln(output, "    Validating compose.yaml...")
		if err := client.CreateStack(ctx, composeContent); err != nil {
			return fmt.Errorf("failed to create stack: %w", err)
		}

		logln(output, "    Creating networks...")
		if err := client.EnsureNetwork(ctx, "traefik_web"); err != nil {
			return fmt.Errorf("failed to ensure network traefik_web: %w", err)
		}

		project := filepath.Base(cfg.StackPath())
		internalNetwork := project + "_internal"
		if err := client.EnsureNetwork(ctx, internalNetwork); err != nil {
			return fmt.Errorf("failed to ensure network %s: %w", internalNetwork, err)
		}

		logln(output, "    Stack created successfully")
	}

	// Get current version
	currentVersion, err := client.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	newVersion := currentVersion + 1
	logf(output, "==> Version: %d -> %d\n", currentVersion, newVersion)

	// Check and start dependencies if needed (skip in BuildOnly mode)
	buildOnly := opts != nil && opts.BuildOnly
	if !buildOnly && len(cfg.DependsOn) > 0 {
		logln(output, "==> Checking dependencies...")
		for _, dep := range cfg.DependsOn {
			running, err := client.IsServiceRunning(ctx, dep)
			if err != nil {
				return fmt.Errorf("failed to check if dependency %s is running: %w", dep, err)
			}

			if !running {
				logf(output, "    Starting %s...\n", dep)

				// Check if dependency is pre-built and needs image pull
				if opts != nil && opts.Dependencies != nil {
					if depCfg, exists := opts.Dependencies[dep]; exists && depCfg.IsPrebuilt() {
						logf(output, "    Pulling image %s...\n", depCfg.Image)
						if err := client.PullImage(ctx, depCfg.Image); err != nil {
							return fmt.Errorf("failed to pull image for dependency %s: %w", dep, err)
						}
					}
				}

				if err := client.StartService(ctx, dep); err != nil {
					return fmt.Errorf("failed to start dependency %s: %w", dep, err)
				}
			} else {
				logf(output, "    %s: running\n", dep)
			}
		}
	}

	// Create temp directory on server
	tempDir, err := client.MakeTempDir(ctx)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		if cleanupErr := client.Cleanup(ctx, tempDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp directory: %v", cleanupErr)
		}
	}()

	// Check if this is a pre-built image
	if cfg.IsPrebuilt() {
		logf(output, "==> Pulling image %s...\n", cfg.Image)
		if err := client.PullImage(ctx, cfg.Image); err != nil {
			return fmt.Errorf("failed to pull image: %w", err)
		}
	} else {
		logf(output, "==> Syncing code to %s...\n", cfg.Server)
		localContext, err := filepath.Abs(cfg.Context)
		if err != nil {
			return fmt.Errorf("failed to resolve context path: %w", err)
		}
		if err := client.Rsync(ctx, localContext, tempDir); err != nil {
			return fmt.Errorf("failed to sync code: %w", err)
		}

		logf(output, "==> Building image %s:%d...\n", cfg.ImageName(), newVersion)
		if err := client.BuildImage(ctx, tempDir, newVersion); err != nil {
			return fmt.Errorf("failed to build image: %w", err)
		}

		logln(output, "==> Updating compose.yaml...")
		if err := client.UpdateCompose(ctx, newVersion); err != nil {
			return fmt.Errorf("failed to update compose.yaml: %w", err)
		}
	}

	// In BuildOnly mode, skip starting — caller will docker compose up -d once
	if buildOnly {
		logf(output, "    Built %s version %d\n", cfg.Name, newVersion)
		return nil
	}

	logf(output, "==> Starting service %s...\n", cfg.Name)
	if err := client.StartService(ctx, cfg.Name); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
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

	// Restart only this service, not all
	logf(output, "Restarting service %s...\n", cfg.Name)
	if err := client.StartService(ctx, cfg.Name); err != nil {
		return fmt.Errorf("failed to restart service: %w", err)
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

	// Rollback only this service, not all
	logf(output, "Starting service %s with version %d...\n", cfg.Name, previousVersion)
	if err := client.StartService(ctx, cfg.Name); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	logf(output, "\nRolled back %s to version %d successfully!\n", cfg.Name, previousVersion)
	return nil
}
