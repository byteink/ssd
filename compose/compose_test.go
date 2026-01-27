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

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
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

	result, err := GenerateCompose(services, "/stacks/myproject", map[string]int{"web": 5, "api": 5})
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

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"postgres": 3, "web": 3})
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

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"postgres": 1})
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

	_, err := GenerateCompose(services, "/stacks/myapp", map[string]int{})
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

		result, err := GenerateCompose(services, tt.stack, map[string]int{"web": 1})
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

func TestGenerateCompose_WithHealthCheck(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   3000,
			HealthCheck: &config.HealthCheck{
				Cmd:      "curl -f http://localhost:3000/health || exit 1",
				Interval: "30s",
				Timeout:  "10s",
				Retries:  3,
			},
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	// Verify healthcheck block exists
	healthcheck, ok := webService["healthcheck"].(map[string]interface{})
	if !ok {
		t.Fatal("healthcheck missing or not a map")
	}

	// Verify test command
	test, ok := healthcheck["test"].([]interface{})
	if !ok {
		t.Fatal("healthcheck test missing or not an array")
	}
	if len(test) != 4 {
		t.Fatalf("healthcheck test length = %d, want 4", len(test))
	}
	if test[0] != "CMD" || test[1] != "sh" || test[2] != "-c" {
		t.Errorf("healthcheck test format incorrect, got %v", test)
	}
	if test[3] != "curl -f http://localhost:3000/health || exit 1" {
		t.Errorf("healthcheck test cmd = %v, want curl command", test[3])
	}

	// Verify interval
	if healthcheck["interval"] != "30s" {
		t.Errorf("healthcheck interval = %v, want 30s", healthcheck["interval"])
	}

	// Verify timeout
	if healthcheck["timeout"] != "10s" {
		t.Errorf("healthcheck timeout = %v, want 10s", healthcheck["timeout"])
	}

	// Verify retries
	if healthcheck["retries"] != 3 {
		t.Errorf("healthcheck retries = %v, want 3", healthcheck["retries"])
	}
}

func TestGenerateCompose_WithoutHealthCheck(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	// Verify healthcheck block does not exist
	if _, ok := webService["healthcheck"]; ok {
		t.Error("healthcheck should not be present when not configured")
	}
}

func TestGenerateCompose_WithDomainAndHTTPS(t *testing.T) {
	trueVal := true
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domain: "example.com",
			HTTPS:  &trueVal,
			Port:   3000,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	// Verify labels exist
	labels, ok := webService["labels"].([]interface{})
	if !ok {
		t.Fatal("labels missing or not an array")
	}

	labelStrings := make([]string, len(labels))
	for i, label := range labels {
		labelStrings[i] = label.(string)
	}

	// Required labels for HTTPS service
	expectedLabels := []string{
		"traefik.enable=true",
		"traefik.http.routers.myapp-web.rule=Host(`example.com`)",
		"traefik.http.routers.myapp-web.entrypoints=websecure",
		"traefik.http.routers.myapp-web.tls=true",
		"traefik.http.routers.myapp-web.tls.certresolver=letsencrypt",
		"traefik.http.services.myapp-web.loadbalancer.server.port=3000",
		"traefik.http.routers.myapp-web-http.rule=Host(`example.com`)",
		"traefik.http.routers.myapp-web-http.entrypoints=web",
		"traefik.http.routers.myapp-web-http.middlewares=redirect-to-https",
		"traefik.http.middlewares.redirect-to-https.redirectscheme.scheme=https",
	}

	for _, expected := range expectedLabels {
		found := false
		for _, actual := range labelStrings {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected label %q not found", expected)
		}
	}
}

