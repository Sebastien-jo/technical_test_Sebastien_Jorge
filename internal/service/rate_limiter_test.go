package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
	"github.com/sebastien-jorge/rate-limiter/internal/storage"
)

func newTestLimiter(t *testing.T) *RateLimiter {
	t.Helper()
	store := storage.NewMemoryStore()
	t.Cleanup(func() { store.Close() })
	return NewRateLimiter(store)
}

func testPolicy(route, method string, limit int64) *models.RoutePolicy {
	return &models.RoutePolicy{
		Route:      route,
		RouteType:  models.RouteExact,
		Method:     method,
		Limit:      limit,
		Window:     time.Minute,
		Identifier: models.IdentifierNone,
	}
}

func TestRateLimiter_Check_AllowsFirstRequest(t *testing.T) {
	rl := newTestLimiter(t)
	policy := testPolicy("/api", "GET", 10)

	d := rl.Check(context.Background(), "client-a", policy, "global")
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(9), d.Remaining)
	assert.Zero(t, d.RetryAfter)
	assert.Empty(t, d.Message)
}

func TestRateLimiter_Check_DeniesAfterLimit(t *testing.T) {
	rl := newTestLimiter(t)
	policy := testPolicy("/api", "GET", 2)
	ctx := context.Background()

	rl.Check(ctx, "client", policy, "id")
	rl.Check(ctx, "client", policy, "id")

	d := rl.Check(ctx, "client", policy, "id")
	assert.False(t, d.Allowed)
	assert.Equal(t, int64(0), d.Remaining)
	assert.Positive(t, d.RetryAfter)
	assert.Equal(t, "rate limit exceeded", d.Message)
}

func TestRateLimiter_Check_EmptyIdentifierUsesGlobal(t *testing.T) {
	rl := newTestLimiter(t)
	policy := testPolicy("/api", "GET", 5)
	ctx := context.Background()

	d1 := rl.Check(ctx, "c", policy, "")
	d2 := rl.Check(ctx, "c", policy, "")
	assert.True(t, d1.Allowed)
	assert.True(t, d2.Allowed)
	// Both calls must decrement the same bucket.
	assert.Equal(t, int64(4), d1.Remaining)
	assert.Equal(t, int64(3), d2.Remaining)
}

func TestRateLimiter_Check_SeparateBucketsPerIdentifier(t *testing.T) {
	rl := newTestLimiter(t)
	policy := testPolicy("/api", "GET", 1)
	ctx := context.Background()

	d1 := rl.Check(ctx, "c", policy, "user-A")
	d2 := rl.Check(ctx, "c", policy, "user-B")
	assert.True(t, d1.Allowed)
	assert.True(t, d2.Allowed)
}

func TestRateLimiter_Check_ConcurrentSameBucket(t *testing.T) {
	const limit = 100
	rl := newTestLimiter(t)
	policy := testPolicy("/api", "GET", limit)
	ctx := context.Background()

	var (
		wg      sync.WaitGroup
		allowed atomic.Int64
	)
	for range 150 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := rl.Check(ctx, "c", policy, "shared")
			if d.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(limit), allowed.Load())
}

func TestRateLimiter_Check_ConcurrentDifferentBuckets(t *testing.T) {
	rl := newTestLimiter(t)
	policy := testPolicy("/api", "GET", 1)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		id := string(rune('A' + i))
		go func(identifier string) {
			defer wg.Done()
			d := rl.Check(ctx, "c", policy, identifier)
			assert.True(t, d.Allowed, "each unique identifier should have its own bucket")
		}(id)
	}
	wg.Wait()
}

func TestRateLimiter_ResetTime_IsSet(t *testing.T) {
	rl := newTestLimiter(t)
	policy := testPolicy("/api", "GET", 10)

	d := rl.Check(context.Background(), "c", policy, "u")
	assert.False(t, d.ResetTime.IsZero())
}
