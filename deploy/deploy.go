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
	"github.com/byteink/ssd/ui"
)

// logf writes formatted output, logging errors to stderr if write fails
func logf(w io.Writer, format string, args ...interface{}) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
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

// OrderByDependsOn returns service names ordered so every service comes after
// the dependencies it declares via depends_on — a stable (alphabetical
// tie-break) topological sort (Kahn's algorithm). Deploy-all uses it so a
// dependency (e.g. a database) is started and Ready before a dependent whose
// readiness probe needs it; iterating alphabetically would start the dependent
// first and deadlock on its progress deadline.
//
// depends_on entries that name a non-service are ignored. Services caught in a
// cycle are appended in alphabetical order so a bad depends_on never drops a
// service from the deploy.
func OrderByDependsOn(services map[string]*config.Config) []string {
	names := sortedKeys(services)
	indeg, dependents := dependencyGraph(services, names)

	queue := make([]string, 0, len(names))
	for _, n := range names {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}

	ordered := make([]string, 0, len(names))
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		ordered = append(ordered, n)
		for _, m := range dependents[n] {
			indeg[m]--
			if indeg[m] == 0 {
				queue = insertSorted(queue, m)
			}
		}
	}

	return appendLeftover(ordered, names)
}

// dependencyGraph builds in-degree counts and a dep->dependents adjacency map
// over the depends_on edges. Only edges to real services in the map count;
// self-edges and dangling names are ignored. `names` must be sorted so the
// adjacency lists are deterministic.
func dependencyGraph(services map[string]*config.Config, names []string) (map[string]int, map[string][]string) {
	indeg := make(map[string]int, len(names))
	dependents := make(map[string][]string, len(names))
	for _, n := range names {
		for _, dep := range services[n].DependsOn.Names() {
			if _, ok := services[dep]; ok && dep != n {
				indeg[n]++
				dependents[dep] = append(dependents[dep], n)
			}
		}
	}
	return indeg, dependents
}

// appendLeftover appends any names not already in ordered (services caught in
// a cycle) in their given order, so a bad depends_on never drops a service.
func appendLeftover(ordered, names []string) []string {
	if len(ordered) == len(names) {
		return ordered
	}
	seen := make(map[string]bool, len(ordered))
	for _, n := range ordered {
		seen[n] = true
	}
	for _, n := range names {
		if !seen[n] {
			ordered = append(ordered, n)
		}
	}
	return ordered
}