func TestGenerateCompose_WithDomainNoHTTPS(t *testing.T) {
	falseVal := false
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domain: "example.com",
			HTTPS:  &falseVal,
			Port:   8080,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	// Verify labels exist
	labels, ok := webService["labels"].([]interface{})
	if !ok {
		t.Fatal("labels missing or not an array")
	}

	labelStrings := make([]string, len(labels))
	for i, label := range labels {
		labelStrings[i] = label.(string)
	}

	// Required labels for HTTP-only service
	expectedLabels := []string{
		"traefik.enable=true",
		"traefik.http.routers.myapp-web.rule=Host(`example.com`)",
		"traefik.http.routers.myapp-web.entrypoints=web",
		"traefik.http.services.myapp-web.loadbalancer.server.port=8080",
	}

	for _, expected := range expectedLabels {
		found := false
		for _, actual := range labelStrings {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected label %q not found", expected)
		}
	}

	// Verify redirect labels and certresolver are NOT present
	disallowedLabels := []string{
		"traefik.http.routers.myapp-web.tls=true",
		"traefik.http.routers.myapp-web.tls.certresolver=letsencrypt",
		"traefik.http.routers.myapp-web-http.rule=Host(`example.com`)",
		"traefik.http.routers.myapp-web-http.entrypoints=web",
		"traefik.http.routers.myapp-web-http.middlewares=redirect-to-https",
		"traefik.http.middlewares.redirect-to-https.redirectscheme.scheme=https",
	}

	for _, disallowed := range disallowedLabels {
		for _, actual := range labelStrings {
			if actual == disallowed {
				t.Errorf("Unexpected label %q found in HTTP-only config", disallowed)
			}
		}
	}
}

