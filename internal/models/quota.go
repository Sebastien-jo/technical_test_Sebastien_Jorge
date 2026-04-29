package models

import (
	"fmt"
	"time"
)

type QuotaID string

type QuotaKey struct {
	ClientID   string
	Route      string
	Method     string
	Identifier string
}

func (qk QuotaKey) String() string {
	return fmt.Sprintf("rl:%s:%s:%s:%s", qk.ClientID, qk.Method, qk.Route, qk.Identifier)
}

type TokenBucket struct {
	Capacity   int64
	Current    float64
	RefillRate float64
	LastRefill time.Time
}

func NewTokenBucket(limit int64, window time.Duration) *TokenBucket {
	return &TokenBucket{
		Capacity:   limit,
		Current:    float64(limit),
		RefillRate: float64(limit) / window.Seconds(),
		LastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Allow(tokensNeeded int64) (allowed bool, remaining int64, retryAfter time.Duration) {
	now := time.Now()
	elapsed := now.Sub(tb.LastRefill).Seconds()

	tb.Current = min(float64(tb.Capacity), tb.Current+elapsed*tb.RefillRate)
	tb.LastRefill = now

	if tb.Current >= float64(tokensNeeded) {
		tb.Current -= float64(tokensNeeded)
		return true, int64(tb.Current), 0
	}

	deficit := float64(tokensNeeded) - tb.Current
	wait := time.Duration(deficit / tb.RefillRate * float64(time.Second))
	return false, int64(tb.Current), wait
}

func (tb *TokenBucket) GetRemaining() int64 {
	return int64(tb.Current)
}

func (tb *TokenBucket) GetResetTime() time.Time {
	if tb.Current >= float64(tb.Capacity) {
		return time.Now()
	}
	deficit := float64(tb.Capacity) - tb.Current
	return time.Now().Add(time.Duration(deficit / tb.RefillRate * float64(time.Second)))
}

type QuotaTracker struct {
	ID         QuotaID
	ClientID   ClientID
	Route      string
	Method     string
	Identifier string
	Bucket     *TokenBucket
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (qt *QuotaTracker) Allow(tokensNeeded int64) (bool, int64, time.Duration) {
	allowed, remaining, retryAfter := qt.Bucket.Allow(tokensNeeded)
	qt.UpdatedAt = time.Now()
	return allowed, remaining, retryAfter
}
