package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/byteink/ssd/config"
)

// Options holds configuration for init command
type Options struct {
	Server  string // Required: SSH host name
	Runtime string // Optional: "compose" (default) or "k3s"
	Stack   string // Optional: stack path (e.g., /dockge/stacks/myapp)
	Service string // Optional: service name (default: "app")
	Domain  string // Optional: domain for Traefik routing
	Path    string // Optional: path prefix for Traefik routing (e.g., /api)
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
	if opts.Runtime != "" {
		if err := config.ValidateRuntime(opts.Runtime); err != nil {
			return err
		}
	}
	return nil
}

// Generate creates the ssd.yaml content from options
func Generate(opts Options) string {
	var sb strings.Builder

	// Runtime (only emit if not default)
	if opts.Runtime == "k3s" {
		fmt.Fprintf(&sb, "runtime: %s\n", opts.Runtime)
	}

	// Server (required)
	fmt.Fprintf(&sb, "server: %s\n", opts.Server)

	// Stack (optional)
	if opts.Stack != "" {
		fmt.Fprintf(&sb, "stack: %s\n", opts.Stack)
	}

	sb.WriteString("\nservices:\n")

	// Service name (default: app)
	serviceName := opts.Service
	if serviceName == "" {
		serviceName = "app"
	}
	fmt.Fprintf(&sb, "  %s:\n", serviceName)

	// Domain, path, port - show configured values, comment out unconfigured
	hasDomain := opts.Domain != ""
	hasPath := opts.Path != ""
	hasPort := opts.Port > 0

	if hasDomain {
		fmt.Fprintf(&sb, "    domain: %s\n", opts.Domain)
	}
	if hasPath {
		fmt.Fprintf(&sb, "    path: %s\n", opts.Path)
	}
	if hasPort {
		fmt.Fprintf(&sb, "    port: %d\n", opts.Port)
	}

	// Add commented hints for unconfigured options
	if !hasDomain || !hasPath || !hasPort {
		sb.WriteString("    # Uncomment and configure as needed:\n")
		if !hasDomain {
			sb.WriteString("    # domain: example.com\n")
		}
		if !hasPath {
			sb.WriteString("    # path: /api\n")
		}
		if !hasPort {
			sb.WriteString("    # port: 3000\n")
		}
	}

	return sb.String()
}

// gitignoreContent is written into .ssd/.gitignore on init so generated
// artifacts (manifests, build metadata) never land in version control.
const gitignoreContent = ".cache/\n"

// TargetPath returns the path init will write to in dir.
//
// Preferred: <dir>/.ssd/ssd.yaml (new layout, keeps repo root clean).
// Legacy:    <dir>/ssd.yaml when it already exists, to avoid surprising
//            existing projects with a new .ssd/ directory.
func TargetPath(dir string) string {
	legacy := filepath.Join(dir, "ssd.yaml")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return filepath.Join(dir, ".ssd", "ssd.yaml")
}

// WriteFile writes the generated ssd config to dir using the layout
// chosen by TargetPath. Creates .ssd/ and .ssd/.gitignore as needed.
//
// When the chosen target already exists, returns an error unless
// opts.Force is set.
func WriteFile(dir string, opts Options) error {
	filePath := TargetPath(dir)

	if _, err := os.Stat(filePath); err == nil && !opts.Force {
		return fmt.Errorf("%s already exists", filePath)
	}

	parent := filepath.Dir(filePath)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	// Drop a .gitignore inside .ssd/ so generated artifacts under .cache/
	// stay out of version control. Idempotent: skipped when the file
	// already exists. Only relevant for the new layout.
	if filepath.Base(parent) == ".ssd" {
		ignorePath := filepath.Join(parent, ".gitignore")
		if _, err := os.Stat(ignorePath); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(ignorePath, []byte(gitignoreContent), 0644); err != nil {
				return fmt.Errorf("failed to write .gitignore: %w", err)
			}
		}
	}

	content := Generate(opts)
	return os.WriteFile(filePath, []byte(content), 0644)
}
