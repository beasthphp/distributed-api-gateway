package ratelimit

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestTokenBucketRejectsInvalidPolicy(t *testing.T) {
	limiter := NewRedisLimiter(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "test")
	if _, err := limiter.Allow(context.Background(), Request{Identity: "key", Route: "/api/users"}); err == nil {
		t.Fatal("Allow() error = nil, want invalid policy error")
	}
}

func TestTokenBucketIsAtomicAcrossInstances(t *testing.T) {
	address := os.Getenv("REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}

	clients := make([]*redis.Client, 5)
	limiters := make([]*RedisLimiter, 5)
	for i := range clients {
		clients[i] = redis.NewClient(&redis.Options{Addr: address, DB: 15})
		limiters[i] = NewRedisLimiter(clients[i], "integration")
		defer clients[i].Close()
	}
	if err := clients[0].FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("FlushDB() error = %v", err)
	}

	request := Request{Identity: "shared-key", Route: "/api/users", RatePerSecond: 1, BurstCapacity: 10}
	start := make(chan struct{})
	var allowed atomic.Int64
	var wait sync.WaitGroup
	for i := 0; i < 50; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			decision, err := limiters[index%len(limiters)].Allow(context.Background(), request)
			if err != nil {
				t.Errorf("Allow() error = %v", err)
				return
			}
			if decision.Allowed {
				allowed.Add(1)
			}
		}(i)
	}
	close(start)
	wait.Wait()

	if got := allowed.Load(); got != request.BurstCapacity {
		t.Fatalf("allowed = %d, want exactly %d", got, request.BurstCapacity)
	}
}

func TestTokenBucketSeparatesRoutes(t *testing.T) {
	address := os.Getenv("REDIS_TEST_ADDR")
	if address == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: 15})
	defer client.Close()
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("FlushDB() error = %v", err)
	}
	limiter := NewRedisLimiter(client, "routes")

	for _, route := range []string{"/api/users", "/api/orders"} {
		decision, err := limiter.Allow(context.Background(), Request{
			Identity: "same-key", Route: route, RatePerSecond: 1, BurstCapacity: 1,
		})
		if err != nil {
			t.Fatalf("Allow(%s) error = %v", route, err)
		}
		if !decision.Allowed {
			t.Fatalf("first request to %s was denied", route)
		}
	}
}