func TestGenerateCompose_WithDomainAndPath_HTTPS(t *testing.T) {
	trueVal := true
	services := map[string]*config.Config{
		"api": {
			Name:   "api",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domain: "example.com",
			Path:   "/api",
			HTTPS:  &trueVal,
			Port:   8080,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"api": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	apiService := servicesMap["api"].(map[string]interface{})

	labels, ok := apiService["labels"].([]interface{})
	if !ok {
		t.Fatal("labels missing or not an array")
	}

	labelStrings := make([]string, len(labels))
	for i, label := range labels {
		labelStrings[i] = label.(string)
	}

	expectedLabels := []string{
		"traefik.enable=true",
		"traefik.http.routers.myapp-api.rule=Host(`example.com`) && PathPrefix(`/api`)",
		"traefik.http.services.myapp-api.loadbalancer.server.port=8080",
		"traefik.http.middlewares.myapp-api-strip.stripprefix.prefixes=/api",
		"traefik.http.routers.myapp-api.middlewares=myapp-api-strip",
		"traefik.http.routers.myapp-api.entrypoints=websecure",
		"traefik.http.routers.myapp-api.tls=true",
		"traefik.http.routers.myapp-api.tls.certresolver=letsencrypt",
		"traefik.http.routers.myapp-api-http.rule=Host(`example.com`) && PathPrefix(`/api`)",
		"traefik.http.routers.myapp-api-http.entrypoints=web",
		"traefik.http.routers.myapp-api-http.middlewares=myapp-api-strip,redirect-to-https",
		"traefik.http.middlewares.redirect-to-https.redirectscheme.scheme=https",
	}

	for _, expected := range expectedLabels {
		found := false
		for _, actual := range labelStrings {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected label %q not found", expected)
		}
	}
}

func TestGenerateCompose_WithDomainAndPath_NoHTTPS(t *testing.T) {
	falseVal := false
	services := map[string]*config.Config{
		"api": {
			Name:   "api",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domain: "example.com",
			Path:   "/api",
			HTTPS:  &falseVal,
			Port:   8080,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"api": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	apiService := servicesMap["api"].(map[string]interface{})

	labels, ok := apiService["labels"].([]interface{})
	if !ok {
		t.Fatal("labels missing or not an array")
	}

	labelStrings := make([]string, len(labels))
	for i, label := range labels {
		labelStrings[i] = label.(string)
	}

	expectedLabels := []string{
		"traefik.enable=true",
		"traefik.http.routers.myapp-api.rule=Host(`example.com`) && PathPrefix(`/api`)",
		"traefik.http.services.myapp-api.loadbalancer.server.port=8080",
		"traefik.http.middlewares.myapp-api-strip.stripprefix.prefixes=/api",
		"traefik.http.routers.myapp-api.middlewares=myapp-api-strip",
		"traefik.http.routers.myapp-api.entrypoints=web",
	}

	for _, expected := range expectedLabels {
		found := false
		for _, actual := range labelStrings {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected label %q not found", expected)
		}
	}

	// No TLS labels should be present
	for _, actual := range labelStrings {
		if strings.Contains(actual, "tls") || strings.Contains(actual, "certresolver") {
			t.Errorf("Unexpected TLS label in HTTP-only path config: %q", actual)
		}
	}
}

func TestGenerateCompose_WithDomainAndRootPath_HTTPS(t *testing.T) {
	trueVal := true
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Domain: "example.com",
			Path:   "/",
			HTTPS:  &trueVal,
			Port:   80,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	labels, ok := webService["labels"].([]interface{})
	if !ok {
		t.Fatal("labels missing or not an array")
	}

	labelStrings := make([]string, len(labels))
	for i, label := range labels {
		labelStrings[i] = label.(string)
	}

	// Root path should use Host-only rule (PathPrefix('/') is redundant)
	expectedLabels := []string{
		"traefik.enable=true",
		"traefik.http.routers.myapp-web.rule=Host(`example.com`)",
		"traefik.http.services.myapp-web.loadbalancer.server.port=80",
		"traefik.http.routers.myapp-web.entrypoints=websecure",
		"traefik.http.routers.myapp-web.tls=true",
		"traefik.http.routers.myapp-web.tls.certresolver=letsencrypt",
		"traefik.http.routers.myapp-web-http.rule=Host(`example.com`)",
		"traefik.http.routers.myapp-web-http.entrypoints=web",
		"traefik.http.routers.myapp-web-http.middlewares=redirect-to-https",
		"traefik.http.middlewares.redirect-to-https.redirectscheme.scheme=https",
	}

	for _, expected := range expectedLabels {
		found := false
		for _, actual := range labelStrings {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected label %q not found", expected)
		}
	}

	// StripPrefix must NOT be present for root path
	for _, actual := range labelStrings {
		if strings.Contains(actual, "stripprefix") {
			t.Errorf("StripPrefix label should not exist for root path, found: %q", actual)
		}
	}
}

func TestGenerateCompose_NoDomain(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	// Verify no labels when domain is not set
	if labels, ok := webService["labels"]; ok {
		t.Errorf("Labels should not exist when domain is not set, but found: %v", labels)
	}
}

func TestGenerateCompose_WithDependsOn(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:      "web",
			Server:    "myserver",
			Stack:     "/stacks/myapp",
			Port:      80,
			DependsOn: []string{"db", "redis"},
		},
		"db": {
			Name:   "db",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Image:  "postgres:16-alpine",
		},
		"redis": {
			Name:   "redis",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Image:  "redis:7-alpine",
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1, "db": 1, "redis": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	// Verify depends_on block exists
	dependsOn, ok := webService["depends_on"].([]interface{})
	if !ok {
		t.Fatal("depends_on missing or not an array")
	}

	if len(dependsOn) != 2 {
		t.Fatalf("depends_on length = %d, want 2", len(dependsOn))
	}

	// Verify dependencies are present
	deps := make(map[string]bool)
	for _, dep := range dependsOn {
		depStr, ok := dep.(string)
		if !ok {
			t.Fatalf("dependency is not a string: %v", dep)
		}
		deps[depStr] = true
	}

	if !deps["db"] {
		t.Error("dependency 'db' not found")
	}
	if !deps["redis"] {
		t.Error("dependency 'redis' not found")
	}

	// Verify other services don't have depends_on
	dbService := servicesMap["db"].(map[string]interface{})
	if _, ok := dbService["depends_on"]; ok {
		t.Error("db service should not have depends_on")
	}

	redisService := servicesMap["redis"].(map[string]interface{})
	if _, ok := redisService["depends_on"]; ok {
		t.Error("redis service should not have depends_on")
	}
}

func TestGenerateCompose_WithoutDependsOn(t *testing.T) {
	services := map[string]*config.Config{
		"web": {
			Name:   "web",
			Server: "myserver",
			Stack:  "/stacks/myapp",
			Port:   80,
		},
	}

	result, err := GenerateCompose(services, "/stacks/myapp", map[string]int{"web": 1})
	if err != nil {
		t.Fatalf("GenerateCompose failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v", err)
	}

	servicesMap := parsed["services"].(map[string]interface{})
	webService := servicesMap["web"].(map[string]interface{})

	// Verify depends_on does not exist
	if _, ok := webService["depends_on"]; ok {
		t.Error("depends_on should not be present when not configured")
	}
}

func TestGenerateTraefikCompose(t *testing.T) {
	email := "admin@example.com"
	result := GenerateTraefikCompose(email)

	// Verify valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("Generated YAML is invalid: %v\nYAML:\n%s", err, result)
	}

	// Verify services section
	servicesMap, ok := parsed["services"].(map[string]interface{})
	if !ok {
		t.Fatal("services section missing or not a map")
	}

	// Verify traefik service exists
	traefikService, ok := servicesMap["traefik"].(map[string]interface{})
	if !ok {
		t.Fatal("traefik service missing")
	}

	// Verify image
	image, ok := traefikService["image"].(string)
	if !ok {
		t.Fatal("image missing or not a string")
	}
	if image != "traefik:3" {
		t.Errorf("image = %q, want traefik:3", image)
	}

	// Verify restart policy
	if restart := traefikService["restart"]; restart != "unless-stopped" {
		t.Errorf("restart = %v, want unless-stopped", restart)
	}

	// Verify ports
	ports, ok := traefikService["ports"].([]interface{})
	if !ok {
		t.Fatal("ports missing or not an array")
	}
	if len(ports) != 2 {
		t.Fatalf("ports count = %d, want 2", len(ports))
	}

	hasPort80 := false
	hasPort443 := false
	for _, p := range ports {
		portStr, ok := p.(string)
		if !ok {
			continue
		}
		if portStr == "80:80" {
			hasPort80 = true
		}
		if portStr == "443:443" {
			hasPort443 = true
		}
	}
	if !hasPort80 {
		t.Error("port 80:80 missing")
	}
	if !hasPort443 {
		t.Error("port 443:443 missing")
	}

	// Verify command contains email
	command, ok := traefikService["command"].([]interface{})
	if !ok {
		t.Fatal("command missing or not an array")
	}

	hasEmail := false
	for _, cmd := range command {
		cmdStr, ok := cmd.(string)
		if !ok {
			continue
		}
		if strings.Contains(cmdStr, email) {
			hasEmail = true
			break
		}
	}
	if !hasEmail {
		t.Errorf("email %q not found in command", email)
	}

	// Verify certresolver is mentioned
	hasCertResolver := false
	for _, cmd := range command {
		cmdStr, ok := cmd.(string)
		if !ok {
			continue
		}
		if strings.Contains(cmdStr, "letsencrypt") {
			hasCertResolver = true
			break
		}
	}
	if !hasCertResolver {
		t.Error("certresolver 'letsencrypt' not found in command")
	}

	// Verify volumes
	volumes, ok := traefikService["volumes"].([]interface{})
	if !ok {
		t.Fatal("volumes missing or not an array")
	}

	hasDockerSocket := false
	hasAcmeJson := false
	for _, v := range volumes {
		volStr, ok := v.(string)
		if !ok {
			continue
		}
		if volStr == "/var/run/docker.sock:/var/run/docker.sock:ro" {
			hasDockerSocket = true
		}
		if strings.Contains(volStr, "acme.json") {
			hasAcmeJson = true
		}
	}
	if !hasDockerSocket {
		t.Error("docker socket volume missing")
	}
	if !hasAcmeJson {
		t.Error("acme.json volume missing")
	}

	// Verify networks
	networks, ok := traefikService["networks"].([]interface{})
	if !ok {
		t.Fatal("networks missing or not an array")
	}
	if len(networks) != 1 {
		t.Fatalf("networks count = %d, want 1", len(networks))
	}
	if networks[0] != "traefik_web" {
		t.Errorf("network = %v, want traefik_web", networks[0])
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
	if traefikNet["driver"] != "bridge" {
		t.Error("traefik_web network should use bridge driver")
	}

	// Verify top-level volumes section for acme.json
	volumesMap, ok := parsed["volumes"].(map[string]interface{})
	if !ok {
		t.Fatal("volumes section missing or not a map")
	}

	if _, ok := volumesMap["acme"]; !ok {
		t.Error("acme volume missing in top-level volumes")
	}
}
