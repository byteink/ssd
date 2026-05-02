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
    # path: /api
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
    # path: /api
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
    # path: /api
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
    # path: /api
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
    # path: /api
    # port: 3000
`,
		},
		{
			name: "with domain and path",
			opts: Options{
				Server: "myserver",
				Domain: "example.com",
				Path:   "/api",
			},
			expected: `server: myserver

services:
  app:
    domain: example.com
    path: /api
    # Uncomment and configure as needed:
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
				Path:    "/api",
				Port:    3000,
			},
			expected: `server: prod-server
stack: /dockge/stacks/myapp-prod

services:
  api:
    domain: api.example.com
    path: /api
    port: 3000
`,
		},
		{
			name: "with k3s runtime",
			opts: Options{
				Server:  "myserver",
				Runtime: "k3s",
			},
			expected: `runtime: k3s
server: myserver

services:
  app:
    # Uncomment and configure as needed:
    # domain: example.com
    # path: /api
    # port: 3000
`,
		},
		{
			name: "compose runtime omitted from output",
			opts: Options{
				Server:  "myserver",
				Runtime: "compose",
			},
			expected: `server: myserver

services:
  app:
    # Uncomment and configure as needed:
    # domain: example.com
    # path: /api
    # port: 3000
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
		{
			name: "invalid runtime",
			opts: Options{
				Server:  "myserver",
				Runtime: "swarm",
			},
			wantErr: "invalid runtime \"swarm\": must be compose or k3s",
		},
		{
			name: "valid k3s runtime",
			opts: Options{
				Server:  "myserver",
				Runtime: "k3s",
			},
			wantErr: "",
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

	t.Run("creates .ssd/ssd.yaml in fresh dir", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test1")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}

		opts := Options{Server: "myserver"}
		if err := WriteFile(dir, opts); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		content, err := os.ReadFile(filepath.Join(dir, ".ssd", "ssd.yaml"))
		if err != nil {
			t.Fatalf("failed to read .ssd/ssd.yaml: %v", err)
		}
		if string(content) != Generate(opts) {
			t.Errorf("file content =\n%s\nwant:\n%s", string(content), Generate(opts))
		}

		ignore, err := os.ReadFile(filepath.Join(dir, ".ssd", ".gitignore"))
		if err != nil {
			t.Fatalf("failed to read .ssd/.gitignore: %v", err)
		}
		if string(ignore) != gitignoreContent {
			t.Errorf(".gitignore content = %q, want %q", string(ignore), gitignoreContent)
		}
	})

	t.Run("writes to legacy ssd.yaml when it already exists", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test-legacy")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(dir, "ssd.yaml")
		if err := os.WriteFile(legacy, []byte("existing"), 0644); err != nil {
			t.Fatal(err)
		}

		opts := Options{Server: "newserver", Force: true}
		if err := WriteFile(dir, opts); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		content, err := os.ReadFile(legacy)
		if err != nil {
			t.Fatalf("failed to read legacy ssd.yaml: %v", err)
		}
		if string(content) != Generate(opts) {
			t.Errorf("legacy ssd.yaml not overwritten")
		}
		if _, err := os.Stat(filepath.Join(dir, ".ssd")); !os.IsNotExist(err) {
			t.Errorf(".ssd/ should not be created when legacy file exists")
		}
	})

	t.Run("fails if target file exists", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test2")
		if err := os.MkdirAll(filepath.Join(dir, ".ssd"), 0755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, ".ssd", "ssd.yaml")
		if err := os.WriteFile(target, []byte("existing"), 0644); err != nil {
			t.Fatal(err)
		}

		opts := Options{Server: "myserver"}
		err := WriteFile(dir, opts)
		if err == nil {
			t.Fatal("WriteFile() should fail when file exists")
		}
		want := target + " already exists"
		if err.Error() != want {
			t.Errorf("WriteFile() error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("force overwrites existing file", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "test3")
		if err := os.MkdirAll(filepath.Join(dir, ".ssd"), 0755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, ".ssd", "ssd.yaml")
		if err := os.WriteFile(target, []byte("existing"), 0644); err != nil {
			t.Fatal(err)
		}

		opts := Options{Server: "newserver", Force: true}
		if err := WriteFile(dir, opts); err != nil {
			t.Fatalf("WriteFile() with force error = %v", err)
		}

		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("failed to read .ssd/ssd.yaml: %v", err)
		}
		if string(content) != Generate(opts) {
			t.Errorf("file content =\n%s\nwant:\n%s", string(content), Generate(opts))
		}
	})
}

func TestMigrateLegacy(t *testing.T) {
	t.Run("moves ssd.yaml into .ssd and writes gitignore", func(t *testing.T) {
		dir := t.TempDir()
		legacy := filepath.Join(dir, "ssd.yaml")
		body := []byte("server: legacy\nservices:\n  app: {}\n")
		if err := os.WriteFile(legacy, body, 0644); err != nil {
			t.Fatal(err)
		}

		target, err := MigrateLegacy(dir)
		if err != nil {
			t.Fatalf("MigrateLegacy() error = %v", err)
		}
		want := filepath.Join(dir, ".ssd", "ssd.yaml")
		if target != want {
			t.Errorf("target = %q, want %q", target, want)
		}

		moved, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("failed to read migrated file: %v", err)
		}
		if string(moved) != string(body) {
			t.Errorf("migrated content mismatch: %q vs %q", moved, body)
		}

		if _, err := os.Stat(legacy); !os.IsNotExist(err) {
			t.Error("legacy ssd.yaml should be gone")
		}

		ignore, err := os.ReadFile(filepath.Join(dir, ".ssd", ".gitignore"))
		if err != nil {
			t.Fatalf("failed to read .gitignore: %v", err)
		}
		if string(ignore) != gitignoreContent {
			t.Errorf(".gitignore = %q, want %q", ignore, gitignoreContent)
		}
	})

	t.Run("errors when no legacy file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := MigrateLegacy(dir)
		if err == nil {
			t.Fatal("expected error for missing legacy file")
		}
	})

	t.Run("errors when modern file already exists", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".ssd"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ssd.yaml"), []byte("server: legacy\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".ssd", "ssd.yaml"), []byte("server: modern\n"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := MigrateLegacy(dir)
		if err == nil {
			t.Fatal("expected error when modern file already exists")
		}

		// Both files must remain untouched.
		legacyContent, _ := os.ReadFile(filepath.Join(dir, "ssd.yaml"))
		if string(legacyContent) != "server: legacy\n" {
			t.Errorf("legacy file was modified: %q", legacyContent)
		}
		modernContent, _ := os.ReadFile(filepath.Join(dir, ".ssd", "ssd.yaml"))
		if string(modernContent) != "server: modern\n" {
			t.Errorf("modern file was modified: %q", modernContent)
		}
	})

	t.Run("preserves existing .gitignore", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".ssd"), 0755); err != nil {
			t.Fatal(err)
		}
		existing := []byte("# custom\nfoo/\n")
		if err := os.WriteFile(filepath.Join(dir, ".ssd", ".gitignore"), existing, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ssd.yaml"), []byte("server: x\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, err := MigrateLegacy(dir); err != nil {
			t.Fatalf("MigrateLegacy() error = %v", err)
		}

		got, err := os.ReadFile(filepath.Join(dir, ".ssd", ".gitignore"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(existing) {
			t.Errorf(".gitignore was overwritten: %q, want %q", got, existing)
		}
	})
}
