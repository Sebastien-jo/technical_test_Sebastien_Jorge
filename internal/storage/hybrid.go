package storage

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
)

type HybridStore struct {
	memory *MemoryStore
	cfg    models.RedisConfig
	prefix string

	mu    sync.RWMutex
	redis *RedisStore

	usingMem    atomic.Bool
	reconnectOn atomic.Bool
	done        chan struct{}
}

func NewHybridStore(cfg models.RedisConfig, prefix string) (*HybridStore, error) {
	hs := &HybridStore{
		memory: NewMemoryStore(),
		cfg:    cfg,
		prefix: prefix,
		done:   make(chan struct{}),
	}

	r, err := NewRedisStore(cfg, prefix)
	if err != nil {
		slog.Warn("[storage] Redis unavailable at startup, using memory fallback", "error", err)
		hs.usingMem.Store(true)
		hs.startReconnect()
	} else {
		hs.redis = r
	}

	return hs, nil
}

func (hs *HybridStore) UsingMemoryFallback() bool {
	return hs.usingMem.Load()
}

func (hs *HybridStore) Close() error {
	close(hs.done)
	hs.memory.Close()

	hs.mu.RLock()
	r := hs.redis
	hs.mu.RUnlock()

	if r != nil {
		return r.Close()
	}
	return nil
}

func (hs *HybridStore) Health(ctx context.Context) error {
	if r, ok := hs.tryRedis(); ok {
		if err := r.Health(ctx); err == nil {
			return nil
		}
		hs.onRedisFailure()
	}
	return nil
}

func (hs *HybridStore) Clear(ctx context.Context) error {
	if r, ok := hs.tryRedis(); ok {
		_ = r.Clear(ctx)
	}
	return hs.memory.Clear(ctx)
}

func (hs *HybridStore) CheckTokenBucket(ctx context.Context, key string, capacity int64, refillRate float64, ttl time.Duration) (BucketResult, error) {
	if r, ok := hs.tryRedis(); ok {
		res, err := r.CheckTokenBucket(ctx, key, capacity, refillRate, ttl)
		if err == nil {
			return res, nil
		}
		hs.onRedisFailure()
	}
	return hs.memory.CheckTokenBucket(ctx, key, capacity, refillRate, ttl)
}

func (hs *HybridStore) tryRedis() (*RedisStore, bool) {
	if hs.usingMem.Load() {
		return nil, false
	}
	hs.mu.RLock()
	r := hs.redis
	hs.mu.RUnlock()
	return r, r != nil
}

func (hs *HybridStore) onRedisFailure() {
	if hs.usingMem.CompareAndSwap(false, true) {
		slog.Warn("[storage] Redis error detected, switching to in-memory fallback")
		hs.startReconnect()
	}
}

func (hs *HybridStore) startReconnect() {
	if hs.reconnectOn.CompareAndSwap(false, true) {
		go hs.reconnectLoop()
	}
}

func (hs *HybridStore) reconnectLoop() {
	defer hs.reconnectOn.Store(false)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-hs.done:
			return
		case <-ticker.C:
			r, err := NewRedisStore(hs.cfg, hs.prefix)
			if err != nil {
				slog.Warn("[storage] Redis reconnect attempt failed", "error", err)
				continue
			}
			hs.mu.Lock()
			if hs.redis != nil {
				_ = hs.redis.Close()
			}
			hs.redis = r
			hs.mu.Unlock()
			hs.usingMem.Store(false)
			slog.Info("[storage] Redis reconnected successfully")
			return
		}
	}
}
