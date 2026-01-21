package testhelpers

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SSHContainer wraps an SSH server container for testing
type SSHContainer struct {
	Container testcontainers.Container
	Host      string
	Port      string
	User      string
	KeyPath   string
	tmpDir    string
}

// StartSSHContainer starts an OpenSSH server container for testing
func StartSSHContainer(ctx context.Context, t *testing.T) (*SSHContainer, error) {
	t.Helper()

	// Create temporary directory for SSH keys
	tmpDir, err := os.MkdirTemp("", "ssd-test-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Generate test SSH key pair
	keyPath := filepath.Join(tmpDir, "test_key")
	pubKeyPath := keyPath + ".pub"

	// Generate key using ssh-keygen
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-q")
	if err := cmd.Run(); err != nil {
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to generate SSH key: %w", err)
	}

	pubKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to read public key: %w", err)
	}

	req := testcontainers.ContainerRequest{
		Image:        "linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PUID":            "1000",
			"PGID":            "1000",
			"TZ":              "UTC",
			"USER_NAME":       "testuser",
			"PUBLIC_KEY":      string(pubKey),
			"SUDO_ACCESS":     "true",
			"PASSWORD_ACCESS": "false",
		},
		WaitingFor: wait.ForListeningPort("2222/tcp").WithStartupTimeout(120 * time.Second),
		LifecycleHooks: []testcontainers.ContainerLifecycleHooks{
			{
				PostStarts: []testcontainers.ContainerHook{
					func(ctx context.Context, container testcontainers.Container) error {
						// Install rsync for rsync integration tests
						exitCode, _, err := container.Exec(ctx, []string{"apk", "add", "--no-cache", "rsync"})
						if err != nil {
							return fmt.Errorf("failed to install rsync: %w", err)
						}
						if exitCode != 0 {
							return fmt.Errorf("failed to install rsync: exit code %d", exitCode)
						}
						return nil
					},
				},
			},
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		if termErr := container.Terminate(ctx); termErr != nil {
			log.Printf("failed to terminate container: %v", termErr)
		}
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	port, err := container.MappedPort(ctx, "2222")
	if err != nil {
		if termErr := container.Terminate(ctx); termErr != nil {
			log.Printf("failed to terminate container: %v", termErr)
		}
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	// Wait a bit for SSH to be fully ready
	time.Sleep(3 * time.Second)

	return &SSHContainer{
		Container: container,
		Host:      host,
		Port:      port.Port(),
		User:      "testuser",
		KeyPath:   keyPath,
		tmpDir:    tmpDir,
	}, nil
}

// SSHCommand returns a configured SSH command for this container
func (s *SSHContainer) SSHCommand(command string) *exec.Cmd {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", s.KeyPath,
		"-p", s.Port,
		fmt.Sprintf("%s@%s", s.User, s.Host),
		command,
	}
	return exec.Command("ssh", args...)
}

// RunSSH executes a command via SSH and returns the output
func (s *SSHContainer) RunSSH(command string) (string, error) {
	cmd := s.SSHCommand(command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("SSH command failed: %s\nstderr: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// WriteSSHConfig writes a temporary SSH config file for use with this container
func (s *SSHContainer) WriteSSHConfig(hostAlias string) (string, error) {
	configContent := fmt.Sprintf(`Host %s
    HostName %s
    Port %s
    User %s
    IdentityFile %s
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    LogLevel ERROR
`, hostAlias, s.Host, s.Port, s.User, s.KeyPath)

	configPath := filepath.Join(s.tmpDir, "ssh_config")
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		return "", fmt.Errorf("failed to write SSH config: %w", err)
	}
	return configPath, nil
}

// Cleanup terminates the container and removes temp files
func (s *SSHContainer) Cleanup(ctx context.Context) {
	if s.Container != nil {
		if err := s.Container.Terminate(ctx); err != nil {
			log.Printf("failed to terminate container: %v", err)
		}
	}
	if s.tmpDir != "" {
		if err := os.RemoveAll(s.tmpDir); err != nil {
			log.Printf("failed to cleanup temp dir: %v", err)
		}
	}
}

// SSHConfigExecutor is a custom executor that uses a specific SSH config file
type SSHConfigExecutor struct {
	ConfigPath string
}

// Run executes a command using the custom SSH config
func (e *SSHConfigExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	args = e.injectSSHConfig(name, args)
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command failed: %s\n%s", err, stderr.String())
	}
	return stdout.String(), nil
}

// RunInteractive executes a command with terminal output
func (e *SSHConfigExecutor) RunInteractive(ctx context.Context, name string, args ...string) error {
	args = e.injectSSHConfig(name, args)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// injectSSHConfig modifies command args to use the custom SSH config
func (e *SSHConfigExecutor) injectSSHConfig(name string, args []string) []string {
	switch name {
	case "ssh":
		// Inject -F flag for SSH config
		newArgs := []string{"-F", e.ConfigPath}
		return append(newArgs, args...)
	case "rsync":
		// Inject -e flag to use SSH with custom config
		sshCmd := fmt.Sprintf("ssh -F %s", e.ConfigPath)
		newArgs := []string{"-e", sshCmd}
		return append(newArgs, args...)
	default:
		return args
	}
}

// StartSSHDockerContainer starts a privileged container with both SSH server and Docker daemon
// for integration tests that need to build Docker images via SSH
func StartSSHDockerContainer(ctx context.Context, t *testing.T) (*SSHContainer, error) {
	t.Helper()

	// Create temporary directory for SSH keys
	tmpDir, err := os.MkdirTemp("", "ssd-test-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Generate test SSH key pair
	keyPath := filepath.Join(tmpDir, "test_key")
	pubKeyPath := keyPath + ".pub"

	// Generate key using ssh-keygen
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "", "-q")
	if err := cmd.Run(); err != nil {
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to generate SSH key: %w", err)
	}

	pubKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to read public key: %w", err)
	}

	// Use docker:dind with SSH server installed via lifecycle hooks
	req := testcontainers.ContainerRequest{
		Image:        "docker:dind",
		ExposedPorts: []string{"22/tcp", "2375/tcp"},
		Privileged:   true,
		Env: map[string]string{
			"DOCKER_TLS_CERTDIR": "", // Disable TLS for simplicity in tests
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("2375/tcp").WithStartupTimeout(120*time.Second),
		),
		LifecycleHooks: []testcontainers.ContainerLifecycleHooks{
			{
				PostStarts: []testcontainers.ContainerHook{
					func(ctx context.Context, container testcontainers.Container) error {
						// Install and configure SSH server
						commands := [][]string{
							{"apk", "add", "--no-cache", "openssh-server", "openssh-client", "rsync", "bash"},
							{"ssh-keygen", "-A"},
							{"mkdir", "-p", "/root/.ssh"},
							{"chmod", "700", "/root/.ssh"},
							{"sh", "-c", "echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config"},
							{"sh", "-c", "echo 'PubkeyAuthentication yes' >> /etc/ssh/sshd_config"},
							{"sh", "-c", fmt.Sprintf("echo '%s' > /root/.ssh/authorized_keys", string(pubKey))},
							{"chmod", "600", "/root/.ssh/authorized_keys"},
							{"/usr/sbin/sshd"},
						}
						for _, cmdArgs := range commands {
							exitCode, _, err := container.Exec(ctx, cmdArgs)
							if err != nil {
								return fmt.Errorf("failed to execute %v: %w", cmdArgs, err)
							}
							if exitCode != 0 {
								return fmt.Errorf("command %v failed with exit code %d", cmdArgs, exitCode)
							}
						}
						return nil
					},
				},
			},
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		if termErr := container.Terminate(ctx); termErr != nil {
			log.Printf("failed to terminate container: %v", termErr)
		}
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	port, err := container.MappedPort(ctx, "22")
	if err != nil {
		if termErr := container.Terminate(ctx); termErr != nil {
			log.Printf("failed to terminate container: %v", termErr)
		}
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			log.Printf("failed to cleanup temp dir: %v", cleanupErr)
		}
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	// Wait a bit for SSH to be fully ready
	time.Sleep(2 * time.Second)

	return &SSHContainer{
		Container: container,
		Host:      host,
		Port:      port.Port(),
		User:      "root",
		KeyPath:   keyPath,
		tmpDir:    tmpDir,
	}, nil
}