// insertSorted inserts v into the already-sorted slice s, keeping it sorted.
func insertSorted(s []string, v string) []string {
	i := sort.SearchStrings(s, v)
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
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
	// SetOutput redirects subprocess stdout/stderr for the next interactive
	// call (build, pull, start, rollout). Passing nil restores os.Stdout /
	// os.Stderr. Used to feed output into ui.Reporter's tail window.
	SetOutput(stdout, stderr io.Writer)
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

// TagCleaner is the narrow surface DeployWithClient needs for post-deploy
// image tag cleanup. The full cleanup.ImageCleaner interface is wider; we
// only consume the orchestration entry point here to keep test seams small.
type TagCleaner interface {
	PruneOldTags(ctx context.Context, image string, retention, running int) error
}

// Options holds configuration for the deployment
type Options struct {
	// Output is where to write progress messages (used only when Reporter is nil).
	Output io.Writer
	// Reporter renders deploy progress. When nil, a plain reporter is built
	// from Output (or io.Discard if Output is also nil).
	Reporter ui.Reporter
	// Dependencies maps dependency service names to their configs
	Dependencies map[string]*config.Config
	// AllServices maps all service names to their configs (used for initial stack creation)
	AllServices map[string]*config.Config
	// BuildOnly builds/pulls the image and updates the manifest but does not start the service.
	// Used by deploy-all: build everything first, then start all services at once.
	BuildOnly bool
	// Runtime is the deployment runtime ("compose" or "k3s")
	Runtime string
	// TagCleaner, if set, is invoked after a successful rollout to prune
	// old image tags per cfg.RetainTags(). Failures are warn-only; a deploy
	// never fails because cleanup failed. Pre-built images and BuildOnly
	// mode skip the hook entirely.
	TagCleaner TagCleaner
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

// reporterFromOpts returns opts.Reporter when set, otherwise builds a
// plain reporter from opts.Output (io.Discard when both are nil). Keeps
// existing call sites that pass only Output working unchanged.
func reporterFromOpts(opts *Options) ui.Reporter {
	if opts != nil && opts.Reporter != nil {
		return opts.Reporter
	}
	w := io.Writer(io.Discard)
	if opts != nil && opts.Output != nil {
		w = opts.Output
	}
	return ui.NewPlain(w)
}

// DeployWithClient performs a deployment with a custom client.
//
//nolint:gocyclo // orchestration step list; splitting hides the deploy flow
func DeployWithClient(cfg *config.Config, client Deployer, opts *Options) error {
	ctx := context.Background()

	r := reporterFromOpts(opts)

	rt := "compose"
	if opts != nil && opts.Runtime != "" {
		rt = opts.Runtime
	}

	unlock, err := acquireLock(cfg.StackPath())
	if err != nil {
		return fmt.Errorf("failed to acquire deployment lock: %w", err)
	}
	defer unlock()

	r.Header("Deploying %s → %s", cfg.Name, cfg.Server)

	stackExists, err := client.StackExists(ctx)
	if err != nil {
		return fmt.Errorf("failed to check stack existence: %w", err)
	}

	if !stackExists {
		if err := createStackStep(ctx, r, client, cfg, opts, rt); err != nil {
			return err
		}
	}

	if len(cfg.Files) > 0 {
		s := r.Step("Copying config files")
		if err := client.CopyFiles(ctx, cfg.Files); err != nil {
			s.Fail(err)
			return fmt.Errorf("failed to copy config files: %w", err)
		}
		s.Done()
	}

	currentVersion, err := client.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}
	newVersion := currentVersion + 1
	r.Info("Version: %d → %d", currentVersion, newVersion)

	buildOnly := opts != nil && opts.BuildOnly
	if !buildOnly {
		if err := dependenciesStep(ctx, r, client, cfg, opts); err != nil {
			return err
		}
	}

	tempDir, err := client.MakeTempDir(ctx)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		if cleanupErr := client.Cleanup(ctx, tempDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp directory: %v", cleanupErr)
		}
	}()

	if err := imageStep(ctx, r, client, cfg, tempDir, newVersion); err != nil {
		return err
	}

	if err := updateManifestStep(ctx, r, client, cfg, opts, rt, newVersion); err != nil {
		return err
	}

	services := map[string]*config.Config{cfg.Name: cfg}
	if opts != nil && len(opts.AllServices) > 0 {
		services = opts.AllServices
	}
	if err := uploadEnvFiles(ctx, client, services); err != nil {
		return err
	}

	if buildOnly {
		r.Info("Built %s version %d", cfg.Name, newVersion)
		return nil
	}

	if err := startStep(ctx, r, client, cfg); err != nil {
		return err
	}

	if opts != nil && opts.TagCleaner != nil && !cfg.IsPrebuilt() && cfg.RetainTags() > 0 {
		if err := opts.TagCleaner.PruneOldTags(ctx, cfg.ImageName(), cfg.RetainTags(), newVersion); err != nil {
			r.Warn("image cleanup failed: %v", err)
		}
	}

	r.Info("Deployed %s version %d successfully", cfg.Name, newVersion)
	return nil
}

