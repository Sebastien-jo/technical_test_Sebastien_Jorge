package service

import (
	"sync"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
)

type RateLimiter struct {
	mu      sync.RWMutex
	buckets map[string]*TokenBucket
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*TokenBucket),
	}
}

func (rl *RateLimiter) Check(clientID string, policy *models.RoutePolicy, identifier string) *models.Decision {
	if identifier == "" {
		identifier = "global"
	}

	key := rl.buildKey(clientID, policy, identifier)
	bucket := rl.getOrCreate(key, policy)

	allowed, remaining, retryAfter := bucket.Allow(1)

	decision := &models.Decision{
		Allowed:   allowed,
		Remaining: remaining,
		ResetTime: bucket.GetResetTime(),
	}
	if !allowed {
		decision.RetryAfter = retryAfter
		decision.Message = "rate limit exceeded"
	}
	return decision
}

func (rl *RateLimiter) GetStats() map[string]int64 {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	stats := make(map[string]int64, len(rl.buckets))
	for key, bucket := range rl.buckets {
		stats[key] = bucket.GetRemaining()
	}
	return stats
}

// Reset clears all in-memory buckets. Intended for tests only.
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.buckets = make(map[string]*TokenBucket)
}

func (rl *RateLimiter) getOrCreate(key string, policy *models.RoutePolicy) *TokenBucket {
	rl.mu.RLock()
	if bucket, ok := rl.buckets[key]; ok {
		rl.mu.RUnlock()
		return bucket
	}
	rl.mu.RUnlock()

	rl.mu.Lock()
	defer rl.mu.Unlock()
	// Re-check: another goroutine may have created the bucket between the two locks.
	if bucket, ok := rl.buckets[key]; ok {
		return bucket
	}
	bucket := NewTokenBucket(policy.Limit, policy.Window)
	rl.buckets[key] = bucket
	return bucket
}

func (rl *RateLimiter) buildKey(clientID string, policy *models.RoutePolicy, identifier string) string {
	return models.QuotaKey{
		ClientID:   clientID,
		Route:      policy.Route,
		Method:     policy.Method,
		Identifier: identifier,
	}.String()
}
