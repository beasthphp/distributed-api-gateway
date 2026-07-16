package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/beasthphp/distributed-api-gateway/internal/auth"
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
