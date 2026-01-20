package deploy

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/remote"
	"golang.org/x/sys/unix"
)

// Deployer defines the interface for deployment operations
type Deployer interface {
	GetCurrentVersion() (int, error)
	MakeTempDir() (string, error)
	Rsync(localPath, remotePath string) error
	BuildImage(buildDir string, version int) error
	UpdateCompose(version int) error
	RestartStack() error
	Cleanup(path string) error
}

// Options holds configuration for the deployment
type Options struct {
	// Output is where to write progress messages (defaults to os.Stdout)
	Output io.Writer
}

// Deploy performs a full deployment using the default client
func Deploy(cfg *config.Config) error {
	client := remote.NewClient(cfg)
	return DeployWithClient(cfg, client, nil)
}

// DeployWithClient performs a deployment with a custom client (for testing)
func DeployWithClient(cfg *config.Config, client Deployer, opts *Options) error {
	// Default output to discarding if nil (for cleaner test output)
	output := io.Discard
	if opts != nil && opts.Output != nil {
		output = opts.Output
	}

	// Acquire deployment lock
	unlock, err := acquireLock(cfg.StackPath())
	if err != nil {
		return fmt.Errorf("failed to acquire deployment lock: %w", err)
	}
	defer unlock()

	// Get current version
	fmt.Fprintf(output, "Checking current version on %s...\n", cfg.Server)
	currentVersion, err := client.GetCurrentVersion()
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	newVersion := currentVersion + 1
	fmt.Fprintf(output, "Current version: %d, deploying version: %d\n", currentVersion, newVersion)

	// Create temp directory on server
	fmt.Fprintln(output, "Creating temp build directory...")
	tempDir, err := client.MakeTempDir()
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		fmt.Fprintln(output, "Cleaning up temp directory...")
		client.Cleanup(tempDir)
	}()

	// Rsync code to server
	fmt.Fprintf(output, "Syncing code to %s:%s...\n", cfg.Server, tempDir)
	localContext, err := filepath.Abs(cfg.Context)
	if err != nil {
		return fmt.Errorf("failed to resolve context path: %w", err)
	}
	if err := client.Rsync(localContext, tempDir); err != nil {
		return fmt.Errorf("failed to sync code: %w", err)
	}

	// Build image on server
	fmt.Fprintf(output, "Building image %s:%d...\n", cfg.ImageName(), newVersion)
	if err := client.BuildImage(tempDir, newVersion); err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	// Update compose.yaml
	fmt.Fprintln(output, "Updating compose.yaml...")
	if err := client.UpdateCompose(newVersion); err != nil {
		return fmt.Errorf("failed to update compose.yaml: %w", err)
	}

	// Restart stack
	fmt.Fprintln(output, "Restarting stack...")
	if err := client.RestartStack(); err != nil {
		return fmt.Errorf("failed to restart stack: %w", err)
	}

	fmt.Fprintf(output, "\nDeployed %s version %d successfully!\n", cfg.Name, newVersion)
	return nil
}

// acquireLock creates a file-based lock for the given stack path
// Returns an unlock function that must be called when deployment completes
// Timeout is 5 minutes
func acquireLock(stackPath string) (func(), error) {
	hash := sha256.Sum256([]byte(stackPath))
	lockPath := filepath.Join(os.TempDir(), fmt.Sprintf("ssd-lock-%x", hash[:8]))

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create lock file: %w", err)
	}

	timeout := 5 * time.Minute
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}

		if err != unix.EWOULDBLOCK {
			lockFile.Close()
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}

		if time.Now().After(deadline) {
			lockFile.Close()
			return nil, fmt.Errorf("timeout waiting for deployment lock after %v", timeout)
		}

		<-ticker.C
	}

	return func() {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
	}, nil
}
