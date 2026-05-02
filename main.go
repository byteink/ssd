package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"

	"github.com/byteink/ssd/cleanup"
	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/deploy"
	"github.com/byteink/ssd/provision"
	"github.com/byteink/ssd/remote"
	"github.com/byteink/ssd/runtime"
	"github.com/byteink/ssd/runtime/k3s"
	"github.com/byteink/ssd/scaffold"
)

// deployServiceBuildOnly builds/pulls the image for a service without starting it.
// Used by deploy-all: build everything first, then docker compose up -d once.
func deployServiceBuildOnly(rootCfg *config.RootConfig, serviceName string, allServices map[string]*config.Config) error {
	cfg, err := rootCfg.GetService(serviceName)
	if err != nil {
		return err
	}

	fmt.Printf("Building %s...\n", cfg.Name)

	client := runtime.New(rootCfg.Runtime, cfg)
	opts := &deploy.Options{
		Output:      os.Stdout,
		AllServices: allServices,
		BuildOnly:   true,
		Runtime:     rootCfg.Runtime,
	}
	// BuildOnly deploys don't start services, so no tag cleanup here —
	// the full-deploy pass that follows will handle cleanup per service.

	return deploy.DeployWithClient(cfg, client, opts)
}

// tagCleanerFor returns a deploy.TagCleaner backed by the real runtime
// cleanup implementation. Returns nil when the client doesn't expose SSH
// (shouldn't happen for compose/k3s clients, but keeps the contract safe).
func tagCleanerFor(rt string, client remote.RemoteClient) deploy.TagCleaner {
	return &deployTagCleaner{cleaner: cleanup.NewCleaner(rt, client)}
}

type deployTagCleaner struct {
	cleaner cleanup.ImageCleaner
}

func (d *deployTagCleaner) PruneOldTags(ctx context.Context, image string, retention, running int) error {
	_, err := cleanup.PruneOldTags(ctx, d.cleaner, image, retention, running)
	return err
}

var version = "dev"

// errorFmt is the standard fmt.Printf format for printing an error to
// stdout before os.Exit(1). Centralised so the wording is consistent.
const errorFmt = "Error: %v\n"

// Global flags: --config and --env/-e are accepted on every command and
// stripped from args before the command-specific parser sees them. They
// only apply to commands that load ssd.yaml; runtime-only commands (init,
// skill, version, help) ignore them.
var (
	globalConfigPath string
	globalEnvName    string
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	// Strip global flags from args so existing per-command parsers stay
	// untouched. Errors are reported to the user and abort the run.
	cleaned, err := extractGlobalFlags(args)
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}
	args = cleaned

	switch command {
	case "version", "-v", "--version":
		fmt.Printf("ssd version %s\n", version)
	case "deploy", "up":
		runDeploy(args)
	case "down":
		runDown(args)
	case "rm":
		runRm(args)
	case "restart":
		runRestart(args)
	case "rollback":
		runRollback(args)
	case "status":
		runStatus(args)
	case "logs":
		runLogs(args)
	case "config":
		runConfig(args)
	case "env":
		runEnv(args)
	case "secret":
		runSecret(args)
	case "prune":
		runPrune(args)
	case "scale":
		runScale(args)
	case "init":
		runInit(args)
	case "skill":
		runSkill(args)
	case "provision":
		runProvision(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

// extractGlobalFlags peels --config <path>, --config=<path>, --env <name>,
// --env=<name>, and -e <name> out of args. Recognised on every command;
// commands that don't load ssd.yaml simply ignore the resolved values.
// Stops at "--" to leave pass-through args alone (e.g. logs follow flags).
func extractGlobalFlags(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			return out, nil
		}
		switch {
		case a == "--config":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag --config requires a value")
			}
			globalConfigPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			globalConfigPath = strings.TrimPrefix(a, "--config=")
		case a == "--env" || a == "-e":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %s requires a value", a)
			}
			globalEnvName = args[i+1]
			i++
		case strings.HasPrefix(a, "--env="):
			globalEnvName = strings.TrimPrefix(a, "--env=")
		default:
			out = append(out, a)
		}
	}
	return out, nil
}

// loadRootConfig resolves and loads the ssd config using the global
// --config and --env flags. Exits on error.
func loadRootConfig() *config.RootConfig {
	rootCfg, _, err := config.Resolve(globalConfigPath, globalEnvName)
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}
	return rootCfg
}

func loadConfig(serviceName string) (*config.RootConfig, *config.Config) {
	rootCfg := loadRootConfig()

	cfg, err := rootCfg.GetService(serviceName)
	if err != nil {
		fmt.Printf(errorFmt, err)
		if !rootCfg.IsSingleService() {
			fmt.Printf("Available services: %s\n", strings.Join(rootCfg.ListServices(), ", "))
		}
		os.Exit(1)
	}

	return rootCfg, cfg
}

func runDeploy(args []string) {
	if wantsHelp(args) {
		printDeployHelp()
		return
	}

	rootCfg := loadRootConfig()

	// No args: deploy all services
	if len(args) == 0 {
		services := rootCfg.ListServices()
		if len(services) == 0 {
			fmt.Println("Error: no services defined in ssd.yaml")
			os.Exit(1)
		}
		sort.Strings(services)

		fmt.Printf("Deploying all services: %s\n\n", strings.Join(services, ", "))

		// Precompute all service configs once
		allServices := make(map[string]*config.Config, len(services))
		for _, name := range services {
			svcCfg, err := rootCfg.GetService(name)
			if err != nil {
				fmt.Printf("\nError loading service %s: %v\n", name, err)
				os.Exit(1)
			}
			allServices[name] = svcCfg
		}

		// Build/pull all images first (BuildOnly mode)
		for _, name := range services {
			if err := deployServiceBuildOnly(rootCfg, name, allServices); err != nil {
				fmt.Printf("\nError building %s: %v\n", name, err)
				os.Exit(1)
			}
		}

		// Deploy each service using its configured strategy
		fmt.Println("\n==> Starting all services...")
		client := runtime.New(rootCfg.Runtime, allServices[services[0]])
		tagCleaner := tagCleanerFor(rootCfg.Runtime, client)
		for _, name := range services {
			cfg := allServices[name]
			strategy := cfg.DeployStrategy()
			fmt.Printf("    %s (strategy: %s)...\n", name, strategy)
			switch strategy {
			case "rollout":
				if err := client.RolloutService(context.Background(), name); err != nil {
					fmt.Printf("\nError rolling out %s: %v\n", name, err)
					os.Exit(1)
				}
			default:
				if err := client.StartService(context.Background(), name); err != nil {
					fmt.Printf("\nError starting %s: %v\n", name, err)
					os.Exit(1)
				}
			}

			// Post-deploy image cleanup per service (warn-only).
			// Use a per-service client so GetCurrentVersion parses the
			// correct image tag from the manifest.
			if !cfg.IsPrebuilt() && cfg.RetainTags() > 0 {
				svcClient := runtime.New(rootCfg.Runtime, cfg)
				version, _ := svcClient.GetCurrentVersion(context.Background())
				if err := tagCleaner.PruneOldTags(context.Background(), cfg.ImageName(), cfg.RetainTags(), version); err != nil {
					fmt.Printf("    Warning: image cleanup failed for %s: %v\n", name, err)
				}
			}
		}

		fmt.Println("\nAll services deployed successfully!")

		// Detect orphaned services on the server
		detectOrphans(rootCfg, allServices, client)
		return
	}

	serviceName := args[0]
	if err := deployService(rootCfg, serviceName); err != nil {
		fmt.Printf("\nError: %v\n", err)
		os.Exit(1)
	}
}

