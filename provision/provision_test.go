package provision

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// MockRemoteClient is a test double for remote operations
type MockRemoteClient struct {
	SSHCalls         []string
	SSHOutputs       map[string]string
	SSHErrors        map[string]error
	SSHInteractiveCalls []string
	InteractiveErrors   map[string]error
}

func NewMockRemoteClient() *MockRemoteClient {
	return &MockRemoteClient{
		SSHCalls:          make([]string, 0),
		SSHOutputs:        make(map[string]string),
		SSHErrors:         make(map[string]error),
		SSHInteractiveCalls: make([]string, 0),
		InteractiveErrors:   make(map[string]error),
	}
}

func (m *MockRemoteClient) SSH(ctx context.Context, command string) (string, error) {
	m.SSHCalls = append(m.SSHCalls, command)

	// Check exact match first
	if err, ok := m.SSHErrors[command]; ok {
		return "", err
	}
	if output, ok := m.SSHOutputs[command]; ok {
		return output, nil
	}

	// Check substring match for errors (useful for dynamic commands)
	for key, err := range m.SSHErrors {
		if strings.Contains(command, key) {
			return "", err
		}
	}

	// Check substring match for outputs
	for key, output := range m.SSHOutputs {
		if strings.Contains(command, key) && key != "" {
			return output, nil
		}
	}

	return "", nil
}

func (m *MockRemoteClient) SSHInteractive(ctx context.Context, command string) error {
	m.SSHInteractiveCalls = append(m.SSHInteractiveCalls, command)
	if err, ok := m.InteractiveErrors[command]; ok {
		return err
	}
	return nil
}

func TestProvision_InstallsDocker(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = ""

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify Docker installation attempted
	found := false
	for _, call := range mock.SSHInteractiveCalls {
		if strings.Contains(call, "curl -fsSL https://get.docker.com | sh") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Docker installation command, but not found")
	}
}

func TestProvision_SkipsDockerIfInstalled(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify Docker installation NOT attempted
	for _, call := range mock.SSHInteractiveCalls {
		if strings.Contains(call, "curl -fsSL https://get.docker.com") {
			t.Error("Docker installation should have been skipped")
		}
	}
}

func TestProvision_InstallsDockerRollout(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	found := false
	for _, call := range mock.SSHCalls {
		if strings.Contains(call, "docker-rollout") && strings.Contains(call, "curl -fsSL") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected docker-rollout installation command, but not found")
	}
}

func TestProvision_ErrorInInstallDockerRolloutReturnsError(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"
	mock.SSHErrors["docker-rollout"] = fmt.Errorf("curl failed")

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err == nil {
		t.Error("expected error when docker-rollout install fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to install docker-rollout") {
		t.Errorf("expected error message to contain 'failed to install docker-rollout', got: %v", err)
	}
}

func TestProvision_CreatesNetwork(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify network creation command
	found := false
	for _, call := range mock.SSHCalls {
		if strings.Contains(call, "docker network create traefik_web") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected network creation command, but not found")
	}
}

func TestProvision_CreatesTraefikDirectory(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify directory creation
	found := false
	for _, call := range mock.SSHCalls {
		if strings.Contains(call, "mkdir -p /stacks/traefik") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected traefik directory creation, but not found")
	}
}

func TestProvision_CreatesAcmeJson(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify acme.json creation with chmod 600
	found := false
	for _, call := range mock.SSHCalls {
		if strings.Contains(call, "test -f /stacks/traefik/acme.json") &&
			strings.Contains(call, "touch /stacks/traefik/acme.json") &&
			strings.Contains(call, "chmod 600 /stacks/traefik/acme.json") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected acme.json creation with chmod 600, but not found")
	}
}

func TestProvision_WritesComposeYaml(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify compose.yaml write with email
	found := false
	for _, call := range mock.SSHCalls {
		if strings.Contains(call, "test@example.com") && strings.Contains(call, "/stacks/traefik/compose.yaml.tmp") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected compose.yaml write with email, but not found")
	}
}

func TestProvision_StartsTraefik(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify docker compose up -d
	found := false
	for _, call := range mock.SSHInteractiveCalls {
		if strings.Contains(call, "cd /stacks/traefik") && strings.Contains(call, "docker compose up -d") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected docker compose up -d in traefik directory, but not found")
	}
}

func TestProvision_IsIdempotent(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	// Run provision twice
	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error on first run, got: %v", err)
	}

	err = provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error on second run, got: %v", err)
	}

	// Both runs should succeed without errors (idempotent)
}

