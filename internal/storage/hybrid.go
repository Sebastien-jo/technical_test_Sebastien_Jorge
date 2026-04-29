package storage

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type redisCfg struct {
	host, prefix, password string
	port, db               int
	tlsEnabled             bool
}

type HybridStore struct {
	memory *MemoryStore
	cfg    redisCfg

	mu    sync.RWMutex
	redis *RedisStore

	usingMem    atomic.Bool
	reconnectOn atomic.Bool
	done        chan struct{}
}

func NewHybridStore(host string, port, db int, password string, tlsEnabled bool, prefix string) (*HybridStore, error) {
	cfg := redisCfg{host: host, port: port, db: db, password: password, tlsEnabled: tlsEnabled, prefix: prefix}
	hs := &HybridStore{
		memory: NewMemoryStore(),
		cfg:    cfg,
		done:   make(chan struct{}),
	}

	r, err := NewRedisStore(host, port, db, password, tlsEnabled, prefix)
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

func (hs *HybridStore) Set(ctx context.Context, key string, value int64, ttl time.Duration) error {
	if r, ok := hs.tryRedis(); ok {
		if err := r.Set(ctx, key, value, ttl); err == nil {
			return nil
		}
		hs.onRedisFailure()
	}
	return hs.memory.Set(ctx, key, value, ttl)
}

func (hs *HybridStore) Get(ctx context.Context, key string) (int64, error) {
	if r, ok := hs.tryRedis(); ok {
		val, err := r.Get(ctx, key)
		if err == nil {
			return val, nil
		}
		hs.onRedisFailure()
	}
	return hs.memory.Get(ctx, key)
}

func (hs *HybridStore) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	if r, ok := hs.tryRedis(); ok {
		val, err := r.Increment(ctx, key, delta, ttl)
		if err == nil {
			return val, nil
		}
		hs.onRedisFailure()
	}
	return hs.memory.Increment(ctx, key, delta, ttl)
}

func (hs *HybridStore) Delete(ctx context.Context, key string) error {
	// Delete from both to keep them consistent when switching modes.
	if r, ok := hs.tryRedis(); ok {
		_ = r.Delete(ctx, key)
	}
	return hs.memory.Delete(ctx, key)
}

func (hs *HybridStore) Exists(ctx context.Context, key string) (bool, error) {
	if r, ok := hs.tryRedis(); ok {
		exists, err := r.Exists(ctx, key)
		if err == nil {
			return exists, nil
		}
		hs.onRedisFailure()
	}
	return hs.memory.Exists(ctx, key)
}

func (hs *HybridStore) Clear(ctx context.Context) error {
	if r, ok := hs.tryRedis(); ok {
		_ = r.Clear(ctx)
	}
	return hs.memory.Clear(ctx)
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
			r, err := NewRedisStore(hs.cfg.host, hs.cfg.port, hs.cfg.db, hs.cfg.password, hs.cfg.tlsEnabled, hs.cfg.prefix)
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
