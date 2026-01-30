package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Image       string       `yaml:"image"`
	Restart     string       `yaml:"restart"`
	EnvFile     string       `yaml:"env_file,omitempty"`
	Ports       []string     `yaml:"ports,omitempty"`
	Command     []string     `yaml:"command,omitempty"`
	Networks    []string     `yaml:"networks"`
	Volumes     []string     `yaml:"volumes,omitempty"`
	Labels      []string     `yaml:"labels,omitempty"`
	DependsOn   []string     `yaml:"depends_on,omitempty"`
	HealthCheck *HealthCheck `yaml:"healthcheck,omitempty"`
}

// HealthCheck represents a Docker Compose healthcheck definition
type HealthCheck struct {
	Test     []string `yaml:"test"`
	Interval string   `yaml:"interval,omitempty"`
	Timeout  string   `yaml:"timeout,omitempty"`
	Retries  int      `yaml:"retries,omitempty"`
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
func GenerateCompose(services map[string]*config.Config, stack string, versions map[string]int) (string, error) {
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
			svc.Image = fmt.Sprintf("ssd-%s-%s:%d", project, name, versions[name])
		}

		// Add volume mounts
		if len(cfg.Volumes) > 0 {
			svc.Volumes = make([]string, 0, len(cfg.Volumes))
			for volumeName, mountPath := range cfg.Volumes {
				svc.Volumes = append(svc.Volumes, fmt.Sprintf("%s:%s", volumeName, mountPath))
				volumesUsed[volumeName] = true
			}
		}

		// Add depends_on if configured
		if len(cfg.DependsOn) > 0 {
			svc.DependsOn = cfg.DependsOn
		}

		// Add healthcheck if configured
		if cfg.HealthCheck != nil {
			svc.HealthCheck = &HealthCheck{
				Test:     []string{"CMD", "sh", "-c", cfg.HealthCheck.Cmd},
				Interval: cfg.HealthCheck.Interval,
				Timeout:  cfg.HealthCheck.Timeout,
				Retries:  cfg.HealthCheck.Retries,
			}
		}

		// Add Traefik labels if domain is configured
		if cfg.PrimaryDomain() != "" {
			svc.Labels = generateTraefikLabels(project, name, cfg)
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

// generateTraefikLabels creates Traefik routing labels for a service
// project: project name from stack path
// name: service name
// cfg: service configuration
//
// Returns a slice of label strings in Docker Compose format
func routerMiddlewaresLabel(router, middlewares string) string {
	return fmt.Sprintf("traefik.http.routers.%s.middlewares=%s", router, middlewares)
}

func generateTraefikLabels(project, name string, cfg *config.Config) []string {
	primaryDomain := cfg.PrimaryDomain()
	aliasDomains := cfg.AliasDomains()

	labels := generatePrimaryDomainLabels(project, name, cfg, primaryDomain)

	// Add redirect labels for alias domains
	for _, aliasDomain := range aliasDomains {
		labels = append(labels, generateAliasRedirectLabels(project, name, cfg, aliasDomain, primaryDomain)...)
	}

	return labels
}

// generatePrimaryDomainLabels creates Traefik labels for the primary domain
func generatePrimaryDomainLabels(project, name string, cfg *config.Config, domain string) []string {
	routerName := fmt.Sprintf("%s-%s", project, name)

	// Root path "/" is equivalent to no path (matches everything)
	hasSubPath := cfg.Path != "" && cfg.Path != "/"

	rule := fmt.Sprintf("Host(`%s`)", domain)
	if hasSubPath {
		rule = fmt.Sprintf("Host(`%s`) && PathPrefix(`%s`)", domain, cfg.Path)
	}

	labels := []string{
		"traefik.enable=true",
		fmt.Sprintf("traefik.http.routers.%s.rule=%s", routerName, rule),
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", routerName, cfg.Port),
	}

	// StripPrefix middleware when sub-path routing is used (not for root "/")
	stripMiddleware := ""
	if hasSubPath {
		stripName := fmt.Sprintf("%s-strip", routerName)
		stripMiddleware = stripName
		labels = append(labels,
			fmt.Sprintf("traefik.http.middlewares.%s.stripprefix.prefixes=%s", stripName, cfg.Path),
		)
	}

	if cfg.UseHTTPS() {
		if stripMiddleware != "" {
			labels = append(labels, routerMiddlewaresLabel(routerName, stripMiddleware))
		}
		labels = append(labels,
			fmt.Sprintf("traefik.http.routers.%s.entrypoints=websecure", routerName),
			fmt.Sprintf("traefik.http.routers.%s.tls=true", routerName),
			fmt.Sprintf("traefik.http.routers.%s.tls.certresolver=letsencrypt", routerName),
		)

		httpRouterName := fmt.Sprintf("%s-http", routerName)
		httpMiddlewares := "redirect-to-https"
		if stripMiddleware != "" {
			httpMiddlewares = stripMiddleware + ",redirect-to-https"
		}
		labels = append(labels,
			fmt.Sprintf("traefik.http.routers.%s.rule=%s", httpRouterName, rule),
			fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", httpRouterName),
			routerMiddlewaresLabel(httpRouterName, httpMiddlewares),
			"traefik.http.middlewares.redirect-to-https.redirectscheme.scheme=https",
		)
	} else {
		if stripMiddleware != "" {
			labels = append(labels, routerMiddlewaresLabel(routerName, stripMiddleware))
		}
		labels = append(labels,
			fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", routerName),
		)
	}

	return labels
}

// generateAliasRedirectLabels creates Traefik labels to redirect an alias domain to the primary domain
func generateAliasRedirectLabels(project, name string, cfg *config.Config, aliasDomain, primaryDomain string) []string {
	// Sanitize domain for use in label names (replace dots with hyphens)
	sanitizedAlias := strings.ReplaceAll(aliasDomain, ".", "-")
	routerName := fmt.Sprintf("%s-%s-alias-%s", project, name, sanitizedAlias)
	middlewareName := fmt.Sprintf("%s-%s-redirect-%s", project, name, sanitizedAlias)

	scheme := "http"
	if cfg.UseHTTPS() {
		scheme = "https"
	}

	// Escape dots in regex pattern
	escapedAlias := strings.ReplaceAll(aliasDomain, ".", "\\.")
	escapedPrimary := primaryDomain

	labels := []string{
		fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", routerName, aliasDomain),
		fmt.Sprintf("traefik.http.routers.%s.middlewares=%s", routerName, middlewareName),
		fmt.Sprintf("traefik.http.middlewares.%s.redirectregex.regex=^%s://%s/(.*)", middlewareName, scheme, escapedAlias),
		fmt.Sprintf("traefik.http.middlewares.%s.redirectregex.replacement=%s://%s/$${1}", middlewareName, scheme, escapedPrimary),
		fmt.Sprintf("traefik.http.middlewares.%s.redirectregex.permanent=false", middlewareName),
	}

	if cfg.UseHTTPS() {
		labels = append(labels,
			fmt.Sprintf("traefik.http.routers.%s.entrypoints=websecure", routerName),
			fmt.Sprintf("traefik.http.routers.%s.tls=true", routerName),
			fmt.Sprintf("traefik.http.routers.%s.tls.certresolver=letsencrypt", routerName),
		)

		// HTTP router for alias (redirects to HTTPS first, then HTTPS redirects to primary domain)
		httpRouterName := fmt.Sprintf("%s-http", routerName)
		labels = append(labels,
			fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", httpRouterName, aliasDomain),
			fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", httpRouterName),
			fmt.Sprintf("traefik.http.routers.%s.middlewares=redirect-to-https", httpRouterName),
		)
	} else {
		labels = append(labels,
			fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", routerName),
		)
	}

	return labels
}

// GenerateTraefikCompose generates a docker-compose.yaml for Traefik reverse proxy.
// email: email address for ACME/Let's Encrypt certificate registration
//
// Returns a compose file configured for:
// - Traefik v3 with HTTP (80) and HTTPS (443) entrypoints
// - Let's Encrypt ACME with provided email
// - Certificate resolver named "letsencrypt"
// - Volume for acme.json persistence
// - traefik_web network for service discovery
func GenerateTraefikCompose(email string) string {
	compose := ComposeFile{
		Services: map[string]Service{
			"traefik": {
				Image:   "traefik:3",
				Restart: "unless-stopped",
				Ports: []string{
					"80:80",
					"443:443",
				},
				Command: []string{
					"--api.dashboard=true",
					"--providers.docker=true",
					"--providers.docker.exposedbydefault=false",
					"--entrypoints.web.address=:80",
					"--entrypoints.websecure.address=:443",
					"--certificatesresolvers.letsencrypt.acme.email=" + email,
					"--certificatesresolvers.letsencrypt.acme.storage=/acme.json",
					"--certificatesresolvers.letsencrypt.acme.httpchallenge.entrypoint=web",
				},
				Networks: []string{"traefik_web"},
				Volumes: []string{
					"/var/run/docker.sock:/var/run/docker.sock:ro",
					"acme:/acme.json",
				},
			},
		},
		Networks: map[string]Network{
			"traefik_web": {
				Driver: "bridge",
			},
		},
		Volumes: map[string]interface{}{
			"acme": nil,
		},
	}

	data, _ := yaml.Marshal(compose)
	return string(data)
}

// AtomicWrite writes content to destPath atomically after validating it as YAML.
// The write is atomic: if validation fails, the destination file is not modified.
// This prevents partial writes or invalid YAML from being written to disk.
func AtomicWrite(content, destPath string) error {
	tmpPath := destPath + ".tmp"

	// Write to temp file
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Validate YAML
	var parsed interface{}
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		// Clean up temp file on validation failure
		_ = os.Remove(tmpPath)
		return fmt.Errorf("invalid YAML: %w", err)
	}

	// Atomic rename (on same filesystem)
	if err := os.Rename(tmpPath, destPath); err != nil {
		// Clean up temp file on rename failure
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
