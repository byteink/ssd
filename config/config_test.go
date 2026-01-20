package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testdataPath(filename string) string {
	return filepath.Join("..", "testdata", filename)
}

func TestLoad_SimpleConfig(t *testing.T) {
	cfg, err := Load(testdataPath("simple.ssd.yaml"))
	require.NoError(t, err)

	assert.Equal(t, "testserver", cfg.Server)
	assert.Equal(t, "myapp", cfg.Name)
	assert.Empty(t, cfg.Services)
}

func TestLoad_MonorepoConfig(t *testing.T) {
	cfg, err := Load(testdataPath("monorepo.ssd.yaml"))
	require.NoError(t, err)

	assert.Equal(t, "myserver", cfg.Server)
	assert.Equal(t, "/stacks/myproject", cfg.Stack)
	assert.Len(t, cfg.Services, 2)
	assert.Contains(t, cfg.Services, "web")
	assert.Contains(t, cfg.Services, "api")

	// Check service details
	assert.Equal(t, "myproject-web", cfg.Services["web"].Name)
	assert.Equal(t, "./apps/web", cfg.Services["web"].Context)
	assert.Equal(t, "myproject-api", cfg.Services["api"].Name)
	assert.Equal(t, "./apps/api", cfg.Services["api"].Context)
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("nonexistent-file.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestLoad_InvalidYAML(t *testing.T) {
	_, err := Load(testdataPath("invalid.ssd.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse config file")
}

func TestLoad_DefaultPath(t *testing.T) {
	// Create a temp directory with ssd.yaml
	tmpDir, err := os.MkdirTemp("", "ssd-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Write a test config
	configContent := "server: tempserver\nname: tempapp"
	err = os.WriteFile(filepath.Join(tmpDir, "ssd.yaml"), []byte(configContent), 0644)
	require.NoError(t, err)

	// Change to temp dir
	oldDir, _ := os.Getwd()
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	defer os.Chdir(oldDir)

	// Load with empty path (should use default "ssd.yaml")
	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "tempserver", cfg.Server)
	assert.Equal(t, "tempapp", cfg.Name)
}

func TestRootConfig_GetService_SingleService(t *testing.T) {
	cfg := &RootConfig{
		Name:   "myapp",
		Server: "myserver",
	}

	svc, err := cfg.GetService("")
	require.NoError(t, err)

	assert.Equal(t, "myapp", svc.Name)
	assert.Equal(t, "myserver", svc.Server)
	assert.Equal(t, "/stacks/myapp", svc.Stack) // Default applied
	assert.Equal(t, "./Dockerfile", svc.Dockerfile)
	assert.Equal(t, ".", svc.Context)
}

func TestRootConfig_GetService_SingleServiceMissingServer(t *testing.T) {
	cfg := &RootConfig{
		Name: "myapp",
		// Missing Server
	}

	_, err := cfg.GetService("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server is required")
}

func TestRootConfig_GetService_MultiService(t *testing.T) {
	cfg := &RootConfig{
		Server: "shared-server",
		Stack:  "/stacks/project",
		Services: map[string]*Config{
			"web": {Name: "web-svc", Context: "./web"},
			"api": {Name: "api-svc", Context: "./api"},
		},
	}

	svc, err := cfg.GetService("web")
	require.NoError(t, err)

	assert.Equal(t, "web-svc", svc.Name)
	assert.Equal(t, "shared-server", svc.Server) // Inherited
	assert.Equal(t, "/stacks/project", svc.Stack) // Inherited
	assert.Equal(t, "./web", svc.Context)
}

func TestRootConfig_GetService_MultiServiceInheritance(t *testing.T) {
	cfg := &RootConfig{
		Server: "default-server",
		Stack:  "/stacks/default",
		Services: map[string]*Config{
			"web": {
				Name:   "web-svc",
				Server: "custom-server", // Overrides root
				// Stack not set - should inherit
			},
		},
	}

	svc, err := cfg.GetService("web")
	require.NoError(t, err)

	assert.Equal(t, "custom-server", svc.Server) // Uses custom
	assert.Equal(t, "/stacks/default", svc.Stack) // Inherited from root
}

func TestRootConfig_GetService_MultiServiceMissingName(t *testing.T) {
	cfg := &RootConfig{
		Services: map[string]*Config{
			"web": {Name: "web-svc"},
		},
	}

	_, err := cfg.GetService("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service name required")
}

func TestRootConfig_GetService_ServiceNotFound(t *testing.T) {
	cfg := &RootConfig{
		Services: map[string]*Config{
			"web": {Name: "web-svc"},
		},
	}

	_, err := cfg.GetService("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRootConfig_ListServices(t *testing.T) {
	cfg := &RootConfig{
		Services: map[string]*Config{
			"web": {},
			"api": {},
			"worker": {},
		},
	}

	services := cfg.ListServices()
	assert.Len(t, services, 3)
	assert.Contains(t, services, "web")
	assert.Contains(t, services, "api")
	assert.Contains(t, services, "worker")
}

func TestRootConfig_ListServices_SingleService(t *testing.T) {
	cfg := &RootConfig{
		Name:   "app",
		Server: "server",
	}

	services := cfg.ListServices()
	assert.Nil(t, services)
}

func TestRootConfig_ListServices_EmptyMap(t *testing.T) {
	cfg := &RootConfig{
		Services: map[string]*Config{},
	}

	services := cfg.ListServices()
	assert.Nil(t, services)
}

func TestRootConfig_IsSingleService(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *RootConfig
		expected bool
	}{
		{
			name:     "single service",
			cfg:      &RootConfig{Name: "app", Server: "server"},
			expected: true,
		},
		{
			name:     "multi service",
			cfg:      &RootConfig{Services: map[string]*Config{"web": {}}},
			expected: false,
		},
		{
			name:     "empty services map",
			cfg:      &RootConfig{Services: map[string]*Config{}},
			expected: true,
		},
		{
			name:     "nil config",
			cfg:      &RootConfig{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cfg.IsSingleService())
		})
	}
}

func TestApplyDefaults_AllDefaults(t *testing.T) {
	// Save and restore working directory
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)

	// Create temp dir with known name
	tmpDir, _ := os.MkdirTemp("", "testproject")
	defer os.RemoveAll(tmpDir)
	os.Chdir(tmpDir)

	cfg := &Config{Server: "myserver"}
	result, err := applyDefaults(cfg, "")
	require.NoError(t, err)

	assert.Equal(t, "myserver", result.Server)
	assert.Equal(t, "./Dockerfile", result.Dockerfile)
	assert.Equal(t, ".", result.Context)
	// Name defaults to directory name
	assert.NotEmpty(t, result.Name)
	// Stack defaults to /stacks/{name}
	assert.Equal(t, filepath.Join("/stacks", result.Name), result.Stack)
}

