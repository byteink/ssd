package compose

import (
	"os"
	"strings"
	"testing"

	"github.com/byteink/ssd/config"
	"gopkg.in/yaml.v3"
)

func TestGenerateCompose_SingleService(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", 1)
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	// Verify valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v\nYAML:\n%s", err, result)
	}

	// Verify service exists
	servicesMap, ok := parsed["services"].(map[string]interface{})
	if !ok {
		t.Fatal("services key missing or not a map")
	}

	webService, ok := servicesMap["web"].(map[string]interface{})
	if !ok {
		t.Fatal("web service missing")
	}

	// Verify image format
	image, ok := webService["image"].(string)
	if !ok {
		t.Fatal("image missing or not a string")
	}
	expected := "ssd-myapp-web:1"
	if image != expected {
		t.Errorf("image = %q, want %q", image, expected)
	}

	// Verify restart policy
	if restart := webService["restart"]; restart != "unless-stopped" {
		t.Errorf("restart = %v, want unless-stopped", restart)
	}

	// Verify env_file
	envFile, ok := webService["env_file"].(string)
	if !ok {
		t.Fatal("env_file missing or not a string")
	}
	if envFile != "./web.env" {
		t.Errorf("env_file = %q, want ./web.env", envFile)
	}

	// Verify networks
	networks, ok := webService["networks"].([]interface{})
	if !ok {
		t.Fatal("networks missing or not an array")
	}
	if len(networks) != 2 {
		t.Fatalf("networks count = %d, want 2", len(networks))
	}

	// Verify traefik_web and myapp_internal networks
	hasTraefik := false
	hasInternal := false
	for _, n := range networks {
		name, ok := n.(string)
		if !ok {
			continue
		}
		if name == "traefik_web" {
			hasTraefik = true
		}
		if name == "myapp_internal" {
			hasInternal = true
		}
	}
	if !hasTraefik {
		t.Error("traefik_web network missing")
	}
	if !hasInternal {
		t.Error("myapp_internal network missing")
	}

	// Verify top-level networks section
	networksMap, ok := parsed["networks"].(map[string]interface{})
	if !ok {
		t.Fatal("networks section missing or not a map")
	}

	traefikNet, ok := networksMap["traefik_web"].(map[string]interface{})
	if !ok {
		t.Fatal("traefik_web network definition missing")
	}
	if traefikNet["external"] != true {
		t.Error("traefik_web network should be external")
	}

	internalNet, ok := networksMap["myapp_internal"].(map[string]interface{})
	if !ok {
		t.Fatal("myapp_internal network definition missing")
	}
	if internalNet["driver"] != "bridge" {
		t.Error("myapp_internal network should use bridge driver")
	}
}

func TestGenerateCompose_MultipleServices(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myproject",
			Port:   80,
		},
		"api": {
			Name:   "api",
			Server: "myserver",
			Stack:  "/stacks/myproject",
			Port:   3000,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myproject", 5)
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	// Verify valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})

	// Verify web service
	webService := servicesMap["web"].(map[string]interface{})
	if webService["image"] != "ssd-myproject-web:5" {
		t.Errorf("web image = %v, want ssd-myproject-web:5", webService["image"])
	}

	// Verify api service
	apiService := servicesMap["api"].(map[string]interface{})
	if apiService["image"] != "ssd-myproject-api:5" {
		t.Errorf("api image = %v, want ssd-myproject-api:5", apiService["image"])
	}

	// Both should use myproject_internal network
	webNetworks := webService["networks"].([]interface{})
	apiNetworks := apiService["networks"].([]interface{})

	hasInternalWeb := false
	for _, n := range webNetworks {
		if n == "myproject_internal" {
			hasInternalWeb = true
		}
	}
	hasInternalAPI := false
	for _, n := range apiNetworks {
		if n == "myproject_internal" {
			hasInternalAPI = true
		}
	}

	if !hasInternalWeb {
		t.Error("web service missing myproject_internal network")
	}
	if !hasInternalAPI {
		t.Error("api service missing myproject_internal network")
	}
}

func TestGenerateCompose_PrebuiltImage(t *testing.T) {
	services := map[string]*config.Config{
		"postgres": {
			Name:   "postgres",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Image:  "postgres:16-alpine",
		},
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", 3)
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})

	// Prebuilt service should use exact image (no version suffix)
	postgresService := servicesMap["postgres"].(map[string]interface{})
	if postgresService["image"] != "postgres:16-alpine" {
		t.Errorf("postgres image = %v, want postgres:16-alpine", postgresService["image"])
	}

	// Built service should have version
	webService := servicesMap["web"].(map[string]interface{})
	if webService["image"] != "ssd-myapp-web:3" {
		t.Errorf("web image = %v, want ssd-myapp-web:3", webService["image"])
	}
}