func runDown(args []string) {
	if wantsHelp(args) {
		printDownHelp()
		return
	}

	rootCfg := loadRootConfig()

	var services []string
	if len(args) == 0 {
		services = rootCfg.ListServices()
		sort.Strings(services)
	} else {
		services = []string{args[0]}
	}

	// Use first service to create client
	cfg, err := rootCfg.GetService(services[0])
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	client := runtime.New(rootCfg.Runtime, cfg)
	ctx := context.Background()

	for _, name := range services {
		svcCfg, err := rootCfg.GetService(name)
		if err != nil {
			fmt.Printf(errorFmt, err)
			os.Exit(1)
		}

		fmt.Printf("Stopping %s...\n", svcCfg.Name)

		switch rootCfg.Runtime {
		case "k3s":
			namespace := filepath.Base(svcCfg.Stack)
			cmd := fmt.Sprintf("k3s kubectl scale deployment/%s -n %s --replicas=0",
				shellescape.Quote(svcCfg.Name),
				shellescape.Quote(namespace))
			if _, err := client.SSH(ctx, cmd); err != nil {
				fmt.Printf("Error stopping %s: %v\n", svcCfg.Name, err)
				os.Exit(1)
			}
		default: // compose
			cmd := fmt.Sprintf("cd %s && docker compose stop %s",
				shellescape.Quote(svcCfg.StackPath()),
				shellescape.Quote(svcCfg.Name))
			if _, err := client.SSH(ctx, cmd); err != nil {
				fmt.Printf("Error stopping %s: %v\n", svcCfg.Name, err)
				os.Exit(1)
			}
		}
	}

	if len(services) == 1 {
		fmt.Printf("%s stopped.\n", services[0])
	} else {
		fmt.Println("All services stopped.")
	}
}

func runRm(args []string) {
	if wantsHelp(args) {
		printRmHelp()
		return
	}

	rootCfg := loadRootConfig()

	var services []string
	if len(args) == 0 {
		services = rootCfg.ListServices()
		sort.Strings(services)
	} else {
		services = []string{args[0]}
	}

	// Use first service for server info and client
	cfg, err := rootCfg.GetService(services[0])
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	client := runtime.New(rootCfg.Runtime, cfg)
	ctx := context.Background()

	// Refuse to remove running services
	var running []string
	for _, name := range services {
		ok, err := client.IsServiceRunning(ctx, name)
		if err == nil && ok {
			running = append(running, name)
		}
	}
	if len(running) > 0 {
		stackName := filepath.Base(cfg.Stack)
		if len(running) == 1 {
			fmt.Printf("Error: service '%s' is still running.\n", running[0])
			fmt.Printf("Run 'ssd down %s' first.\n", running[0])
		} else {
			fmt.Printf("Error: %d services are still running in stack '%s':\n", len(running), stackName)
			for _, name := range running {
				fmt.Printf("  - %s\n", name)
			}
			fmt.Printf("Run 'ssd down' to stop all services first.\n")
		}
		os.Exit(1)
	}

	// Warning
	if len(services) == 1 {
		fmt.Printf("\nWARNING: This will permanently remove '%s' from %s.\n", services[0], cfg.Server)
	} else {
		fmt.Printf("\nWARNING: This will permanently remove the entire stack from %s:\n", cfg.Server)
		for _, name := range services {
			fmt.Printf("  - %s\n", name)
		}
	}
	fmt.Printf("\nAll containers, env files, images, and related resources will be deleted.\n")
	fmt.Printf("This action cannot be undone.\n")
	fmt.Print("Continue? [y/N] ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}
	if strings.ToLower(strings.TrimSpace(input)) != "y" {
		fmt.Println("Aborted.")
		return
	}

	for _, name := range services {
		svcCfg, err := rootCfg.GetService(name)
		if err != nil {
			fmt.Printf(errorFmt, err)
			os.Exit(1)
		}
		rmService(rootCfg, svcCfg, client, ctx)
	}

	// Remove stack directory if removing all services
	if len(args) == 0 {
		fmt.Printf("Removing stack directory %s...\n", cfg.StackPath())
		cmd := fmt.Sprintf("rm -rf %s", shellescape.Quote(cfg.StackPath()))
		_, _ = client.SSH(ctx, cmd)
	}

	if len(services) == 1 {
		fmt.Printf("\n%s removed.\n", services[0])
	} else {
		fmt.Println("\nAll services removed.")
	}
}

func rmService(rootCfg *config.RootConfig, cfg *config.Config, client remote.RemoteClient, ctx context.Context) {
	fmt.Printf("Removing %s...\n", cfg.Name)

	switch rootCfg.Runtime {
	case "k3s":
		namespace := filepath.Base(cfg.Stack)
		cmd := fmt.Sprintf("k3s kubectl delete deployment,service,ingress,configmap -n %s -l app=%s --ignore-not-found",
			shellescape.Quote(namespace),
			shellescape.Quote(cfg.Name))
		if _, err := client.SSH(ctx, cmd); err != nil {
			fmt.Printf("  Warning: failed to delete resources: %v\n", err)
		}
		cmd = fmt.Sprintf("nerdctl --namespace k8s.io rmi %s 2>/dev/null || true",
			shellescape.Quote(cfg.ImageName()))
		_, _ = client.SSH(ctx, cmd)

	default: // compose
		cmd := fmt.Sprintf("cd %s && docker compose rm -sf %s",
			shellescape.Quote(cfg.StackPath()),
			shellescape.Quote(cfg.Name))
		if _, err := client.SSH(ctx, cmd); err != nil {
			fmt.Printf("  Warning: failed to remove container: %v\n", err)
		}
		cmd = fmt.Sprintf("docker rmi %s 2>/dev/null || true",
			shellescape.Quote(cfg.ImageName()))
		_, _ = client.SSH(ctx, cmd)
	}

	// Remove env file
	envPath := filepath.Join(cfg.StackPath(), fmt.Sprintf("%s.env", cfg.Name))
	rmCmd := fmt.Sprintf("rm -f %s", shellescape.Quote(envPath))
	_, _ = client.SSH(ctx, rmCmd)
}

func deployService(rootCfg *config.RootConfig, serviceName string) error {
	cfg, err := rootCfg.GetService(serviceName)
	if err != nil {
		if !rootCfg.IsSingleService() {
			return fmt.Errorf("%w\nAvailable services: %s", err, strings.Join(rootCfg.ListServices(), ", "))
		}
		return err
	}

	// Load dependency configs if any
	var depConfigs map[string]*config.Config
	depNames := cfg.DependsOn.Names()
	if len(depNames) > 0 {
		depConfigs = make(map[string]*config.Config)
		for _, dep := range depNames {
			depCfg, err := rootCfg.GetService(dep)
			if err != nil {
				fmt.Printf("Warning: Could not load dependency %s config: %v\n", dep, err)
				continue
			}
			depConfigs[dep] = depCfg
		}
	}

	// Load all service configs for initial stack creation
	allServices := make(map[string]*config.Config)
	for _, name := range rootCfg.ListServices() {
		svcCfg, err := rootCfg.GetService(name)
		if err != nil {
			continue
		}
		allServices[name] = svcCfg
	}

	fmt.Printf("Deploying %s to %s...\n\n", cfg.Name, cfg.Server)

	client := runtime.New(rootCfg.Runtime, cfg)
	opts := &deploy.Options{
		Output:       os.Stdout,
		Dependencies: depConfigs,
		AllServices:  allServices,
		Runtime:      rootCfg.Runtime,
		TagCleaner:   tagCleanerFor(rootCfg.Runtime, client),
	}

	return deploy.DeployWithClient(cfg, client, opts)
}

func runRestart(args []string) {
	if wantsHelp(args) {
		printRestartHelp()
		return
	}

	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rootCfg, cfg := loadConfig(serviceName)

	fmt.Printf("Restarting %s on %s...\n\n", cfg.Name, cfg.Server)

	client := runtime.New(rootCfg.Runtime, cfg)
	if err := deploy.RestartWithClient(cfg, client, &deploy.Options{Output: os.Stdout, Runtime: rootCfg.Runtime}); err != nil {
		fmt.Printf("\nError: %v\n", err)
		os.Exit(1)
	}
}

func runRollback(args []string) {
	if wantsHelp(args) {
		printRollbackHelp()
		return
	}

	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rootCfg, cfg := loadConfig(serviceName)

	fmt.Printf("Rolling back %s on %s...\n\n", cfg.Name, cfg.Server)

	client := runtime.New(rootCfg.Runtime, cfg)
	if err := deploy.RollbackWithClient(cfg, client, &deploy.Options{Output: os.Stdout, Runtime: rootCfg.Runtime}); err != nil {
		fmt.Printf("\nError: %v\n", err)
		os.Exit(1)
	}
}

func runStatus(args []string) {
	if wantsHelp(args) {
		printStatusHelp()
		return
	}

	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rootCfg, cfg := loadConfig(serviceName)
	client := runtime.New(rootCfg.Runtime, cfg)

	fmt.Printf("Status for %s on %s:\n\n", cfg.Name, cfg.Server)

	status, err := client.GetContainerStatus(context.Background())
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	if status == "" {
		fmt.Println("No containers found")
	} else {
		fmt.Println(status)
	}
}

func runLogs(args []string) {
	if wantsHelp(args) {
		printLogsHelp()
		return
	}

	serviceName := ""
	follow := false
	tail := 100

	for _, arg := range args {
		if arg == "-f" || arg == "--follow" {
			follow = true
		} else if !strings.HasPrefix(arg, "-") {
			serviceName = arg
		}
	}

	rootCfg, cfg := loadConfig(serviceName)
	client := runtime.New(rootCfg.Runtime, cfg)

	if err := client.GetLogs(context.Background(), follow, tail); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}
}

