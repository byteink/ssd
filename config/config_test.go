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
	assert.Len(t, cfg.Services, 1)
	assert.Contains(t, cfg.Services, "myapp")
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
	assert.Contains(t, err.Error(), "failed to parse config")
}

func TestLoad_DefaultPath(t *testing.T) {
	// Create a temp directory with ssd.yaml
	tmpDir, err := os.MkdirTemp("", "ssd-test-*")
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir: %v", err)
		}
	})

	// Write a test config
	configContent := "server: tempserver\nservices:\n  tempapp:\n    name: tempapp"
	err = os.WriteFile(filepath.Join(tmpDir, "ssd.yaml"), []byte(configContent), 0644)
	require.NoError(t, err)

	// Change to temp dir
	oldDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Logf("failed to restore working directory: %v", err)
		}
	})

	// Load with empty path (should use default "ssd.yaml")
	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "tempserver", cfg.Server)
	assert.Contains(t, cfg.Services, "tempapp")
}

func TestRootConfig_GetService_WithService(t *testing.T) {
	cfg := &RootConfig{
		Server: "myserver",
		Services: map[string]*Config{
			"myapp": {Name: "myapp"},
		},
	}

	svc, err := cfg.GetService("myapp")
	require.NoError(t, err)

	assert.Equal(t, "myapp", svc.Name)
	assert.Equal(t, "myserver", svc.Server)
	assert.Equal(t, "/stacks/myapp", svc.Stack) // Default applied
	assert.Equal(t, "./Dockerfile", svc.Dockerfile)
	assert.Equal(t, ".", svc.Context)
}

func TestRootConfig_GetService_NoServices(t *testing.T) {
	cfg := &RootConfig{
		Server: "myserver",
	}

	_, err := cfg.GetService("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services: is required")
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

func TestRootConfig_ListServices_NoServices(t *testing.T) {
	cfg := &RootConfig{
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
			name:     "has services",
			cfg:      &RootConfig{Services: map[string]*Config{"web": {}}},
			expected: false,
		},
		{
			name:     "empty services map",
			cfg:      &RootConfig{Services: map[string]*Config{}},
			expected: true,
		},
		{
			name:     "no services",
			cfg:      &RootConfig{Server: "server"},
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
	oldDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Logf("failed to restore working directory: %v", err)
		}
	})

	// Create temp dir with known name
	tmpDir, err := os.MkdirTemp("", "testproject")
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("failed to remove temp dir: %v", err)
		}
	})
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

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
	result, err := applyDefaults(cfg, "web")
	require.NoError(t, err)

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

	result, err := applyDefaults(cfg, "ignored-service-name")
	require.NoError(t, err)

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
		cfg      *Config
		expected string
	}{
		{
			name:     "pre-built image",
			cfg:      &Config{Image: "postgres:16"},
			expected: "postgres:16",
		},
		{
			name:     "monorepo service",
			cfg:      &Config{Stack: "/stacks/myproject", Name: "api"},
			expected: "ssd-myproject-api",
		},
		{
			name:     "simple service",
			cfg:      &Config{Stack: "/stacks/webapp", Name: "webapp"},
			expected: "ssd-webapp-webapp",
		},
		{
			name:     "nested stack path",
			cfg:      &Config{Stack: "/var/stacks/project-x", Name: "web"},
			expected: "ssd-project-x-web",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cfg.ImageName())
		})
	}
}