func TestGenerateCompose_WithVolumes(t *testing.T) {
	services := map[string]*config.Config{
		"postgres": {
			Name:   "postgres",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Image:  "postgres:16-alpine",
			Volumes: map[string]string{
				"postgres_data": "/var/lib/postgresql/data",
			},
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", 1)
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	// Verify volumes section exists
	volumesMap, ok := parsed["volumes"].(map[string]interface{})
	if !ok {
		t.Fatal("volumes section missing or not a map")
	}

	if _, ok := volumesMap["postgres_data"]; !ok {
		t.Error("postgres_data volume missing")
	}

	// Verify service has volume mount
	servicesMap := parsed["services"].(map[string]interface{})
	postgresService := servicesMap["postgres"].(map[string]interface{})
	volumeMounts, ok := postgresService["volumes"].([]interface{})
	if !ok {
		t.Fatal("service volumes missing or not an array")
	}

	if len(volumeMounts) != 1 {
		t.Fatalf("volume mounts count = %d, want 1", len(volumeMounts))
	}

	expected := "postgres_data:/var/lib/postgresql/data"
	if volumeMounts[0] != expected {
		t.Errorf("volume mount = %v, want %s", volumeMounts[0], expected)
	}
}

func TestGenerateCompose_EmptyServices(t *testing.T) {
	services := map[string]*config.Config{}

	_, err := GenerateCompose(services, "/stacks/myapp", 1)
	if err == nil {
		t.Fatal("expected error for empty services, got nil")
	}

	if !strings.Contains(err.Error(), "at least one service") {
		t.Errorf("error message = %q, want message about requiring at least one service", err.Error())
	}
}

func TestGenerateCompose_ProjectNameFromStack(t *testing.T) {
	tests := []struct {
		stack   string
		project string
	}{
		{"/stacks/myapp", "myapp"},
		{"/stacks/nested/project", "project"},
		{"/custom/path/to/stack", "stack"},
	}

	for _, tt := range tests {
		services := map[string]*config.Config{
			"web": {
				Name:   "web",
				Server: "myserver",
				Stack:  tt.stack,
			},
		}

		result, err := GenerateCompose(services, tt.stack, 1)
		if err != nil {
			t.Fatalf("GenerateCompose failed for stack %s: %v", tt.stack, err)
		}

		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Generated YAML is invalid: %v", err)
		}

		servicesMap := parsed["services"].(map[string]interface{})
		webService := servicesMap["web"].(map[string]interface{})
		image := webService["image"].(string)

		expectedImage := "ssd-" + tt.project + "-web:1"
		if image != expectedImage {
			t.Errorf("stack %s: image = %q, want %q", tt.stack, image, expectedImage)
		}

		// Verify internal network name
		networksMap := parsed["networks"].(map[string]interface{})
		expectedNetwork := tt.project + "_internal"
		if _, ok := networksMap[expectedNetwork]; !ok {
			t.Errorf("stack %s: network %q missing", tt.stack, expectedNetwork)
		}
	}
}

func TestAtomicWrite_ValidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := tmpDir + "/compose.yaml"

	validYAML := `services:
  web:
    image: nginx:latest
    restart: unless-stopped
networks:
  traefik_web:
    external: true
`

	err := AtomicWrite(validYAML, destPath)
	if err != nil {
		t.Fatalf("AtomicWrite failed: %v", err)
	}

	// Verify file was written
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}

	if string(content) != validYAML {
		t.Errorf("File content doesn't match\nGot:\n%s\nWant:\n%s", string(content), validYAML)
	}

	// Verify temp file was cleaned up
	tmpPath := destPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("Temp file %s still exists", tmpPath)
	}
}

func TestAtomicWrite_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := tmpDir + "/compose.yaml"

	invalidYAML := `services:
  web:
    image: nginx:latest
    restart: [invalid: yaml
`

	err := AtomicWrite(invalidYAML, destPath)
	if err == nil {
		t.Fatal("Expected error for invalid YAML, got nil")
	}

	if !strings.Contains(err.Error(), "invalid YAML") {
		t.Errorf("Error message should mention invalid YAML, got: %v", err)
	}

	// Verify no file was written
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Errorf("Destination file should not exist after failed write")
	}

	// Verify temp file was cleaned up
	tmpPath := destPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("Temp file %s should be cleaned up after error", tmpPath)
	}
}

func TestAtomicWrite_OverwriteExisting(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := tmpDir + "/compose.yaml"

	// Write initial content
	initialYAML := `services:
  old:
    image: old:1
`
	if err := os.WriteFile(destPath, []byte(initialYAML), 0644); err != nil {
		t.Fatalf("Failed to write initial file: %v", err)
	}

	// Overwrite with new content
	newYAML := `services:
  new:
    image: new:2
`
	err := AtomicWrite(newYAML, destPath)
	if err != nil {
		t.Fatalf("AtomicWrite failed: %v", err)
	}

	// Verify new content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(content) != newYAML {
		t.Errorf("File not properly overwritten\nGot:\n%s\nWant:\n%s", string(content), newYAML)
	}
}

func TestAtomicWrite_FailedWritePreservesOriginal(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := tmpDir + "/compose.yaml"

	// Write initial valid content
	originalYAML := `services:
  web:
    image: original:1
`
	if err := os.WriteFile(destPath, []byte(originalYAML), 0644); err != nil {
		t.Fatalf("Failed to write initial file: %v", err)
	}

	// Try to write invalid YAML
	invalidYAML := `invalid: [yaml content`
	err := AtomicWrite(invalidYAML, destPath)
	if err == nil {
		t.Fatal("Expected error for invalid YAML")
	}

	// Verify original file is unchanged
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if string(content) != originalYAML {
		t.Errorf("Original file was modified\nGot:\n%s\nWant:\n%s", string(content), originalYAML)
	}
}
