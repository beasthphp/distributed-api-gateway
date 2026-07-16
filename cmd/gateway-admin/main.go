package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/beasthphp/distributed-api-gateway/internal/auth"
	"github.com/beasthphp/distributed-api-gateway/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL := env("DATABASE_URL", "postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable")
	postgresStore, err := store.Open(ctx, databaseURL)
	if err != nil {
		fatal(err)
	}
	defer postgresStore.Close()

	switch os.Args[1] {
	case "migrate":
		err = postgresStore.Migrate(ctx)
	case "bootstrap":
		err = bootstrap(ctx, postgresStore)
	case "create-plan":
		err = createPlan(ctx, postgresStore, os.Args[2:])
	case "create-client":
		err = createClient(ctx, postgresStore, os.Args[2:])
	case "create-key":
		err = createKey(ctx, postgresStore, os.Args[2:])
	case "revoke-key":
		err = revokeKey(ctx, postgresStore, os.Args[2:])
	case "set-route-quota":
		err = setRouteQuota(ctx, postgresStore, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func bootstrap(ctx context.Context, postgresStore *store.Postgres) error {
	if err := postgresStore.Migrate(ctx); err != nil {
		return err
	}
	if _, err := postgresStore.UpsertPlan(ctx, "developer", "Developer", 5, 10); err != nil {
		return fmt.Errorf("upsert developer plan: %w", err)
	}
	clientID, err := postgresStore.EnsureClient(ctx, "Local Development", "developer")
	if err != nil {
		return fmt.Errorf("ensure development client: %w", err)
	}
	pepper, err := apiKeyPepper()
	if err != nil {
		return err
	}
	rawKey := env("DEV_API_KEY", "dev-key-change-me")
	keyID, err := postgresStore.EnsureAPIKey(ctx, clientID, "Local development key", auth.Prefix(rawKey), auth.Digest([]byte(pepper), rawKey))
	if err != nil {
		return fmt.Errorf("ensure development API key: %w", err)
	}
	if err := postgresStore.SetRouteQuota(ctx, clientID, "/api/orders", 2, 4); err != nil {
		return fmt.Errorf("set development route quota: %w", err)
	}
	fmt.Printf("bootstrap complete: client_id=%s key_id=%s\n", clientID, keyID)
	return nil
}

func createPlan(ctx context.Context, postgresStore *store.Postgres, arguments []string) error {
	flags := flag.NewFlagSet("create-plan", flag.ContinueOnError)
	slug := flags.String("slug", "", "stable plan slug")
	name := flags.String("name", "", "display name")
	rate := flags.Int64("rate", 0, "refill rate in requests per second")
	burst := flags.Int64("burst", 0, "maximum burst capacity")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *slug == "" || *name == "" || *rate <= 0 || *burst <= 0 {
		return fmt.Errorf("--slug, --name, positive --rate, and positive --burst are required")
	}
	id, err := postgresStore.UpsertPlan(ctx, *slug, *name, *rate, *burst)
	if err == nil {
		fmt.Printf("plan_id=%s\n", id)
	}
	return err
}

func createClient(ctx context.Context, postgresStore *store.Postgres, arguments []string) error {
	flags := flag.NewFlagSet("create-client", flag.ContinueOnError)
	name := flags.String("name", "", "client name")
	plan := flags.String("plan", "", "existing plan slug")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *name == "" || *plan == "" {
		return fmt.Errorf("--name and --plan are required")
	}
	id, err := postgresStore.CreateClient(ctx, *name, *plan)
	if err == nil {
		fmt.Printf("client_id=%s\n", id)
	}
	return err
}

func createKey(ctx context.Context, postgresStore *store.Postgres, arguments []string) error {
	flags := flag.NewFlagSet("create-key", flag.ContinueOnError)
	clientID := flags.String("client", "", "client UUID")
	name := flags.String("name", "", "key name")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *clientID == "" || *name == "" {
		return fmt.Errorf("--client and --name are required")
	}
	rawKey, prefix, err := auth.GenerateKey()
	if err != nil {
		return err
	}
	pepper, err := apiKeyPepper()
	if err != nil {
		return err
	}
	keyID, err := postgresStore.CreateAPIKey(ctx, *clientID, *name, prefix, auth.Digest([]byte(pepper), rawKey))
	if err != nil {
		return err
	}
	fmt.Printf("key_id=%s\nkey_prefix=%s\napi_key=%s\n", keyID, prefix, rawKey)
	fmt.Println("Save api_key now. It cannot be recovered from the database.")
	return nil
}

func revokeKey(ctx context.Context, postgresStore *store.Postgres, arguments []string) error {
	flags := flag.NewFlagSet("revoke-key", flag.ContinueOnError)
	prefix := flags.String("prefix", "", "exact displayed key prefix")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *prefix == "" {
		return fmt.Errorf("--prefix is required")
	}
	count, err := postgresStore.RevokeKey(ctx, *prefix)
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("no active API key found for prefix %q", *prefix)
	}
	fmt.Printf("revoked=%d\n", count)
	return nil
}

func setRouteQuota(ctx context.Context, postgresStore *store.Postgres, arguments []string) error {
	flags := flag.NewFlagSet("set-route-quota", flag.ContinueOnError)
	clientID := flags.String("client", "", "client UUID")
	route := flags.String("route", "", "route prefix such as /api/orders")
	rate := flags.Int64("rate", 0, "refill rate in requests per second")
	burst := flags.Int64("burst", 0, "maximum burst capacity")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *clientID == "" || *route == "" || *rate <= 0 || *burst <= 0 {
		return fmt.Errorf("--client, --route, positive --rate, and positive --burst are required")
	}
	return postgresStore.SetRouteQuota(ctx, *clientID, *route, *rate, *burst)
}

func usage() {
	fmt.Fprintln(os.Stderr, "gateway-admin commands:")
	fmt.Fprintln(os.Stderr, "  migrate")
	fmt.Fprintln(os.Stderr, "  bootstrap")
	fmt.Fprintln(os.Stderr, "  create-plan --slug SLUG --name NAME --rate N --burst N")
	fmt.Fprintln(os.Stderr, "  create-client --name NAME --plan SLUG")
	fmt.Fprintln(os.Stderr, "  create-key --client UUID --name NAME")
	fmt.Fprintln(os.Stderr, "  revoke-key --prefix PREFIX")
	fmt.Fprintln(os.Stderr, "  set-route-quota --client UUID --route /api/orders --rate N --burst N")
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func apiKeyPepper() (string, error) {
	pepper := env("API_KEY_PEPPER", "dev-only-pepper-change-me")
	if len(pepper) < 16 {
		return "", fmt.Errorf("API_KEY_PEPPER must be at least 16 characters")
	}
	return pepper, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
