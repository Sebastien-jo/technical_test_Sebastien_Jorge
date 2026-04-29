package service

import (
	"sync"
	"time"
)

type TokenBucket struct {
	mu         sync.Mutex
	capacity   int64
	tokens     float64
	refillRate float64
	lastRefill time.Time
}

func NewTokenBucket(limit int64, window time.Duration) *TokenBucket {
	return &TokenBucket{
		capacity:   limit,
		tokens:     float64(limit),
		refillRate: float64(limit) / window.Seconds(),
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Allow(tokensNeeded int64) (allowed bool, remaining int64, retryAfter time.Duration) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill(time.Now())

	if tb.tokens >= float64(tokensNeeded) {
		tb.tokens -= float64(tokensNeeded)
		return true, int64(tb.tokens), 0
	}

	deficit := float64(tokensNeeded) - tb.tokens
	wait := time.Duration(deficit / tb.refillRate * float64(time.Second))
	return false, int64(tb.tokens), wait
}

func (tb *TokenBucket) GetRemaining() int64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill(time.Now())
	return int64(tb.tokens)
}

func (tb *TokenBucket) GetResetTime() time.Time {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	tb.refill(now)

	if tb.tokens >= float64(tb.capacity) {
		return now
	}
	deficit := float64(tb.capacity) - tb.tokens
	return now.Add(time.Duration(deficit / tb.refillRate * float64(time.Second)))
}

func (tb *TokenBucket) refill(now time.Time) {
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens = min(float64(tb.capacity), tb.tokens+elapsed*tb.refillRate)
	tb.lastRefill = now
}
