package testhelpers

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// Executor defines the interface for command execution
type Executor interface {
	Execute(ctx context.Context, command string) (string, error)
}

// ChaosExecutor wraps an Executor and injects faults for testing
type ChaosExecutor struct {
	inner         Executor
	mu            sync.Mutex
	callCount     int
	failAfterN    int
	timeoutAfter  time.Duration
	forceError    error
	failProbility float64
	rng           *rand.Rand
}

// NewChaosExecutor creates a new ChaosExecutor wrapping the provided executor
func NewChaosExecutor(inner Executor) *ChaosExecutor {
	return &ChaosExecutor{
		inner:         inner,
		failAfterN:    -1,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
		failProbility: 0.0,
	}
}

// FailAfterN configures the executor to fail after n successful calls
func (c *ChaosExecutor) FailAfterN(n int) *ChaosExecutor {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failAfterN = n
	return c
}

// TimeoutAfter configures the executor to hang then timeout after duration d
func (c *ChaosExecutor) TimeoutAfter(d time.Duration) *ChaosExecutor {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeoutAfter = d
	return c
}

// FailWithError configures the executor to always fail with the provided error
func (c *ChaosExecutor) FailWithError(err error) *ChaosExecutor {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forceError = err
	return c
}

// FailRandomly configures the executor to fail randomly with given probability (0.0 to 1.0)
func (c *ChaosExecutor) FailRandomly(probability float64) *ChaosExecutor {
	c.mu.Lock()
	defer c.mu.Unlock()
	if probability < 0.0 {
		probability = 0.0
	}
	if probability > 1.0 {
		probability = 1.0
	}
	c.failProbility = probability
	return c
}

// Reset clears all fault injection settings and resets call count
func (c *ChaosExecutor) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callCount = 0
	c.failAfterN = -1
	c.timeoutAfter = 0
	c.forceError = nil
	c.failProbility = 0.0
}

// Execute executes the command with fault injection
func (c *ChaosExecutor) Execute(ctx context.Context, command string) (string, error) {
	c.mu.Lock()
	c.callCount++
	currentCount := c.callCount
	failAfter := c.failAfterN
	timeout := c.timeoutAfter
	forceErr := c.forceError
	probability := c.failProbility
	c.mu.Unlock()

	// Check if we should force an error
	if forceErr != nil {
		return "", forceErr
	}

	// Check if we should fail after N calls
	if failAfter >= 0 && currentCount > failAfter {
		return "", errors.New("chaos: failed after N calls")
	}

	// Check if we should fail randomly
	if probability > 0.0 {
		c.mu.Lock()
		shouldFail := c.rng.Float64() < probability
		c.mu.Unlock()
		if shouldFail {
			return "", errors.New("chaos: random failure")
		}
	}

	// Check if we should timeout
	if timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		resultChan := make(chan struct {
			output string
			err    error
		}, 1)

		go func() {
			output, err := c.inner.Execute(ctx, command)
			resultChan <- struct {
				output string
				err    error
			}{output, err}
		}()

		select {
		case result := <-resultChan:
			return result.output, result.err
		case <-timeoutCtx.Done():
			return "", errors.New("chaos: timeout exceeded")
		}
	}

	return c.inner.Execute(ctx, command)
}
