# API-key and quota operations

The `gateway-admin` binary is the only Phase 2 administration surface. It uses the same `DATABASE_URL` and `API_KEY_PEPPER` as the gateway.

## Development bootstrap

Docker Compose runs this automatically:

```bash
go run ./cmd/gateway-admin bootstrap
```

It applies migrations, creates the `developer` plan, creates a local client, stores the digest of `DEV_API_KEY`, and gives `/api/orders` a smaller demonstration quota.

## Create a plan and client

```bash
go run ./cmd/gateway-admin create-plan \
  --slug standard --name "Standard" --rate 20 --burst 40

go run ./cmd/gateway-admin create-client \
  --name "Placement Demo" --plan standard
```

Save the returned client UUID.

## Issue a key

```bash
go run ./cmd/gateway-admin create-key \
  --client CLIENT_UUID --name "Primary key"
```

The command prints the raw key exactly once. Send it through a secure channel and store it in a secret manager. PostgreSQL receives only its safe prefix and HMAC digest.

## Override one route

```bash
go run ./cmd/gateway-admin set-route-quota \
  --client CLIENT_UUID --route /api/orders --rate 5 --burst 10
```

The longest matching route prefix wins; otherwise the plan default applies.

## Revoke a key

```bash
go run ./cmd/gateway-admin revoke-key \
  --prefix gw_live_DISPLAYED
```

Revocation is checked in PostgreSQL on every request and therefore has no gateway-cache delay. Redis buckets use the internal key UUID, so a replacement key begins with an independent bucket.

## Production rules

- Generate a high-entropy `API_KEY_PEPPER`; do not reuse the development value.
- Keep the pepper outside PostgreSQL and outside source control.
- Use TLS for PostgreSQL connections outside a private local network.
- Restrict the admin binary to operators; do not expose it as a public endpoint.
- Back up PostgreSQL and test key revocation during deployment verification.
- Use `docker compose --env-file .env.production -f compose.production.yaml run --rm gateway-admin COMMAND` on the VPS; production startup runs `migrate`, never `bootstrap`.
