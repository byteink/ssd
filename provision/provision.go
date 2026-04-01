package provision

import (
	"context"
	"fmt"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/byteink/ssd/compose"
	"github.com/byteink/ssd/remote"
)

// RemoteClient defines the interface for remote operations needed by provision
type RemoteClient interface {
	SSH(ctx context.Context, command string) (string, error)
	SSHInteractive(ctx context.Context, command string) error
}

// Provision sets up a server with Docker, Traefik, and required infrastructure.
// All operations are idempotent and safe to run multiple times.
//
// Steps performed:
// 1. Install Docker if not present
// 2. Install docker-rollout plugin if not present
// 3. Create traefik_web network
// 4. Create /stacks/traefik directory
// 5. Create acme.json with mode 600
// 6. Write Traefik compose.yaml with atomic write
// 7. Start Traefik with docker compose up -d
//
// server: SSH host from ~/.ssh/config
// email: email for Let's Encrypt certificate registration
func Provision(server, email string) error {
	return provisionWithClient(context.Background(), nil, server, email)
}

// provisionWithClient is the internal implementation that accepts a RemoteClient.
// When client is nil, a real SSH client is created using the server parameter.
func provisionWithClient(ctx context.Context, client RemoteClient, server, email string) error {
	// Validate inputs
	if server == "" {
		return fmt.Errorf("server cannot be empty")
	}
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}

	// Create real client if not provided (for production use)
	if client == nil {
		client = remote.NewSSHClient(server)
	}

	// Step 1: Install Docker (idempotent)
	if err := installDocker(ctx, client); err != nil {
		return fmt.Errorf("failed to install Docker: %w", err)
	}

	// Step 2: Install docker-rollout plugin (idempotent)
	if err := installDockerRollout(ctx, client); err != nil {
		return fmt.Errorf("failed to install docker-rollout: %w", err)
	}

	// Step 3: Create network (idempotent)
	if err := createNetwork(ctx, client); err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	// Step 4: Create traefik directory (idempotent)
	if err := createTraefikDirectory(ctx, client); err != nil {
		return fmt.Errorf("failed to create traefik directory: %w", err)
	}

	// Step 5: Create acme.json (idempotent)
	if err := createAcmeJson(ctx, client); err != nil {
		return fmt.Errorf("failed to create acme.json: %w", err)
	}

	// Step 6: Write compose.yaml (atomic)
	if err := writeTraefikCompose(ctx, client, email); err != nil {
		return fmt.Errorf("failed to write compose.yaml: %w", err)
	}

	// Step 7: Start Traefik
	if err := startTraefik(ctx, client); err != nil {
		return fmt.Errorf("failed to start Traefik: %w", err)
	}

	return nil
}

// installDocker installs Docker if not present (idempotent)
func installDocker(ctx context.Context, client RemoteClient) error {
	// Check if Docker is installed
	output, err := client.SSH(ctx, "which docker")
	if err == nil && strings.TrimSpace(output) != "" {
		// Docker already installed, skip
		return nil
	}

	// Install Docker
	cmd := "which docker || curl -fsSL https://get.docker.com | sh"
	return client.SSHInteractive(ctx, cmd)
}

// installDockerRollout installs the docker-rollout CLI plugin for zero-downtime deploys (idempotent)
func installDockerRollout(ctx context.Context, client RemoteClient) error {
	cmd := "test -f ~/.docker/cli-plugins/docker-rollout || " +
		"(mkdir -p ~/.docker/cli-plugins && " +
		"curl -fsSL https://raw.githubusercontent.com/wowu/docker-rollout/main/docker-rollout " +
		"-o ~/.docker/cli-plugins/docker-rollout && " +
		"chmod +x ~/.docker/cli-plugins/docker-rollout)"
	_, err := client.SSH(ctx, cmd)
	return err
}

// createNetwork creates the traefik_web network (idempotent)
func createNetwork(ctx context.Context, client RemoteClient) error {
	cmd := "docker network create traefik_web 2>/dev/null || true"
	_, err := client.SSH(ctx, cmd)
	return err
}

// createTraefikDirectory creates /stacks/traefik directory (idempotent)
func createTraefikDirectory(ctx context.Context, client RemoteClient) error {
	cmd := "mkdir -p /stacks/traefik"
	_, err := client.SSH(ctx, cmd)
	return err
}

// createAcmeJson creates acme.json with mode 600 (idempotent)
func createAcmeJson(ctx context.Context, client RemoteClient) error {
	cmd := "test -f /stacks/traefik/acme.json || touch /stacks/traefik/acme.json && chmod 600 /stacks/traefik/acme.json"
	_, err := client.SSH(ctx, cmd)
	return err
}

