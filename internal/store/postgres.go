package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/beasthphp/distributed-api-gateway/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Postgres struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	config.MinConns = 1
	config.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (p *Postgres) Migrate(ctx context.Context) error {
	files, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)

	if _, err := p.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, path := range files {
		if err := p.applyMigration(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) applyMigration(ctx context.Context, path string) error {
	content, err := migrationFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", path, err)
	}
	version := strings.TrimSuffix(strings.TrimPrefix(path, "migrations/"), ".sql")

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(7261746567617465)); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	var applied bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %s: %w", version, err)
	}
	if applied {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES ($1)", version); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	return nil
}

func (p *Postgres) LookupActiveKey(ctx context.Context, digest []byte, route string) (auth.Principal, error) {
	const query = `
		SELECT
			k.id::text,
			c.id::text,
			c.name,
			pl.slug,
			COALESCE(route_quota.rate_per_second, pl.rate_per_second),
			COALESCE(route_quota.burst_capacity, pl.burst_capacity)
		FROM api_keys k
		JOIN clients c ON c.id = k.client_id
		JOIN plans pl ON pl.id = c.plan_id
		LEFT JOIN LATERAL (
			SELECT q.rate_per_second, q.burst_capacity
			FROM client_route_quotas q
			WHERE q.client_id = c.id AND $2 LIKE q.route_prefix || '%'
			ORDER BY length(q.route_prefix) DESC
			LIMIT 1
		) route_quota ON true
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL AND c.active = true
	`
	var principal auth.Principal
	err := p.pool.QueryRow(ctx, query, digest, route).Scan(
		&principal.KeyID,
		&principal.ClientID,
		&principal.ClientName,
		&principal.Plan,
		&principal.RatePerSecond,
		&principal.BurstCapacity,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, auth.ErrInvalidKey
	}
	if err != nil {
		return auth.Principal{}, fmt.Errorf("lookup API key: %w", err)
	}
	return principal, nil
}

func (p *Postgres) UpsertPlan(ctx context.Context, slug, name string, rate, burst int64) (string, error) {
	var id string
	err := p.pool.QueryRow(ctx, `
		INSERT INTO plans(slug, name, rate_per_second, burst_capacity)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (slug) DO UPDATE SET
			name = EXCLUDED.name,
			rate_per_second = EXCLUDED.rate_per_second,
			burst_capacity = EXCLUDED.burst_capacity,
			updated_at = now()
		RETURNING id::text
	`, slug, name, rate, burst).Scan(&id)
	return id, err
}

func (p *Postgres) CreateClient(ctx context.Context, name, planSlug string) (string, error) {
	var id string
	err := p.pool.QueryRow(ctx, `
		INSERT INTO clients(name, plan_id)
		SELECT $1, id FROM plans WHERE slug = $2
		RETURNING id::text
	`, name, planSlug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("plan %q does not exist", planSlug)
	}
	return id, err
}

func (p *Postgres) EnsureClient(ctx context.Context, name, planSlug string) (string, error) {
	var id string
	err := p.pool.QueryRow(ctx, `
		SELECT c.id::text
		FROM clients c JOIN plans p ON p.id = c.plan_id
		WHERE c.name = $1 AND p.slug = $2
		ORDER BY c.created_at LIMIT 1
	`, name, planSlug).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return p.CreateClient(ctx, name, planSlug)
}

func (p *Postgres) CreateAPIKey(ctx context.Context, clientID, name, prefix string, digest []byte) (string, error) {
	var id string
	err := p.pool.QueryRow(ctx, `
		INSERT INTO api_keys(client_id, name, key_prefix, key_hash)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING id::text
	`, clientID, name, prefix, digest).Scan(&id)
	return id, err
}

func (p *Postgres) EnsureAPIKey(ctx context.Context, clientID, name, prefix string, digest []byte) (string, error) {
	var id string
	err := p.pool.QueryRow(ctx, `
		INSERT INTO api_keys(client_id, name, key_prefix, key_hash)
		VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (key_hash) DO UPDATE SET revoked_at = NULL
		RETURNING id::text
	`, clientID, name, prefix, digest).Scan(&id)
	return id, err
}

func (p *Postgres) SetRouteQuota(ctx context.Context, clientID, route string, rate, burst int64) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO client_route_quotas(client_id, route_prefix, rate_per_second, burst_capacity)
		VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (client_id, route_prefix) DO UPDATE SET
			rate_per_second = EXCLUDED.rate_per_second,
			burst_capacity = EXCLUDED.burst_capacity,
			updated_at = now()
	`, clientID, route, rate, burst)
	return err
}

func (p *Postgres) RevokeKey(ctx context.Context, prefix string) (int64, error) {
	result, err := p.pool.Exec(ctx, `
		UPDATE api_keys SET revoked_at = now()
		WHERE key_prefix = $1 AND revoked_at IS NULL
	`, prefix)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
