package storage

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	value     int64
	expiresAt time.Time
}

type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]entry
	done chan struct{}
}

func NewMemoryStore() *MemoryStore {
	ms := &MemoryStore{
		data: make(map[string]entry),
		done: make(chan struct{}),
	}
	go ms.cleanupLoop()
	return ms
}

func (ms *MemoryStore) Close() {
	close(ms.done)
}

func (ms *MemoryStore) Set(_ context.Context, key string, value int64, ttl time.Duration) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data[key] = entry{value: value, expiresAt: time.Now().Add(ttl)}
	return nil
}

func (ms *MemoryStore) Get(_ context.Context, key string) (int64, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	e, ok := ms.data[key]
	if !ok || time.Now().After(e.expiresAt) {
		return 0, nil
	}
	return e.value, nil
}

func (ms *MemoryStore) Increment(_ context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	var current int64
	if e, ok := ms.data[key]; ok && !time.Now().After(e.expiresAt) {
		current = e.value
	}

	newVal := current + delta
	ms.data[key] = entry{value: newVal, expiresAt: time.Now().Add(ttl)}
	return newVal, nil
}

func (ms *MemoryStore) Delete(_ context.Context, key string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	delete(ms.data, key)
	return nil
}

func (ms *MemoryStore) Exists(_ context.Context, key string) (bool, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	e, ok := ms.data[key]
	if !ok {
		return false, nil
	}
	return !time.Now().After(e.expiresAt), nil
}

func (ms *MemoryStore) Clear(_ context.Context) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data = make(map[string]entry)
	return nil
}

func (ms *MemoryStore) Health(_ context.Context) error {
	return nil
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
	for key, e := range ms.data {
		if now.After(e.expiresAt) {
			delete(ms.data, key)
		}
	}
}
