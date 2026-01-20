//go:build !windows

package deploy

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// acquireLock creates a file-based lock for the given stack path
// Returns an unlock function that must be called when deployment completes
// Timeout is 5 minutes
func acquireLock(stackPath string) (func(), error) {
	return acquireLockWithTimeout(stackPath, 5*time.Minute)
}

// acquireLockWithTimeout creates a file-based lock with a custom timeout
func acquireLockWithTimeout(stackPath string, timeout time.Duration) (func(), error) {
	hash := sha256.Sum256([]byte(stackPath))
	lockPath := filepath.Join(os.TempDir(), fmt.Sprintf("ssd-lock-%x", hash[:8]))

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create lock file: %w", err)
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}

		if err != unix.EWOULDBLOCK {
			if closeErr := lockFile.Close(); closeErr != nil {
				log.Printf("failed to close lock file: %v", closeErr)
			}
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}

		if time.Now().After(deadline) {
			if closeErr := lockFile.Close(); closeErr != nil {
				log.Printf("failed to close lock file: %v", closeErr)
			}
			return nil, fmt.Errorf("timeout waiting for deployment lock after %v", timeout)
		}

		<-ticker.C
	}

	return func() {
		if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_UN); err != nil {
			log.Printf("failed to unlock file: %v", err)
		}
		if err := lockFile.Close(); err != nil {
			log.Printf("failed to close lock file: %v", err)
		}
	}, nil
}