func TestRootConfig_GetService_InvalidName(t *testing.T) {
	tests := []struct {
		name        string
		config      *RootConfig
		serviceName string
		expectError string
	}{
		{
			name: "service with invalid name characters",
			config: &RootConfig{
				Server: "myserver",
				Services: map[string]*Config{
					"app": {Name: "my;app"},
				},
			},
			serviceName: "app",
			expectError: "invalid service name",
		},
		{
			name: "service with invalid name",
			config: &RootConfig{
				Server: "myserver",
				Services: map[string]*Config{
					"web": {Name: "bad|name"},
				},
			},
			serviceName: "web",
			expectError: "invalid service name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.config.GetService(tt.serviceName)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

func TestRootConfig_GetService_InvalidServer(t *testing.T) {
	tests := []struct {
		name        string
		config      *RootConfig
		serviceName string
		expectError string
	}{
		{
			name: "service with inherited invalid server",
			config: &RootConfig{
				Server: "bad|server",
				Services: map[string]*Config{
					"web": {Name: "web-svc"},
				},
			},
			serviceName: "web",
			expectError: "invalid server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.config.GetService(tt.serviceName)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

func TestRootConfig_GetService_InvalidStackPath(t *testing.T) {
	tests := []struct {
		name        string
		config      *RootConfig
		serviceName string
		expectError string
	}{
		{
			name: "service with invalid stack path",
			config: &RootConfig{
				Server: "myserver",
				Services: map[string]*Config{
					"web": {
						Name:  "web-svc",
						Stack: "relative/stack",
					},
				},
			},
			serviceName: "web",
			expectError: "invalid stack path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.config.GetService(tt.serviceName)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

func TestApplyDefaults_InvalidStackPath(t *testing.T) {
	cfg := &Config{
		Name:   "myapp",
		Server: "myserver",
		Stack:  "relative/path",
	}

	_, err := applyDefaults(cfg, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stack path")
}

func TestLoadFromBytes_NoServices(t *testing.T) {
	yamlData := []byte(`server: myserver`)
	cfg, err := LoadFromBytes(yamlData)
	require.NoError(t, err)

	_, err = cfg.GetService("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "services: is required")
}

func TestRootConfig_GetService_EmptyServiceNameWithServices(t *testing.T) {
	cfg := &RootConfig{
		Server: "myserver",
		Services: map[string]*Config{
			"web": {Name: "web-svc"},
		},
	}

	_, err := cfg.GetService("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service name required")
}

func TestApplyDefaults_PortDefault(t *testing.T) {
	// Port not set → should default to 80
	cfg := &Config{
		Name:   "myapp",
		Server: "myserver",
	}

	result, err := applyDefaults(cfg, "")
	require.NoError(t, err)

	assert.Equal(t, 80, result.Port)
}

func TestApplyDefaults_PortPreserved(t *testing.T) {
	// Port explicitly set → should preserve it
	cfg := &Config{
		Name:   "myapp",
		Server: "myserver",
		Port:   3000,
	}

	result, err := applyDefaults(cfg, "")
	require.NoError(t, err)

	assert.Equal(t, 3000, result.Port)
}

func TestLoadFromBytes_DependsOn(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected []string
	}{
		{
			name: "single dependency",
			yaml: `server: myserver
services:
  web:
    name: web
    depends_on: [db]`,
			expected: []string{"db"},
		},
		{
			name: "multiple dependencies",
			yaml: `server: myserver
services:
  web:
    name: web
    depends_on: [db, redis]`,
			expected: []string{"db", "redis"},
		},
		{
			name: "empty depends_on",
			yaml: `server: myserver
services:
  web:
    name: web
    depends_on: []`,
			expected: []string{},
		},
		{
			name: "no depends_on field",
			yaml: `server: myserver
services:
  web:
    name: web`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFromBytes([]byte(tt.yaml))
			require.NoError(t, err)

			svc, err := cfg.GetService("web")
			require.NoError(t, err)

			if tt.expected == nil {
				assert.Nil(t, svc.DependsOn)
			} else {
				assert.Equal(t, tt.expected, svc.DependsOn)
			}
		})
	}
}

func TestLoadFromBytes_Volumes(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected map[string]string
	}{
		{
			name: "single volume",
			yaml: `server: myserver
services:
  db:
    name: db
    volumes:
      data: /var/lib/postgresql/data`,
			expected: map[string]string{"data": "/var/lib/postgresql/data"},
		},
		{
			name: "multiple volumes",
			yaml: `server: myserver
services:
  db:
    name: db
    volumes:
      data: /var/lib/postgresql/data
      config: /etc/postgresql`,
			expected: map[string]string{
				"data":   "/var/lib/postgresql/data",
				"config": "/etc/postgresql",
			},
		},
		{
			name: "empty volumes",
			yaml: `server: myserver
services:
  db:
    name: db
    volumes: {}`,
			expected: map[string]string{},
		},
		{
			name: "no volumes field",
			yaml: `server: myserver
services:
  db:
    name: db`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFromBytes([]byte(tt.yaml))
			require.NoError(t, err)

			svc, err := cfg.GetService("db")
			require.NoError(t, err)

			if tt.expected == nil {
				assert.Nil(t, svc.Volumes)
			} else {
				assert.Equal(t, tt.expected, svc.Volumes)
			}
		})
	}
}

func TestConfig_IsPrebuilt(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *Config
		expected bool
	}{
		{
			name:     "empty image",
			cfg:      &Config{Image: ""},
			expected: false,
		},
		{
			name:     "prebuilt image",
			cfg:      &Config{Image: "postgres:16"},
			expected: true,
		},
		{
			name:     "nil config with empty image",
			cfg:      &Config{},
			expected: false,
		},
		{
			name:     "custom prebuilt image",
			cfg:      &Config{Image: "myregistry.com/myapp:latest"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cfg.IsPrebuilt())
		})
	}
}

func TestLoadFromBytes_HealthCheck(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected *HealthCheck
	}{
		{
			name: "full healthcheck",
			yaml: `server: myserver
services:
  web:
    name: web
    healthcheck:
      cmd: "curl -f http://localhost:8080/health || exit 1"
      interval: "30s"
      timeout: "10s"
      retries: 3`,
			expected: &HealthCheck{
				Cmd:      "curl -f http://localhost:8080/health || exit 1",
				Interval: "30s",
				Timeout:  "10s",
				Retries:  3,
			},
		},
		{
			name: "minimal healthcheck",
			yaml: `server: myserver
services:
  web:
    name: web
    healthcheck:
      cmd: "exit 0"`,
			expected: &HealthCheck{
				Cmd: "exit 0",
			},
		},
		{
			name: "no healthcheck field",
			yaml: `server: myserver
services:
  web:
    name: web`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFromBytes([]byte(tt.yaml))
			require.NoError(t, err)

			svc, err := cfg.GetService("web")
			require.NoError(t, err)

			if tt.expected == nil {
				assert.Nil(t, svc.HealthCheck)
			} else {
				require.NotNil(t, svc.HealthCheck)
				assert.Equal(t, tt.expected.Cmd, svc.HealthCheck.Cmd)
				assert.Equal(t, tt.expected.Interval, svc.HealthCheck.Interval)
				assert.Equal(t, tt.expected.Timeout, svc.HealthCheck.Timeout)
				assert.Equal(t, tt.expected.Retries, svc.HealthCheck.Retries)
			}
		})
	}
}

func TestValidateDomain(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		wantErr bool
	}{
		{
			name:    "empty string",
			domain:  "",
			wantErr: true,
		},
		{
			name:    "http protocol prefix",
			domain:  "http://example.com",
			wantErr: true,
		},
		{
			name:    "https protocol prefix",
			domain:  "https://x.com",
			wantErr: true,
		},
		{
			name:    "with path",
			domain:  "x.com/path",
			wantErr: true,
		},
		{
			name:    "with port",
			domain:  "x.com:8080",
			wantErr: true,
		},
		{
			name:    "with space",
			domain:  "x.com bad",
			wantErr: true,
		},
		{
			name:    "with semicolon",
			domain:  "x.com;rm",
			wantErr: true,
		},
		{
			name:    "with pipe",
			domain:  "x.com|ls",
			wantErr: true,
		},
		{
			name:    "with ampersand",
			domain:  "x.com&pwd",
			wantErr: true,
		},
		{
			name:    "with backtick",
			domain:  "x.com`id`",
			wantErr: true,
		},
		{
			name:    "with dollar sign",
			domain:  "x.com$(date)",
			wantErr: true,
		},
		{
			name:    "exceeds max length",
			domain:  "a" + string(make([]byte, 253)) + ".com",
			wantErr: true,
		},
		{
			name:    "valid simple domain",
			domain:  "example.com",
			wantErr: false,
		},
		{
			name:    "valid subdomain",
			domain:  "api.example.com",
			wantErr: false,
		},
		{
			name:    "valid multi-level subdomain",
			domain:  "api.staging.example.com",
			wantErr: false,
		},
		{
			name:    "valid with hyphen",
			domain:  "my-app.example.com",
			wantErr: false,
		},
		{
			name:    "valid short domain",
			domain:  "x.co",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDomain(tt.domain)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
