package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]

	switch command {
	case "version", "-v", "--version":
		fmt.Printf("ssd version %s\n", version)
	case "deploy":
		fmt.Println("🚀 Deploying... (coming soon)")
	case "status":
		fmt.Println("📊 Checking status... (coming soon)")
	case "logs":
		fmt.Println("📜 Showing logs... (coming soon)")
	case "config":
		fmt.Println("⚙️  Showing config... (coming soon)")
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("ssd - SSH Deploy")
	fmt.Println()
	fmt.Println("Agentless remote deployment tool for Docker Compose stacks.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  ssd deploy [service]     Deploy application")
	fmt.Println("  ssd status [service]     Check deployment status")
	fmt.Println("  ssd logs [service]       View logs")
	fmt.Println("  ssd config               Show current configuration")
	fmt.Println("  ssd version              Show version")
	fmt.Println("  ssd help                 Show this help")
	fmt.Println()
	fmt.Println("Learn more: https://github.com/byteink/ssd")
}
