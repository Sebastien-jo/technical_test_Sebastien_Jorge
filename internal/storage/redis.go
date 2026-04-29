package storage

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// incrWithExpire is a Lua script that atomically increments a key by delta
// and sets its TTL in milliseconds. Unlike a pipeline, a Lua script runs
// as a single unit on the Redis server — no other command can interleave.
var incrWithExpire = redis.NewScript(`
local v = redis.call('INCRBY', KEYS[1], ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return v
`)

type RedisStore struct {
	client *redis.Client
	prefix string
}

func NewRedisStore(host string, port, db int, password string, tlsEnabled bool, prefix string) (*RedisStore, error) {
	opts := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		DB:       db,
		Password: password,
		PoolSize: 10,
	}
	if tlsEnabled {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return &RedisStore{client: client, prefix: prefix}, nil
}

func (rs *RedisStore) Close() error {
	return rs.client.Close()
}

func (rs *RedisStore) key(k string) string { return rs.prefix + k }

func (rs *RedisStore) Set(ctx context.Context, key string, value int64, ttl time.Duration) error {
	return rs.client.Set(ctx, rs.key(key), value, ttl).Err()
}

func (rs *RedisStore) Get(ctx context.Context, key string) (int64, error) {
	val, err := rs.client.Get(ctx, rs.key(key)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

func (rs *RedisStore) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	result, err := incrWithExpire.Run(ctx, rs.client,
		[]string{rs.key(key)},
		delta,
		ttl.Milliseconds(),
	).Int64()
	if err != nil {
		return 0, fmt.Errorf("increment script: %w", err)
	}
	return result, nil
}

func (rs *RedisStore) Delete(ctx context.Context, key string) error {
	return rs.client.Del(ctx, rs.key(key)).Err()
}

func (rs *RedisStore) Exists(ctx context.Context, key string) (bool, error) {
	n, err := rs.client.Exists(ctx, rs.key(key)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (rs *RedisStore) Clear(ctx context.Context) error {
	var cursor uint64
	for {
		keys, next, err := rs.client.Scan(ctx, cursor, rs.prefix+"*", 100).Result()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if len(keys) > 0 {
			if err := rs.client.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("del: %w", err)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return nil
}

func (rs *RedisStore) Health(ctx context.Context) error {
	return rs.client.Ping(ctx).Err()
}