func TestProvision_ValidatesEmail(t *testing.T) {
	mock := NewMockRemoteClient()

	err := provisionWithClient(context.Background(), mock, "test-server", "")
	if err == nil {
		t.Error("expected error for empty email, got nil")
	}
}

func TestProvision_ValidatesServer(t *testing.T) {
	mock := NewMockRemoteClient()

	err := provisionWithClient(context.Background(), mock, "", "test@example.com")
	if err == nil {
		t.Error("expected error for empty server, got nil")
	}
}

func TestProvision_CallsStepsInOrder(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify all steps were called in the correct order
	expectedSSHSequence := []string{
		"which docker",                                     // Step 1: Check Docker
		"docker-rollout",                                   // Step 2: Install docker-rollout
		"docker network create traefik_web",               // Step 3: Create network
		"mkdir -p /stacks/traefik",                        // Step 4: Create directory
		"test -f /stacks/traefik/acme.json",               // Step 5: Create acme.json
	}

	if len(mock.SSHCalls) < len(expectedSSHSequence) {
		t.Fatalf("expected at least %d SSH calls, got %d", len(expectedSSHSequence), len(mock.SSHCalls))
	}

	// Verify the first calls match the expected sequence
	for i, expected := range expectedSSHSequence {
		if !strings.Contains(mock.SSHCalls[i], expected) {
			t.Errorf("SSH call %d: expected to contain %q, got %q", i, expected, mock.SSHCalls[i])
		}
	}

	// Verify interactive calls for starting Traefik
	found := false
	for _, call := range mock.SSHInteractiveCalls {
		if strings.Contains(call, "docker compose up -d") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected docker compose up -d to be called")
	}
}

func TestProvision_ErrorInInstallDockerReturnsError(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = ""
	mock.InteractiveErrors["which docker || curl -fsSL https://get.docker.com | sh"] =
		fmt.Errorf("failed to install Docker")

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err == nil {
		t.Error("expected error when Docker installation fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to install Docker") {
		t.Errorf("expected error message to contain 'failed to install Docker', got: %v", err)
	}
}

func TestProvision_ErrorInCreateNetworkReturnsError(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"
	mock.SSHErrors["docker network create traefik_web 2>/dev/null || true"] =
		fmt.Errorf("network creation failed")

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err == nil {
		t.Error("expected error when network creation fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create network") {
		t.Errorf("expected error message to contain 'failed to create network', got: %v", err)
	}
}

func TestProvision_ErrorInCreateDirectoryReturnsError(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"
	mock.SSHErrors["mkdir -p /stacks/traefik"] = fmt.Errorf("permission denied")

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err == nil {
		t.Error("expected error when directory creation fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create traefik directory") {
		t.Errorf("expected error message to contain 'failed to create traefik directory', got: %v", err)
	}
}

func TestProvision_ErrorInCreateAcmeJsonReturnsError(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"
	mock.SSHErrors["test -f /stacks/traefik/acme.json || touch /stacks/traefik/acme.json && chmod 600 /stacks/traefik/acme.json"] =
		fmt.Errorf("permission denied")

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err == nil {
		t.Error("expected error when acme.json creation fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create acme.json") {
		t.Errorf("expected error message to contain 'failed to create acme.json', got: %v", err)
	}
}

func TestProvision_ErrorInWriteComposeReturnsError(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"

	// Set error for any command containing compose.yaml.tmp (substring match)
	mock.SSHErrors["compose.yaml.tmp"] = fmt.Errorf("disk full")

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err == nil {
		t.Error("expected error when compose.yaml write fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to write compose.yaml") {
		t.Errorf("expected error message to contain 'failed to write compose.yaml', got: %v", err)
	}
}

func TestProvision_ErrorInStartTraefikReturnsError(t *testing.T) {
	mock := NewMockRemoteClient()
	mock.SSHOutputs["which docker"] = "/usr/bin/docker"
	mock.InteractiveErrors["cd /stacks/traefik && docker compose up -d"] =
		fmt.Errorf("compose file invalid")

	err := provisionWithClient(context.Background(), mock, "test-server", "test@example.com")
	if err == nil {
		t.Error("expected error when Traefik start fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed to start Traefik") {
		t.Errorf("expected error message to contain 'failed to start Traefik', got: %v", err)
	}
}
