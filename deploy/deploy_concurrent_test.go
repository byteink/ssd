package deploy

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteink/ssd/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestConcurrent_SameStackBlocked verifies that two deployments to the same stack
// cannot run simultaneously - the second must wait for the first to complete
func TestConcurrent_SameStackBlocked(t *testing.T) {
	stackPath := "/stacks/concurrent-same"
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      stackPath,
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	var firstStarted, secondStarted atomic.Bool
	var firstCompleted, secondCompleted atomic.Bool
	var mu sync.Mutex
	executionOrder := []string{}

	// First deployment - holds lock for 200ms
	mockClient1 := new(MockDeployer)
	mockClient1.On("GetCurrentVersion").Run(func(args mock.Arguments) {
		firstStarted.Store(true)
		mu.Lock()
		executionOrder = append(executionOrder, "first-started")
		mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}).Return(1, nil)
	mockClient1.On("MakeTempDir").Return("/tmp/build1", nil)
	mockClient1.On("Rsync", mock.Anything, "/tmp/build1").Return(nil)
	mockClient1.On("BuildImage", "/tmp/build1", 2).Return(nil)
	mockClient1.On("UpdateCompose", 2).Return(nil)
	mockClient1.On("RestartStack").Run(func(args mock.Arguments) {
		mu.Lock()
		executionOrder = append(executionOrder, "first-completed")
		mu.Unlock()
		firstCompleted.Store(true)
	}).Return(nil)
	mockClient1.On("Cleanup", "/tmp/build1").Return(nil)

	// Second deployment - should wait for first
	mockClient2 := new(MockDeployer)
	mockClient2.On("GetCurrentVersion").Run(func(args mock.Arguments) {
		secondStarted.Store(true)
		mu.Lock()
		executionOrder = append(executionOrder, "second-started")
		mu.Unlock()
		// Second deploy should only start after first completes
		assert.True(t, firstCompleted.Load(), "second deploy started before first completed")
	}).Return(2, nil)
	mockClient2.On("MakeTempDir").Return("/tmp/build2", nil)
	mockClient2.On("Rsync", mock.Anything, "/tmp/build2").Return(nil)
	mockClient2.On("BuildImage", "/tmp/build2", 3).Return(nil)
	mockClient2.On("UpdateCompose", 3).Return(nil)
	mockClient2.On("RestartStack").Run(func(args mock.Arguments) {
		mu.Lock()
		executionOrder = append(executionOrder, "second-completed")
		mu.Unlock()
		secondCompleted.Store(true)
	}).Return(nil)
	mockClient2.On("Cleanup", "/tmp/build2").Return(nil)

	var wg sync.WaitGroup
	errors := make(chan error, 2)

	// Start first deployment
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := DeployWithClient(cfg, mockClient1, nil)
		errors <- err
	}()

	// Wait a bit to ensure first deployment acquires lock
	time.Sleep(50 * time.Millisecond)

	// Start second deployment - should block
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := DeployWithClient(cfg, mockClient2, nil)
		errors <- err
	}()

	wg.Wait()
	close(errors)

	// Both should succeed
	for err := range errors {
		require.NoError(t, err)
	}

	// Verify execution order
	assert.True(t, firstStarted.Load(), "first deployment should have started")
	assert.True(t, secondStarted.Load(), "second deployment should have started")
	assert.True(t, firstCompleted.Load(), "first deployment should have completed")
	assert.True(t, secondCompleted.Load(), "second deployment should have completed")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{
		"first-started",
		"first-completed",
		"second-started",
		"second-completed",
	}, executionOrder, "deployments must execute in sequence")

	mockClient1.AssertExpectations(t)
	mockClient2.AssertExpectations(t)
}

