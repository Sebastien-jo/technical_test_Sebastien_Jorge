package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStore runs the full Store contract against any implementation.
// Add new store types by writing a small factory function and calling testStore.
func testStore(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("Set_and_Get", func(t *testing.T) {
		require.NoError(t, s.Set(ctx, "k1", 42, time.Minute))

		val, err := s.Get(ctx, "k1")
		require.NoError(t, err)
		assert.Equal(t, int64(42), val)
	})

	t.Run("Get_missing_key_returns_zero", func(t *testing.T) {
		val, err := s.Get(ctx, "does-not-exist")
		require.NoError(t, err)
		assert.Equal(t, int64(0), val)
	})

	t.Run("Set_overwrites_existing_value", func(t *testing.T) {
		require.NoError(t, s.Set(ctx, "overwrite", 1, time.Minute))
		require.NoError(t, s.Set(ctx, "overwrite", 99, time.Minute))

		val, err := s.Get(ctx, "overwrite")
		require.NoError(t, err)
		assert.Equal(t, int64(99), val)
	})

	t.Run("Increment_creates_key_at_delta", func(t *testing.T) {
		val, err := s.Increment(ctx, "new-counter", 5, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, int64(5), val)
	})

	t.Run("Increment_accumulates", func(t *testing.T) {
		key := "accum"
		for i := int64(1); i <= 5; i++ {
			val, err := s.Increment(ctx, key, 1, time.Minute)
			require.NoError(t, err)
			assert.Equal(t, i, val)
		}
	})

	t.Run("Increment_by_arbitrary_delta", func(t *testing.T) {
		val, err := s.Increment(ctx, "delta-key", 10, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, int64(10), val)

		val, err = s.Increment(ctx, "delta-key", 7, time.Minute)
		require.NoError(t, err)
		assert.Equal(t, int64(17), val)
	})

	t.Run("Exists_true_for_live_key", func(t *testing.T) {
		require.NoError(t, s.Set(ctx, "exists-key", 1, time.Minute))

		ok, err := s.Exists(ctx, "exists-key")
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("Exists_false_for_missing_key", func(t *testing.T) {
		ok, err := s.Exists(ctx, "ghost")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("Delete_removes_key", func(t *testing.T) {
		require.NoError(t, s.Set(ctx, "del-key", 1, time.Minute))
		require.NoError(t, s.Delete(ctx, "del-key"))

		ok, err := s.Exists(ctx, "del-key")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("Delete_missing_key_is_no_op", func(t *testing.T) {
		assert.NoError(t, s.Delete(ctx, "no-such-key"))
	})

	t.Run("Clear_removes_all_keys", func(t *testing.T) {
		require.NoError(t, s.Set(ctx, "c1", 1, time.Minute))
		require.NoError(t, s.Set(ctx, "c2", 2, time.Minute))
		require.NoError(t, s.Clear(ctx))

		for _, k := range []string{"c1", "c2"} {
			val, err := s.Get(ctx, k)
			require.NoError(t, err)
			assert.Zero(t, val, "key %q should be cleared", k)
		}
	})

	t.Run("Health_returns_nil", func(t *testing.T) {
		assert.NoError(t, s.Health(ctx))
	})
}

func TestMemoryStore_Contract(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	testStore(t, ms)
}

func TestMemoryStore_TTLExpiry(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	ctx := context.Background()

	require.NoError(t, ms.Set(ctx, "short", 42, 100*time.Millisecond))

	ok, err := ms.Exists(ctx, "short")
	require.NoError(t, err)
	assert.True(t, ok)

	time.Sleep(150 * time.Millisecond)

	ok, err = ms.Exists(ctx, "short")
	require.NoError(t, err)
	assert.False(t, ok, "key should have expired")

	val, err := ms.Get(ctx, "short")
	require.NoError(t, err)
	assert.Zero(t, val, "expired key should return 0")
}

func TestMemoryStore_IncrementOnExpiredKey_StartsFromZero(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	ctx := context.Background()

	_, err := ms.Increment(ctx, "exp-incr", 10, 100*time.Millisecond)
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)

	// Expired key — increment should restart from 0.
	val, err := ms.Increment(ctx, "exp-incr", 3, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(3), val, "should restart from 0 after expiry, not add to 10")
}

func TestMemoryStore_ConcurrentIncrement_IsAtomic(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()
	ctx := context.Background()

	const goroutines = 50
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = ms.Increment(ctx, "race-key", 1, time.Minute)
		}()
	}
	wg.Wait()

	val, err := ms.Get(ctx, "race-key")
	require.NoError(t, err)
	assert.Equal(t, int64(goroutines), val, "each goroutine contributes exactly 1")
}

func newRedisStoreOrSkip(t *testing.T) *RedisStore {
	t.Helper()
	s, err := NewRedisStore("localhost", 6379, 15, "", false, "test:rl:")
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

func TestRedisStore_TTLExpiry(t *testing.T) {
	s := newRedisStoreOrSkip(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "ttl-key", 1, 100*time.Millisecond))
	time.Sleep(200 * time.Millisecond)

	val, err := s.Get(ctx, "ttl-key")
	require.NoError(t, err)
	assert.Zero(t, val, "key should have expired in Redis")
}

func TestHybridStore_MemoryFallbackWhenRedisDown(t *testing.T) {
	// Point to a port with nothing listening → forces memory fallback.
	hs, err := NewHybridStore("localhost", 19999, 0, "", false, "test:")
	require.NoError(t, err, "NewHybridStore should never return an error")
	defer hs.Close()

	assert.True(t, hs.UsingMemoryFallback(), "should be in memory mode")

	ctx := context.Background()
	require.NoError(t, hs.Set(ctx, "k", 7, time.Minute))

	val, err := hs.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, int64(7), val)
}

func TestHybridStore_Contract_MemoryMode(t *testing.T) {
	hs, err := NewHybridStore("localhost", 19999, 0, "", false, "test:")
	require.NoError(t, err)
	defer hs.Close()
	testStore(t, hs)
}

func TestMemoryStore_PurgeExpired(t *testing.T) {
	ms := NewMemoryStore()
	defer ms.Close()

	ctx := context.Background()
	require.NoError(t, ms.Set(ctx, "alive", 1, time.Hour))
	require.NoError(t, ms.Set(ctx, "dead", 2, time.Nanosecond))

	time.Sleep(5 * time.Millisecond)
	ms.purgeExpired()

	val, err := ms.Get(ctx, "alive")
	require.NoError(t, err)
	assert.Equal(t, int64(1), val, "non-expired key must survive purge")

	val, err = ms.Get(ctx, "dead")
	require.NoError(t, err)
	assert.Equal(t, int64(0), val, "expired key must be removed by purge")
}