// writeTraefikCompose writes the Traefik compose.yaml atomically
func writeTraefikCompose(ctx context.Context, client RemoteClient, email string) error {
	content := compose.GenerateTraefikCompose(email)

	// Write to temp file first
	tmpPath := "/stacks/traefik/compose.yaml.tmp"
	finalPath := "/stacks/traefik/compose.yaml"

	// Escape content for shell
	escapedContent := strings.ReplaceAll(content, "'", "'\\''")

	// Write to temp file
	writeCmd := fmt.Sprintf("echo '%s' > %s", escapedContent, shellescape.Quote(tmpPath))
	if _, err := client.SSH(ctx, writeCmd); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic move
	moveCmd := fmt.Sprintf("mv %s %s", shellescape.Quote(tmpPath), shellescape.Quote(finalPath))
	if _, err := client.SSH(ctx, moveCmd); err != nil {
		// Clean up temp file on failure
		_, _ = client.SSH(ctx, fmt.Sprintf("rm -f %s", shellescape.Quote(tmpPath)))
		return fmt.Errorf("failed to move temp file: %w", err)
	}

	return nil
}

// startTraefik starts Traefik using docker compose
func startTraefik(ctx context.Context, client RemoteClient) error {
	cmd := "cd /stacks/traefik && docker compose up -d"
	return client.SSHInteractive(ctx, cmd)
}

// CheckStatus represents the severity of a check result
type CheckStatus int

const (
	StatusOK   CheckStatus = iota
	StatusWarn             // optional component missing (e.g., Traefik)
	StatusFail             // required component missing
)

// CheckResult represents the result of a single readiness check
type CheckResult struct {
	Name    string
	Status  CheckStatus
	Message string
}

// Check verifies that a server is ready for ssd deployments.
// Returns a slice of check results (one per check) and an error only for invalid inputs.
// Individual check failures are reported via CheckResult.OK, not as errors.
func Check(server string) ([]CheckResult, error) {
	return checkWithClient(context.Background(), nil, server)
}

// checkWithClient is the internal implementation that accepts a RemoteClient.
func checkWithClient(ctx context.Context, client RemoteClient, server string) ([]CheckResult, error) {
	if server == "" {
		return nil, fmt.Errorf("server cannot be empty")
	}

	if client == nil {
		client = remote.NewSSHClient(server)
	}

	results := make([]CheckResult, 0, 5)

	results = append(results, checkDocker(ctx, client))
	results = append(results, checkDockerCompose(ctx, client))
	results = append(results, checkDockerRollout(ctx, client))
	results = append(results, checkTraefikNetwork(ctx, client))
	results = append(results, checkTraefikRunning(ctx, client))

	return results, nil
}

func checkDocker(ctx context.Context, client RemoteClient) CheckResult {
	output, err := client.SSH(ctx, "which docker")
	if err != nil || strings.TrimSpace(output) == "" {
		return CheckResult{Name: "Docker", Status: StatusFail, Message: "not installed"}
	}
	return CheckResult{Name: "Docker", Status: StatusOK, Message: strings.TrimSpace(output)}
}

func checkDockerCompose(ctx context.Context, client RemoteClient) CheckResult {
	output, err := client.SSH(ctx, "docker compose version")
	if err != nil || strings.TrimSpace(output) == "" {
		return CheckResult{Name: "Docker Compose", Status: StatusFail, Message: "not installed"}
	}
	return CheckResult{Name: "Docker Compose", Status: StatusOK, Message: strings.TrimSpace(output)}
}

func checkDockerRollout(ctx context.Context, client RemoteClient) CheckResult {
	_, err := client.SSH(ctx, "test -f ~/.docker/cli-plugins/docker-rollout && echo ok")
	if err != nil {
		return CheckResult{Name: "docker-rollout", Status: StatusFail, Message: "plugin not installed"}
	}
	return CheckResult{Name: "docker-rollout", Status: StatusOK, Message: "installed"}
}

func checkTraefikNetwork(ctx context.Context, client RemoteClient) CheckResult {
	_, err := client.SSH(ctx, "docker network inspect traefik_web >/dev/null 2>&1 && echo ok")
	if err != nil {
		return CheckResult{Name: "traefik_web network", Status: StatusWarn, Message: "not found (needed for domain routing)"}
	}
	return CheckResult{Name: "traefik_web network", Status: StatusOK, Message: "exists"}
}

func checkTraefikRunning(ctx context.Context, client RemoteClient) CheckResult {
	output, err := client.SSH(ctx, "cd /stacks/traefik && docker compose ps --format '{{.State}}' 2>/dev/null")
	if err != nil || strings.TrimSpace(output) == "" {
		return CheckResult{Name: "Traefik", Status: StatusWarn, Message: "not running (needed for domain routing)"}
	}
	if strings.Contains(output, "running") {
		return CheckResult{Name: "Traefik", Status: StatusOK, Message: "running"}
	}
	return CheckResult{Name: "Traefik", Status: StatusWarn, Message: "not running (state: " + strings.TrimSpace(output) + ")"}
}
