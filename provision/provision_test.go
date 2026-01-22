package provision

import (
	"context"
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
	if err, ok := m.SSHErrors[command]; ok {
		return "", err
	}
	if output, ok := m.SSHOutputs[command]; ok {
		return output, nil
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
