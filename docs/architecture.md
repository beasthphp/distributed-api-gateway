# Architecture and design notes

## Current Phase 4 boundary

The current milestone places the authenticated request path behind a public TLS edge and adds a private monitoring plane:

```mermaid
flowchart LR
    I[Internet] -->|80/443| N[Nginx + Certbot webroot]
    N --> G[Go gateway]
    G --> R[(Redis)]
    G --> P[(PostgreSQL)]
    G -->|Tailscale/private network| U[User and order services]
    G -. metrics .-> M[Prometheus]
    R -. redis exporter .-> M
    P -. postgres exporter .-> M
    H[VPS node exporter] -.-> M
    B[Blackbox readiness probe] -.-> M
    M --> D[Grafana on 127.0.0.1]
    M --> A[Alert rules]
```

Nginx is the only service with public host bindings. Grafana has a loopback-only binding for SSH forwarding. The remaining services communicate on purpose-specific Docker networks without published ports.

The protected request and usage path remains:

```mermaid
sequenceDiagram
    participant C as Client
    participant G as Gateway
    participant P as PostgreSQL
    participant R as Redis
    participant S as Service
    participant Q as Usage queue
    participant W as Batch worker
    C->>G: Request + X-API-Key
    G->>G: HMAC supplied key
    G->>P: Active digest + route lookup
    P-->>G: Client, plan, effective quota
    G->>R: Atomic token-bucket Lua script
    R-->>G: Decision, tokens, retry time
    alt quota available
        G->>S: Proxied request + request ID
        S-->>G: Service response
        G-->>C: Response + quota headers
    else quota exceeded
        G-->>C: 429 + Retry-After
    end
    G->>Q: Non-blocking normalized usage event
    Note over G,Q: Full queue drops and counts the event
    W->>Q: Drain bounded batch
    W->>P: Idempotent events + hourly aggregates
```

## Components

### Gateway

The Go gateway is stateless. Configuration comes from environment variables, and shared quota state lives in Redis. This permits horizontal replication behind Nginx without each instance maintaining conflicting local counters.

Middleware responsibilities are separated:

1. Request ID creation and propagation
2. Panic recovery
3. Metrics and structured access logging
4. API-key authentication
5. Distributed rate limiting
6. Route selection and reverse proxying
7. Non-blocking usage emission after the outcome is known

### PostgreSQL authentication and policy

Plans define a default requests-per-second refill rate and burst capacity. Clients belong to a plan and may have longest-prefix route overrides. API keys contain only a display prefix and a 32-byte HMAC-SHA-256 digest; the raw value is returned once by the admin CLI and cannot be reconstructed from the database.

Revocation sets `revoked_at`. Authentication joins the active key, client, plan, and best matching route override in one query, so revocation takes effect on the next request without an in-memory cache window.

### Redis limiter

The token-bucket algorithm performs the following operations in one Lua script:

1. Read Redis server time.
2. Refill integer microtokens up to the configured burst capacity.
3. Consume one request token when available.
4. Persist the remaining tokens and timestamp with a bounded TTL.
5. Return remaining capacity and an exact retry delay.

Lua execution is atomic in Redis, preventing races between gateway processes. Buckets are isolated by API-key ID and normalized route. Integer microtokens avoid floating-point drift, and Redis server time avoids inconsistent decisions from host clock skew.

### Reverse proxy

Routes are selected by stable path prefixes. The gateway preserves the request path, sets the upstream host, forwards the request ID, and converts connection failures into a consistent `502` JSON response.

### Asynchronous usage pipeline

Authenticated request outcomes are copied into privacy-conscious events containing internal client/key IDs, normalized route, method, status, duration, byte count, and occurrence time. API keys, query strings, headers, and bodies never enter the pipeline.

Admission uses a non-blocking send to a bounded channel. A full channel increments a drop counter while leaving request latency unchanged. One worker drains batches by size or time, retries transient PostgreSQL failures with bounded exponential backoff, and writes terminal failures to `usage_dead_letters`.

Each event has a random UUID. PostgreSQL inserts raw events with a conflict guard and derives hourly aggregate changes only from rows inserted by that statement. Retrying a partially uncertain batch therefore cannot double-count it.

### Observability

