#!/bin/sh
set -eu

output=${1:-results/current}
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$root"
case "$output" in
    /*) ;;
    *) output="$root/$output" ;;
esac

requests=${BENCH_REQUESTS:-2000}
concurrency=${BENCH_CONCURRENCY:-32}
warmup=${BENCH_WARMUP:-100}
multi_instances=${BENCH_MULTI_INSTANCES:-3}

for value in "$requests" "$concurrency" "$warmup" "$multi_instances"; do
    case "$value" in
        ''|*[!0-9]*) echo "benchmark counts must be non-negative integers" >&2; exit 1 ;;
    esac
done
if [ "$requests" -eq 0 ] || [ "$concurrency" -eq 0 ] || [ "$multi_instances" -eq 0 ]; then
    echo "requests, concurrency, and multi-instance count must be positive" >&2
    exit 1
fi

compose() {
    docker compose -f compose.benchmark.yaml "$@"
}

cleanup() {
    if [ "${KEEP_BENCHMARK_STACK:-0}" != "1" ]; then
        compose down --volumes --remove-orphans >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT INT TERM

mkdir -p "$output/raw" "$output/analysis" bin
go build -trimpath -o bin/loadgen ./cmd/loadgen
g++ -std=c++20 -O2 -Wall -Wextra -Werror -pedantic \
    benchmarks/cpp/main.cpp -o bin/cpp-benchmark -pthread $(curl-config --cflags --libs)

compose down --volumes --remove-orphans >/dev/null 2>&1 || true
compose up -d --build --scale gateway=1 --wait --wait-timeout 180

compose run --rm gateway-admin create-plan \
    --slug benchmark --name "Benchmark" --rate 1000000 --burst 1000000 >/dev/null
client_output=$(compose run --rm gateway-admin create-client --name "Benchmark run" --plan benchmark)
client_id=$(printf '%s\n' "$client_output" | awk -F= '/^client_id=/{print $2}' | tail -n 1)
if [ -z "$client_id" ]; then
    echo "could not create benchmark client" >&2
    exit 1
fi
key_output=$(compose run --rm gateway-admin create-key --client "$client_id" --name "Ephemeral benchmark key")
benchmark_key=$(printf '%s\n' "$key_output" | awk -F= '/^api_key=/{print $2}' | tail -n 1)
if [ -z "$benchmark_key" ]; then
    echo "could not issue benchmark API key" >&2
    exit 1
fi

scale_gateway() {
    instances=$1
    compose up -d --scale "gateway=$instances" --wait --wait-timeout 180 gateway
    compose up -d --force-recreate --wait --wait-timeout 90 benchmark-proxy
    curl --fail --silent --show-error --max-time 5 http://127.0.0.1:8080/nginx-health >/dev/null
}

scale_gateway 1
API_KEY="$benchmark_key" bin/loadgen \
    --url http://127.0.0.1:8080/api/users --requests "$requests" --concurrency "$concurrency" \
    --warmup "$warmup" --scenario single-go --instances 1 --min-upstreams 1 \
    --output "$output/raw/single-go.json"
API_KEY="$benchmark_key" bin/cpp-benchmark \
    --url http://127.0.0.1:8080/api/users --requests "$requests" --concurrency "$concurrency" \
    --warmup "$warmup" --scenario single-cpp --instances 1 --min-upstreams 1 \
    --output "$output/raw/single-cpp.json"

scale_gateway "$multi_instances"
compose exec -T redis redis-cli FLUSHDB >/dev/null
API_KEY="$benchmark_key" bin/loadgen \
    --url http://127.0.0.1:8080/api/users --requests "$requests" --concurrency "$concurrency" \
    --warmup "$warmup" --scenario multi-go --instances "$multi_instances" --min-upstreams "$multi_instances" \
    --output "$output/raw/multi-go.json"
API_KEY="$benchmark_key" bin/cpp-benchmark \
    --url http://127.0.0.1:8080/api/users --requests "$requests" --concurrency "$concurrency" \
    --warmup "$warmup" --scenario multi-cpp --instances "$multi_instances" --min-upstreams "$multi_instances" \
    --output "$output/raw/multi-cpp.json"

compose exec -T redis redis-cli FLUSHDB >/dev/null
API_KEY=dev-key-change-me bin/loadgen \
    --url http://127.0.0.1:8080/api/orders --requests 100 --concurrency 100 --warmup 0 \
    --scenario rate-limit-multi --instances "$multi_instances" --min-upstreams "$multi_instances" \
    --allowed-statuses 200,429 --verify-rate-limit --rate 2 --burst 4 \
    --output "$output/raw/rate-limit-multi.json"

go run ./cmd/benchreport --input "$output/raw" --output "$output/analysis"

{
    printf 'commit=%s\n' "${GITHUB_SHA:-$(git rev-parse HEAD 2>/dev/null || printf unknown)}"
    printf 'run_id=%s\n' "${GITHUB_RUN_ID:-local}"
    printf 'recorded_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    uname -a
    docker version --format 'docker_client={{.Client.Version}} docker_server={{.Server.Version}}'
    docker compose version
    lscpu
} >"$output/environment.txt"

echo "benchmark evidence written to $output"
