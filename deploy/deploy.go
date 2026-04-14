package deploy

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"

	"github.com/byteink/ssd/compose"
	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/k8s"
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
	ReadManifest(ctx context.Context) (string, error)
	MakeTempDir(ctx context.Context) (string, error)
	Rsync(ctx context.Context, localPath, remotePath string) error
	BuildImage(ctx context.Context, buildDir string, version int) error
	UpdateManifest(ctx context.Context, version int) error
	RestartStack(ctx context.Context) error
	Cleanup(ctx context.Context, path string) error
	StackExists(ctx context.Context) (bool, error)
	CreateStack(ctx context.Context, content string) error
	EnsureNetwork(ctx context.Context, name string) error
	CreateEnvFiles(ctx context.Context, serviceNames []string) error
	UploadEnvFile(ctx context.Context, serviceName, localPath string) error
	IsServiceRunning(ctx context.Context, serviceName string) (bool, error)
	PullImage(ctx context.Context, image string) error
	StartService(ctx context.Context, serviceName string) error
	RolloutService(ctx context.Context, serviceName string) error
	CopyFiles(ctx context.Context, files map[string]string) error
}

// parseServiceVersions extracts current version numbers from manifest content
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
	// BuildOnly builds/pulls the image and updates the manifest but does not start the service.
	// Used by deploy-all: build everything first, then start all services at once.
	BuildOnly bool
	// Runtime is the deployment runtime ("compose" or "k3s")
	Runtime string
}

// generateManifest calls the appropriate manifest generator based on runtime.
func generateManifest(runtime string, services map[string]*config.Config, stack string, versions map[string]int) (string, error) {
	if runtime == "k3s" {
		return k8s.GenerateManifests(services, stack, versions)
	}
	return compose.GenerateCompose(services, stack, versions)
}

// manifestName returns the filename for the current runtime.
func manifestName(runtime string) string {
	if runtime == "k3s" {
		return "manifests.yaml"
	}
	return "compose.yaml"
}

// uploadEnvFiles pushes any service's env_file to {stack}/{service}.env on
// the server. Overwrites any values set via `ssd env set`. Called on every
// deploy before env files are consumed (by compose up or by the k3s
// ConfigMap sync in StartService/RolloutService).
func uploadEnvFiles(ctx context.Context, client Deployer, services map[string]*config.Config) error {
	for _, name := range sortedKeys(services) {
		svc := services[name]
		if svc == nil || svc.EnvFile == "" {
			continue
		}
		if err := client.UploadEnvFile(ctx, name, svc.EnvFile); err != nil {
			return fmt.Errorf("failed to upload env_file for %s: %w", name, err)
		}
	}
	return nil
}

