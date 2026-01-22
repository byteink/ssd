package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options holds configuration for init command
type Options struct {
	Server  string // Required: SSH host name
	Stack   string // Optional: stack path (e.g., /dockge/stacks/myapp)
	Service string // Optional: service name (default: "app")
	Domain  string // Optional: domain for Traefik routing
	Port    int    // Optional: container port
	Force   bool   // Optional: overwrite existing ssd.yaml
}

// Validate checks that required options are set and values are valid
func Validate(opts Options) error {
	if opts.Server == "" {
		return errors.New("server is required")
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

// Generate creates the ssd.yaml content from options
func Generate(opts Options) string {
	var sb strings.Builder

	// Server (required)
	sb.WriteString(fmt.Sprintf("server: %s\n", opts.Server))

	// Stack (optional)
	if opts.Stack != "" {
		sb.WriteString(fmt.Sprintf("stack: %s\n", opts.Stack))
	}

	sb.WriteString("\nservices:\n")

	// Service name (default: app)
	serviceName := opts.Service
	if serviceName == "" {
		serviceName = "app"
	}
	sb.WriteString(fmt.Sprintf("  %s:\n", serviceName))

	// Domain and port - show configured values, comment out unconfigured
	hasDomain := opts.Domain != ""
	hasPort := opts.Port > 0

	if hasDomain {
		sb.WriteString(fmt.Sprintf("    domain: %s\n", opts.Domain))
	}
	if hasPort {
		sb.WriteString(fmt.Sprintf("    port: %d\n", opts.Port))
	}

	// Add commented hints for unconfigured options
	if !hasDomain || !hasPort {
		sb.WriteString("    # Uncomment and configure as needed:\n")
		if !hasDomain {
			sb.WriteString("    # domain: example.com\n")
		}
		if !hasPort {
			sb.WriteString("    # port: 3000\n")
		}
	}

	return sb.String()
}

// WriteFile writes the generated ssd.yaml to the specified directory
func WriteFile(dir string, opts Options) error {
	filePath := filepath.Join(dir, "ssd.yaml")

	// Check if file exists
	if _, err := os.Stat(filePath); err == nil {
		if !opts.Force {
			return errors.New("ssd.yaml already exists")
		}
	}

	content := Generate(opts)
	return os.WriteFile(filePath, []byte(content), 0644)
}
