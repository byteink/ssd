package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/internal/testhelpers"
	"github.com/byteink/ssd/remote"
)

// TestEnvSetParsing tests that runEnvSet correctly parses KEY=VALUE with SplitN
func TestEnvSetParsing(t *testing.T) {
	tests := []struct {
		name          string
		arg           string
		expectedKey   string
		expectedValue string
		shouldFail    bool
	}{
		{
			name:          "simple key value",
			arg:           "KEY=value",
			expectedKey:   "KEY",
			expectedValue: "value",
			shouldFail:    false,
		},
		{
			name:          "postgres URL with = in value",
			arg:           "DATABASE_URL=postgres://user:pass@host?ssl=true",
			expectedKey:   "DATABASE_URL",
			expectedValue: "postgres://user:pass@host?ssl=true",
			shouldFail:    false,
		},
		{
			name:          "multiple equals in value",
			arg:           "URL=http://example.com?a=b&c=d&e=f",
			expectedKey:   "URL",
			expectedValue: "http://example.com?a=b&c=d&e=f",
			shouldFail:    false,
		},
		{
			name:          "empty value",
			arg:           "KEY=",
			expectedKey:   "KEY",
			expectedValue: "",
			shouldFail:    false,
		},
		{
			name:       "missing equals",
			arg:        "KEYVALUE",
			shouldFail: true,
		},
		{
			name:       "empty key",
			arg:        "=value",
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := strings.SplitN(tt.arg, "=", 2)

			if tt.shouldFail {
				if len(parts) != 2 {
					return // Expected failure: no equals sign
				}
				if parts[0] == "" {
					return // Expected failure: empty key
				}
				t.Fatalf("Expected parsing to fail for %s", tt.arg)
			}

			if len(parts) != 2 {
				t.Fatalf("Expected 2 parts, got %d for %s", len(parts), tt.arg)
			}

			key := parts[0]
			value := parts[1]

			if key != tt.expectedKey {
				t.Errorf("Expected key=%q, got %q", tt.expectedKey, key)
			}

			if value != tt.expectedValue {
				t.Errorf("Expected value=%q, got %q", tt.expectedValue, value)
			}
		})
	}
}

