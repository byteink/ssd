package deploy

import (
	"fmt"
	"path/filepath"

	"github.com/byteink/ssd/config"
	"github.com/byteink/ssd/remote"
)

// Deploy performs a full deployment
func Deploy(cfg *config.Config) error {
	client := remote.NewClient(cfg)

	// Get current version
	fmt.Printf("Checking current version on %s...\n", cfg.Server)
	currentVersion, err := client.GetCurrentVersion()
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	newVersion := currentVersion + 1
	fmt.Printf("Current version: %d, deploying version: %d\n", currentVersion, newVersion)

	// Create temp directory on server
	fmt.Println("Creating temp build directory...")
	tempDir, err := client.MakeTempDir()
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() {
		fmt.Println("Cleaning up temp directory...")
		client.Cleanup(tempDir)
	}()

	// Rsync code to server
	fmt.Printf("Syncing code to %s:%s...\n", cfg.Server, tempDir)
	localContext, err := filepath.Abs(cfg.Context)
	if err != nil {
		return fmt.Errorf("failed to resolve context path: %w", err)
	}
	if err := client.Rsync(localContext, tempDir); err != nil {
		return fmt.Errorf("failed to sync code: %w", err)
	}

	// Build image on server
	fmt.Printf("Building image %s:%d...\n", cfg.ImageName(), newVersion)
	if err := client.BuildImage(tempDir, newVersion); err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	// Update compose.yaml
	fmt.Println("Updating compose.yaml...")
	if err := client.UpdateCompose(newVersion); err != nil {
		return fmt.Errorf("failed to update compose.yaml: %w", err)
	}

	// Restart stack
	fmt.Println("Restarting stack...")
	if err := client.RestartStack(); err != nil {
		return fmt.Errorf("failed to restart stack: %w", err)
	}

	fmt.Printf("\nDeployed %s version %d successfully!\n", cfg.Name, newVersion)
	return nil
}