func runConfig(args []string) {
	if wantsHelp(args) {
		printConfigHelp()
		return
	}

	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rootCfg := loadRootConfig()

	// If multi-service and no service specified, show all
	if !rootCfg.IsSingleService() && serviceName == "" {
		fmt.Println("Services:")
		for _, name := range rootCfg.ListServices() {
			cfg, _ := rootCfg.GetService(name)
			fmt.Printf("\n  %s:\n", name)
			printConfig(cfg, "    ")
		}
		return
	}

	cfg, err := rootCfg.GetService(serviceName)
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	fmt.Println("Configuration:")
	printConfig(cfg, "  ")
}

func runEnv(args []string) {
	if wantsHelp(args) || len(args) < 2 {
		printEnvHelp()
		if !wantsHelp(args) && len(args) < 2 {
			os.Exit(1)
		}
		return
	}
	service := args[0]
	action := args[1]

	switch action {
	case "set":
		runEnvSet(service, args[2:])
	case "list":
		runEnvList(service, args[2:])
	case "rm":
		runEnvRm(service, args[2:])
	default:
		fmt.Printf("Unknown action: %s\n", action)
		fmt.Println("Usage: ssd env <service> <set|list|rm> [...]")
		os.Exit(1)
	}
}

func runEnvSet(service string, args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: ssd env <service> set KEY=VALUE")
		os.Exit(1)
	}

	arg := args[0]
	parts := strings.SplitN(arg, "=", 2)
	if len(parts) != 2 {
		fmt.Printf("Error: Invalid format. Expected KEY=VALUE, got: %s\n", arg)
		os.Exit(1)
	}

	key := parts[0]
	value := parts[1]

	if key == "" {
		fmt.Println("Error: KEY cannot be empty")
		os.Exit(1)
	}

	rootCfg, cfg := loadConfig(service)
	client := runtime.New(rootCfg.Runtime, cfg)

	if err := client.SetEnvVar(context.Background(), service, key, value); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	fmt.Printf("Set %s=%s for service %s\n", key, value, service)
}

func runEnvList(service string, args []string) {
	rootCfg, cfg := loadConfig(service)
	client := runtime.New(rootCfg.Runtime, cfg)

	content, err := client.GetEnvFile(context.Background(), service)
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	if content == "" || strings.TrimSpace(content) == "" {
		fmt.Println("No environment variables set")
		return
	}

	fmt.Print(content)
}

func runEnvRm(service string, args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: ssd env <service> rm KEY")
		os.Exit(1)
	}

	key := args[0]

	rootCfg, cfg := loadConfig(service)
	client := runtime.New(rootCfg.Runtime, cfg)

	if err := client.RemoveEnvVar(context.Background(), service, key); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	fmt.Printf("Removed %s from service %s\n", key, service)
}

func detectOrphans(rootCfg *config.RootConfig, allServices map[string]*config.Config, client remote.RemoteClient) {
	configServices := make(map[string]bool, len(allServices))
	for name := range allServices {
		configServices[name] = true
	}

	// Pick any config to get stack path
	var cfg *config.Config
	for _, c := range allServices {
		cfg = c
		break
	}
	if cfg == nil {
		return
	}

	var orphans []string

	switch rootCfg.Runtime {
	case "k3s":
		namespace := filepath.Base(cfg.Stack)
		cmd := fmt.Sprintf("k3s kubectl get deployments -n %s -l managed-by=ssd -o jsonpath='{range .items[*]}{.metadata.name}{\"\\n\"}{end}' 2>/dev/null",
			shellescape.Quote(namespace))
		output, err := client.SSH(context.Background(), cmd)
		if err == nil {
			for _, name := range strings.Split(strings.TrimSpace(output), "\n") {
				name = strings.TrimSpace(name)
				if name != "" && !configServices[name] {
					orphans = append(orphans, name)
				}
			}
		}
	default: // compose
		cmd := fmt.Sprintf("cd %s && docker compose ps --format '{{.Service}}' 2>/dev/null",
			shellescape.Quote(cfg.StackPath()))
		output, err := client.SSH(context.Background(), cmd)
		if err == nil {
			for _, name := range strings.Split(strings.TrimSpace(output), "\n") {
				name = strings.TrimSpace(name)
				if name != "" && !configServices[name] {
					orphans = append(orphans, name)
				}
			}
		}
	}

	if len(orphans) > 0 {
		fmt.Printf("\nWarning: %d orphaned services detected on server (not in ssd.yaml):\n", len(orphans))
		for _, name := range orphans {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Println("\nRun \"ssd prune\" to remove them.")
	}
}

// scaleCommand builds the runtime-specific scale command for SSH execution.
// Pure function for testability.
func scaleCommand(rt string, cfg *config.Config, count int) string {
	if rt == "k3s" {
		namespace := filepath.Base(cfg.Stack)
		return fmt.Sprintf("k3s kubectl scale deployment/%s -n %s --replicas=%d",
			shellescape.Quote(cfg.Name),
			shellescape.Quote(namespace),
			count)
	}
	return fmt.Sprintf("cd %s && docker compose up -d --scale %s=%d --no-recreate",
		shellescape.Quote(cfg.StackPath()),
		shellescape.Quote(cfg.Name),
		count)
}

// runScale performs a live scale of a service. Does NOT modify ssd.yaml
// (matches the kubectl scale contract).
func runScale(args []string) {
	if wantsHelp(args) {
		printScaleHelp()
		return
	}
	if len(args) < 2 {
		fmt.Println("Usage: ssd scale <service> <count>")
		os.Exit(1)
	}
	serviceName := args[0]
	count, err := strconv.Atoi(args[1])
	if err != nil || count < 0 {
		fmt.Printf("Error: invalid replica count %q (must be a non-negative integer)\n", args[1])
		os.Exit(1)
	}

	rootCfg, cfg := loadConfig(serviceName)
	client := runtime.New(rootCfg.Runtime, cfg)
	ctx := context.Background()

	cmd := scaleCommand(rootCfg.Runtime, cfg, count)

	if _, err := client.SSH(ctx, cmd); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}
	fmt.Printf("Scaled %s to %d replica(s)\n", cfg.Name, count)
}

