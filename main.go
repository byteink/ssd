package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/deploy"
	"github.com/byteink/ssd/provision"
	"github.com/byteink/ssd/remote"
	"github.com/byteink/ssd/scaffold"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "version", "-v", "--version":
		fmt.Printf("ssd version %s\n", version)
	case "deploy":
		runDeploy(args)
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
	case "init":
		runInit(args)
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

func loadConfig(serviceName string) *config.Config {
	rootCfg, err := config.Load("")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := rootCfg.GetService(serviceName)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		if !rootCfg.IsSingleService() {
			fmt.Printf("Available services: %s\n", strings.Join(rootCfg.ListServices(), ", "))
		}
		os.Exit(1)
	}

	return cfg
}

func runDeploy(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: ssd deploy <service>")
		os.Exit(1)
	}
	serviceName := args[0]

	rootCfg, err := config.Load("")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := rootCfg.GetService(serviceName)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		if !rootCfg.IsSingleService() {
			fmt.Printf("Available services: %s\n", strings.Join(rootCfg.ListServices(), ", "))
		}
		os.Exit(1)
	}

	// Load dependency configs if any
	var depConfigs map[string]*config.Config
	if len(cfg.DependsOn) > 0 {
		depConfigs = make(map[string]*config.Config)
		for _, dep := range cfg.DependsOn {
			depCfg, err := rootCfg.GetService(dep)
			if err != nil {
				fmt.Printf("Warning: Could not load dependency %s config: %v\n", dep, err)
				continue
			}
			depConfigs[dep] = depCfg
		}
	}

	fmt.Printf("Deploying %s to %s...\n\n", cfg.Name, cfg.Server)

	client := remote.NewClient(cfg)
	opts := &deploy.Options{
		Output:       os.Stdout,
		Dependencies: depConfigs,
	}

	if err := deploy.DeployWithClient(cfg, client, opts); err != nil {
		fmt.Printf("\nError: %v\n", err)
		os.Exit(1)
	}
}

func runRestart(args []string) {
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	cfg := loadConfig(serviceName)

	fmt.Printf("Restarting %s on %s...\n\n", cfg.Name, cfg.Server)

	if err := deploy.Restart(cfg); err != nil {
		fmt.Printf("\nError: %v\n", err)
		os.Exit(1)
	}
}

func runRollback(args []string) {
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	cfg := loadConfig(serviceName)

	fmt.Printf("Rolling back %s on %s...\n\n", cfg.Name, cfg.Server)

	if err := deploy.Rollback(cfg); err != nil {
		fmt.Printf("\nError: %v\n", err)
		os.Exit(1)
	}
}

func runStatus(args []string) {
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	cfg := loadConfig(serviceName)
	client := remote.NewClient(cfg)

	fmt.Printf("Status for %s on %s:\n\n", cfg.Name, cfg.Server)

	status, err := client.GetContainerStatus(context.Background())
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if status == "" {
		fmt.Println("No containers found")
	} else {
		fmt.Println(status)
	}
}

func runLogs(args []string) {
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

	cfg := loadConfig(serviceName)
	client := remote.NewClient(cfg)

	if err := client.GetLogs(context.Background(), follow, tail); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func runConfig(args []string) {
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rootCfg, err := config.Load("")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

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
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Configuration:")
	printConfig(cfg, "  ")
}

func runEnv(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: ssd env <service> <set|list|rm> [...]")
		os.Exit(1)
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

	cfg := loadConfig(service)
	client := remote.NewClient(cfg)

	if err := client.SetEnvVar(context.Background(), service, key, value); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Set %s=%s for service %s\n", key, value, service)
}

func runEnvList(service string, args []string) {
	cfg := loadConfig(service)
	client := remote.NewClient(cfg)

	content, err := client.GetEnvFile(context.Background(), service)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
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

	cfg := loadConfig(service)
	client := remote.NewClient(cfg)

	if err := client.RemoveEnvVar(context.Background(), service, key); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed %s from service %s\n", key, service)
}

func runProvision(args []string) {
	var server, email string

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
		default:
			fmt.Printf("Error: Unknown flag: %s\n", args[i])
			fmt.Println("Usage: ssd provision [--server SERVER] [--email EMAIL]")
			os.Exit(1)
		}
	}

	// If no server flag, try to get from config
	if server == "" {
		rootCfg, err := config.Load("")
		if err == nil && rootCfg.Server != "" {
			server = rootCfg.Server
		}
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

	fmt.Printf("Provisioning server %s with email %s...\n\n", server, email)

	if err := provision.Provision(server, email); err != nil {
		fmt.Printf("\nError: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nProvisioning completed successfully!")
}

func runInit(args []string) {
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
		case "-f", "--force":
			opts.Force = true
			i++
		default:
			fmt.Printf("Error: Unknown flag: %s\n", args[i])
			printInitUsage()
			os.Exit(1)
		}
	}

	// Interactive mode if no server specified
	if opts.Server == "" {
		reader := bufio.NewReader(os.Stdin)

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
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Get current directory
	dir, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Write file
	if err := scaffold.WriteFile(dir, opts); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Created ssd.yaml")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit ssd.yaml to configure your service")
	fmt.Println("  2. Ensure you have a Dockerfile in your project")
	fmt.Println("  3. Run: ssd deploy app")
}

func printInitUsage() {
	fmt.Println("Usage: ssd init [flags]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -s, --server STRING   SSH host name (required)")
	fmt.Println("      --stack STRING    Stack path (e.g., /dockge/stacks/myapp)")
	fmt.Println("      --service STRING  Service name (default: app)")
	fmt.Println("  -d, --domain STRING   Domain for Traefik routing")
	fmt.Println("  -p, --port INT        Container port")
	fmt.Println("  -f, --force           Overwrite existing ssd.yaml")
	fmt.Println()
	fmt.Println("If no flags are provided, runs in interactive mode.")
}

func printConfig(cfg *config.Config, indent string) {
	fmt.Printf("%sname: %s\n", indent, cfg.Name)
	fmt.Printf("%sserver: %s\n", indent, cfg.Server)
	fmt.Printf("%sstack: %s\n", indent, cfg.Stack)
	fmt.Printf("%sstack_path: %s\n", indent, cfg.StackPath())
	if cfg.Domain != "" {
		fmt.Printf("%sdomain: %s\n", indent, cfg.Domain)
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
}

func printUsage() {
	fmt.Println("ssd - SSH Deploy")
	fmt.Println()
	fmt.Println("Agentless remote deployment tool for Docker Compose stacks.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  ssd init                        Create ssd.yaml (interactive or flags)")
	fmt.Println("  ssd deploy <service>            Deploy application (build + restart)")
	fmt.Println("  ssd restart [service]           Restart stack without rebuilding")
	fmt.Println("  ssd rollback [service]          Rollback to previous version")
	fmt.Println("  ssd status [service]            Check deployment status")
	fmt.Println("  ssd logs [service] [-f]         View logs (-f to follow)")
	fmt.Println("  ssd config [service]            Show current configuration")
	fmt.Println("  ssd env <svc> set K=V           Set environment variable")
	fmt.Println("  ssd env <svc> list              List environment variables")
	fmt.Println("  ssd env <svc> rm KEY            Remove environment variable")
	fmt.Println("  ssd provision                   Provision server (Docker + Traefik)")
	fmt.Println("  ssd version                     Show version")
	fmt.Println("  ssd help                        Show this help")
	fmt.Println()
	fmt.Println("Learn more: https://github.com/byteink/ssd")
}