func createStackStep(ctx context.Context, r ui.Reporter, client Deployer, cfg *config.Config, opts *Options, rt string) error {
	s := r.Step("Creating stack (first deploy)")
	services := map[string]*config.Config{cfg.Name: cfg}
	if opts != nil && len(opts.AllServices) > 0 {
		services = opts.AllServices
	}

	manifest := manifestName(rt)
	s.Detail("Generating %s", manifest)
	versions := make(map[string]int, len(services))
	manifestContent, err := generateManifest(rt, services, cfg.StackPath(), versions)
	if err != nil {
		s.Fail(err)
		return fmt.Errorf("failed to generate %s: %w", manifest, err)
	}

	s.Detail("Creating env files")
	envNames := sortedKeys(services)
	if err := client.CreateEnvFiles(ctx, envNames); err != nil {
		s.Fail(err)
		return fmt.Errorf("failed to create env files: %w", err)
	}

	s.Detail("Validating %s", manifest)
	if err := client.CreateStack(ctx, manifestContent); err != nil {
		s.Fail(err)
		return fmt.Errorf("failed to create stack: %w", err)
	}

	if rt != "k3s" {
		if err := ensureNetworksLocked(ctx, client, services, cfg, s); err != nil {
			return err
		}
	}

	s.Done()
	return nil
}

func ensureNetworksLocked(ctx context.Context, client Deployer, services map[string]*config.Config, cfg *config.Config, s ui.Step) error {
	s.Detail("Creating networks")
	needsTraefik := false
	for _, svc := range services {
		if svc.PrimaryDomain() != "" {
			needsTraefik = true
			break
		}
	}
	if needsTraefik {
		if err := client.EnsureNetwork(ctx, "traefik_web"); err != nil {
			s.Fail(err)
			return fmt.Errorf("failed to ensure network traefik_web: %w", err)
		}
	}
	project := filepath.Base(cfg.StackPath())
	internalNetwork := project + "_internal"
	if err := client.EnsureNetwork(ctx, internalNetwork); err != nil {
		s.Fail(err)
		return fmt.Errorf("failed to ensure network %s: %w", internalNetwork, err)
	}
	return nil
}

func dependenciesStep(ctx context.Context, r ui.Reporter, client Deployer, cfg *config.Config, opts *Options) error {
	depNames := cfg.DependsOn.Names()
	if len(depNames) == 0 {
		return nil
	}
	check := r.Step("Checking dependencies")
	type pending struct {
		name string
		cfg  *config.Config
	}
	var toStart []pending
	for _, dep := range depNames {
		running, err := client.IsServiceRunning(ctx, dep)
		if err != nil {
			check.Fail(err)
			return fmt.Errorf("failed to check if dependency %s is running: %w", dep, err)
		}
		if running {
			check.Detail("%s: running", dep)
			continue
		}
		check.Detail("%s: needs start", dep)
		var depCfg *config.Config
		if opts != nil && opts.Dependencies != nil {
			depCfg = opts.Dependencies[dep]
		}
		toStart = append(toStart, pending{dep, depCfg})
	}
	check.Done()

	for _, p := range toStart {
		ds := r.Step("Starting " + p.name)
		var pullErr, startErr error
		err := withStreamedOutput(client, ds, func() error {
			if p.cfg != nil && p.cfg.IsPrebuilt() {
				if pullErr = client.PullImage(ctx, p.cfg.Image); pullErr != nil {
					return pullErr
				}
			}
			startErr = client.StartService(ctx, p.name)
			return startErr
		})
		if err != nil {
			ds.Fail(err)
			if pullErr != nil {
				return fmt.Errorf("failed to pull image for dependency %s: %w", p.name, pullErr)
			}
			return fmt.Errorf("failed to start dependency %s: %w", p.name, startErr)
		}
		ds.Done()
	}
	return nil
}

// streamTailLines is how many lines of subprocess output are kept in the
// live tail window beneath the spinner header. Mirrors docker buildkit's
// default tail height — small enough to stay glanceable, big enough to
// show the current build stage and a line or two of context.
const streamTailLines = 4

