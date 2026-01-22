package compose

import (
	"fmt"
	"path/filepath"

	"github.com/byteink/ssd/config"
	"gopkg.in/yaml.v3"
)

// ComposeFile represents the structure of a docker-compose.yaml file
type ComposeFile struct {
	Services map[string]Service         `yaml:"services"`
	Networks map[string]Network         `yaml:"networks"`
	Volumes  map[string]interface{}     `yaml:"volumes,omitempty"`
}

// Service represents a Docker Compose service definition
type Service struct {
	Image    string   `yaml:"image"`
	Restart  string   `yaml:"restart"`
	EnvFile  string   `yaml:"env_file"`
	Networks []string `yaml:"networks"`
	Volumes  []string `yaml:"volumes,omitempty"`
}

// Network represents a Docker Compose network definition
type Network struct {
	External bool   `yaml:"external,omitempty"`
	Driver   string `yaml:"driver,omitempty"`
}

// GenerateCompose generates a docker-compose.yaml file for the given services
// services: map of service name to config
// stack: full path to stack directory (used to derive project name)
// version: version number to tag built images with
//
// Returns the generated YAML as a string, or an error
func GenerateCompose(services map[string]*config.Config, stack string, version int) (string, error) {
	if len(services) == 0 {
		return "", fmt.Errorf("at least one service is required")
	}

	project := filepath.Base(stack)
	internalNetwork := project + "_internal"

	compose := ComposeFile{
		Services: make(map[string]Service),
		Networks: map[string]Network{
			"traefik_web": {
				External: true,
			},
			internalNetwork: {
				Driver: "bridge",
			},
		},
	}

	// Track which volumes are used
	volumesUsed := make(map[string]bool)

	// Generate service definitions
	for name, cfg := range services {
		svc := Service{
			Restart:  "unless-stopped",
			EnvFile:  fmt.Sprintf("./%s.env", name),
			Networks: []string{"traefik_web", internalNetwork},
		}

		// Set image name
		if cfg.IsPrebuilt() {
			svc.Image = cfg.Image
		} else {
			svc.Image = fmt.Sprintf("ssd-%s-%s:%d", project, name, version)
		}

		// Add volume mounts
		if len(cfg.Volumes) > 0 {
			svc.Volumes = make([]string, 0, len(cfg.Volumes))
			for volumeName, mountPath := range cfg.Volumes {
				svc.Volumes = append(svc.Volumes, fmt.Sprintf("%s:%s", volumeName, mountPath))
				volumesUsed[volumeName] = true
			}
		}

		compose.Services[name] = svc
	}

	// Add volumes section if any volumes are used
	if len(volumesUsed) > 0 {
		compose.Volumes = make(map[string]interface{})
		for volumeName := range volumesUsed {
			compose.Volumes[volumeName] = nil
		}
	}

	// Marshal to YAML
	data, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	return string(data), nil
}