// pruneFlags captures the parsed state of `ssd prune` options.
// Zero value is invalid — use parsePruneFlags.
type pruneFlags struct {
	orphans    bool
	images     bool
	buildCache bool
	dangling   bool
	dryRun     bool
	keep       *int // override per-service retention when set
}

// parsePruneFlags parses the flag list for `ssd prune`.
// No args → orphan-only mode (preserves the historical behavior).
// --all expands to orphans + images + build-cache + dangling.
// --keep requires a non-negative integer.
func parsePruneFlags(args []string) (pruneFlags, error) {
	var f pruneFlags
	anySelector := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			f.dryRun = true
		case "--images":
			f.images = true
			anySelector = true
		case "--build-cache":
			f.buildCache = true
			anySelector = true
		case "--dangling":
			f.dangling = true
			anySelector = true
		case "--all":
			f.orphans = true
			f.images = true
			f.buildCache = true
			f.dangling = true
			anySelector = true
		case "--keep":
			if i+1 >= len(args) {
				return pruneFlags{}, fmt.Errorf("--keep requires a value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return pruneFlags{}, fmt.Errorf("--keep must be a non-negative integer, got %q", args[i+1])
			}
			f.keep = &n
			i++
		default:
			return pruneFlags{}, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	// No selector flags means "default": orphan services only.
	if !anySelector {
		f.orphans = true
	}
	return f, nil
}

func runPrune(args []string) {
	if wantsHelp(args) {
		printPruneHelp()
		return
	}

	flags, err := parsePruneFlags(args)
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	rootCfg := loadRootConfig()

	services := rootCfg.ListServices()
	if len(services) == 0 {
		fmt.Println("Error: no services defined in ssd.yaml")
		os.Exit(1)
	}

	// Get first service config for server connection
	cfg, err := rootCfg.GetService(services[0])
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	client := runtime.New(rootCfg.Runtime, cfg)
	ctx := context.Background()

	if flags.orphans {
		pruneOrphans(ctx, rootCfg, cfg, services, client, flags.dryRun)
	}
	if flags.images {
		pruneImages(ctx, rootCfg, services, flags.keep, flags.dryRun)
	}
	if flags.buildCache {
		pruneBuildCache(ctx, rootCfg.Runtime, client, flags.dryRun)
	}
	if flags.dangling {
		pruneDangling(ctx, rootCfg.Runtime, client, flags.dryRun)
	}
}

// pruneOrphans removes services running on the server that no longer
// exist in ssd.yaml. Extracted from runPrune unchanged to keep behavior
// identical for the no-flag case.
func pruneOrphans(ctx context.Context, rootCfg *config.RootConfig, cfg *config.Config, services []string, client remote.RemoteClient, dryRun bool) {
	configServices := make(map[string]bool, len(services))
	for _, s := range services {
		configServices[s] = true
	}

	var orphans []string
	switch rootCfg.Runtime {
	case "k3s":
		namespace := filepath.Base(cfg.Stack)
		cmd := fmt.Sprintf("k3s kubectl get deployments -n %s -l managed-by=ssd -o jsonpath='{range .items[*]}{.metadata.name}{\"\\n\"}{end}'",
			shellescape.Quote(namespace))
		output, err := client.SSH(ctx, cmd)
		if err == nil {
			for _, name := range strings.Split(strings.TrimSpace(output), "\n") {
				name = strings.TrimSpace(name)
				if name != "" && !configServices[name] {
					orphans = append(orphans, name)
				}
			}
		}
	default: // compose
		stackPath := cfg.StackPath()
		cmd := fmt.Sprintf("cd %s && docker compose ps --format '{{.Service}}' 2>/dev/null",
			shellescape.Quote(stackPath))
		output, err := client.SSH(ctx, cmd)
		if err == nil {
			for _, name := range strings.Split(strings.TrimSpace(output), "\n") {
				name = strings.TrimSpace(name)
				if name != "" && !configServices[name] {
					orphans = append(orphans, name)
				}
			}
		}
	}

	if len(orphans) == 0 {
		fmt.Println("Orphans: none.")
		return
	}

	fmt.Printf("Orphans (%d):\n", len(orphans))
	for _, name := range orphans {
		fmt.Printf("  - %s\n", name)
	}
	if dryRun {
		fmt.Println("(dry run — no changes made)")
		return
	}

	for _, name := range orphans {
		fmt.Printf("  Removing %s...\n", name)
		switch rootCfg.Runtime {
		case "k3s":
			namespace := filepath.Base(cfg.Stack)
			cmd := fmt.Sprintf("k3s kubectl delete deployment,service,ingress -n %s -l app=%s",
				shellescape.Quote(namespace),
				shellescape.Quote(name))
			if _, err := client.SSH(ctx, cmd); err != nil {
				fmt.Printf("    Warning: failed to remove %s: %v\n", name, err)
			}
		default: // compose
			stackPath := cfg.StackPath()
			cmd := fmt.Sprintf("cd %s && docker compose rm -sf %s",
				shellescape.Quote(stackPath),
				shellescape.Quote(name))
			if _, err := client.SSH(ctx, cmd); err != nil {
				fmt.Printf("    Warning: failed to remove %s: %v\n", name, err)
			}
		}

		// Remove env file
		envPath := filepath.Join(cfg.StackPath(), fmt.Sprintf("%s.env", name))
		rmCmd := fmt.Sprintf("rm -f %s", shellescape.Quote(envPath))
		_, _ = client.SSH(ctx, rmCmd)
	}
	fmt.Printf("Orphans: removed %d.\n", len(orphans))
}

// pruneImages removes old image tags per service, honoring retention
// config and the --keep override. Always iterates all services in
// ssd.yaml in sorted order for deterministic output.
func pruneImages(ctx context.Context, rootCfg *config.RootConfig, services []string, keepOverride *int, dryRun bool) {
	sort.Strings(services)
	total := 0
	for _, name := range services {
		cfg, err := rootCfg.GetService(name)
		if err != nil {
			fmt.Printf("  Warning: skip %s: %v\n", name, err)
			continue
		}
		if cfg.IsPrebuilt() {
			continue
		}

		keep := cfg.RetainTags()
		if keepOverride != nil {
			keep = *keepOverride
		}
		if keep <= 0 {
			continue
		}

		svcClient := runtime.New(rootCfg.Runtime, cfg)
		cleaner := cleanup.NewCleaner(rootCfg.Runtime, svcClient)
		tags, err := cleaner.ListTags(ctx, cfg.ImageName())
		if err != nil {
			fmt.Printf("  Warning: %s: list tags failed: %v\n", name, err)
			continue
		}
		running, _ := svcClient.GetCurrentVersion(ctx)
		old := cleanup.SelectOldTags(tags, keep, running)
		if len(old) == 0 {
			continue
		}

		fmt.Printf("Images %s (keep=%d, running=%d): %d old tag(s)\n", name, keep, running, len(old))
		for _, t := range old {
			ref := fmt.Sprintf("%s:%d", cfg.ImageName(), t.Numeric)
			fmt.Printf("  - %s\n", ref)
			if dryRun {
				continue
			}
			if err := cleaner.RemoveImage(ctx, ref); err != nil {
				fmt.Printf("    Warning: %v\n", err)
				continue
			}
			total++
		}
	}

	if dryRun {
		return
	}
	fmt.Printf("Images: removed %d tag(s).\n", total)
}

// pruneBuildCache invokes the runtime's build cache prune command.
func pruneBuildCache(ctx context.Context, rt string, client remote.RemoteClient, dryRun bool) {
	if dryRun {
		fmt.Println("Build cache: would prune entries older than 168h (dry run).")
		return
	}
	cleaner := cleanup.NewCleaner(rt, client)
	if err := cleaner.PruneBuildCache(ctx); err != nil {
		fmt.Printf("Build cache: warning: %v\n", err)
		return
	}
	fmt.Println("Build cache: pruned entries older than 168h.")
}

// pruneDangling removes unreferenced images from the runtime store.
func pruneDangling(ctx context.Context, rt string, client remote.RemoteClient, dryRun bool) {
	if dryRun {
		fmt.Println("Dangling: would remove unreferenced images (dry run).")
		return
	}
	cleaner := cleanup.NewCleaner(rt, client)
	if err := cleaner.PruneDangling(ctx); err != nil {
		fmt.Printf("Dangling: warning: %v\n", err)
		return
	}
	fmt.Println("Dangling: removed.")
}

func runSecret(args []string) {
	if wantsHelp(args) || len(args) < 2 {
		printSecretHelp()
		if !wantsHelp(args) && len(args) < 2 {
			os.Exit(1)
		}
		return
	}

	service := args[0]
	action := args[1]

	rootCfg := loadRootConfig()

	if rootCfg.Runtime != "k3s" {
		fmt.Println("Error: secrets require runtime: k3s. Use \"ssd env\" for compose runtime.")
		os.Exit(1)
	}

	cfg, err := rootCfg.GetService(service)
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	client := k3s.NewClient(cfg)

	switch action {
	case "set":
		if len(args) < 3 {
			fmt.Println("Usage: ssd secret <service> set KEY=VALUE")
			os.Exit(1)
		}
		parts := strings.SplitN(args[2], "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			fmt.Printf("Error: Invalid format. Expected KEY=VALUE, got: %s\n", args[2])
			os.Exit(1)
		}
		if err := client.SetSecret(context.Background(), service, parts[0], parts[1]); err != nil {
			fmt.Printf(errorFmt, err)
			os.Exit(1)
		}
		fmt.Printf("Set secret %s for service %s\n", parts[0], service)
	case "list":
		output, err := client.ListSecrets(context.Background(), service)
		if err != nil {
			fmt.Printf(errorFmt, err)
			os.Exit(1)
		}
		if output == "" || strings.TrimSpace(output) == "" {
			fmt.Println("No secrets set")
			return
		}
		fmt.Print(output)
	case "rm":
		if len(args) < 3 {
			fmt.Println("Usage: ssd secret <service> rm KEY")
			os.Exit(1)
		}
		if err := client.RemoveSecret(context.Background(), service, args[2]); err != nil {
			fmt.Printf(errorFmt, err)
			os.Exit(1)
		}
		fmt.Printf("Removed secret %s from service %s\n", args[2], service)
	default:
		fmt.Printf("Unknown action: %s\n", action)
		fmt.Println("Usage: ssd secret <service> <set|list|rm> [...]")
		os.Exit(1)
	}
}

func runProvision(args []string) {
	if wantsHelp(args) {
		printProvisionHelp()
		return
	}

	// Handle subcommands
	if len(args) > 0 && args[0] == "check" {
		runProvisionCheck(args[1:])
		return
	}

	var server, email, rt string

	// Parse flags
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				fmt.Println("Error: --server requires a value")
				os.Exit(1)
			}
			server = args[i+1]
			i += 2
		case "--email":
			if i+1 >= len(args) {
				fmt.Println("Error: --email requires a value")
				os.Exit(1)
			}
			email = args[i+1]
			i += 2
		case "--runtime":
			if i+1 >= len(args) {
				fmt.Println("Error: --runtime requires a value")
				os.Exit(1)
			}
			rt = args[i+1]
			i += 2
		default:
			fmt.Printf("Error: Unknown flag: %s\n", args[i])
			fmt.Println("Usage: ssd provision [--server SERVER] [--email EMAIL] [--runtime RUNTIME]")
			os.Exit(1)
		}
	}

	// Try to get server and runtime from config if not specified
	if server == "" || rt == "" {
		rootCfg, _, err := config.Resolve(globalConfigPath, globalEnvName)
		if err == nil {
			if server == "" && rootCfg.Server != "" {
				server = rootCfg.Server
			}
			if rt == "" {
				rt = rootCfg.Runtime
			}
		}
	}
	if rt == "" {
		rt = "compose"
	}

	if server == "" {
		fmt.Println("Error: server not specified and not found in config")
		fmt.Println("Usage: ssd provision --server SERVER [--email EMAIL]")
		os.Exit(1)
	}

	// If no email flag, prompt user
	if email == "" {
		fmt.Print("Enter email for Let's Encrypt: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Error reading email: %v\n", err)
			os.Exit(1)
		}
		email = strings.TrimSpace(input)
		if email == "" {
			fmt.Println("Error: email cannot be empty")
			os.Exit(1)
		}
	}

	fmt.Printf("Provisioning server %s (runtime: %s) with email %s...\n\n", server, rt, email)

	var provErr error
	switch rt {
	case "k3s":
		provErr = provision.ProvisionK3s(server, email)
	default:
		provErr = provision.Provision(server, email)
	}
	if provErr != nil {
		fmt.Printf("\nError: %v\n", provErr)
		os.Exit(1)
	}

	fmt.Println("\nProvisioning completed successfully!")
}

