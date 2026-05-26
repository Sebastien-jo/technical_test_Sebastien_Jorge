package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/sebastien-jorge/rate-limiter/internal/models"
	"github.com/sebastien-jorge/rate-limiter/internal/storage"
)

type RateLimiter struct {
	store storage.Store
}

func NewRateLimiter(store storage.Store) *RateLimiter {
	return &RateLimiter{store: store}
}

func (rl *RateLimiter) Check(ctx context.Context, clientID string, policy *models.RoutePolicy, identifier string) *models.Decision {
	if identifier == "" {
		identifier = "global"
	}

	key := rl.buildKey(clientID, policy, identifier)
	refillRate := float64(policy.Limit) / policy.Window.Seconds()
	ttl := 2 * policy.Window

	res, err := rl.store.CheckTokenBucket(ctx, key, policy.Limit, refillRate, ttl)
	if err != nil {
		slog.Error("rate limiter storage failure, failing open", "key", key, "error", err)
		return &models.Decision{
			Allowed:   true,
			Remaining: policy.Limit,
			ResetTime: time.Now().Add(policy.Window),
		}
	}

	decision := &models.Decision{
		Allowed:   res.Allowed,
		Remaining: res.Remaining,
		ResetTime: time.Now().Add(res.ResetAfter),
	}
	if !res.Allowed {
		decision.RetryAfter = res.RetryAfter
		decision.Message = "rate limit exceeded"
	}
	return decision
}

func (rl *RateLimiter) buildKey(clientID string, policy *models.RoutePolicy, identifier string) string {
	return models.QuotaKey{
		ClientID:   clientID,
		Route:      policy.Route,
		Method:     policy.Method,
		Identifier: identifier,
	}.String()
}
