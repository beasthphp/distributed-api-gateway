# Roadmap

## Phase 1 — Gateway foundation

- [x] Go reverse proxy
- [x] API-key authentication
- [x] Initial atomic Redis limiter (replaced by the Phase 2 token bucket)
- [x] User and order mock services
- [x] Request IDs and structured logs
- [x] Liveness, readiness, and Prometheus metrics
- [x] Docker Compose, unit tests, and CI

## Phase 2 — Policies and persistence

- [x] PostgreSQL schema for clients, hashed API keys, plans, and revocation
- [x] Admin CLI for issuing and revoking keys
- [x] Per-client and per-route quotas
- [x] Token-bucket Redis Lua implementation
- [x] PostgreSQL lifecycle and real-Redis concurrency tests

## Phase 3 — Asynchronous usage logging

- [x] Bounded event queue between request handling and usage persistence
- [x] Worker with batching, retry, and dead-letter behavior
- [x] PostgreSQL usage records and hourly aggregates
- [x] Backpressure, queue-depth, retry, batch, and dead-letter metrics

## Phase 4 — Deployment and monitoring

- [ ] Nginx reverse proxy and HTTPS certificate
- [ ] VPS deployment with private service networking
- [ ] Grafana dashboard for traffic, latency, errors, and rate-limit denials
- [ ] Redis and host exporters
- [ ] Alert rules and a failure runbook

## Phase 5 — Performance evidence

- [ ] Go load generator for repeatable end-to-end tests
- [ ] C++ benchmark client for latency/throughput comparison
- [ ] p50, p95, and p99 latency report
- [ ] Single-instance versus multi-instance measurements
- [ ] Documented bottlenecks and optimization decisions

## Phase 6 — Portfolio finish

- [ ] OpenAPI specification and hosted docs
- [ ] Architecture and dashboard screenshots
- [ ] Short demo recording
- [ ] Resume bullets using only measured results
- [ ] Interview question-and-answer notes
