# Repository instructions

## Goal

Maintain an interview-ready distributed API gateway whose behavior is easy to run, test, explain, and measure.

## Before changing code

1. Read `README.md`, `docs/architecture.md`, and the relevant package.
2. State the requested behavior and the narrowest files that should change.
3. Do not claim roadmap features are complete before code and verification exist.

## Validation

Run the checks relevant to the change:

```bash
gofmt -w .
go vet ./...
go test -race ./...
docker compose config
```

For request-path changes, also run:

```bash
docker compose up --build -d
sh scripts/smoke-test.sh
```

## Engineering rules

- Keep the gateway stateless; shared enforcement state belongs in Redis or PostgreSQL.
- Do not log secrets, full API keys, request bodies, or authentication headers.
- Do not use API keys, request IDs, or raw resource paths as metric labels.
- Preserve fail-closed rate limiting unless a change explicitly documents the availability trade-off.
- Add or update tests when behavior changes.
- Prefer one focused feature branch and a draft pull request for each roadmap milestone.
