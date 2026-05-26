package storage

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sebastien-jorge/rate-limiter/internal/models"
)

var checkBucketScript = redis.NewScript(`
local key         = KEYS[1]
local capacity    = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local ttl_ms      = tonumber(ARGV[3])

local t = redis.call('TIME')
local now_ms = t[1] * 1000 + math.floor(t[2] / 1000)

local data = redis.call('HMGET', key, 'tokens', 'last_refill_ms')
local tokens         = tonumber(data[1])
local last_refill_ms = tonumber(data[2])

if tokens == nil then
    tokens = capacity
    last_refill_ms = now_ms
end

local elapsed_sec = (now_ms - last_refill_ms) / 1000.0
if elapsed_sec > 0 then
    tokens = math.min(capacity, tokens + elapsed_sec * refill_rate)
end

local allowed = 0
local retry_after_ms = 0
if tokens >= 1 then
    tokens = tokens - 1
    allowed = 1
else
    retry_after_ms = math.ceil((1 - tokens) / refill_rate * 1000)
end

local deficit = capacity - tokens
local reset_after_ms = 0
if deficit > 0 then
    reset_after_ms = math.ceil(deficit / refill_rate * 1000)
end

redis.call('HSET', key, 'tokens', tostring(tokens), 'last_refill_ms', tostring(now_ms))
redis.call('PEXPIRE', key, ttl_ms)

return {allowed, math.floor(tokens), retry_after_ms, reset_after_ms}
`)

type RedisStore struct {
	client *redis.Client
	prefix string
}

func NewRedisStore(cfg models.RedisConfig, prefix string) (*RedisStore, error) {
	opts := &redis.Options{
		Addr:     cfg.Addr(),
		DB:       cfg.DB,
		Password: cfg.Password,
		PoolSize: 10,
	}
	if cfg.TLS {
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

func (rs *RedisStore) Health(ctx context.Context) error {
	return rs.client.Ping(ctx).Err()
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

func (rs *RedisStore) CheckTokenBucket(ctx context.Context, key string, capacity int64, refillRate float64, ttl time.Duration) (BucketResult, error) {
	res, err := checkBucketScript.Run(ctx, rs.client,
		[]string{rs.key(key)},
		capacity,
		refillRate,
		ttl.Milliseconds(),
	).Slice()
	if err != nil {
		return BucketResult{}, fmt.Errorf("check_token_bucket script: %w", err)
	}
	if len(res) != 4 {
		return BucketResult{}, fmt.Errorf("check_token_bucket: unexpected result length %d", len(res))
	}
	return BucketResult{
		Allowed:    luaInt(res[0]) == 1,
		Remaining:  luaInt(res[1]),
		RetryAfter: time.Duration(luaInt(res[2])) * time.Millisecond,
		ResetAfter: time.Duration(luaInt(res[3])) * time.Millisecond,
	}, nil
}

// luaInt coerces a value returned by go-redis from a Lua script into int64.
// Lua integers come back as int64; strings (in case of large values) are parsed.
func luaInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}
