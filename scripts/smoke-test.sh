#!/usr/bin/env sh
set -eu

BASE_URL="${BASE_URL:-http://localhost:8080}"
API_KEY="${API_KEY:-dev-key-change-me}"

curl --fail --silent --show-error "$BASE_URL/health/live"
curl --fail --silent --show-error "$BASE_URL/health/ready"
curl --fail --silent --show-error -H "X-API-Key: $API_KEY" "$BASE_URL/api/users"
curl --fail --silent --show-error -H "X-API-Key: $API_KEY" "$BASE_URL/api/orders/101"

printf '\nSmoke test passed.\n'
