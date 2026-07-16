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

mode=$(stat -c '%a' "$env_file" 2>/dev/null || true)
case "$mode" in
    400|600) ;;
    *)
        echo "$env_file must have mode 600 (or stricter); run: chmod 600 $env_file" >&2
        exit 1
        ;;
esac

set -a
# The operator-owned file is trusted input and uses shell-compatible KEY=VALUE lines.
. "$env_file"
set +a
: "${GATEWAY_DOMAIN:?set GATEWAY_DOMAIN in $env_file}"

compose() {
    docker compose --env-file "$env_file" -f compose.production.yaml "$@"
}

compose config --quiet
compose up -d --build --remove-orphans --wait --wait-timeout 180

curl --fail --show-error --silent --max-time 10 \
    "https://${GATEWAY_DOMAIN}/health/ready" >/dev/null

echo "production deployment is ready at https://${GATEWAY_DOMAIN}"
