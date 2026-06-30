package storage

import (
	"context"
	"math"
	"sync"
	"time"
)

type localBucket struct {
	tokens     float64
	lastRefill time.Time
	expiresAt  time.Time
}

type MemoryStore struct {
	mu      sync.Mutex
	buckets map[string]*localBucket
	done    chan struct{}
}

func NewMemoryStore() *MemoryStore {
	ms := &MemoryStore{
		buckets: make(map[string]*localBucket),
		done:    make(chan struct{}),
	}
	go ms.cleanupLoop()
	return ms
}

func (ms *MemoryStore) Close() {
	close(ms.done)
}

func (ms *MemoryStore) Health(_ context.Context) error {
	return nil
}

func (ms *MemoryStore) Clear(_ context.Context) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.buckets = make(map[string]*localBucket)
	return nil
}

func (ms *MemoryStore) CheckTokenBucket(_ context.Context, key string, capacity int64, refillRate float64, ttl time.Duration) (BucketResult, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := time.Now()
	b, ok := ms.buckets[key]
	if !ok || now.After(b.expiresAt) {
		b = &localBucket{tokens: float64(capacity), lastRefill: now}
		ms.buckets[key] = b
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(float64(capacity), b.tokens+elapsed*refillRate)
	}
	b.lastRefill = now
	b.expiresAt = now.Add(ttl)

	res := BucketResult{}
	if b.tokens >= 1 {
		b.tokens--
		res.Allowed = true
	} else {
		deficit := 1 - b.tokens
		res.RetryAfter = time.Duration(deficit / refillRate * float64(time.Second))
	}
	res.Remaining = int64(b.tokens)

	if deficit := float64(capacity) - b.tokens; deficit > 0 {
		res.ResetAfter = time.Duration(deficit / refillRate * float64(time.Second))
	}
	return res, nil
}

func (ms *MemoryStore) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ms.done:
			return
		case <-ticker.C:
			ms.purgeExpired()
		}
	}
}

func (ms *MemoryStore) purgeExpired() {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	now := time.Now()
	for key, b := range ms.buckets {
		if now.After(b.expiresAt) {
			delete(ms.buckets, key)
		}
	}
}
