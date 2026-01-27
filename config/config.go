package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// HealthCheck represents Docker healthcheck configuration
type HealthCheck struct {
	Cmd      string `yaml:"cmd"`
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
	Retries  int    `yaml:"retries"`
}

// Config represents a single service configuration
type Config struct {
	Name        string            `yaml:"name"`
	Server      string            `yaml:"server"`
	Stack       string            `yaml:"stack"`
	Dockerfile  string            `yaml:"dockerfile"`
	Context     string            `yaml:"context"`
	Domain      string            `yaml:"domain"`      // optional, enables Traefik
	Path        string            `yaml:"path"`        // optional, path prefix for Traefik routing
	HTTPS       *bool             `yaml:"https"`       // default true, pointer for nil check
	Port        int               `yaml:"port"`        // default 80
	Image       string            `yaml:"image"`       // if set, skip build (pre-built)
	Target      string            `yaml:"target"`      // Docker build target stage
	DependsOn   []string          `yaml:"depends_on"`
	Volumes     map[string]string `yaml:"volumes"`     // name: mount_path
	HealthCheck *HealthCheck      `yaml:"healthcheck"`
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

	// Service name is required
	if serviceName == "" {
		return nil, fmt.Errorf("service name required")
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

	if err := validateConfig(result); err != nil {
		return nil, err
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

// validateConfig validates all fields of a resolved config
func validateConfig(cfg *Config) error {
	if err := ValidateServer(cfg.Server); err != nil {
		return fmt.Errorf("invalid server: %w", err)
	}

	if cfg.Domain != "" {
		if err := ValidateDomain(cfg.Domain); err != nil {
			return fmt.Errorf("invalid domain: %w", err)
		}
	}

	if cfg.Path != "" {
		if cfg.Domain == "" {
			return fmt.Errorf("path requires domain to be set")
		}
		if err := ValidatePath(cfg.Path); err != nil {
			return fmt.Errorf("invalid path: %w", err)
		}
	}

	for volumeName := range cfg.Volumes {
		if err := ValidateVolumeName(volumeName); err != nil {
			return fmt.Errorf("invalid volume name %q: %w", volumeName, err)
		}
	}

	if err := ValidateHealthCheck(cfg.HealthCheck); err != nil {
		return fmt.Errorf("invalid healthcheck: %w", err)
	}

	if cfg.Target != "" {
		if err := ValidateTarget(cfg.Target); err != nil {
			return fmt.Errorf("invalid target: %w", err)
		}
	}

	return nil
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

// IsPrebuilt returns true if this config uses a pre-built image
func (c *Config) IsPrebuilt() bool {
	return c.Image != ""
}

// UseHTTPS returns true if HTTPS should be used for this config
// Returns true when HTTPS is nil (default) or explicitly set to true
func (c *Config) UseHTTPS() bool {
	if c.HTTPS == nil {
		return true
	}
	return *c.HTTPS
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

// ValidateDomain validates a domain name for security and correctness
func ValidateDomain(domain string) error {
	// Reject empty string
	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}

	// Reject protocol prefix
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		return fmt.Errorf("domain cannot contain protocol prefix")
	}

	// Reject paths
	if strings.Contains(domain, "/") {
		return fmt.Errorf("domain cannot contain path")
	}

	// Reject ports
	if strings.Contains(domain, ":") {
		return fmt.Errorf("domain cannot contain port")
	}

	// Reject spaces
	if strings.Contains(domain, " ") {
		return fmt.Errorf("domain cannot contain spaces")
	}

	// Max length check (DNS limit)
	if len(domain) > 253 {
		return fmt.Errorf("domain exceeds maximum length of 253 characters")
	}

	// Reject shell metacharacters
	dangerousChars := ";|&$`(){}[]<>\\\"'"
	for _, r := range domain {
		if strings.ContainsRune(dangerousChars, r) {
			return fmt.Errorf("domain contains invalid character: %c", r)
		}
	}

	// Validate allowed characters: letters, digits, hyphens, dots
	for _, r := range domain {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		isAllowed := isLetter || isDigit || r == '-' || r == '.'
		if !isAllowed {
			return fmt.Errorf("domain contains invalid character: %c (only letters, digits, hyphens, and dots allowed)", r)
		}
	}

	return nil
}

// ValidatePath validates a URL path prefix for Traefik routing
func ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if len(path) > 256 {
		return fmt.Errorf("path exceeds maximum length of 256 characters")
	}

	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must start with /")
	}

	if strings.Contains(path, "..") {
		return fmt.Errorf("path cannot contain traversal sequence (..)")
	}

	dangerousChars := ";|&$`(){}[]<>\\\"' *?"
	for _, r := range path {
		if strings.ContainsRune(dangerousChars, r) {
			return fmt.Errorf("path contains invalid character: %c", r)
		}
	}

	// Only allow alphanumeric, hyphens, underscores, dots, slashes
	for _, r := range path {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		isAllowed := isLetter || isDigit || r == '-' || r == '_' || r == '.' || r == '/'
		if !isAllowed {
			return fmt.Errorf("path contains invalid character: %c", r)
		}
	}

	return nil
}

// ValidateVolumeName validates a Docker volume name for security and correctness
func ValidateVolumeName(name string) error {
	if name == "" {
		return fmt.Errorf("volume name cannot be empty")
	}

	if len(name) > 64 {
		return fmt.Errorf("volume name exceeds maximum length of 64 characters")
	}

	// Reject names starting with - or .
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("volume name cannot start with '-' or '.'")
	}

	// Shell metacharacters to reject
	dangerousChars := ";|&$`(){}[]<>\\\"' *?"
	for _, r := range name {
		if strings.ContainsRune(dangerousChars, r) {
			return fmt.Errorf("volume name contains invalid character: %c", r)
		}
	}

	// Validate characters: only alphanumeric, hyphens, underscores, dots
	for _, r := range name {
		isLower := r >= 'a' && r <= 'z'
		isUpper := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		isAllowed := isLower || isUpper || isDigit || r == '-' || r == '_' || r == '.'
		if !isAllowed {
			return fmt.Errorf("volume name contains invalid character: %c (only alphanumeric, hyphens, underscores, and dots allowed)", r)
		}
	}

	return nil
}

