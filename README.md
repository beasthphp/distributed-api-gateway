# Distributed API Gateway

A production-style API gateway built in Go to demonstrate backend engineering, distributed rate limiting, reverse proxying, observability, and containerized service deployment.

> Phase 1 is a runnable foundation, not a claim of production readiness. The roadmap adds persistence, asynchronous usage logging, dashboards, deployment hardening, and benchmarking.

## What is implemented

- API-key authentication using constant-time comparison
- Atomic, per-key Redis fixed-window rate limiting
- Reverse-proxy routing to user and order microservices
- Request IDs propagated to upstream services
- JSON structured gateway logs
- Liveness and Redis-backed readiness checks
- Prometheus-compatible metrics endpoint
- Fail-closed rate-limiter behavior by default
- Graceful shutdown and bounded HTTP server timeouts
- Docker Compose development environment
- Unit tests, race-detector CI, vet, formatting, and image build checks

## Architecture

```mermaid
flowchart TD
    C[Client] --> G[Go API Gateway]
    G --> A[API-key authentication]
    A --> R[Redis rate limiter]
    R --> U[User service]
    R --> O[Order service]
    G --> M[Prometheus metrics]
```

The gateway exposes one entry point while authentication and rate-limit policy stay centralized. Redis makes counters consistent when multiple gateway replicas are added later.

## Quick start

Requirements: Docker with the Compose plugin.

```bash
cp .env.example .env
docker compose up --build -d
```

Verify the stack:

```bash
curl http://localhost:8080/health/live
curl http://localhost:8080/health/ready
curl -H "X-API-Key: dev-key-change-me" http://localhost:8080/api/users
curl -H "X-API-Key: dev-key-change-me" http://localhost:8080/api/orders/101
```

Prometheus is available at `http://localhost:9090`; raw gateway metrics are at `http://localhost:8080/metrics`.

Stop the stack:

```bash
docker compose down
```

## Request flow

1. The gateway assigns or accepts an `X-Request-ID`.
2. Public health and metrics endpoints bypass API authentication.
3. Protected `/api/*` routes validate `X-API-Key`.
4. A Redis Lua script atomically increments the key's counter and sets its expiry.
5. Accepted requests are routed to the relevant upstream service.
6. The response includes rate-limit and request-tracing headers.
7. Metrics and a structured completion log record the outcome.

When the quota is exhausted, the gateway returns HTTP `429` with `Retry-After`, `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset` headers.

## Routes

| Method | Route | Authentication | Purpose |
|---|---|---:|---|
| `GET` | `/health/live` | No | Process liveness |
| `GET` | `/health/ready` | No | Redis-backed readiness |
| `GET` | `/metrics` | No | Prometheus scrape endpoint |
| `GET` | `/api/users` | API key | List mock users |
| `GET` | `/api/users/{id}` | API key | Get a mock user |
| `GET` | `/api/orders` | API key | List mock orders |
| `GET` | `/api/orders/{id}` | API key | Get a mock order |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Gateway listen address |
| `USER_SERVICE_URL` | `http://localhost:8081` | User-service origin |
| `ORDER_SERVICE_URL` | `http://localhost:8082` | Order-service origin |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | empty | Redis password |
| `REDIS_DB` | `0` | Redis database number |
| `API_KEYS` | `dev-key-change-me` | Comma-separated accepted keys |
| `RATE_LIMIT_REQUESTS` | `100` | Requests allowed per window |
| `RATE_LIMIT_WINDOW` | `1m` | Fixed-window duration |
| `RATE_LIMIT_FAIL_OPEN` | `false` | Allow traffic when Redis fails |

Never use the development API key in a public deployment. Put production secrets in the VPS secret environment rather than the repository or Compose file.

## Local development without Docker

Start Redis, then run each process in a separate terminal:

```bash
go run ./cmd/user-service
go run ./cmd/order-service
go run ./cmd/gateway
```

Run verification:

```bash
go test -race ./...
go vet ./...
sh scripts/smoke-test.sh
```

## Repository layout

```text
cmd/                    runnable gateway and mock-service binaries
internal/config/        environment configuration
internal/gateway/       routing and middleware pipeline
internal/ratelimit/     atomic Redis limiter
internal/metrics/       bounded-cardinality Prometheus metrics
internal/mockservice/   demonstration upstream services
deploy/prometheus/      scrape configuration
docs/                   design decisions and roadmap
scripts/                smoke test
```

## Design decisions

- **Fixed window first:** easy to reason about and atomic in one Redis script. A token-bucket implementation is planned for smoother burst behavior.
- **Fail closed:** Redis failure returns `503` by default so quota enforcement is not silently bypassed.
- **Hashed Redis keys:** raw API keys are never stored as Redis key names.
- **Bounded metric labels:** response status is bounded; API keys and raw paths are never labels.
- **Thin gateway:** mock business data lives in upstream services, not in routing middleware.

See [docs/architecture.md](docs/architecture.md) for deeper trade-offs and [docs/roadmap.md](docs/roadmap.md) for the next milestones.

## Resume-ready description after completion

> Built a Go API gateway with API-key authentication, atomic Redis rate limiting, reverse-proxy routing, health checks, Prometheus metrics, containerized microservices, automated tests, and CI; designed for multi-instance deployment and production monitoring.

Only add later roadmap features to the resume after they are implemented and verified.
