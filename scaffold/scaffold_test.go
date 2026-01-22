package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		name     string
		opts     Options
		expected string
	}{
		{
			name: "minimal config",
			opts: Options{
				Server: "myserver",
			},
			expected: `server: myserver

services:
  app:
    # Uncomment and configure as needed:
    # domain: example.com
    # port: 3000
`,
		},
		{
			name: "with stack",
			opts: Options{
				Server: "myserver",
				Stack:  "/dockge/stacks/myapp",
			},
			expected: `server: myserver
stack: /dockge/stacks/myapp

services:
  app:
    # Uncomment and configure as needed:
    # domain: example.com
    # port: 3000
`,
		},
		{
			name: "with domain",
			opts: Options{
				Server: "myserver",
				Domain: "myapp.example.com",
			},
			expected: `server: myserver

services:
  app:
    domain: myapp.example.com
    # Uncomment and configure as needed:
    # port: 3000
`,
		},
		{
			name: "with port",
			opts: Options{
				Server: "myserver",
				Port:   8080,
			},
			expected: `server: myserver

services:
  app:
    port: 8080
    # Uncomment and configure as needed:
    # domain: example.com
`,
		},
		{
			name: "with service name",
			opts: Options{
				Server:  "myserver",
				Service: "web",
			},
			expected: `server: myserver

services:
  web:
    # Uncomment and configure as needed:
    # domain: example.com
    # port: 3000
`,
		},
		{
			name: "full config",
			opts: Options{
				Server:  "prod-server",
				Stack:   "/dockge/stacks/myapp-prod",
				Service: "api",
				Domain:  "api.example.com",
				Port:    3000,
			},
			expected: `server: prod-server
stack: /dockge/stacks/myapp-prod

services:
  api:
    domain: api.example.com
    port: 3000
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Generate(tt.opts)
			if result != tt.expected {
				t.Errorf("Generate() =\n%s\nwant:\n%s", result, tt.expected)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{
			name:    "missing server",
			opts:    Options{},
			wantErr: "server is required",
		},
		{
			name: "valid minimal",
			opts: Options{
				Server: "myserver",
			},
			wantErr: "",
		},
		{
			name: "invalid port zero",
			opts: Options{
				Server: "myserver",
				Port:   0,
			},
			wantErr: "", // 0 means not set, which is valid
		},
		{
			name: "invalid port negative",
			opts: Options{
				Server: "myserver",
				Port:   -1,
			},
			wantErr: "port must be between 1 and 65535",
		},
		{
			name: "invalid port too high",
			opts: Options{
				Server: "myserver",
				Port:   70000,
			},
			wantErr: "port must be between 1 and 65535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.opts)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() error = %v, want nil", err)
				}
			} else {
				if err == nil {
					t.Errorf("Validate() error = nil, want %q", tt.wantErr)
				} else if err.Error() != tt.wantErr {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestWriteFile(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	t.Run("creates ssd.yaml", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test1")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}

		opts := Options{Server: "myserver"}
		err := WriteFile(dir, opts)
		if err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		// Verify file exists
		content, err := os.ReadFile(filepath.Join(dir, "ssd.yaml"))
		if err != nil {
			t.Fatalf("failed to read ssd.yaml: %v", err)
		}

		expected := Generate(opts)
		if string(content) != expected {
			t.Errorf("file content =\n%s\nwant:\n%s", string(content), expected)
		}
	})

	t.Run("fails if file exists", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test2")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create existing file
		if err := os.WriteFile(filepath.Join(dir, "ssd.yaml"), []byte("existing"), 0644); err != nil {
			t.Fatal(err)
		}

		opts := Options{Server: "myserver"}
		err := WriteFile(dir, opts)
		if err == nil {
			t.Error("WriteFile() should fail when file exists")
		}
		if err.Error() != "ssd.yaml already exists" {
			t.Errorf("WriteFile() error = %q, want 'ssd.yaml already exists'", err.Error())
		}
	})

	t.Run("force overwrites existing file", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test3")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create existing file
		if err := os.WriteFile(filepath.Join(dir, "ssd.yaml"), []byte("existing"), 0644); err != nil {
			t.Fatal(err)
		}

		opts := Options{Server: "newserver", Force: true}
		err := WriteFile(dir, opts)
		if err != nil {
			t.Fatalf("WriteFile() with force error = %v", err)
		}

		// Verify content was overwritten
		content, err := os.ReadFile(filepath.Join(dir, "ssd.yaml"))
		if err != nil {
			t.Fatalf("failed to read ssd.yaml: %v", err)
		}

		expected := Generate(opts)
		if string(content) != expected {
			t.Errorf("file content =\n%s\nwant:\n%s", string(content), expected)
		}
	})
}
