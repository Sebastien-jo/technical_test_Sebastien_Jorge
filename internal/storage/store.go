package storage

import (
	"context"
	"time"
)

// Store is the persistence contract for the rate limiter. Implementations must
// be safe under concurrent callers. CheckTokenBucket is the only operation on
// the request hot path; Health and Clear exist for liveness checks and tests.
type Store interface {
	CheckTokenBucket(ctx context.Context, key string, capacity int64, refillRate float64, ttl time.Duration) (BucketResult, error)
	Health(ctx context.Context) error
	Clear(ctx context.Context) error
}

// BucketResult is the outcome of a CheckTokenBucket call.
// Remaining is the integer floor of tokens left after the operation.
// RetryAfter is non-zero only when Allowed is false.
// ResetAfter is the duration until the bucket would be full again.
type BucketResult struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
	ResetAfter time.Duration
}
