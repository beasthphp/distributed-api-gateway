# Benchmark evidence

Committed baseline directories contain an immutable evidence bundle:

- `environment.txt` — commit, workflow run, Docker versions, kernel, and CPU details
- `raw/*.json.gz` — losslessly compressed per-request JSON samples plus calculated aggregates
- `analysis/summary.csv` — machine-readable comparison
- `analysis/report.md` — measured facts separated from interpretation
- `analysis/comparison.svg` — chart generated from the raw JSON

Do not edit generated aggregate values by hand. Decompress the raw files and re-run `cmd/benchreport` against that directory. See [the benchmark methodology](../docs/benchmarking.md) for reproduction and limitations.

## Committed baseline

[`baseline-github-actions-29522346155`](baseline-github-actions-29522346155/) is the reviewed Phase 5 baseline from [workflow run 29522346155](https://github.com/beasthphp/distributed-api-gateway/actions/runs/29522346155). All five scenario verification objects are `passed=true`.

- One gateway, Go client: 1,987.70 req/s, p95 30.91 ms, 0% errors
- Three gateways, Go client: 1,827.84 req/s, p95 32.79 ms, 0% errors, all three upstreams observed
- Shared-quota test: 4 accepted and 96 denied, below the computed maximum of 5, with all three upstreams and required headers observed

Use the [generated report](baseline-github-actions-29522346155/analysis/report.md) for the complete Go/C++ table and interpretation limits.

Decompress a committed raw result without changing its bytes:

```bash
gzip -cd results/baseline-github-actions-29522346155/raw/single-go.json.gz > /tmp/single-go.json
```
