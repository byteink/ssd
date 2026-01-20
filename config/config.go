package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents a single service configuration
type Config struct {
	Name       string `yaml:"name"`
	Server     string `yaml:"server"`
	Stack      string `yaml:"stack"`
	Dockerfile string `yaml:"dockerfile"`
	Context    string `yaml:"context"`
}

// RootConfig represents the ssd.yaml file structure
type RootConfig struct {
	// Single service mode
	Name       string `yaml:"name"`
	Server     string `yaml:"server"`
	Stack      string `yaml:"stack"`
	Dockerfile string `yaml:"dockerfile"`
	Context    string `yaml:"context"`

	// Multi-service mode
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

	var cfg RootConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}

// GetService returns the configuration for a specific service
// If serviceName is empty and it's a single-service config, returns that config
// If serviceName is empty and it's a multi-service config, returns an error
func (r *RootConfig) GetService(serviceName string) (*Config, error) {
	// Multi-service mode
	if len(r.Services) > 0 {
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

		return applyDefaults(&cfg, serviceName), nil
	}

	// Single-service mode
	if r.Server == "" {
		return nil, fmt.Errorf("server is required in config")
	}

	cfg := &Config{
		Name:       r.Name,
		Server:     r.Server,
		Stack:      r.Stack,
		Dockerfile: r.Dockerfile,
		Context:    r.Context,
	}

	return applyDefaults(cfg, ""), nil
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

// applyDefaults fills in default values for a config
func applyDefaults(cfg *Config, serviceName string) *Config {
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

	// Default stack: /stacks/{name}
	// If stack is set, use it as the full path (don't append name)
	if result.Stack == "" {
		result.Stack = filepath.Join("/stacks", result.Name)
	}

	// Default dockerfile: ./Dockerfile
	if result.Dockerfile == "" {
		result.Dockerfile = "./Dockerfile"
	}

	// Default context: .
	if result.Context == "" {
		result.Context = "."
	}

	return &result
}

// StackPath returns the full path to the stack directory on the server
// This is the directory containing compose.yaml
func (c *Config) StackPath() string {
	return c.Stack
}

// ImageName returns the Docker image name (without tag)
func (c *Config) ImageName() string {
	return fmt.Sprintf("ssd-%s", c.Name)
}