func runProvisionCheck(args []string) {
	if wantsHelp(args) {
		printProvisionCheckHelp()
		return
	}

	var server, rt string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				fmt.Println("Error: --server requires a value")
				os.Exit(1)
			}
			server = args[i+1]
			i += 2
		case "--runtime":
			if i+1 >= len(args) {
				fmt.Println("Error: --runtime requires a value")
				os.Exit(1)
			}
			rt = args[i+1]
			i += 2
		default:
			fmt.Printf("Error: Unknown flag: %s\n", args[i])
			fmt.Println("Usage: ssd provision check [--server SERVER] [--runtime RUNTIME]")
			os.Exit(1)
		}
	}

	if server == "" || rt == "" {
		rootCfg, _, err := config.Resolve(globalConfigPath, globalEnvName)
		if err == nil {
			if server == "" && rootCfg.Server != "" {
				server = rootCfg.Server
			}
			if rt == "" {
				rt = rootCfg.Runtime
			}
		}
	}
	if rt == "" {
		rt = "compose"
	}

	if server == "" {
		fmt.Println("Error: server not specified and not found in config")
		fmt.Println("Usage: ssd provision check [--server SERVER]")
		os.Exit(1)
	}

	fmt.Printf("Checking server %s (runtime: %s)...\n\n", server, rt)

	var results []provision.CheckResult
	var err error
	switch rt {
	case "k3s":
		results, err = provision.CheckK3s(server)
	default:
		results, err = provision.Check(server)
	}
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	hasFail := false
	hasWarn := false
	for _, r := range results {
		var label string
		switch r.Status {
		case provision.StatusOK:
			label = "OK"
		case provision.StatusWarn:
			label = "WARN"
			hasWarn = true
		default:
			label = "FAIL"
			hasFail = true
		}
		fmt.Printf("  %-22s %-4s  %s\n", r.Name, label, r.Message)
	}

	fmt.Println()
	if hasFail {
		fmt.Println("Server is not ready. Run 'ssd provision' to set up missing components.")
		os.Exit(1)
	}
	if hasWarn {
		fmt.Println("Server is ready for ssd deployments.")
		fmt.Println("Traefik is not configured — domain routing will not work.")
	} else {
		fmt.Println("Server is ready for ssd deployments.")
	}
}

