package remote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ChaosExecutor implements CommandExecutor for chaos testing
type ChaosExecutor struct {
	hangDuration      time.Duration
	forceError        error
	shouldHang        bool
	shouldRefuse      bool
	timeoutOnRun      bool
	timeoutOnInteract bool
}

// NewChaosExecutor creates a new chaos executor for testing
func NewChaosExecutor() *ChaosExecutor {
	return &ChaosExecutor{}
}

// WithHang configures the executor to hang for the specified duration
func (e *ChaosExecutor) WithHang(d time.Duration) *ChaosExecutor {
	e.hangDuration = d
	e.shouldHang = true
	return e
}

// WithConnectionRefused configures the executor to simulate connection refused
func (e *ChaosExecutor) WithConnectionRefused() *ChaosExecutor {
	e.shouldRefuse = true
	e.forceError = errors.New("ssh: connect to host testserver port 22: Connection refused")
	return e
}

// WithTimeoutOnRun configures Run to timeout
func (e *ChaosExecutor) WithTimeoutOnRun() *ChaosExecutor {
	e.timeoutOnRun = true
	return e
}

// WithTimeoutOnInteractive configures RunInteractive to timeout
func (e *ChaosExecutor) WithTimeoutOnInteractive() *ChaosExecutor {
	e.timeoutOnInteract = true
	return e
}

// Run simulates command execution with chaos injection
func (e *ChaosExecutor) Run(ctx context.Context, name string, args ...string) (string, error) {
	if e.shouldRefuse {
		return "", e.forceError
	}

	if e.timeoutOnRun {
		// Simulate a command that takes longer than the context timeout
		select {
		case <-time.After(10 * time.Second):
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if e.shouldHang {
		select {
		case <-time.After(e.hangDuration):
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	return "success", nil
}

// RunInteractive simulates interactive command execution with chaos injection
func (e *ChaosExecutor) RunInteractive(ctx context.Context, name string, args ...string) error {
	if e.shouldRefuse {
		return e.forceError
	}

	if e.timeoutOnInteract {
		// Simulate a command that takes longer than the context timeout
		select {
		case <-time.After(10 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if e.shouldHang {
		select {
		case <-time.After(e.hangDuration):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// TestChaos_SSHConnectionTimeout verifies that SSH commands timeout after configured duration
func TestChaos_SSHConnectionTimeout(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithTimeoutOnRun()
	client := NewClientWithExecutor(cfg, chaosExec)

	// Create a context with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.SSH(ctx, "echo hello")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	// Verify timeout triggered within expected window
	assert.Less(t, elapsed, 200*time.Millisecond, "timeout should trigger around 100ms")
	assert.Greater(t, elapsed, 50*time.Millisecond, "timeout should not be instant")
}

// TestChaos_SSHCommandHangs verifies that hanging commands are killed by context timeout
func TestChaos_SSHCommandHangs(t *testing.T) {
	cfg := newTestConfig()
	// Simulate a command that hangs for 5 seconds
	chaosExec := NewChaosExecutor().WithHang(5 * time.Second)
	client := NewClientWithExecutor(cfg, chaosExec)

	// Create a context with a timeout shorter than the hang duration
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.SSH(ctx, "sleep 100")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	// Command should be killed around 200ms, not wait for the full 5 seconds
	assert.Less(t, elapsed, 500*time.Millisecond, "command should be killed by timeout")
}

// TestChaos_SSHConnectionRefused verifies clean error handling on connection refused
func TestChaos_SSHConnectionRefused(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithConnectionRefused()
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx := context.Background()
	_, err := client.SSH(ctx, "echo hello")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh command failed")
	assert.Contains(t, err.Error(), "Connection refused")
}

// TestChaos_SSHInteractiveTimeout verifies that interactive commands timeout correctly
func TestChaos_SSHInteractiveTimeout(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithTimeoutOnInteractive()
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := client.SSHInteractive(ctx, "docker ps")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.Less(t, elapsed, 200*time.Millisecond)
}

// TestChaos_SSHInteractiveCommandHangs verifies hanging interactive commands are killed
func TestChaos_SSHInteractiveCommandHangs(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithHang(5 * time.Second)
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := client.SSHInteractive(ctx, "docker build .")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.Less(t, elapsed, 500*time.Millisecond, "command should be killed by timeout")
}

// TestChaos_RsyncTimeout verifies rsync operations timeout correctly
func TestChaos_RsyncTimeout(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithTimeoutOnInteractive()
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := client.Rsync(ctx, "/local/path", "/remote/path")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.Less(t, elapsed, 200*time.Millisecond)
}

// TestChaos_MakeTempDirTimeout verifies temp dir creation handles timeouts
func TestChaos_MakeTempDirTimeout(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithTimeoutOnRun()
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.MakeTempDir(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// TestChaos_GetCurrentVersionTimeout verifies version retrieval handles timeouts
// Note: GetCurrentVersion intentionally returns (0, nil) on SSH errors to handle
// the case where compose.yaml doesn't exist yet. Timeout errors are swallowed.
func TestChaos_GetCurrentVersionTimeout(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithTimeoutOnRun()
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	version, err := client.GetCurrentVersion(ctx)

	// GetCurrentVersion returns (0, nil) on SSH errors by design
	require.NoError(t, err)
	assert.Equal(t, 0, version)
}

// TestChaos_BuildImageTimeout verifies image build handles timeouts
func TestChaos_BuildImageTimeout(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithTimeoutOnInteractive()
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.BuildImage(ctx, "/tmp/build", 1)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// TestChaos_RestartStackTimeout verifies stack restart handles timeouts
func TestChaos_RestartStackTimeout(t *testing.T) {
	cfg := newTestConfig()
	chaosExec := NewChaosExecutor().WithTimeoutOnInteractive()
	client := NewClientWithExecutor(cfg, chaosExec)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.RestartStack(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}