// TestConcurrent_DifferentStacksParallel verifies that deployments to different stacks
// can run in parallel without blocking each other
func TestConcurrent_DifferentStacksParallel(t *testing.T) {
	cfg1 := &config.Config{
		Name:       "app1",
		Server:     "testserver",
		Stack:      "/stacks/app1",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	cfg2 := &config.Config{
		Name:       "app2",
		Server:     "testserver",
		Stack:      "/stacks/app2",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	var deploy1Started, deploy2Started atomic.Bool
	var deploy1InProgress, deploy2InProgress atomic.Bool

	// First deployment
	mockClient1 := new(MockDeployer)
	mockClient1.On("GetCurrentVersion").Run(func(args mock.Arguments) {
		deploy1Started.Store(true)
		deploy1InProgress.Store(true)
		time.Sleep(100 * time.Millisecond)
		// Both should be running concurrently
		assert.True(t, deploy2Started.Load() || deploy2InProgress.Load(),
			"deploy2 should start while deploy1 is in progress")
		deploy1InProgress.Store(false)
	}).Return(1, nil)
	mockClient1.On("MakeTempDir").Return("/tmp/build1", nil)
	mockClient1.On("Rsync", mock.Anything, "/tmp/build1").Return(nil)
	mockClient1.On("BuildImage", "/tmp/build1", 2).Return(nil)
	mockClient1.On("UpdateCompose", 2).Return(nil)
	mockClient1.On("RestartStack").Return(nil)
	mockClient1.On("Cleanup", "/tmp/build1").Return(nil)

	// Second deployment
	mockClient2 := new(MockDeployer)
	mockClient2.On("GetCurrentVersion").Run(func(args mock.Arguments) {
		deploy2Started.Store(true)
		deploy2InProgress.Store(true)
		time.Sleep(100 * time.Millisecond)
		// Both should be running concurrently
		assert.True(t, deploy1InProgress.Load() || deploy1Started.Load(),
			"deploy1 should be running concurrently with deploy2")
		deploy2InProgress.Store(false)
	}).Return(3, nil)
	mockClient2.On("MakeTempDir").Return("/tmp/build2", nil)
	mockClient2.On("Rsync", mock.Anything, "/tmp/build2").Return(nil)
	mockClient2.On("BuildImage", "/tmp/build2", 4).Return(nil)
	mockClient2.On("UpdateCompose", 4).Return(nil)
	mockClient2.On("RestartStack").Return(nil)
	mockClient2.On("Cleanup", "/tmp/build2").Return(nil)

	var wg sync.WaitGroup
	errors := make(chan error, 2)

	start := time.Now()

	// Start both deployments simultaneously
	wg.Add(2)
	go func() {
		defer wg.Done()
		errors <- DeployWithClient(cfg1, mockClient1, nil)
	}()
	go func() {
		defer wg.Done()
		errors <- DeployWithClient(cfg2, mockClient2, nil)
	}()

	wg.Wait()
	close(errors)

	duration := time.Since(start)

	// Both should succeed
	for err := range errors {
		require.NoError(t, err)
	}

	// If they ran serially, it would take >200ms. Parallel should be ~100-150ms
	assert.Less(t, duration, 180*time.Millisecond,
		"deployments should run in parallel, not serially")

	assert.True(t, deploy1Started.Load(), "deploy1 should have started")
	assert.True(t, deploy2Started.Load(), "deploy2 should have started")

	mockClient1.AssertExpectations(t)
	mockClient2.AssertExpectations(t)
}

// TestConcurrent_LockTimeout verifies that a deployment waiting for a lock
// will timeout after the configured duration
func TestConcurrent_LockTimeout(t *testing.T) {
	stackPath := "/stacks/timeout-test"
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      stackPath,
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	// First deployment holds lock for 500ms
	mockClient1 := new(MockDeployer)
	mockClient1.On("GetCurrentVersion").Run(func(args mock.Arguments) {
		time.Sleep(500 * time.Millisecond)
	}).Return(1, nil)
	mockClient1.On("MakeTempDir").Return("/tmp/build1", nil)
	mockClient1.On("Rsync", mock.Anything, "/tmp/build1").Return(nil)
	mockClient1.On("BuildImage", "/tmp/build1", 2).Return(nil)
	mockClient1.On("UpdateCompose", 2).Return(nil)
	mockClient1.On("RestartStack").Return(nil)
	mockClient1.On("Cleanup", "/tmp/build1").Return(nil)

	var wg sync.WaitGroup
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)

	// Start first deployment
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := DeployWithClient(cfg, mockClient1, nil)
		firstResult <- err
	}()

	// Wait to ensure first deployment acquires lock
	time.Sleep(50 * time.Millisecond)

	// Start second deployment with short timeout - should fail
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Manually test lock timeout with custom duration
		unlock, err := acquireLockWithTimeout(stackPath, 200*time.Millisecond)
		if err == nil {
			unlock()
		}
		secondResult <- err
	}()

	wg.Wait()
	close(firstResult)
	close(secondResult)

	// First should succeed
	err1 := <-firstResult
	assert.NoError(t, err1, "first deployment should succeed")

	// Second should timeout
	err2 := <-secondResult
	require.Error(t, err2, "second deployment should timeout")
	assert.Contains(t, err2.Error(), "timeout waiting for deployment lock",
		"error should indicate lock timeout")

	mockClient1.AssertExpectations(t)
}