func skillDir() string {
	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("Error: cannot resolve executable path: %v\n", err)
		os.Exit(1)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Printf("Error: cannot resolve symlinks: %v\n", err)
		os.Exit(1)
	}
	return filepath.Join(filepath.Dir(exe), "..", "share", "ssd", "skill")
}

func runSkill(args []string) {
	if wantsHelp(args) {
		printSkillHelp()
		return
	}

	var targetDir string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--path":
			if i+1 >= len(args) {
				fmt.Println("Error: --path requires a value")
				os.Exit(1)
			}
			targetDir = args[i+1]
			i += 2
		default:
			fmt.Printf("Error: unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	src := skillDir()

	// Verify skill dir exists
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		fmt.Printf("Error: skill directory not found at %s\n", src)
		fmt.Println("This may happen if ssd was not installed via brew.")
		os.Exit(1)
	}

	if targetDir == "" {
		// Prompt user to pick agent
		fmt.Println("Select your coding agent:")
		fmt.Println("  1) Claude Code (~/.claude/skills/ssd)")
		fmt.Println("  2) Custom path")
		fmt.Print("Choice [1]: ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(input)

		switch choice {
		case "", "1":
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Printf(errorFmt, err)
				os.Exit(1)
			}
			targetDir = filepath.Join(home, ".claude", "skills", "ssd")
		case "2":
			fmt.Print("Enter path: ")
			input, _ = reader.ReadString('\n')
			targetDir = strings.TrimSpace(input)
			if targetDir == "" {
				fmt.Println("Error: path cannot be empty")
				os.Exit(1)
			}
		default:
			fmt.Println("Error: invalid choice")
			os.Exit(1)
		}
	}

	// Remove existing symlink or directory
	if info, err := os.Lstat(targetDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(targetDir); err != nil {
				fmt.Printf("Error: failed to remove existing symlink: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Error: %s already exists and is not a symlink\n", targetDir)
			os.Exit(1)
		}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	if err := os.Symlink(src, targetDir); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	fmt.Printf("Linked %s -> %s\n", targetDir, src)
}

func runInit(args []string) {
	if wantsHelp(args) {
		printInitHelp()
		return
	}

	opts := scaffold.Options{}

	// Parse flags
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-s", "--server":
			if i+1 >= len(args) {
				fmt.Println("Error: --server requires a value")
				os.Exit(1)
			}
			opts.Server = args[i+1]
			i += 2
		case "--stack":
			if i+1 >= len(args) {
				fmt.Println("Error: --stack requires a value")
				os.Exit(1)
			}
			opts.Stack = args[i+1]
			i += 2
		case "--service":
			if i+1 >= len(args) {
				fmt.Println("Error: --service requires a value")
				os.Exit(1)
			}
			opts.Service = args[i+1]
			i += 2
		case "-d", "--domain":
			if i+1 >= len(args) {
				fmt.Println("Error: --domain requires a value")
				os.Exit(1)
			}
			opts.Domain = args[i+1]
			i += 2
		case "--path":
			if i+1 >= len(args) {
				fmt.Println("Error: --path requires a value")
				os.Exit(1)
			}
			opts.Path = args[i+1]
			i += 2
		case "-p", "--port":
			if i+1 >= len(args) {
				fmt.Println("Error: --port requires a value")
				os.Exit(1)
			}
			port, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Printf("Error: invalid port: %s\n", args[i+1])
				os.Exit(1)
			}
			opts.Port = port
			i += 2
		case "-r", "--runtime":
			if i+1 >= len(args) {
				fmt.Println("Error: --runtime requires a value")
				os.Exit(1)
			}
			opts.Runtime = args[i+1]
			i += 2
		case "-f", "--force":
			opts.Force = true
			i++
		default:
			fmt.Printf("Error: Unknown flag: %s\n", args[i])
			printInitHelp()
			os.Exit(1)
		}
	}

	// Interactive mode if no server specified
	if opts.Server == "" {
		reader := bufio.NewReader(os.Stdin)

		if opts.Runtime == "" {
			fmt.Print("Runtime (compose/k3s) [compose]: ")
			rt, _ := reader.ReadString('\n')
			opts.Runtime = strings.TrimSpace(rt)
		}

		fmt.Print("Server (SSH host): ")
		server, _ := reader.ReadString('\n')
		opts.Server = strings.TrimSpace(server)

		fmt.Print("Stack path (e.g., /dockge/stacks/myapp) [optional]: ")
		stack, _ := reader.ReadString('\n')
		opts.Stack = strings.TrimSpace(stack)

		fmt.Print("Service name [app]: ")
		service, _ := reader.ReadString('\n')
		opts.Service = strings.TrimSpace(service)

		fmt.Print("Domain (e.g., myapp.example.com) [optional]: ")
		domain, _ := reader.ReadString('\n')
		opts.Domain = strings.TrimSpace(domain)

		fmt.Print("Path prefix (e.g., /api) [optional]: ")
		path, _ := reader.ReadString('\n')
		opts.Path = strings.TrimSpace(path)

		fmt.Print("Port [optional]: ")
		portStr, _ := reader.ReadString('\n')
		portStr = strings.TrimSpace(portStr)
		if portStr != "" {
			port, err := strconv.Atoi(portStr)
			if err != nil {
				fmt.Printf("Error: invalid port: %s\n", portStr)
				os.Exit(1)
			}
			opts.Port = port
		}
	}

	// Validate
	if err := scaffold.Validate(opts); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	// Get current directory
	dir, err := os.Getwd()
	if err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	// Write file
	target := scaffold.TargetPath(dir)
	if err := scaffold.WriteFile(dir, opts); err != nil {
		fmt.Printf(errorFmt, err)
		os.Exit(1)
	}

	rel, err := filepath.Rel(dir, target)
	if err != nil {
		rel = target
	}
	fmt.Printf("Created %s\n", rel)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. Edit %s to configure your service\n", rel)
	fmt.Println("  2. Ensure you have a Dockerfile in your project")
	fmt.Println("  3. Run: ssd deploy app")
}

func printSkillHelp() {
	fmt.Print(`ssd skill - Install the ssd skill for your coding agent

Usage:
  ssd skill                       Interactive mode (prompts for agent selection)
  ssd skill --path <dir>          Symlink skill directory to a custom path

Creates a symlink from your agent's skill directory to the ssd skill files.
The skill auto-updates whenever ssd is upgraded.

Supported agents:
  Claude Code                     ~/.claude/skills/ssd

Examples:
  ssd skill
  ssd skill --path ~/.claude/skills/ssd
`)
}

