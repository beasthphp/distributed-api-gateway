package ratelimit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Decision is the result of one distributed rate-limit check.
type Decision struct {
	Allowed   bool
	Limit     int64
	Remaining int64
	ResetAt   time.Time
}

// Limiter is implemented by RedisLimiter and by lightweight test doubles.
type Limiter interface {
	Allow(context.Context, string) (Decision, error)
}

// HealthChecker lets the readiness endpoint verify Redis independently of a
// request's rate-limit decision.
type HealthChecker interface {
	Ping(context.Context) error
}

// RedisLimiter uses one Lua script so INCR, first-expiry assignment, and TTL
// lookup execute atomically on Redis, even when multiple gateway instances run.
type RedisLimiter struct {
	client *redis.Client
	limit  int64
	window time.Duration
	prefix string
	now    func() time.Time
}

var fixedWindowScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {current, ttl}
`)

func NewRedisLimiter(client *redis.Client, limit int64, window time.Duration, prefix string) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		limit:  limit,
		window: window,
		prefix: prefix,
		now:    time.Now,
	}
}

func (l *RedisLimiter) Allow(ctx context.Context, identity string) (Decision, error) {
	if l.client == nil {
		return Decision{}, fmt.Errorf("redis client is nil")
	}

	digest := sha256.Sum256([]byte(identity))
	key := fmt.Sprintf("%s:%x", l.prefix, digest[:12])
	result, err := fixedWindowScript.Run(
		ctx,
		l.client,
		[]string{key},
		l.window.Milliseconds(),
	).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("execute rate-limit script: %w", err)
	}
	if len(result) != 2 {
		return Decision{}, fmt.Errorf("unexpected rate-limit result length: %d", len(result))
	}

	used, ttlMillis := result[0], result[1]
	if ttlMillis < 0 {
		ttlMillis = l.window.Milliseconds()
	}
	remaining := l.limit - used
	if remaining < 0 {
		remaining = 0
	}

	return Decision{
		Allowed:   used <= l.limit,
		Limit:     l.limit,
		Remaining: remaining,
		ResetAt:   l.now().Add(time.Duration(ttlMillis) * time.Millisecond),
	}, nil
}

func (l *RedisLimiter) Ping(ctx context.Context) error {
	if l.client == nil {
		return fmt.Errorf("redis client is nil")
	}
	return l.client.Ping(ctx).Err()
}