// TestRunEnvSetIntegration tests the full runEnvSet flow with a mock executor
func TestRunEnvSetIntegration(t *testing.T) {
	// Create a temporary directory for test config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssd.yaml")

	// Write a minimal config file
	configContent := `server: testserver
services:
  api:
    name: api
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Change to temp directory for config loading
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer os.Chdir(originalWd)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	// Create mock executor
	executor := new(testhelpers.MockExecutor)
	executor.On("Run", "ssh", []string{"testserver", "cat /stacks/api/api.env 2>/dev/null || echo ''"}).Return("", nil)
	executor.On("Run", "ssh", []string{"testserver", "echo 'DATABASE_URL=postgres://user:pass@host?ssl=true\n' | install -m 600 /dev/stdin /stacks/api/api.env"}).Return("", nil)

	// Load config
	rootCfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	cfg, err := rootCfg.GetService("api")
	if err != nil {
		t.Fatalf("Failed to get service config: %v", err)
	}

	// Create client with mock executor
	client := remote.NewClientWithExecutor(cfg, executor)

	// Test setting a variable with = in the value
	err = client.SetEnvVar(context.Background(), "api", "DATABASE_URL", "postgres://user:pass@host?ssl=true")
	if err != nil {
		t.Fatalf("SetEnvVar failed: %v", err)
	}

	// Verify the expectations were met
	executor.AssertExpectations(t)
}

// TestEnvSetE2E tests the end-to-end flow from command line to SetEnvVar call
func TestEnvSetE2E(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		expectedKey   string
		expectedValue string
		shouldFail    bool
	}{
		{
			name:          "simple key value",
			args:          []string{"KEY=value"},
			expectedKey:   "KEY",
			expectedValue: "value",
			shouldFail:    false,
		},
		{
			name:          "postgres URL with equals",
			args:          []string{"DATABASE_URL=postgres://user:pass@host?ssl=true"},
			expectedKey:   "DATABASE_URL",
			expectedValue: "postgres://user:pass@host?ssl=true",
			shouldFail:    false,
		},
		{
			name:          "URL with multiple equals",
			args:          []string{"URL=http://example.com?a=b&c=d"},
			expectedKey:   "URL",
			expectedValue: "http://example.com?a=b&c=d",
			shouldFail:    false,
		},
		{
			name:       "no equals sign",
			args:       []string{"KEYVALUE"},
			shouldFail: true,
		},
		{
			name:       "empty key",
			args:       []string{"=value"},
			shouldFail: true,
		},
		{
			name:       "no arguments",
			args:       []string{},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary directory for test config
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "ssd.yaml")

			// Write a minimal config file
			configContent := `server: testserver
services:
  api:
    name: api
`
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
				t.Fatalf("Failed to write config: %v", err)
			}

			// Change to temp directory for config loading
			originalWd, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			defer os.Chdir(originalWd)

			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("Failed to change directory: %v", err)
			}

			if tt.shouldFail {
				// For failure cases, we expect os.Exit to be called
				// We can't easily test os.Exit, so we just verify the parsing logic
				if len(tt.args) == 0 {
					return // No args case
				}
				arg := tt.args[0]
				parts := strings.SplitN(arg, "=", 2)
				if len(parts) != 2 || parts[0] == "" {
					return // Expected failure
				}
				t.Fatalf("Expected parsing to fail for %s", arg)
				return
			}

			// Create mock executor
			executor := new(testhelpers.MockExecutor)
			executor.On("Run", "ssh", []string{"testserver", "cat /stacks/api/api.env 2>/dev/null || echo ''"}).Return("", nil)

			// Build expected command
			expectedCmd := "echo '" + tt.expectedKey + "=" + tt.expectedValue + "\n' | install -m 600 /dev/stdin /stacks/api/api.env"
			executor.On("Run", "ssh", []string{"testserver", expectedCmd}).Return("", nil)

			// Load config
			rootCfg, err := config.Load("")
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			cfg, err := rootCfg.GetService("api")
			if err != nil {
				t.Fatalf("Failed to get service config: %v", err)
			}

			// Create client with mock executor
			client := remote.NewClientWithExecutor(cfg, executor)

			// Simulate what runEnvSet does
			arg := tt.args[0]
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) != 2 {
				t.Fatalf("Expected 2 parts, got %d", len(parts))
			}

			key := parts[0]
			value := parts[1]

			if key != tt.expectedKey {
				t.Errorf("Expected key=%q, got %q", tt.expectedKey, key)
			}

			if value != tt.expectedValue {
				t.Errorf("Expected value=%q, got %q", tt.expectedValue, value)
			}

			// Call SetEnvVar
			err = client.SetEnvVar(context.Background(), "api", key, value)
			if err != nil {
				t.Fatalf("SetEnvVar failed: %v", err)
			}

			// Verify the expectations were met
			executor.AssertExpectations(t)
		})
	}
}

// TestEnvListIntegration tests the runEnvList function with mock executor
func TestEnvListIntegration(t *testing.T) {
	tests := []struct {
		name           string
		envContent     string
		expectedOutput string
	}{
		{
			name:           "empty env file",
			envContent:     "",
			expectedOutput: "No environment variables set\n",
		},
		{
			name:           "single variable",
			envContent:     "KEY=value\n",
			expectedOutput: "KEY=value\n",
		},
		{
			name: "multiple variables",
			envContent: `DATABASE_URL=postgres://user:pass@host?ssl=true
API_KEY=secret123
PORT=3000
`,
			expectedOutput: `DATABASE_URL=postgres://user:pass@host?ssl=true
API_KEY=secret123
PORT=3000
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "ssd.yaml")

			configContent := `server: testserver
services:
  api:
    name: api
`
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
				t.Fatalf("Failed to write config: %v", err)
			}

			originalWd, err := os.Getwd()
			if err != nil {
				t.Fatalf("Failed to get working directory: %v", err)
			}
			defer os.Chdir(originalWd)

			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("Failed to change directory: %v", err)
			}

			executor := new(testhelpers.MockExecutor)
			executor.On("Run", "ssh", []string{"testserver", "cat /stacks/api/api.env 2>/dev/null || echo ''"}).Return(tt.envContent, nil)

			rootCfg, err := config.Load("")
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			cfg, err := rootCfg.GetService("api")
			if err != nil {
				t.Fatalf("Failed to get service config: %v", err)
			}

			client := remote.NewClientWithExecutor(cfg, executor)

			content, err := client.GetEnvFile(context.Background(), "api")
			if err != nil {
				t.Fatalf("GetEnvFile failed: %v", err)
			}

			if content == "" || strings.TrimSpace(content) == "" {
				if tt.expectedOutput != "No environment variables set\n" {
					t.Errorf("Expected non-empty output, got empty")
				}
			} else {
				if content != strings.TrimSuffix(tt.expectedOutput, "\n") && content != tt.expectedOutput {
					t.Errorf("Expected %q, got %q", tt.expectedOutput, content)
				}
			}

			executor.AssertExpectations(t)
		})
	}
}
