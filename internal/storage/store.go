package storage

import (
	"context"
	"time"
)

type Store interface {
	Set(ctx context.Context, key string, value int64, ttl time.Duration) error
	Get(ctx context.Context, key string) (int64, error)
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Clear(ctx context.Context) error
	Health(ctx context.Context) error
}
