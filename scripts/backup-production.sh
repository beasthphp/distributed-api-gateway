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
: "${POSTGRES_USER:?set POSTGRES_USER in $env_file}"
: "${POSTGRES_DB:?set POSTGRES_DB in $env_file}"

retention_days=${BACKUP_RETENTION_DAYS:-14}
case "$retention_days" in
    ''|*[!0-9]*)
        echo "BACKUP_RETENTION_DAYS must be a non-negative integer" >&2
        exit 1
        ;;
esac

compose() {
    docker compose --env-file "$env_file" -f compose.production.yaml "$@"
}

umask 077
mkdir -p backups
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="backups/gateway-${timestamp}.dump"
partial="${backup}.partial"
cleanup() {
    rm -f "$partial"
}
trap cleanup EXIT INT TERM

compose exec -T postgres pg_dump \
    --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --format custom >"$partial"
mv "$partial" "$backup"
trap - EXIT INT TERM
sha256sum "$backup" >"${backup}.sha256"

find backups -type f \( -name 'gateway-*.dump' -o -name 'gateway-*.dump.sha256' \) \
    -mtime "+$retention_days" -delete

echo "created $backup and ${backup}.sha256"
echo "copy both files to encrypted off-host storage"