The gateway exposes Prometheus text-format counters and gauges. Labels are intentionally bounded to status codes. API keys, request IDs, and unnormalized resource paths would create unbounded cardinality and are therefore excluded. Usage metrics expose queue depth, admission, drops, retries, persisted batches/events, and dead-letter outcomes without per-client labels.

Liveness only confirms that the process can serve HTTP. Readiness pings Redis and PostgreSQL because the gateway requires both dependencies before it should receive protected traffic.

Prometheus scrapes the private gateway metrics endpoint, blackbox readiness, Redis, PostgreSQL, and the VPS host. The provisioned Grafana dashboard visualizes traffic, average latency, status codes, rate-limit denials, usage-queue saturation, dependency availability, and host/Redis saturation. Versioned Prometheus rules turn sustained dependency, error, capacity, and usage-loss conditions into alerts.

Average latency is shown because the current dependency-free gateway registry exposes a duration sum and request counter. Phase 5 will add distribution buckets and measured p50/p95/p99 evidence rather than implying percentile precision that is not yet available.

### Production edge and operations

Nginx terminates TLS 1.2/1.3, redirects HTTP except for ACME challenges, adds security headers, applies a coarse per-source-IP edge limit, propagates request IDs, and blocks public `/metrics` access. The gateway retains authoritative per-key/per-route quota enforcement.

Certbot uses a webroot shared only with Nginx. A bootstrap Compose file solves the first-certificate dependency cycle; subsequent renewal reloads Nginx without rebuilding the stack. Deployment scripts validate configuration, wait for health, and probe public HTTPS. Backups use PostgreSQL's custom format plus a SHA-256 checksum and must be copied to encrypted off-host storage.

## Failure behavior

| Failure | Gateway response | Reasoning |
|---|---|---|
| Missing/invalid API key | `401` | Caller is unauthenticated |
| Quota exhausted | `429` | Caller should retry after reset |
| PostgreSQL unavailable | `503` | Authentication policy cannot be loaded |
| Redis unavailable | `503` | Enforcement dependency unavailable |
| Upstream unavailable | `502` | Gateway could not obtain an upstream response |
| Unknown API path | `404` | No route is configured |
| TLS certificate or Nginx failure | Connection/edge failure | Public traffic stops without exposing the private gateway port |
| Private upstream/Tailscale failure | `502` | Gateway is healthy enough to respond but cannot reach the selected service |
| Usage queue full | API response is unchanged | Event is dropped and counted; latency and memory remain bounded |
| Usage write fails transiently | API response is unchanged | Worker retries the idempotent batch with exponential backoff |
| Usage retries exhausted | API response is unchanged | Events move to PostgreSQL dead-letter storage |
| Dead-letter write fails | API response is unchanged | Events are counted as dropped and an error is logged |

`RATE_LIMIT_FAIL_OPEN=true` is available for experiments, but the default is fail closed. In a real service, this choice depends on whether availability or quota/security enforcement has higher business priority.

## Security notes

- HTTPS terminates at Nginx; only ports 80 and 443 are published by the application stack.
- Development keys must be replaced before internet exposure.
- API keys are generated with 256 bits of entropy and stored only as peppered HMAC digests with metadata and revocation state.
- `/metrics` returns `404` at the public edge and is scraped on a private monitoring network.
- Redis, PostgreSQL, Prometheus, exporters, and upstream services are reachable only on private networks; Grafana binds to VPS loopback.
- Request bodies and API keys are not written to logs.
- Usage rows contain normalized routes and internal UUIDs, never raw keys, query strings, headers, or bodies.
- Production Compose runs migrations but never development bootstrap, and its example environment contains no development API key.

## Scaling path

The current VPS topology runs one gateway replica behind Nginx, sharing Redis and PostgreSQL with the private monitoring plane. A later topology can add gateway replicas behind the existing Nginx upstream. Each replica has its own bounded in-memory usage queue; idempotent UUID storage remains safe across replicas. Laptop-hosted upstream services connect through Tailscale for the demonstration, while a real production system would normally keep them in the same private infrastructure.

Phase 5 will measure the single-instance baseline before changing replica count, persistence tuning, or metric distributions, so optimization claims remain evidence-based.