func printInitHelp() {
	fmt.Print(`ssd init - Create an ssd.yaml configuration file

Usage:
  ssd init                        Interactive mode (prompts for each field)
  ssd init [flags]                Non-interactive mode

Output layout:
  Fresh project:                  .ssd/ssd.yaml (preferred, repo-clean)
  Project with existing ssd.yaml: ssd.yaml (legacy, kept as-is)

A .gitignore is created in .ssd/ to keep generated artifacts (.cache/)
out of version control.

Flags:
  -s, --server STRING             SSH host name (from ~/.ssh/config)
  -r, --runtime STRING            Runtime: compose (default) or k3s
      --stack STRING              Stack path on server (default: /stacks/{service})
      --service STRING            Service name (default: app)
  -d, --domain STRING             Domain for Traefik routing
      --path STRING               Path prefix for routing (e.g., /api)
  -p, --port INT                  Container port
  -f, --force                     Overwrite existing config file

If no flags are provided, runs in interactive mode and prompts for each field.

Examples:
  # Interactive mode
  ssd init

  # Minimal non-interactive
  ssd init -s myserver

  # K3s runtime
  ssd init -s myserver -r k3s

  # Full non-interactive
  ssd init -s myserver --stack /stacks/myapp -d myapp.example.com -p 3000

  # Overwrite existing config
  ssd init -s myserver -f
`)
}

func printConfig(cfg *config.Config, indent string) {
	fmt.Printf("%sname: %s\n", indent, cfg.Name)
	fmt.Printf("%sserver: %s\n", indent, cfg.Server)
	fmt.Printf("%sstack: %s\n", indent, cfg.Stack)
	fmt.Printf("%sstack_path: %s\n", indent, cfg.StackPath())
	if cfg.Domain != "" {
		fmt.Printf("%sdomain: %s\n", indent, cfg.Domain)
	}
	if cfg.Path != "" {
		fmt.Printf("%spath: %s\n", indent, cfg.Path)
	}
	// HTTPS defaults to true if not explicitly set
	https := true
	if cfg.HTTPS != nil {
		https = *cfg.HTTPS
	}
	fmt.Printf("%shttps: %v\n", indent, https)
	fmt.Printf("%sport: %d\n", indent, cfg.Port)
	if cfg.Image != "" {
		fmt.Printf("%simage: %s (pre-built)\n", indent, cfg.Image)
	}
	fmt.Printf("%sdockerfile: %s\n", indent, cfg.Dockerfile)
	fmt.Printf("%scontext: %s\n", indent, cfg.Context)
	if cfg.Image == "" {
		fmt.Printf("%simage: %s\n", indent, cfg.ImageName())
	}
	if len(cfg.Files) > 0 {
		fmt.Printf("%sfiles:\n", indent)
		for local, container := range cfg.Files {
			fmt.Printf("%s  %s -> %s\n", indent, local, container)
		}
	}
}

// wantsHelp returns true if args contain -h, --help, or help.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

func printUsage() {
	fmt.Print(`ssd - SSH Deploy

Agentless remote deployment tool for Docker Compose stacks.
Reads ssd.yaml from the current directory, SSHs into the configured server,
builds/pulls Docker images, generates compose.yaml, and starts services.

No agent, no daemon, no CI required. Just SSH and Docker.

Usage:
  ssd <command> [arguments] [global flags]

Global flags (accepted on every command):
      --config PATH               Path to ssd config file (default: .ssd/ssd.yaml,
                                  falls back to ./ssd.yaml for legacy projects)
  -e, --env NAME                  Apply env overlay .ssd/ssd.<NAME>.yaml on top
                                  of the base config (deep-merge)

Commands:
  init                            Create ssd.yaml configuration file
  deploy|up [service]             Build and deploy a service (or all services)
  down [service]                  Stop services (or all if omitted)
  rm [service]                    Permanently remove services (or entire stack)
  restart [service]               Restart without rebuilding
  rollback [service]              Rollback to the previous version
  status [service]                Show container status
  logs [service] [-f]             View service logs
  config [service]                Show resolved configuration
  env <service> <set|list|rm>     Manage environment variables on the server
  secret <service> <set|list|rm>  Manage K8s secrets (k3s runtime only)
  prune [flags]                   Reclaim disk: orphans, images, build cache, dangling
  scale <service> <count>         Live-scale a service (does not edit ssd.yaml)
  provision                       Provision server with Docker and Traefik
  provision check                 Verify server readiness for ssd
  skill                           Install ssd skill for your coding agent
  version                         Show ssd version
  help                            Show this help

Run 'ssd <command> help' or 'ssd <command> -h' for detailed help on any command.

Learn more: https://github.com/byteink/ssd
`)
}

func printDeployHelp() {
	fmt.Print(`ssd deploy - Build and deploy services

Aliases: deploy, up

Usage:
  ssd deploy                      Deploy all services defined in ssd.yaml
  ssd deploy <service>            Deploy a single service

Workflow:
  1. Reads ssd.yaml from the current directory
  2. SSHs into the configured server
  3. Rsyncs source code to a temp directory on the server (skipped for pre-built images)
  4. Builds the Docker image on the server (or pulls if 'image' is set)
  5. Generates compose.yaml in the stack directory
  6. Starts the service using the configured deploy strategy
  7. Cleans up the temp directory

Deploy strategies (set via deploy.strategy in ssd.yaml):
  rollout   (default) Zero-downtime. Scales up new container, health-checks, removes old.
  recreate  In-place replacement via docker compose up --force-recreate. Brief downtime.

Examples:
  # Deploy a single service
  ssd deploy web

  # Deploy all services (builds all images first, then starts)
  ssd deploy

  # ssd.yaml for building from source
  server: myserver
  services:
    web:
      dockerfile: ./Dockerfile
      domain: example.com
      port: 3000

  # ssd.yaml for a pre-built image (no build step)
  server: myserver
  services:
    mongo:
      image: mongo:7
      volumes:
        mongo-data: /data/db
      ports:
        - "27017:27017"

  # ssd.yaml with deploy strategy
  server: myserver
  deploy:
    strategy: rollout
  services:
    web:
      dockerfile: ./Dockerfile
    worker:
      dockerfile: ./Dockerfile.worker
      deploy:
        strategy: recreate    # per-service override
`)
}

func printDownHelp() {
	fmt.Print(`ssd down - Stop running services

Usage:
  ssd down                        Stop all services
  ssd down <service>              Stop a single service

Compose: runs 'docker compose stop'.
K3s: scales deployments to 0 replicas.

The services can be started again with 'ssd up'.

Examples:
  ssd down web
  ssd down
`)
}

func printRmHelp() {
	fmt.Print(`ssd rm - Permanently remove services from the server

Usage:
  ssd rm                          Remove all services and the stack directory
  ssd rm <service>                Remove a single service

Removes containers, env files, images, and all related resources.
With no arguments, also deletes the stack directory.
This action cannot be undone. Requires interactive confirmation.

Compose: stops containers, removes them, deletes images and env files.
K3s: deletes deployments, services, ingresses, configmaps, images, and env files.

To redeploy after removal, run 'ssd up'.

Examples:
  ssd rm web
  ssd rm
`)
}

func printRestartHelp() {
	fmt.Print(`ssd restart - Restart services without rebuilding

Usage:
  ssd restart                     Restart all services in the stack
  ssd restart <service>           Restart a single service

Runs 'docker compose restart' on the server. Does not rebuild images
or update configuration. Use 'ssd deploy' to apply changes.

Examples:
  ssd restart web
  ssd restart
`)
}

func printRollbackHelp() {
	fmt.Print(`ssd rollback - Rollback to the previous version

Usage:
  ssd rollback <service>          Rollback a service to its previous image version

Reads the current image tag from compose.yaml on the server, decrements the
version number, updates compose.yaml, and restarts the service.

Examples:
  ssd rollback web
  ssd rollback api
`)
}

