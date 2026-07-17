# Measured benchmark report

> Every number below is derived from committed raw request samples. These results describe one recorded environment; they are not universal capacity claims.

## Measured facts

| Scenario | Client | Instances | Requests / concurrency | Throughput | p50 | p95 | p99 | Errors | Upstreams |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| multi-cpp | cpp-libcurl | 3 | 2000 / 32 | 2076.93 req/s | 13.91 ms | 30.81 ms | 43.03 ms | 0.00% | 3 |
| multi-go | go-loadgen | 3 | 2000 / 32 | 1827.84 req/s | 13.15 ms | 32.79 ms | 73.25 ms | 0.00% | 3 |
| rate-limit-multi | go-loadgen | 3 | 100 / 100 | 2470.11 req/s | 27.81 ms | 37.16 ms | 38.09 ms | 96.00% | 3 |
| single-cpp | cpp-libcurl | 1 | 2000 / 32 | 2290.80 req/s | 12.86 ms | 25.42 ms | 34.80 ms | 0.00% | 1 |
| single-go | go-loadgen | 1 | 2000 / 32 | 1987.70 req/s | 13.92 ms | 30.91 ms | 46.03 ms | 0.00% | 1 |

## Rate-limit correctness

- `rate-limit-multi`: **passed=true**, accepted=4, denied=96, theoretical maximum accepted=5; required headers and allowed statuses are recorded in the raw result.

## Interpretation (not measured fact)

- In this run, moving from 1 to 3 gateway instances changed Go-client throughput by -8.04% and p95 latency by +6.09%. Shared runner CPU, PostgreSQL, Redis, Nginx, and the client can all influence that observation; the data alone does not identify a cause.
- Go and C++ results are independent client implementations and a reproducibility cross-check. Differences between them must not be described as gateway improvements because their connection handling and runtime overhead differ.
- Percentiles use the nearest-rank method over end-to-end client latency. Warmup requests are excluded.

## Evidence

- `../raw/*.json.gz`: losslessly compressed per-request latency, status, transport outcome, quota-header validity, selected upstream, timing, and tool environment
- `summary.csv`: machine-readable aggregate comparison
- `comparison.svg`: visualization of the measured aggregate values
- `../../docs/benchmarking.md`: exact reproduction commands, workload, and limitations
