# Benchmark evidence

Committed baseline directories contain an immutable evidence bundle:

- `environment.txt` — commit, workflow run, Docker versions, kernel, and CPU details
- `raw/*.json` — per-request samples plus calculated aggregates
- `analysis/summary.csv` — machine-readable comparison
- `analysis/report.md` — measured facts separated from interpretation
- `analysis/comparison.svg` — chart generated from the raw JSON

Do not edit generated analysis by hand. Re-run `cmd/benchreport` against the raw directory. See [the benchmark methodology](../docs/benchmarking.md) for reproduction and limitations.
