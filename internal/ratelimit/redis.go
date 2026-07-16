package ratelimit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const microToken = int64(1_000_000)

type Request struct {
	Identity      string
	Route         string
	RatePerSecond int64
	BurstCapacity int64
}

type Decision struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	ResetAt    time.Time
	RetryAfter time.Duration
}

type Limiter interface {
	Allow(context.Context, Request) (Decision, error)
}

type HealthChecker interface {
	Ping(context.Context) error
}

// RedisLimiter implements a distributed token bucket. Redis TIME provides the
// clock so gateway hosts cannot disagree because of local clock skew.
type RedisLimiter struct {
	client *redis.Client
	prefix string
	now    func() time.Time
}

var tokenBucketScript = redis.NewScript(`
local now_parts = redis.call("TIME")
local now_ms = (now_parts[1] * 1000) + math.floor(now_parts[2] / 1000)
local capacity = tonumber(ARGV[1])
local refill_per_ms = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local bucket = redis.call("HMGET", KEYS[1], "tokens", "updated_at_ms")
local tokens = tonumber(bucket[1])
local updated_at_ms = tonumber(bucket[2])

if tokens == nil or updated_at_ms == nil then
  tokens = capacity
  updated_at_ms = now_ms
else
  local elapsed = math.max(0, now_ms - updated_at_ms)
  tokens = math.min(capacity, tokens + (elapsed * refill_per_ms))
  updated_at_ms = now_ms
end

local allowed = 0
if tokens >= cost then
  allowed = 1
  tokens = tokens - cost
end

local retry_ms = 0
if allowed == 0 then
  retry_ms = math.ceil((cost - tokens) / refill_per_ms)
end
local full_ms = math.ceil((capacity - tokens) / refill_per_ms)

redis.call("HSET", KEYS[1], "tokens", math.floor(tokens), "updated_at_ms", updated_at_ms)
redis.call("PEXPIRE", KEYS[1], ttl_ms)
return {allowed, math.floor(tokens), retry_ms, full_ms}
`)

func NewRedisLimiter(client *redis.Client, prefix string) *RedisLimiter {
	return &RedisLimiter{client: client, prefix: prefix, now: time.Now}
}

func (l *RedisLimiter) Allow(ctx context.Context, request Request) (Decision, error) {
	if l.client == nil {
		return Decision{}, fmt.Errorf("redis client is nil")
	}
	if request.Identity == "" || request.Route == "" {
		return Decision{}, fmt.Errorf("rate-limit identity and route are required")
	}
	if request.RatePerSecond <= 0 || request.BurstCapacity <= 0 {
		return Decision{}, fmt.Errorf("rate-limit policy must be positive")
	}

	digest := sha256.Sum256([]byte(request.Identity + "\x00" + request.Route))
	key := fmt.Sprintf("%s:%x", l.prefix, digest[:16])
	capacity := request.BurstCapacity * microToken
	refillPerMillisecond := request.RatePerSecond * (microToken / 1000)
	ttlMilliseconds := ((request.BurstCapacity*1000)/request.RatePerSecond)*2 + 1000

	result, err := tokenBucketScript.Run(
		ctx,
		l.client,
		[]string{key},
		capacity,
		refillPerMillisecond,
		microToken,
		ttlMilliseconds,
	).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("execute token-bucket script: %w", err)
	}
	if len(result) != 4 {
		return Decision{}, fmt.Errorf("unexpected token-bucket result length: %d", len(result))
	}

	retryAfter := time.Duration(result[2]) * time.Millisecond
	return Decision{
		Allowed:    result[0] == 1,
		Limit:      request.BurstCapacity,
		Remaining:  result[1] / microToken,
		ResetAt:    l.now().Add(time.Duration(result[3]) * time.Millisecond),
		RetryAfter: retryAfter,
	}, nil
}

func (l *RedisLimiter) Ping(ctx context.Context) error {
	if l.client == nil {
		return fmt.Errorf("redis client is nil")
	}
	return l.client.Ping(ctx).Err()
}
