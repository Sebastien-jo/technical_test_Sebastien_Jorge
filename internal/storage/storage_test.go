package storage

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStore runs the Store contract against any implementation. Add new stores
// by writing a small factory and calling testStore with it.
func testStore(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("CheckTokenBucket_AllowsFirstRequest", func(t *testing.T) {
		require.NoError(t, s.Clear(ctx))
		res, err := s.CheckTokenBucket(ctx, "first", 10, 1.0, time.Minute)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, int64(9), res.Remaining)
		assert.Zero(t, res.RetryAfter)
	})

	t.Run("CheckTokenBucket_DeniesWhenExhausted", func(t *testing.T) {
		require.NoError(t, s.Clear(ctx))
		for i := 0; i < 3; i++ {
			_, err := s.CheckTokenBucket(ctx, "exhaust", 3, 1.0, time.Minute)
			require.NoError(t, err)
		}
		res, err := s.CheckTokenBucket(ctx, "exhaust", 3, 1.0, time.Minute)
		require.NoError(t, err)
		assert.False(t, res.Allowed)
		assert.Equal(t, int64(0), res.Remaining)
		assert.Positive(t, res.RetryAfter)
	})

	t.Run("CheckTokenBucket_SeparateKeysAreIndependent", func(t *testing.T) {
		require.NoError(t, s.Clear(ctx))
		a, err := s.CheckTokenBucket(ctx, "key-A", 1, 1.0, time.Minute)
		require.NoError(t, err)
		b, err := s.CheckTokenBucket(ctx, "key-B", 1, 1.0, time.Minute)
		require.NoError(t, err)
		assert.True(t, a.Allowed)
		assert.True(t, b.Allowed)
	})

	t.Run("Clear_RemovesBuckets", func(t *testing.T) {
		_, err := s.CheckTokenBucket(ctx, "to-clear", 1, 1.0, time.Minute)
		require.NoError(t, err)

		require.NoError(t, s.Clear(ctx))

		// After clearing, the bucket starts fresh — a single request consumes
		// from a full bucket, so Remaining drops from capacity to capacity-1.
		res, err := s.CheckTokenBucket(ctx, "to-clear", 5, 1.0, time.Minute)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, int64(4), res.Remaining)
	})

	t.Run("Health_ReturnsNil", func(t *testing.T) {
		assert.NoError(t, s.Health(ctx))
	})
}

func TestMemoryStore_Contract(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	testStore(t, ms)
}

func TestMemoryStore_BucketTTLExpiry(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	ctx := context.Background()

	// First call: consume 1 token from a bucket with capacity 5.
	_, err := ms.CheckTokenBucket(ctx, "expiring", 5, 1.0, 50*time.Millisecond)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// After the TTL, the bucket should be discarded and the next call should
	// see a fresh, full bucket — Remaining is capacity-1 (=4), not 3.
	res, err := ms.CheckTokenBucket(ctx, "expiring", 5, 1.0, 50*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, int64(4), res.Remaining, "expired bucket must start fresh")
}

func TestMemoryStore_PurgeExpired_DropsIdleBuckets(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	ctx := context.Background()

	_, err := ms.CheckTokenBucket(ctx, "stale", 1, 1.0, time.Nanosecond)
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)
	ms.purgeExpired()

	ms.mu.Lock()
	_, present := ms.buckets["stale"]
	ms.mu.Unlock()
	assert.False(t, present, "expired bucket must be removed by purgeExpired")
}

func TestMemoryStore_CheckTokenBucket_Concurrent(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	ctx := context.Background()

	const capacity = 100
	const goroutines = 150

	var (
		wg      sync.WaitGroup
		allowed atomic.Int64
	)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := ms.CheckTokenBucket(ctx, "shared", capacity, 1.0, time.Minute)
			require.NoError(t, err)
			if res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(capacity), allowed.Load(), "exactly capacity requests must be allowed under contention")
}

func newRedisStoreOrSkip(t *testing.T) *RedisStore {
	t.Helper()
	s, err := NewRedisStore(models.RedisConfig{Host: "localhost", Port: 6379, DB: 15}, "test:rl:")
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Clear(context.Background())
		_ = s.Close()
	})
	return s
}

func TestRedisStore_Contract(t *testing.T) {
	testStore(t, newRedisStoreOrSkip(t))
}

func TestRedisStore_BucketTTLExpiry(t *testing.T) {
	s := newRedisStoreOrSkip(t)
	ctx := context.Background()

	_, err := s.CheckTokenBucket(ctx, "ttl-bucket", 5, 1.0, 100*time.Millisecond)
	require.NoError(t, err)

	time.Sleep(250 * time.Millisecond)

	res, err := s.CheckTokenBucket(ctx, "ttl-bucket", 5, 1.0, 100*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, int64(4), res.Remaining, "expired bucket must start fresh in Redis")
}

func TestHybridStore_Contract_MemoryMode(t *testing.T) {
	// Pointing at a closed port forces immediate fallback into memory mode.
	hs, err := NewHybridStore(models.RedisConfig{Host: "localhost", Port: 19999}, "test:")
	require.NoError(t, err)
	defer hs.Close()

	require.True(t, hs.UsingMemoryFallback())
	testStore(t, hs)
}

func TestHybridStore_FallbackServesTraffic(t *testing.T) {
	hs, err := NewHybridStore(models.RedisConfig{Host: "localhost", Port: 19999}, "test:")
	require.NoError(t, err, "constructor must not fail when Redis is unreachable")
	defer hs.Close()

	assert.True(t, hs.UsingMemoryFallback(), "store should be in memory mode")

	ctx := context.Background()
	res, err := hs.CheckTokenBucket(ctx, "fallback-key", 3, 1.0, time.Minute)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
	assert.Equal(t, int64(2), res.Remaining)
}
