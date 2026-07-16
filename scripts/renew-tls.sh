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

compose() {
    docker compose --env-file "$env_file" -f compose.production.yaml "$@"
}

compose config --quiet
compose --profile tools run --rm certbot renew \
    --webroot --webroot-path /var/www/certbot --quiet
compose exec -T nginx nginx -s reload

echo "certificate renewal check completed and Nginx reloaded"
