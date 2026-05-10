package connectors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_ZeroIsUnlimited(t *testing.T) {
	rl := newRateLimiter()
	for i := 0; i < 1000; i++ {
		assert.NoError(t, rl.Allow("c1", 0))
	}
}

func TestRateLimiter_NegativeIsUnlimited(t *testing.T) {
	rl := newRateLimiter()
	assert.NoError(t, rl.Allow("c1", -5))
}

func TestRateLimiter_BlocksAfterLimit(t *testing.T) {
	rl := newRateLimiter()
	for i := 0; i < 5; i++ {
		require.NoError(t, rl.Allow("c1", 5), "call %d should be allowed", i+1)
	}
	err := rl.Allow("c1", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
}

func TestRateLimiter_IndependentPerConnector(t *testing.T) {
	rl := newRateLimiter()
	for i := 0; i < 3; i++ {
		require.NoError(t, rl.Allow("c1", 3))
	}
	// c1 is at limit but c2 should still be free
	require.Error(t, rl.Allow("c1", 3))
	require.NoError(t, rl.Allow("c2", 3))
}

func TestRateLimiter_ErrorMessageContainsLimit(t *testing.T) {
	rl := newRateLimiter()
	for i := 0; i < 10; i++ {
		_ = rl.Allow("c1", 10)
	}
	err := rl.Allow("c1", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10")
}

func TestRateLimiter_ConcurrentSafe(t *testing.T) {
	rl := newRateLimiter()
	allowed := make(chan struct{}, 100)
	done := make(chan struct{}, 20)
	for i := 0; i < 20; i++ {
		go func() {
			if err := rl.Allow("c1", 10); err == nil {
				allowed <- struct{}{}
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	assert.LessOrEqual(t, len(allowed), 10, "at most 10 calls should be allowed")
}