// ValidateHealthCheck validates a healthcheck configuration for security and correctness
func ValidateHealthCheck(hc *HealthCheck) error {
	if hc == nil {
		return nil
	}

	if hc.Cmd == "" {
		return fmt.Errorf("healthcheck cmd cannot be empty")
	}

	// Validate interval format if set
	if hc.Interval != "" {
		if err := validateDuration(hc.Interval); err != nil {
			return fmt.Errorf("invalid healthcheck interval: %w", err)
		}
	}

	// Validate timeout format if set
	if hc.Timeout != "" {
		if err := validateDuration(hc.Timeout); err != nil {
			return fmt.Errorf("invalid healthcheck timeout: %w", err)
		}
	}

	// Validate retries range
	if hc.Retries < 0 || hc.Retries > 100 {
		return fmt.Errorf("healthcheck retries must be between 0 and 100")
	}

	return nil
}

// ValidateTarget validates a Docker build target stage name
func ValidateTarget(target string) error {
	if target == "" {
		return fmt.Errorf("target cannot be empty")
	}

	if len(target) > 128 {
		return fmt.Errorf("target exceeds maximum length of 128 characters")
	}

	if strings.HasPrefix(target, "-") || strings.HasPrefix(target, ".") {
		return fmt.Errorf("target cannot start with '-' or '.'")
	}

	for _, r := range target {
		isLower := r >= 'a' && r <= 'z'
		isUpper := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		isAllowed := isLower || isUpper || isDigit || r == '-' || r == '_'
		if !isAllowed {
			return fmt.Errorf("target contains invalid character: %c (only alphanumeric, hyphens, and underscores allowed)", r)
		}
	}

	return nil
}

// validateDuration validates a Docker duration string (e.g., "30s", "1m", "1h")
func validateDuration(d string) error {
	if d == "" {
		return fmt.Errorf("duration cannot be empty")
	}

	if len(d) < 2 {
		return fmt.Errorf("duration must include number and unit (e.g., 30s, 1m)")
	}

	// Check last character is a valid unit
	unit := d[len(d)-1]
	validUnits := "smh" // seconds, minutes, hours
	if !strings.ContainsRune(validUnits, rune(unit)) {
		return fmt.Errorf("duration unit must be s (seconds), m (minutes), or h (hours)")
	}

	// Check number part is valid
	numPart := d[:len(d)-1]
	if numPart == "" {
		return fmt.Errorf("duration must include a number")
	}

	for _, r := range numPart {
		if r < '0' || r > '9' {
			return fmt.Errorf("duration number contains invalid character: %c", r)
		}
	}

	return nil
}
