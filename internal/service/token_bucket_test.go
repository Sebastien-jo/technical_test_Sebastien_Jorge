package service

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenBucket_StartsFulls(t *testing.T) {
	tb := NewTokenBucket(100, time.Minute)
	assert.Equal(t, int64(100), tb.GetRemaining())
}

func TestAllow_ConsumesTokensSequentially(t *testing.T) {
	tb := NewTokenBucket(10, time.Minute)

	for i := range 10 {
		allowed, remaining, retryAfter := tb.Allow(1)
		assert.True(t, allowed, "request %d should be allowed", i+1)
		assert.Equal(t, int64(9-i), remaining)
		assert.Zero(t, retryAfter)
	}
}

func TestAllow_Burst(t *testing.T) {
	tb := NewTokenBucket(100, time.Minute)

	// Consume half the bucket at once.
	allowed, remaining, _ := tb.Allow(50)
	assert.True(t, allowed)
	assert.Equal(t, int64(50), remaining)

	// Consume the rest.
	allowed, remaining, _ = tb.Allow(50)
	assert.True(t, allowed)
	assert.Equal(t, int64(0), remaining)

	// Next request must be denied.
	allowed, _, _ = tb.Allow(1)
	assert.False(t, allowed)
}

func TestAllow_ExactExhaustion(t *testing.T) {
	tb := NewTokenBucket(10, time.Minute)

	allowed, remaining, _ := tb.Allow(10)
	require.True(t, allowed)
	assert.Equal(t, int64(0), remaining)

	// Consuming 1 token from an empty bucket must fail.
	allowed, _, retryAfter := tb.Allow(1)
	assert.False(t, allowed)
	assert.Positive(t, retryAfter)
}

func TestAllow_DeniedAfterExhaustion(t *testing.T) {
	tb := NewTokenBucket(1, time.Minute)

	allowed, _, _ := tb.Allow(1)
	require.True(t, allowed, "first request should be allowed")

	allowed, remaining, retryAfter := tb.Allow(1)
	assert.False(t, allowed)
	assert.Equal(t, int64(0), remaining)
	assert.Positive(t, retryAfter)
}

func TestAllow_RetryAfterApproximation(t *testing.T) {
	// 1 token per second → retryAfter ≈ 1 s after exhaustion.
	tb := NewTokenBucket(1, time.Second)
	tb.Allow(1)

	_, _, retryAfter := tb.Allow(1)

	const tolerance = 50 * time.Millisecond
	assert.InDelta(t,
		float64(time.Second), float64(retryAfter), float64(tolerance),
		"retryAfter should be ~1s, got %v", retryAfter,
	)
}

func TestAllow_SlowRate_RetryAfterIsLong(t *testing.T) {
	// 1 token per minute → must wait ~60 s.
	tb := NewTokenBucket(1, time.Minute)
	tb.Allow(1)

	allowed, _, retryAfter := tb.Allow(1)
	assert.False(t, allowed)
	assert.GreaterOrEqual(t, retryAfter, 59*time.Second)
}

func TestRefill_TokensAccumulateOverTime(t *testing.T) {
	// 10 tokens/second → 500 ms ≈ 5 new tokens.
	tb := NewTokenBucket(10, time.Second)
	tb.Allow(10) // exhaust the bucket
	assert.Equal(t, int64(0), tb.GetRemaining())

	time.Sleep(500 * time.Millisecond)

	remaining := tb.GetRemaining()
	assert.InDelta(t, 5, remaining, 2, "should have ~5 tokens after 500ms, got %d", remaining)
}

func TestRefill_NeverExceedsCapacity(t *testing.T) {
	tb := NewTokenBucket(10, time.Second)

	// Already full; wait a bit more.
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int64(10), tb.GetRemaining(), "should not exceed capacity")
}

func TestGetResetTime_FullBucketIsNow(t *testing.T) {
	tb := NewTokenBucket(10, time.Minute)
	resetTime := tb.GetResetTime()
	assert.WithinDuration(t, time.Now(), resetTime, 20*time.Millisecond)
}

func TestGetResetTime_AfterExhaustion(t *testing.T) {
	// 100 tokens/minute → full again in ~60 s.
	tb := NewTokenBucket(100, time.Minute)
	tb.Allow(100)

	timeUntilReset := time.Until(tb.GetResetTime())
	assert.InDelta(t,
		float64(60*time.Second), float64(timeUntilReset), float64(2*time.Second),
		"reset should be ~60s away, got %v", timeUntilReset,
	)
}

func TestAllow_ZeroTokensRequested(t *testing.T) {
	tb := NewTokenBucket(10, time.Minute)

	allowed, remaining, _ := tb.Allow(0)
	assert.True(t, allowed)
	assert.Equal(t, int64(10), remaining)
}

func TestAllow_HighThroughput(t *testing.T) {
	tb := NewTokenBucket(10_000, time.Second)

	allowed, remaining, _ := tb.Allow(5_000)
	assert.True(t, allowed)
	assert.Equal(t, int64(5_000), remaining)

	allowed, _, _ = tb.Allow(5_000)
	assert.True(t, allowed)

	allowed, _, _ = tb.Allow(1)
	assert.False(t, allowed)
}

func TestAllow_ConcurrentAccessAllAllowed(t *testing.T) {
	const capacity = 100
	tb := NewTokenBucket(capacity, time.Minute)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)

	for range capacity {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, _ := tb.Allow(1)
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, capacity, allowed, "all %d goroutines should succeed", capacity)

	// Bucket is now empty — one more must fail.
	ok, _, _ := tb.Allow(1)
	assert.False(t, ok)
}

func TestAllow_ConcurrentAccessPartialDeny(t *testing.T) {
	const capacity = 50
	tb := NewTokenBucket(capacity, time.Minute)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)

	// 100 goroutines race for 50 slots — exactly 50 must win.
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, _ := tb.Allow(1)
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, capacity, allowed, "exactly %d of 100 goroutines should succeed", capacity)
}
