package provision

import (
	"context"
	"fmt"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/byteink/ssd/compose"
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
// 2. Create traefik_web network
// 3. Create /stacks/traefik directory
// 4. Create acme.json with mode 600
// 5. Write Traefik compose.yaml with atomic write
// 6. Start Traefik with docker compose up -d
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
		return fmt.Errorf("real SSH client not yet implemented")
	}

	// Step 1: Install Docker (idempotent)
	if err := installDocker(ctx, client); err != nil {
		return fmt.Errorf("failed to install Docker: %w", err)
	}

	// Step 2: Create network (idempotent)
	if err := createNetwork(ctx, client); err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	// Step 3: Create traefik directory (idempotent)
	if err := createTraefikDirectory(ctx, client); err != nil {
		return fmt.Errorf("failed to create traefik directory: %w", err)
	}

	// Step 4: Create acme.json (idempotent)
	if err := createAcmeJson(ctx, client); err != nil {
		return fmt.Errorf("failed to create acme.json: %w", err)
	}

	// Step 5: Write compose.yaml (atomic)
	if err := writeTraefikCompose(ctx, client, email); err != nil {
		return fmt.Errorf("failed to write compose.yaml: %w", err)
	}

	// Step 6: Start Traefik
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