// TestConcurrent_LockReleasedOnFailure verifies that the deployment lock
// is properly released even when the deployment fails
func TestConcurrent_LockReleasedOnFailure(t *testing.T) {
	stackPath := "/stacks/failure-test"
	cfg := &config.Config{
		Name:       "myapp",
		Server:     "testserver",
		Stack:      stackPath,
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	// First deployment fails
	mockClient1 := new(MockDeployer)
	mockClient1.On("GetCurrentVersion").Return(0, errors.New("connection failed"))

	// Second deployment succeeds
	mockClient2 := new(MockDeployer)
	mockClient2.On("GetCurrentVersion").Return(1, nil)
	mockClient2.On("MakeTempDir").Return("/tmp/build2", nil)
	mockClient2.On("Rsync", mock.Anything, "/tmp/build2").Return(nil)
	mockClient2.On("BuildImage", "/tmp/build2", 2).Return(nil)
	mockClient2.On("UpdateCompose", 2).Return(nil)
	mockClient2.On("RestartStack").Return(nil)
	mockClient2.On("Cleanup", "/tmp/build2").Return(nil)

	// First deployment should fail
	err1 := DeployWithClient(cfg, mockClient1, nil)
	require.Error(t, err1)
	assert.Contains(t, err1.Error(), "failed to get current version")

	// Second deployment should succeed immediately (no waiting)
	start := time.Now()
	err2 := DeployWithClient(cfg, mockClient2, nil)
	duration := time.Since(start)

	require.NoError(t, err2, "second deployment should succeed after first fails")

	// Should acquire lock quickly since first released it
	assert.Less(t, duration, 100*time.Millisecond,
		"lock should be available immediately after first deployment fails")

	mockClient1.AssertExpectations(t)
	mockClient2.AssertExpectations(t)
}

// TestConcurrent_RaceConditions is a stress test that launches multiple
// concurrent deployments and ensures no data races occur
func TestConcurrent_RaceConditions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const numGoroutines = 10
	const numDeploysPerGoroutine = 5

	cfg := &config.Config{
		Name:       "racetest",
		Server:     "testserver",
		Stack:      "/stacks/racetest",
		Dockerfile: "./Dockerfile",
		Context:    ".",
	}

	var successCount, failureCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < numDeploysPerGoroutine; j++ {
				mockClient := new(MockDeployer)
				version := goroutineID*numDeploysPerGoroutine + j + 1

				mockClient.On("GetCurrentVersion").Return(version-1, nil)
				mockClient.On("MakeTempDir").Return("/tmp/build", nil)
				mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
				mockClient.On("BuildImage", "/tmp/build", version).Return(nil)
				mockClient.On("UpdateCompose", version).Return(nil)
				mockClient.On("RestartStack").Return(nil)
				mockClient.On("Cleanup", "/tmp/build").Return(nil)

				err := DeployWithClient(cfg, mockClient, nil)
				if err != nil {
					if err.Error() == "timeout waiting for deployment lock after 5m0s" {
						failureCount.Add(1)
					} else {
						t.Errorf("unexpected error: %v", err)
					}
				} else {
					successCount.Add(1)
				}

				// Small delay between attempts
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	total := successCount.Load() + failureCount.Load()
	expected := int32(numGoroutines * numDeploysPerGoroutine)

	assert.Equal(t, expected, total,
		"all deploy attempts should either succeed or timeout")

	// At least some should succeed
	assert.Greater(t, successCount.Load(), int32(0),
		"at least some deployments should succeed")

	t.Logf("Race test completed: %d succeeded, %d timed out",
		successCount.Load(), failureCount.Load())
}

// TestConcurrent_MultipleStacksNoInterference verifies that locks for different
// stacks don't interfere with each other even under concurrent load
func TestConcurrent_MultipleStacksNoInterference(t *testing.T) {
	const numStacks = 5
	const deploysPerStack = 3

	configs := make([]*config.Config, numStacks)
	for i := 0; i < numStacks; i++ {
		configs[i] = &config.Config{
			Name:       "app",
			Server:     "testserver",
			Stack:      "/stacks/multistack-" + string(rune('a'+i)),
			Dockerfile: "./Dockerfile",
			Context:    ".",
		}
	}

	var wg sync.WaitGroup
	errors := make(chan error, numStacks*deploysPerStack)

	for stackIdx, cfg := range configs {
		for deployIdx := 0; deployIdx < deploysPerStack; deployIdx++ {
			wg.Add(1)
			go func(cfg *config.Config, version int) {
				defer wg.Done()

				mockClient := new(MockDeployer)
				mockClient.On("GetCurrentVersion").Return(version-1, nil)
				mockClient.On("MakeTempDir").Return("/tmp/build", nil)
				mockClient.On("Rsync", mock.Anything, "/tmp/build").Return(nil)
				mockClient.On("BuildImage", "/tmp/build", version).Return(nil)
				mockClient.On("UpdateCompose", version).Return(nil)
				mockClient.On("RestartStack").Return(nil)
				mockClient.On("Cleanup", "/tmp/build").Return(nil)

				errors <- DeployWithClient(cfg, mockClient, nil)
			}(cfg, deployIdx+1)

			// Stagger start times slightly
			time.Sleep(5 * time.Millisecond)
		}

		// Small delay between stacks
		if stackIdx < numStacks-1 {
			time.Sleep(20 * time.Millisecond)
		}
	}

	wg.Wait()
	close(errors)

	// All deployments should succeed since they're to different stacks
	for err := range errors {
		require.NoError(t, err)
	}
}
