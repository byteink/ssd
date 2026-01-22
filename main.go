package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/deploy"
	"github.com/byteink/ssd/remote"
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
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	cfg := loadConfig(serviceName)

	fmt.Printf("Deploying %s to %s...\n\n", cfg.Name, cfg.Server)

	if err := deploy.Deploy(cfg); err != nil {
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
	// TODO: Implement env set
	fmt.Printf("runEnvSet called for service=%s with args=%v\n", service, args)
}

func runEnvList(service string, args []string) {
	// TODO: Implement env list
	fmt.Printf("runEnvList called for service=%s with args=%v\n", service, args)
}

func runEnvRm(service string, args []string) {
	// TODO: Implement env rm
	fmt.Printf("runEnvRm called for service=%s with args=%v\n", service, args)
}

func printConfig(cfg *config.Config, indent string) {
	fmt.Printf("%sname: %s\n", indent, cfg.Name)
	fmt.Printf("%sserver: %s\n", indent, cfg.Server)
	fmt.Printf("%sstack: %s\n", indent, cfg.Stack)
	fmt.Printf("%sstack_path: %s\n", indent, cfg.StackPath())
	fmt.Printf("%sdockerfile: %s\n", indent, cfg.Dockerfile)
	fmt.Printf("%scontext: %s\n", indent, cfg.Context)
	fmt.Printf("%simage: %s\n", indent, cfg.ImageName())
}

func printUsage() {
	fmt.Println("ssd - SSH Deploy")
	fmt.Println()
	fmt.Println("Agentless remote deployment tool for Docker Compose stacks.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  ssd deploy [service]     Deploy application (build + restart)")
	fmt.Println("  ssd restart [service]    Restart stack without rebuilding")
	fmt.Println("  ssd rollback [service]   Rollback to previous version")
	fmt.Println("  ssd status [service]     Check deployment status")
	fmt.Println("  ssd logs [service] [-f]  View logs (-f to follow)")
	fmt.Println("  ssd config [service]     Show current configuration")
	fmt.Println("  ssd version              Show version")
	fmt.Println("  ssd help                 Show this help")
	fmt.Println()
	fmt.Println("Learn more: https://github.com/byteink/ssd")
}
