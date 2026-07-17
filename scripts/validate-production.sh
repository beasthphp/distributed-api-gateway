#!/bin/sh
set -eu

env_file=${1:-.env.production.example}
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$root"
case "$env_file" in
    /*) ;;
    *) env_file="$root/$env_file" ;;
esac

if [ ! -f "$env_file" ]; then
    echo "missing environment file: $env_file" >&2
    exit 1
fi

set -a
. "$env_file"
set +a
: "${GATEWAY_DOMAIN:?set GATEWAY_DOMAIN in $env_file}"

for script in scripts/*.sh; do
    sh -n "$script"
done
python3 -m json.tool deploy/grafana/dashboards/api-gateway.json >/dev/null

docker compose --env-file "$env_file" -f compose.production.yaml config --quiet
docker compose --env-file "$env_file" -f deploy/compose.tls-bootstrap.yaml config --quiet
docker compose -f compose.benchmark.yaml config --quiet

docker run --rm --entrypoint /bin/promtool \
    -v "$root/deploy/prometheus:/etc/prometheus:ro" \
    prom/prometheus:v3.13.1 \
    check config /etc/prometheus/prometheus.production.yml

cert_root=$(mktemp -d)
cleanup() {
    rm -rf "$cert_root"
}
trap cleanup EXIT INT TERM
mkdir -p "$cert_root/live/$GATEWAY_DOMAIN"
openssl req -x509 -nodes -newkey rsa:2048 -days 1 \
    -subj "/CN=$GATEWAY_DOMAIN" \
    -keyout "$cert_root/live/$GATEWAY_DOMAIN/privkey.pem" \
    -out "$cert_root/live/$GATEWAY_DOMAIN/fullchain.pem" >/dev/null 2>&1

docker run --rm \
    --add-host gateway:127.0.0.1 \
    -e "GATEWAY_DOMAIN=$GATEWAY_DOMAIN" \
    -v "$root/deploy/nginx/templates/gateway.conf.template:/etc/nginx/templates/default.conf.template:ro" \
    -v "$cert_root:/etc/letsencrypt:ro" \
    nginx:1.31.2-alpine nginx -t

docker run --rm \
    --add-host gateway:127.0.0.1 \
    -v "$root/deploy/nginx/benchmark.conf:/etc/nginx/conf.d/benchmark.conf:ro" \
    nginx:1.31.2-alpine nginx -t

echo "production configuration is valid"
