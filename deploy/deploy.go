package deploy

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"time"

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

// sortedKeys returns the keys of a config map in sorted order for deterministic behavior.
func sortedKeys(m map[string]*config.Config) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Deployer defines the interface for deployment operations
type Deployer interface {
	GetCurrentVersion(ctx context.Context) (int, error)
	ReadCompose(ctx context.Context) (string, error)
	MakeTempDir(ctx context.Context) (string, error)
	Rsync(ctx context.Context, localPath, remotePath string) error
	BuildImage(ctx context.Context, buildDir string, version int) error
	UpdateCompose(ctx context.Context, version int) error
	RestartStack(ctx context.Context) error
	Cleanup(ctx context.Context, path string) error
	StackExists(ctx context.Context) (bool, error)
	CreateStack(ctx context.Context, composeContent string) error
	EnsureNetwork(ctx context.Context, name string) error
	CreateEnvFiles(ctx context.Context, serviceNames []string) error
	IsServiceRunning(ctx context.Context, serviceName string) (bool, error)
	PullImage(ctx context.Context, image string) error
	StartService(ctx context.Context, serviceName string) error
	StopService(ctx context.Context, serviceName string) error
	WaitForHealthy(ctx context.Context, serviceName string, timeout time.Duration) error
}

// parseServiceVersions extracts current version numbers from compose.yaml content
func parseServiceVersions(content, stack string, services map[string]*config.Config) map[string]int {
	versions := make(map[string]int, len(services))
	project := filepath.Base(stack)
	for name, svc := range services {
		if svc.IsPrebuilt() {
			continue
		}
		imageName := fmt.Sprintf("ssd-%s-%s", project, name)
		v, _ := remote.ParseVersionFromContent(content, imageName)
		versions[name] = v
	}
	return versions
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

const (
	defaultHealthTimeout = 30 * time.Second
	maxHealthTimeout     = 5 * time.Minute
)

// healthTimeout computes how long to wait for a service health check.
func healthTimeout(cfg *config.Config) time.Duration {
	if cfg.HealthCheck == nil {
		return defaultHealthTimeout
	}
	interval, _ := time.ParseDuration(cfg.HealthCheck.Interval)
	if interval <= 0 {
		interval = 30 * time.Second
	}
	retries := cfg.HealthCheck.Retries
	if retries <= 0 {
		retries = 3
	}
	timeout := time.Duration(retries)*interval + 30*time.Second
	if timeout > maxHealthTimeout {
		return maxHealthTimeout
	}
	return timeout
}

// canaryDeploy performs a zero-downtime canary deployment for a single service.
// It starts a canary container with the new image alongside the old one,
// waits for it to become healthy, then recreates the main service.
func canaryDeploy(
	ctx context.Context,
	cfg *config.Config,
	client Deployer,
	opts *Options,
	newVersion int,
	output io.Writer,
) error {
	stack := cfg.StackPath()
	canaryName := cfg.Name + "-canary"

	// Read existing compose to get current versions for all services
	existingCompose, _ := client.ReadCompose(ctx)
	currentVersions := parseServiceVersions(existingCompose, stack, opts.AllServices)

	// Step 1: Generate compose with canary (main keeps old version, canary gets new)
	logln(output, "==> Starting canary deployment...")
	canaryCompose, err := compose.GenerateComposeWithCanary(
		opts.AllServices, stack, currentVersions, cfg.Name, newVersion)
	if err != nil {
		return fmt.Errorf("failed to generate canary compose: %w", err)
	}

	envNames := sortedKeys(opts.AllServices)
	if err := client.CreateEnvFiles(ctx, envNames); err != nil {
		return fmt.Errorf("failed to create env files: %w", err)
	}

	if err := client.CreateStack(ctx, canaryCompose); err != nil {
		return fmt.Errorf("failed to write canary compose: %w", err)
	}

	// Step 2: Start canary alongside old main
	logf(output, "    Starting canary %s...\n", canaryName)
	if err := client.StartService(ctx, canaryName); err != nil {
		canaryCleanup(ctx, cfg, client, opts, currentVersions)
		return fmt.Errorf("failed to start canary: %w", err)
	}

	// Step 3: Wait for canary to be healthy
	timeout := healthTimeout(cfg)
	logf(output, "    Waiting for canary health (%v timeout)...\n", timeout)
	if err := client.WaitForHealthy(ctx, canaryName, timeout); err != nil {
		logf(output, "    Canary failed health check, rolling back...\n")
		_ = client.StopService(ctx, canaryName)
		canaryCleanup(ctx, cfg, client, opts, currentVersions)
		return fmt.Errorf("canary health check failed: %w", err)
	}

	// Step 4: Canary healthy — regenerate compose with new version, no canary
	logln(output, "    Canary healthy, promoting...")
	currentVersions[cfg.Name] = newVersion
	finalCompose, err := compose.GenerateCompose(opts.AllServices, stack, currentVersions)
	if err != nil {
		return fmt.Errorf("failed to generate final compose: %w", err)
	}

	if err := client.CreateStack(ctx, finalCompose); err != nil {
		return fmt.Errorf("failed to write final compose: %w", err)
	}

	// Step 5: Recreate main service (canary covers traffic during the gap)
	logf(output, "==> Recreating service %s...\n", cfg.Name)
	if err := client.StartService(ctx, cfg.Name); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	// Step 6: Wait for new main to be healthy before removing canary
	logf(output, "    Waiting for %s to be ready...\n", cfg.Name)
	if err := client.WaitForHealthy(ctx, cfg.Name, timeout); err != nil {
		return fmt.Errorf("new service failed health check after promotion: %w", err)
	}

	// Step 7: Stop canary
	logf(output, "    Stopping canary...\n")
	_ = client.StopService(ctx, canaryName)

	logf(output, "\nDeployed %s version %d successfully! (zero-downtime)\n", cfg.Name, newVersion)
	return nil
}

// canaryCleanup restores compose.yaml to the pre-canary state.
func canaryCleanup(ctx context.Context, cfg *config.Config, client Deployer, opts *Options, versions map[string]int) {
	cleanCompose, err := compose.GenerateCompose(opts.AllServices, cfg.StackPath(), versions)
	if err != nil {
		log.Printf("canary cleanup: failed to generate compose: %v", err)
		return
	}
	if err := client.CreateStack(ctx, cleanCompose); err != nil {
		log.Printf("canary cleanup: failed to write compose: %v", err)
	}
}

// updateComposeVersion updates compose.yaml to reference the new image version.
// Uses full regeneration when AllServices is available, otherwise falls back to sed replacement.
func updateComposeVersion(ctx context.Context, cfg *config.Config, client Deployer, opts *Options, newVersion int) error {
	if opts != nil && len(opts.AllServices) > 0 {
		existingCompose, _ := client.ReadCompose(ctx)
		currentVersions := parseServiceVersions(existingCompose, cfg.StackPath(), opts.AllServices)
		currentVersions[cfg.Name] = newVersion

		newCompose, err := compose.GenerateCompose(opts.AllServices, cfg.StackPath(), currentVersions)
		if err != nil {
			return fmt.Errorf("failed to generate compose.yaml: %w", err)
		}

		envNames := sortedKeys(opts.AllServices)
		if err := client.CreateEnvFiles(ctx, envNames); err != nil {
			return fmt.Errorf("failed to create env files: %w", err)
		}

		if err := client.CreateStack(ctx, newCompose); err != nil {
			return fmt.Errorf("failed to update compose.yaml: %w", err)
		}
		return nil
	}

	if !cfg.IsPrebuilt() {
		if err := client.UpdateCompose(ctx, newVersion); err != nil {
			return fmt.Errorf("failed to update compose.yaml: %w", err)
		}
	}

	return nil
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
		versions := make(map[string]int, len(services))
		composeContent, err := compose.GenerateCompose(services, cfg.StackPath(), versions)
		if err != nil {
			return fmt.Errorf("failed to generate compose file: %w", err)
		}

		// Create env files BEFORE CreateStack, because docker compose config
		// validates that referenced env_file paths exist on disk
		envNames := sortedKeys(services)
		logln(output, "    Creating env files...")
		if err := client.CreateEnvFiles(ctx, envNames); err != nil {
			return fmt.Errorf("failed to create env files: %w", err)
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
	}

	// In BuildOnly mode, update compose and return — caller will docker compose up -d once
	if buildOnly {
		if err := updateComposeVersion(ctx, cfg, client, opts, newVersion); err != nil {
			return err
		}
		logf(output, "    Built %s version %d\n", cfg.Name, newVersion)
		return nil
	}

	// Canary deploy: zero-downtime when service is already running and AllServices available
	if opts != nil && len(opts.AllServices) > 0 {
		running, _ := client.IsServiceRunning(ctx, cfg.Name)
		if running {
			return canaryDeploy(ctx, cfg, client, opts, newVersion, output)
		}
	}

	// Non-canary path: first deploy or no AllServices available
	if err := updateComposeVersion(ctx, cfg, client, opts, newVersion); err != nil {
		return err
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
