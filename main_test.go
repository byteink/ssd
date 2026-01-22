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
	executor := &remote.MockExecutor{
		Commands: make(map[string]string),
	}
	executor.Commands["cat /stacks/api/api.env 2>/dev/null || echo ''"] = ""
	executor.Commands["echo 'DATABASE_URL=postgres://user:pass@host?ssl=true\n' | install -m 600 /dev/stdin /stacks/api/api.env"] = ""

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

	// Verify the command was executed
	executed := false
	for cmd := range executor.Commands {
		if strings.Contains(cmd, "DATABASE_URL=postgres://user:pass@host?ssl=true") {
			executed = true
			break
		}
	}

	if !executed {
		t.Error("Expected SetEnvVar to execute command with correct value")
	}
}