func printStatusHelp() {
	fmt.Print(`ssd status - Show container status

Usage:
  ssd status                      Show status for all containers in the stack
  ssd status <service>            Show status for a specific service

Runs 'docker compose ps' on the server and displays container state,
health, ports, and uptime.

Examples:
  ssd status web
  ssd status
`)
}

func printLogsHelp() {
	fmt.Print(`ssd logs - View service logs

Usage:
  ssd logs [service] [-f]

Flags:
  -f, --follow                    Stream logs in real time (like tail -f)

Shows the last 100 lines of logs by default. Use -f to follow.

Examples:
  ssd logs web                    Show recent logs for web
  ssd logs web -f                 Follow logs for web in real time
  ssd logs                        Show recent logs for all services
`)
}

func printConfigHelp() {
	fmt.Print(`ssd config - Show resolved configuration

Usage:
  ssd config                      Show configuration for all services
  ssd config <service>            Show configuration for a specific service

Displays the fully resolved configuration after applying inheritance
(root-level server, stack, deploy strategy inherited by services).

Examples:
  ssd config web
  ssd config
`)
}

func printEnvHelp() {
	fmt.Print(`ssd env - Manage environment variables on the server

Usage:
  ssd env <service> set KEY=VALUE Set or update an environment variable
  ssd env <service> list          List all environment variables
  ssd env <service> rm KEY        Remove an environment variable

Environment variables are stored in {service}.env files on the server
inside the stack directory (e.g., /stacks/myapp/web.env). These files
are referenced by compose.yaml via env_file and are created automatically
on first deploy with mode 600.

The env file is read, modified in memory, and written back atomically.
Values containing '=' are handled correctly (split on first '=' only).

Examples:
  # Set a database URL (value contains '=')
  ssd env api set DATABASE_URL=postgres://user:pass@host:5432/db?sslmode=require

  # Set multiple variables one at a time
  ssd env api set NODE_ENV=production
  ssd env api set PORT=3000
  ssd env api set SECRET_KEY=abc123

  # List all variables for a service
  ssd env api list

  # Remove a variable
  ssd env api rm OLD_SECRET

  # Variables are available inside containers via env_file in compose.yaml
  # No restart needed after set/rm - run 'ssd restart <service>' to apply

If 'env_file' is set in ssd.yaml for a service, it OVERWRITES any values
set via 'ssd env set' on every deploy. To manage env vars via CLI only,
remove 'env_file' from ssd.yaml first.
`)
}

func printScaleHelp() {
	fmt.Print(`ssd scale - Live-scale a service

Usage:
  ssd scale <service> <count>

Scales a running service to the given replica count. Does NOT modify
ssd.yaml (matches 'kubectl scale' semantics). To persist the change,
edit 'deploy.replicas' in ssd.yaml.

Runtime behavior:
  k3s      k3s kubectl scale deployment/<service> --replicas=<count>
  compose  docker compose up -d --scale <service>=<count> --no-recreate
           (Compose honors scale natively; for scale >1 across restarts
           also set deploy.replicas in ssd.yaml and deploy with
           'docker compose --compatibility'.)

Examples:
  ssd scale web 3
  ssd scale worker 0           # scale down to zero
`)
}

func printPruneHelp() {
	fmt.Print(`ssd prune - Reclaim disk space on the server

Usage:
  ssd prune                       Remove services on server not in ssd.yaml (default)
  ssd prune --images              Remove old image tags beyond per-service retention
  ssd prune --build-cache         Remove build cache entries older than 168h
  ssd prune --dangling            Remove unreferenced (dangling) images
  ssd prune --all                 All of the above (orphans + images + build-cache + dangling)
  ssd prune --keep N              Override per-service retention for --images/--all
  ssd prune --dry-run             Preview candidates without removing
  ssd prune --images --dry-run    Combine flags freely

With no flags, prunes orphans only (preserves historical behavior).

Retention (for --images):
  Default is 2 (current + rollback target) per service.
  Configure in ssd.yaml:

    cleanup:
      retention: 3          # root default
    services:
      web:
        cleanup:
          retention: 5      # per-service override

  retention: 0 disables auto cleanup on deploy but --images still removes
  tags when explicitly invoked with --keep N.

Build cache prunes entries not touched for 168h (7 days). This is
opt-in only — never runs automatically on deploy.

Compose vs k3s:
  compose  docker builder prune / docker image prune / docker rmi
  k3s      sudo buildctl prune / nerdctl image prune / nerdctl rmi
           (buildkitd.sock is root-owned, so build-cache prune needs sudo)

Examples:
  ssd prune
  ssd prune --images --dry-run
  ssd prune --images --keep 3
  ssd prune --all
`)
}

func printSecretHelp() {
	fmt.Print(`ssd secret - Manage K8s secrets (k3s runtime only)

Usage:
  ssd secret <service> set KEY=VALUE  Set or update a secret
  ssd secret <service> list           List all secrets
  ssd secret <service> rm KEY         Remove a secret

Secrets are stored as K8s Secrets and injected as environment variables
into the container alongside ConfigMap env vars. Only available when
runtime is set to k3s in ssd.yaml.

For compose runtime, use 'ssd env' instead.

Examples:
  ssd secret api set DATABASE_URL=postgres://user:pass@host/db
  ssd secret api list
  ssd secret api rm OLD_KEY
`)
}

func printProvisionHelp() {
	fmt.Print(`ssd provision - Provision a server for ssd deployments

Usage:
  ssd provision [flags]           Install runtime dependencies
  ssd provision check [flags]     Verify server readiness for ssd

Flags:
  --server STRING                 SSH host to provision (reads from ssd.yaml if omitted)
  --runtime STRING                Runtime to provision: "compose" (default) or "k3s"
  --email STRING                  Email for Let's Encrypt certificates (prompted if omitted)

Compose runtime (default):
  Installs Docker, Docker Compose, docker-rollout plugin, and sets up Traefik
  as a reverse proxy with automatic HTTPS via Let's Encrypt.

K3s runtime:
  Installs K3s, nerdctl, buildkit (systemd service), and configures Traefik
  ACME for automatic HTTPS. Nerdctl is configured to use K3s's containerd
  socket with namespace k8s.io.

All steps are idempotent and safe to run multiple times.

Examples:
  # Provision compose runtime (default)
  ssd provision --server myserver --email admin@example.com

  # Provision K3s runtime
  ssd provision --server myserver --runtime k3s --email admin@example.com

  # Provision using ssd.yaml (reads runtime and server)
  ssd provision

  # Check if server is ready
  ssd provision check
  ssd provision check --server myserver --runtime k3s
`)
}

func printProvisionCheckHelp() {
	fmt.Print(`ssd provision check - Verify server readiness for ssd

Usage:
  ssd provision check [flags]

Flags:
  --server STRING                 SSH host to check (reads from ssd.yaml if omitted)
  --runtime STRING                Runtime to check: "compose" (default) or "k3s"

Compose checks:
  Docker                          Docker engine is installed
  Docker Compose                  docker compose plugin is available
  docker-rollout                  Zero-downtime rollout plugin is installed
  traefik_web network             Docker network for Traefik routing exists
  Traefik                         Traefik reverse proxy is running

K3s checks:
  K3s                             K3s is installed and running
  kubectl                         kubectl is available
  nerdctl                         nerdctl is installed
  buildkitd                       buildkitd systemd service is active
  Traefik ingress                 Traefik ingress controller is running
  Traefik ACME                    Let's Encrypt is configured

Examples:
  # Check compose runtime (default)
  ssd provision check

  # Check K3s runtime
  ssd provision check --server myserver --runtime k3s
`)
}
