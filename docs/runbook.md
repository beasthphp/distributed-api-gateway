# Production failure runbook

Start every incident by recording the time, deployed commit, recent changes, and active alerts. Preserve logs before recreating containers.

```bash
cd /opt/distributed-api-gateway
export COMPOSE_FILE=compose.production.yaml
docker compose --env-file .env.production ps
docker compose --env-file .env.production logs --since 30m gateway nginx postgres redis prometheus
```

## Alert response

| Alert or symptom | First checks | Expected action |
|---|---|---|
| `GatewayMetricsDown` | Gateway container health and logs | Restart only after identifying startup/configuration failure; roll back a bad release |
| `GatewayNotReady` | `/health/ready`, Redis and PostgreSQL health | Restore the failed dependency; the gateway intentionally fails closed |
| `GatewayHighErrorRatio` | Split 502s from other 5xx; test private upstreams | Repair upstream/Tailscale routing for 502s, otherwise inspect gateway/dependencies |
| `GatewayRateLimitSpike` | Client plan, route override, Nginx and gateway 429 rates | Confirm abuse versus a quota that is too small before changing policy |
| `UsageQueueNearCapacity` | Queue depth, retries, PostgreSQL latency | Restore database throughput; do not make the queue unbounded |
| `UsageEventsDropped` | Queue, shutdown, and dead-letter metrics/logs | Treat as data loss; preserve logs and correct the persistence failure |
| `RedisUnavailable` | Redis health/logs, disk and memory | Restore Redis; `noeviction` plus fail-closed behavior can surface saturation as 503s |
| `RedisMemoryNearLimit` | Key count, used/max memory, traffic change | Find unexpected bucket growth before carefully increasing the explicit limit |
| `PostgreSQLUnavailable` | Container health, disk, connection errors | Restore PostgreSQL before accepting traffic |
| Host memory/disk alerts | `df -h`, `free -h`, container stats | Preserve required evidence/backups, then reduce retention or capacity pressure |

## Common diagnostic commands

```bash
docker compose --env-file .env.production -f compose.production.yaml ps
docker compose --env-file .env.production -f compose.production.yaml logs --since 15m SERVICE
docker stats --no-stream
df -h
free -h
curl --fail --show-error "https://${GATEWAY_DOMAIN}/health/ready"
```

Check private upstream connectivity from the gateway network:

```bash
docker compose --env-file .env.production -f compose.production.yaml run --rm --no-deps \
  --entrypoint /bin/sh gateway -c 'wget -qO- "$USER_SERVICE_URL/health/live"'
```

## Safe restart order

Avoid `docker compose down -v`; `-v` deletes persistent data. Restart a single unhealthy service first. For a complete controlled restart:

```bash
docker compose --env-file .env.production -f compose.production.yaml stop nginx gateway
docker compose --env-file .env.production -f compose.production.yaml up -d --wait postgres redis
docker compose --env-file .env.production -f compose.production.yaml up -d --wait gateway nginx
```

## Application rollback

1. Run `make prod-backup PROD_ENV=.env.production` if PostgreSQL is healthy.
2. Record `git rev-parse HEAD` and choose a previously tested commit.
3. `git switch --detach KNOWN_GOOD_COMMIT`.
4. Run `make prod-validate PROD_ENV=.env.production`.
5. Run `make prod-up PROD_ENV=.env.production` and verify HTTPS, authentication, quota enforcement, dashboards, and alerts.

Do not roll application code behind a schema it cannot understand. Migrations are forward-only; restore the matching database backup when backward compatibility is not guaranteed.

## Database backup verification and restore

Practice this process on a disposable environment. A restore replaces the live database.

```bash
set -a; . ./.env.production; set +a
sha256sum --check backups/gateway-TIMESTAMP.dump.sha256
docker compose --env-file .env.production -f compose.production.yaml stop gateway postgres-exporter
docker compose --env-file .env.production -f compose.production.yaml exec -T postgres \
  dropdb --force --username "$POSTGRES_USER" "$POSTGRES_DB"
docker compose --env-file .env.production -f compose.production.yaml exec -T postgres \
  createdb --username "$POSTGRES_USER" "$POSTGRES_DB"
docker compose --env-file .env.production -f compose.production.yaml exec -T postgres \
  pg_restore --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --no-owner \
  < backups/gateway-TIMESTAMP.dump
docker compose --env-file .env.production -f compose.production.yaml up -d --wait postgres-exporter gateway
```

The variables above must be loaded from the protected production environment file in the operator shell. After restoration, test an existing key, inspect usage aggregates, and confirm that migrations report success before starting Nginx if it was stopped.

## Certificate failure

Check DNS, port 80, Certbot logs, and the renewal timer. The ACME challenge path must remain publicly reachable over HTTP even though other paths redirect to HTTPS.

```bash
systemctl status api-gateway-cert-renew.timer
journalctl -u api-gateway-cert-renew.service --since '2 days ago'
make prod-renew PROD_ENV=.env.production
```

Do not delete the `letsencrypt` volume during an incident unless deliberately replacing all certificate state.

## Post-incident

Record the customer-visible interval, root cause, contributing conditions, lost usage-event count, recovery steps, and follow-up owner. Turn any missing signal or unsafe manual step into a tested dashboard, alert, or script change.
