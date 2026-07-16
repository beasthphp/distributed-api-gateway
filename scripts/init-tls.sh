#!/bin/sh
set -eu

env_file=${1:-.env.production}
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$root"
case "$env_file" in
    /*) ;;
    *) env_file="$root/$env_file" ;;
esac

if [ ! -f "$env_file" ]; then
    echo "missing production environment file: $env_file" >&2
    exit 1
fi

set -a
. "$env_file"
set +a
: "${GATEWAY_DOMAIN:?set GATEWAY_DOMAIN in $env_file}"
: "${LETSENCRYPT_EMAIL:?set LETSENCRYPT_EMAIL in $env_file}"

bootstrap() {
    docker compose --env-file "$env_file" -f deploy/compose.tls-bootstrap.yaml "$@"
}

cleanup() {
    bootstrap down >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

bootstrap config --quiet
bootstrap up -d --wait --wait-timeout 90 nginx-bootstrap
bootstrap run --rm certbot
bootstrap down
trap - EXIT INT TERM

sh scripts/deploy-production.sh "$env_file"
