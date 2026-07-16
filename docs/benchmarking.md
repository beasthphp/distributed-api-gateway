# Benchmark methodology

Phase 5 measures the complete local request path: client → Nginx benchmark proxy → gateway replica → PostgreSQL authentication → shared Redis token bucket → mock upstream → asynchronous usage pipeline. The benchmark project is isolated from development and production Compose volumes.

## What the tools record

Both clients write schema-versioned JSON containing:

- one raw sample per measured request
- end-to-end latency in microseconds
- HTTP status or transport failure
- rate-limit header validity
- the Nginx-selected gateway upstream
- request count, concurrency, warmup, wall duration, and instance count
- nearest-rank p50, p95, and p99 latency
- throughput, status counts, error rate, and client runtime/compiler metadata

The Go client (`cmd/loadgen`) is the primary load generator and also performs executable assertions. The C++20/libcurl client (`benchmarks/cpp/main.cpp`) is an independent implementation and reproducibility cross-check. Differences between client results are not gateway optimization evidence because connection handling and runtime overhead differ.

## Controlled matrix

The default matrix uses the same machine and stack revision for:

| Scenario | Gateway replicas | Client | Default measured requests / concurrency |
|---|---:|---|---:|
| `single-go` | 1 | Go | 2,000 / 32 |
| `single-cpp` | 1 | C++/libcurl | 2,000 / 32 |
| `multi-go` | 3 | Go | 2,000 / 32 |
| `multi-cpp` | 3 | C++/libcurl | 2,000 / 32 |
| `rate-limit-multi` | 3 | Go | 100 / 100 |

Each throughput run excludes 100 warmup requests. The script issues an ephemeral high-quota key so quota denial does not contaminate the throughput scenarios. Nginx returns the chosen upstream in a benchmark-only response header, and both multi-instance clients fail if they do not observe all expected replicas.

The concurrent quota scenario flushes only the isolated benchmark Redis, uses the development `/api/orders` policy (2 requests/second, burst 4), and accepts only `200` and `429`. It fails unless:

- at least one request is accepted and at least one is denied
- accepted requests do not exceed `burst + ceil(rate × measured wall seconds)`
- every token-bucket response has limit/reset/remaining headers and every `429` has `Retry-After`
- no transport errors or unexpected statuses occur
- all gateway replicas are observed behind Nginx

The upper bound deliberately includes tokens refilled while the concurrent batch is in flight. This tests shared enforcement without assuming every request reaches Redis at the same instant.

## Reproduce locally

Requirements: Docker with Compose, Go from `go.mod`, a C++20 compiler, and libcurl development headers with `curl-config`.

```bash
make benchmark BENCH_OUTPUT=results/current
```

Override workload size without editing the scripts:

```bash
BENCH_REQUESTS=10000 BENCH_CONCURRENCY=64 BENCH_WARMUP=500 \
  make benchmark BENCH_OUTPUT=results/current
```

Outputs:

```text
results/current/
├── environment.txt
├── raw/
│   ├── single-go.json
│   ├── single-cpp.json
│   ├── multi-go.json
│   ├── multi-cpp.json
│   └── rate-limit-multi.json
└── analysis/
    ├── summary.csv
    ├── report.md
    └── comparison.svg
```

Set `KEEP_BENCHMARK_STACK=1` to retain the isolated containers for diagnosis. Otherwise the script removes only the benchmark project's temporary containers and volumes.

## CI evidence workflow

Pushes to the Phase 5 branch run `.github/workflows/benchmark.yml`. The workflow uploads raw samples, generated analysis, chart, and runner environment as one artifact. A reviewed baseline artifact is then committed unchanged under `results/` so every published number has inspectable evidence.

The workflow is also manually runnable after merge. Results from separate workflow runs must be reported separately unless the environment and workload are demonstrably controlled.

## Interpretation limits

- GitHub-hosted runner hardware can vary between runs; committed numbers describe only the recorded run.
- The client, Nginx, Redis, PostgreSQL, mock upstreams, and gateway share one runner, so scaling replicas may increase contention rather than throughput.
- Average latency is not a substitute for percentiles; the report publishes p50/p95/p99 from raw client samples.
- A single run is evidence, not a capacity guarantee. Repeat runs and dedicated hosts are needed for stronger statistical claims.
- Charts show measured aggregates. Causal explanations belong in the explicitly labeled interpretation section.