// withStreamedOutput runs fn with the client's stdout/stderr redirected
// into the step's tail-window writer. Always restores the default writers
// (and Done()'s the step) regardless of error.
func withStreamedOutput(client Deployer, s ui.Step, fn func() error) error {
	w := s.Stream(streamTailLines)
	client.SetOutput(w, w)
	defer client.SetOutput(nil, nil)
	return fn()
}

func imageStep(ctx context.Context, r ui.Reporter, client Deployer, cfg *config.Config, tempDir string, newVersion int) error {
	if cfg.IsPrebuilt() {
		s := r.Step(fmt.Sprintf("Pulling image %s", cfg.Image))
		err := withStreamedOutput(client, s, func() error {
			return client.PullImage(ctx, cfg.Image)
		})
		if err != nil {
			s.Fail(err)
			return fmt.Errorf("failed to pull image: %w", err)
		}
		s.Done()
		return nil
	}

	sync := r.Step(fmt.Sprintf("Syncing code to %s", cfg.Server))
	localContext, err := filepath.Abs(cfg.Context)
	if err != nil {
		sync.Fail(err)
		return fmt.Errorf("failed to resolve context path: %w", err)
	}
	if err := client.Rsync(ctx, localContext, tempDir); err != nil {
		sync.Fail(err)
		return fmt.Errorf("failed to sync code: %w", err)
	}
	sync.Done()

	build := r.Step(fmt.Sprintf("Building image %s:%d", cfg.ImageName(), newVersion))
	err = withStreamedOutput(client, build, func() error {
		return client.BuildImage(ctx, tempDir, newVersion)
	})
	if err != nil {
		build.Fail(err)
		return fmt.Errorf("failed to build image: %w", err)
	}
	build.Done()
	return nil
}

func updateManifestStep(ctx context.Context, r ui.Reporter, client Deployer, cfg *config.Config, opts *Options, rt string, newVersion int) error {
	manifest := manifestName(rt)
	if opts != nil && len(opts.AllServices) > 0 {
		s := r.Step(fmt.Sprintf("Updating %s", manifest))
		existingManifest, _ := client.ReadManifest(ctx)
		currentVersions := parseServiceVersions(existingManifest, cfg.StackPath(), opts.AllServices)
		currentVersions[cfg.Name] = newVersion

		newManifest, err := generateManifest(rt, opts.AllServices, cfg.StackPath(), currentVersions)
		if err != nil {
			s.Fail(err)
			return fmt.Errorf("failed to generate %s: %w", manifest, err)
		}
		envNames := sortedKeys(opts.AllServices)
		if err := client.CreateEnvFiles(ctx, envNames); err != nil {
			s.Fail(err)
			return fmt.Errorf("failed to create env files: %w", err)
		}
		if err := client.CreateStack(ctx, newManifest); err != nil {
			s.Fail(err)
			return fmt.Errorf("failed to update %s: %w", manifest, err)
		}
		s.Done()
		return nil
	}
	if cfg.IsPrebuilt() {
		return nil
	}
	s := r.Step(fmt.Sprintf("Updating %s", manifest))
	if err := client.UpdateManifest(ctx, newVersion); err != nil {
		s.Fail(err)
		return fmt.Errorf("failed to update %s: %w", manifest, err)
	}
	s.Done()
	return nil
}

func startStep(ctx context.Context, r ui.Reporter, client Deployer, cfg *config.Config) error {
	strategy := cfg.DeployStrategy()
	s := r.Step(fmt.Sprintf("Starting service %s (strategy: %s)", cfg.Name, strategy))
	err := withStreamedOutput(client, s, func() error {
		if strategy == "rollout" {
			return client.RolloutService(ctx, cfg.Name)
		}
		return client.StartService(ctx, cfg.Name)
	})
	if err != nil {
		s.Fail(err)
		if strategy == "rollout" {
			return fmt.Errorf("failed to rollout service: %w", err)
		}
		return fmt.Errorf("failed to start service: %w", err)
	}
	s.Done()
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
