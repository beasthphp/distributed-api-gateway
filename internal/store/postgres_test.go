package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/beasthphp/distributed-api-gateway/internal/auth"
	"github.com/beasthphp/distributed-api-gateway/internal/usage"
)

func TestPostgresKeyLifecycleAndRouteQuota(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	postgresStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer postgresStore.Close()
	if err := postgresStore.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := postgresStore.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	suffix := time.Now().UnixNano()
	plan := fmt.Sprintf("test_%d", suffix)
	if _, err := postgresStore.UpsertPlan(ctx, plan, "Integration Test", 10, 20); err != nil {
		t.Fatalf("UpsertPlan() error = %v", err)
	}
	clientID, err := postgresStore.CreateClient(ctx, fmt.Sprintf("client-%d", suffix), plan)
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	rawKey, prefix, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	digest := auth.Digest([]byte("integration-test-pepper"), rawKey)
	if _, err := postgresStore.CreateAPIKey(ctx, clientID, "integration", prefix, digest); err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	principal, err := postgresStore.LookupActiveKey(ctx, digest, "/api/users")
	if err != nil {
		t.Fatalf("LookupActiveKey() error = %v", err)
	}
	if principal.RatePerSecond != 10 || principal.BurstCapacity != 20 {
		t.Fatalf("default policy = %d/%d, want 10/20", principal.RatePerSecond, principal.BurstCapacity)
	}
	secondPlan := fmt.Sprintf("small_%d", suffix)
	if _, err := postgresStore.UpsertPlan(ctx, secondPlan, "Small Integration Plan", 1, 2); err != nil {
		t.Fatalf("UpsertPlan(second) error = %v", err)
	}
	secondClient, err := postgresStore.CreateClient(ctx, fmt.Sprintf("second-%d", suffix), secondPlan)
	if err != nil {
		t.Fatalf("CreateClient(second) error = %v", err)
	}
	secondRaw, secondPrefix, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey(second) error = %v", err)
	}
	secondDigest := auth.Digest([]byte("integration-test-pepper"), secondRaw)
	if _, err := postgresStore.CreateAPIKey(ctx, secondClient, "second", secondPrefix, secondDigest); err != nil {
		t.Fatalf("CreateAPIKey(second) error = %v", err)
	}
	secondPrincipal, err := postgresStore.LookupActiveKey(ctx, secondDigest, "/api/users")
	if err != nil {
		t.Fatalf("LookupActiveKey(second plan) error = %v", err)
	}
	if secondPrincipal.RatePerSecond != 1 || secondPrincipal.BurstCapacity != 2 {
		t.Fatalf("second plan policy = %d/%d, want 1/2", secondPrincipal.RatePerSecond, secondPrincipal.BurstCapacity)
	}

	if err := postgresStore.SetRouteQuota(ctx, clientID, "/api/orders", 2, 4); err != nil {
		t.Fatalf("SetRouteQuota() error = %v", err)
	}
	principal, err = postgresStore.LookupActiveKey(ctx, digest, "/api/orders/123")
	if err != nil {
		t.Fatalf("LookupActiveKey(route quota) error = %v", err)
	}
	if principal.RatePerSecond != 2 || principal.BurstCapacity != 4 {
		t.Fatalf("route policy = %d/%d, want 2/4", principal.RatePerSecond, principal.BurstCapacity)
	}

	if count, err := postgresStore.RevokeKey(ctx, prefix); err != nil || count != 1 {
		t.Fatalf("RevokeKey() = %d, %v; want 1, nil", count, err)
	}
	if _, err := postgresStore.LookupActiveKey(ctx, digest, "/api/users"); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("revoked key lookup error = %v, want ErrInvalidKey", err)
	}
}

func TestPostgresUsagePersistenceIsIdempotent(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	postgresStore, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer postgresStore.Close()
	if err := postgresStore.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	suffix := time.Now().UnixNano()
	plan := fmt.Sprintf("usage_%d", suffix)
	if _, err := postgresStore.UpsertPlan(ctx, plan, "Usage Test", 10, 20); err != nil {
		t.Fatalf("UpsertPlan() error = %v", err)
	}
	clientID, err := postgresStore.CreateClient(ctx, fmt.Sprintf("usage-client-%d", suffix), plan)
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	rawKey, prefix, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	keyID, err := postgresStore.CreateAPIKey(ctx, clientID, "usage", prefix, auth.Digest([]byte("integration-test-pepper"), rawKey))
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	eventID, err := usage.NewEventID()
	if err != nil {
		t.Fatalf("NewEventID() error = %v", err)
	}
	event := usage.Event{
		ID: eventID, RequestID: "usage-request", APIKeyID: keyID, ClientID: clientID,
		Route: "/api/orders", Method: "GET", StatusCode: 429,
		DurationMicros: 2500, ResponseBytes: 64, OccurredAt: time.Now().UTC(),
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := postgresStore.PersistUsage(ctx, []usage.Event{event}); err != nil {
			t.Fatalf("PersistUsage(attempt %d) error = %v", attempt+1, err)
		}
	}

	var rawCount, aggregateCount, rateLimitedCount int64
	if err := postgresStore.pool.QueryRow(ctx, "SELECT count(*) FROM usage_events WHERE event_id = $1::uuid", event.ID).Scan(&rawCount); err != nil {
		t.Fatalf("count usage_events: %v", err)
	}
	if err := postgresStore.pool.QueryRow(ctx, `
		SELECT request_count, rate_limited_count FROM usage_hourly
		WHERE bucket_start = date_trunc('hour', $1::timestamptz)
			AND client_id = $2::uuid AND route = $3 AND status_code = $4
	`, event.OccurredAt, clientID, event.Route, event.StatusCode).Scan(&aggregateCount, &rateLimitedCount); err != nil {
		t.Fatalf("read usage_hourly: %v", err)
	}
	if rawCount != 1 || aggregateCount != 1 || rateLimitedCount != 1 {
		t.Fatalf("raw/aggregate/rate-limited = %d/%d/%d, want 1/1/1", rawCount, aggregateCount, rateLimitedCount)
	}

	deadLetterID, _ := usage.NewEventID()
	deadLetter := event
	deadLetter.ID = deadLetterID
	if err := postgresStore.PersistUsageDeadLetters(ctx, []usage.Event{deadLetter}, "permanent test error", 3); err != nil {
		t.Fatalf("PersistUsageDeadLetters() error = %v", err)
	}
	var deadLetterCount int64
	if err := postgresStore.pool.QueryRow(ctx, "SELECT count(*) FROM usage_dead_letters WHERE event_id = $1::uuid", deadLetter.ID).Scan(&deadLetterCount); err != nil {
		t.Fatalf("count usage_dead_letters: %v", err)
	}
	if deadLetterCount != 1 {
		t.Fatalf("dead-letter count = %d, want 1", deadLetterCount)
	}
}