func TestApplyDefaults_WithServiceName(t *testing.T) {
	cfg := &Config{Server: "myserver"}
	result := applyDefaults(cfg, "web")

	assert.Equal(t, "web", result.Name)
	assert.Equal(t, "/stacks/web", result.Stack)
}

func TestApplyDefaults_PreservesExistingValues(t *testing.T) {
	cfg := &Config{
		Name:       "custom-name",
		Server:     "myserver",
		Stack:      "/custom/stack/path",
		Dockerfile: "docker/Dockerfile.prod",
		Context:    "./src",
	}

	result := applyDefaults(cfg, "ignored-service-name")

	assert.Equal(t, "custom-name", result.Name)         // Not overwritten
	assert.Equal(t, "/custom/stack/path", result.Stack) // Not overwritten
	assert.Equal(t, "docker/Dockerfile.prod", result.Dockerfile)
	assert.Equal(t, "./src", result.Context)
}

func TestConfig_StackPath(t *testing.T) {
	cfg := &Config{Stack: "/stacks/myapp"}
	assert.Equal(t, "/stacks/myapp", cfg.StackPath())
}

func TestConfig_StackPath_Empty(t *testing.T) {
	cfg := &Config{}
	assert.Equal(t, "", cfg.StackPath())
}

func TestConfig_ImageName(t *testing.T) {
	tests := []struct {
		name     string
		cfgName  string
		expected string
	}{
		{"simple", "myapp", "ssd-myapp"},
		{"with hyphens", "my-app", "ssd-my-app"},
		{"complex", "project-web-api", "ssd-project-web-api"},
		{"underscore", "my_app", "ssd-my_app"},
		{"numbers", "app123", "ssd-app123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Name: tt.cfgName}
			assert.Equal(t, tt.expected, cfg.ImageName())
		})
	}
}