// DeployWithClient performs a deployment with a custom client
func DeployWithClient(cfg *config.Config, client Deployer, opts *Options) error {
	ctx := context.Background()

	// Default output to discarding if nil (for cleaner test output)
	output := io.Discard
	if opts != nil && opts.Output != nil {
		output = opts.Output
	}

	rt := "compose"
	if opts != nil && opts.Runtime != "" {
		rt = opts.Runtime
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

		// Use all services for manifest generation if available,
		// so depends_on references are valid
		services := map[string]*config.Config{
			cfg.Name: cfg,
		}
		if opts != nil && len(opts.AllServices) > 0 {
			services = opts.AllServices
		}

		manifest := manifestName(rt)
		logf(output, "    Generating %s...\n", manifest)
		versions := make(map[string]int, len(services))
		manifestContent, err := generateManifest(rt, services, cfg.StackPath(), versions)
		if err != nil {
			return fmt.Errorf("failed to generate %s: %w", manifest, err)
		}

		// Create env files BEFORE CreateStack — compose validates env_file
		// paths exist; K3s needs them for ConfigMap population
		envNames := sortedKeys(services)
		logln(output, "    Creating env files...")
		if err := client.CreateEnvFiles(ctx, envNames); err != nil {
			return fmt.Errorf("failed to create env files: %w", err)
		}

		logf(output, "    Validating %s...\n", manifest)
		if err := client.CreateStack(ctx, manifestContent); err != nil {
			return fmt.Errorf("failed to create stack: %w", err)
		}

		// Networks are compose-only; K3s uses K8s Services for networking
		if rt != "k3s" {
			logln(output, "    Creating networks...")

			needsTraefik := false
			for _, svc := range services {
				if svc.PrimaryDomain() != "" {
					needsTraefik = true
					break
				}
			}
			if needsTraefik {
				if err := client.EnsureNetwork(ctx, "traefik_web"); err != nil {
					return fmt.Errorf("failed to ensure network traefik_web: %w", err)
				}
			}

			project := filepath.Base(cfg.StackPath())
			internalNetwork := project + "_internal"
			if err := client.EnsureNetwork(ctx, internalNetwork); err != nil {
				return fmt.Errorf("failed to ensure network %s: %w", internalNetwork, err)
			}
		}

		logln(output, "    Stack created successfully")
	}

	// Copy config files to the stack directory (every deploy, not just first)
	if len(cfg.Files) > 0 {
		logln(output, "==> Copying config files...")
		if err := client.CopyFiles(ctx, cfg.Files); err != nil {
			return fmt.Errorf("failed to copy config files: %w", err)
		}
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
	depNames := cfg.DependsOn.Names()
	if !buildOnly && len(depNames) > 0 {
		logln(output, "==> Checking dependencies...")
		for _, dep := range depNames {
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

	// Update manifest: regenerate from config when all services are known,
	// otherwise fall back to regex replacement for the deployed service only
	manifest := manifestName(rt)
	if opts != nil && len(opts.AllServices) > 0 {
		logf(output, "==> Updating %s...\n", manifest)
		existingManifest, _ := client.ReadManifest(ctx)
		currentVersions := parseServiceVersions(existingManifest, cfg.StackPath(), opts.AllServices)
		currentVersions[cfg.Name] = newVersion

		newManifest, err := generateManifest(rt, opts.AllServices, cfg.StackPath(), currentVersions)
		if err != nil {
			return fmt.Errorf("failed to generate %s: %w", manifest, err)
		}

		envNames := sortedKeys(opts.AllServices)
		if err := client.CreateEnvFiles(ctx, envNames); err != nil {
			return fmt.Errorf("failed to create env files: %w", err)
		}

		if err := client.CreateStack(ctx, newManifest); err != nil {
			return fmt.Errorf("failed to update %s: %w", manifest, err)
		}
	} else if !cfg.IsPrebuilt() {
		logf(output, "==> Updating %s...\n", manifest)
		if err := client.UpdateManifest(ctx, newVersion); err != nil {
			return fmt.Errorf("failed to update %s: %w", manifest, err)
		}
	}

	// Upload env_file (overwrites {service}.env on server). Runs before the
	// service starts so compose/k3s read fresh values.
	services := map[string]*config.Config{cfg.Name: cfg}
	if opts != nil && len(opts.AllServices) > 0 {
		services = opts.AllServices
	}
	if err := uploadEnvFiles(ctx, client, services); err != nil {
		return err
	}

	// In BuildOnly mode, skip starting — caller will start all services at once
	if buildOnly {
		logf(output, "    Built %s version %d\n", cfg.Name, newVersion)
		return nil
	}

	logf(output, "==> Starting service %s (strategy: %s)...\n", cfg.Name, cfg.DeployStrategy())
	switch cfg.DeployStrategy() {
	case "rollout":
		if err := client.RolloutService(ctx, cfg.Name); err != nil {
			return fmt.Errorf("failed to rollout service: %w", err)
		}
	default:
		if err := client.StartService(ctx, cfg.Name); err != nil {
			return fmt.Errorf("failed to start service: %w", err)
		}
	}

	logf(output, "\nDeployed %s version %d successfully!\n", cfg.Name, newVersion)
	return nil
}

// RestartWithClient restarts a service without building a new image
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

// RollbackWithClient rolls back to the previous version
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

	rt := "compose"
	if opts != nil && opts.Runtime != "" {
		rt = opts.Runtime
	}

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

	manifest := manifestName(rt)
	logf(output, "Updating %s...\n", manifest)
	if err := client.UpdateManifest(ctx, previousVersion); err != nil {
		return fmt.Errorf("failed to update %s: %w", manifest, err)
	}

	// Rollback only this service, not all
	logf(output, "Starting service %s with version %d...\n", cfg.Name, previousVersion)
	if err := client.StartService(ctx, cfg.Name); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	logf(output, "\nRolled back %s to version %d successfully!\n", cfg.Name, previousVersion)
	return nil
}
