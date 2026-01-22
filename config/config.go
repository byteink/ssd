package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Config represents a single service configuration
type Config struct {
	Name       string `yaml:"name"`
	Server     string `yaml:"server"`
	Stack      string `yaml:"stack"`
	Dockerfile string `yaml:"dockerfile"`
	Context    string `yaml:"context"`
	Domain     string `yaml:"domain"`      // optional, enables Traefik
	HTTPS      *bool  `yaml:"https"`       // default true, pointer for nil check
	Port       int    `yaml:"port"`        // default 80
	Image      string `yaml:"image"`       // if set, skip build (pre-built)
}

// RootConfig represents the ssd.yaml file structure
type RootConfig struct {
	Server   string              `yaml:"server"`
	Stack    string              `yaml:"stack"`
	Services map[string]*Config `yaml:"services"`
}

// Load reads and parses ssd.yaml from the current directory or specified path
func Load(path string) (*RootConfig, error) {
	if path == "" {
		path = "ssd.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return LoadFromBytes(data)
}

// LoadFromBytes parses raw YAML bytes into RootConfig
// Does not panic on any input, returns error instead
// Enables fuzz testing without file system
func LoadFromBytes(data []byte) (*RootConfig, error) {
	var cfg RootConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &cfg, nil
}

// GetService returns the configuration for a specific service
// serviceName is required when Services map exists
func (r *RootConfig) GetService(serviceName string) (*Config, error) {
	// Services map is required
	if len(r.Services) == 0 {
		return nil, fmt.Errorf("services: is required")
	}

	// Service name is required for multi-service config
	if serviceName == "" {
		return nil, fmt.Errorf("service name required for multi-service config")
	}

	svc, ok := r.Services[serviceName]
	if !ok {
		return nil, fmt.Errorf("service %q not found", serviceName)
	}

	// Inherit root-level values if not set on service
	cfg := *svc
	if cfg.Server == "" {
		cfg.Server = r.Server
	}
	if cfg.Stack == "" {
		cfg.Stack = r.Stack
	}

	result, err := applyDefaults(&cfg, serviceName)
	if err != nil {
		return nil, err
	}

	// Validate server
	if err := ValidateServer(result.Server); err != nil {
		return nil, fmt.Errorf("invalid server: %w", err)
	}

	return result, nil
}

// ListServices returns all service names in a multi-service config
func (r *RootConfig) ListServices() []string {
	if len(r.Services) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.Services))
	for name := range r.Services {
		names = append(names, name)
	}
	return names
}

// IsSingleService returns true if this is a single-service config
func (r *RootConfig) IsSingleService() bool {
	return len(r.Services) == 0
}

// applyDefaults fills in default values for a config and validates the stack path
func applyDefaults(cfg *Config, serviceName string) (*Config, error) {
	result := *cfg

	// Default name: use service name or current directory name
	if result.Name == "" {
		if serviceName != "" {
			result.Name = serviceName
		} else {
			if cwd, err := os.Getwd(); err == nil {
				result.Name = filepath.Base(cwd)
			}
		}
	}

	// Validate name before using it in stack path
	if err := ValidateName(result.Name); err != nil {
		return nil, fmt.Errorf("invalid service name: %w", err)
	}

	// Default stack: /stacks/{name}
	// If stack is set, use it as the full path (don't append name)
	if result.Stack == "" {
		result.Stack = filepath.Join("/stacks", result.Name)
	}

	// Validate stack path
	if err := ValidateStackPath(result.Stack); err != nil {
		return nil, fmt.Errorf("invalid stack path: %w", err)
	}

	// Default dockerfile: ./Dockerfile
	if result.Dockerfile == "" {
		result.Dockerfile = "./Dockerfile"
	}

	// Default context: .
	if result.Context == "" {
		result.Context = "."
	}

	// Default port: 80
	if result.Port == 0 {
		result.Port = 80
	}

	return &result, nil
}

// StackPath returns the full path to the stack directory on the server
// This is the directory containing compose.yaml
func (c *Config) StackPath() string {
	return c.Stack
}

// ImageName returns the Docker image name (without tag)
func (c *Config) ImageName() string {
	if c.Image != "" {
		return c.Image // pre-built image
	}
	project := filepath.Base(c.Stack)
	return fmt.Sprintf("ssd-%s-%s", project, c.Name)
}

// ValidateServer validates a server hostname/identifier
// Returns an error if the server name contains shell metacharacters or is invalid
func ValidateServer(server string) error {
	if server == "" {
		return fmt.Errorf("server cannot be empty")
	}

	if len(server) > 253 {
		return fmt.Errorf("server name exceeds maximum length of 253 characters")
	}

	// Check for shell metacharacters
	dangerous := ";|&$`(){}[]<>\\\"'"
	for _, r := range server {
		if strings.ContainsRune(dangerous, r) {
			return fmt.Errorf("server name contains invalid character: %q", r)
		}
	}

	// Validate allowed characters: alphanumeric, hyphen, underscore, dot
	for _, r := range server {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("server name contains invalid character: %q", r)
		}
	}

	return nil
}

// ValidateName validates a service name for security and correctness
func ValidateName(name string) error {
	// Reject empty names
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	// Max length check
	if len(name) > 128 {
		return fmt.Errorf("name exceeds maximum length of 128 characters")
	}

	// Reject names starting with - or .
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("name cannot start with '-' or '.'")
	}

	// Shell metacharacters to reject
	dangerousChars := ";|&$`(){}[]<>\\\"'"
	for _, r := range name {
		if strings.ContainsRune(dangerousChars, r) {
			return fmt.Errorf("name contains invalid character: %c", r)
		}
	}

	// Validate characters: only alphanumeric, hyphens, underscores
	for _, r := range name {
		isLower := r >= 'a' && r <= 'z'
		isUpper := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		isAllowed := isLower || isUpper || isDigit || r == '-' || r == '_'
		if !isAllowed {
			return fmt.Errorf("name contains invalid character: %c (only alphanumeric, hyphens, and underscores allowed)", r)
		}
	}

	return nil
}

// ValidateStackPath validates a stack path for security and correctness
func ValidateStackPath(path string) error {
	// Reject empty paths
	if path == "" {
		return fmt.Errorf("stack path cannot be empty")
	}

	// Max length check (Linux PATH_MAX is 4096)
	if len(path) > 4096 {
		return fmt.Errorf("stack path exceeds maximum length of 4096 characters")
	}

	// Must be absolute path
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("stack path must be absolute (start with /)")
	}

	// Reject path traversal attempts
	if strings.Contains(path, "..") {
		return fmt.Errorf("stack path contains path traversal sequence (..)")
	}

	// Shell metacharacters to reject for command injection prevention
	dangerousChars := ";|&$`(){}[]<>\\\"'*?"
	for _, r := range path {
		if strings.ContainsRune(dangerousChars, r) {
			return fmt.Errorf("stack path contains shell metacharacter: %c", r)
		}
	}

	return nil
}
