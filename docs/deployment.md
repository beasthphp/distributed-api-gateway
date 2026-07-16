# Production deployment

This runbook deploys the gateway to a Linux VPS with Nginx as the only public entry point. PostgreSQL, Redis, the gateway, Prometheus, and exporters stay on private Docker networks. Grafana binds to VPS loopback and is reached through SSH forwarding.

## 1. Prepare DNS and the VPS

1. Point the gateway domain's `A` record, and its `AAAA` record if used, to the VPS.
2. Install Docker Engine, the Compose plugin, Git, curl, and OpenSSL.
3. Allow inbound SSH, TCP 80, and TCP 443 in both the cloud firewall and host firewall. Do not expose 3000, 5432, 6379, 8080, 9090, or exporter ports.
4. Clone the repository at `/opt/distributed-api-gateway` and check out the reviewed release commit.

Confirm that ports 80 and 443 are free before starting the stack:

```bash
sudo ss -lntp
```

## 2. Configure secrets and private upstreams

```bash
cd /opt/distributed-api-gateway
cp .env.production.example .env.production
chmod 600 .env.production
```

Replace every example value. Hex output from `openssl rand -hex 32` is suitable for the PostgreSQL password, Redis password, API-key pepper, and Grafana password and does not require URL encoding in `DATABASE_URL`.

The production Compose file deliberately does not run the development bootstrap or create `dev-key-change-me`. It applies migrations only. Never copy a development key or pepper into `.env.production`.

`USER_SERVICE_URL` and `ORDER_SERVICE_URL` must be private origins. For the demonstration topology, join both the VPS and upstream host to the same Tailscale network, bind the mock services to ports 8081 and 8082 on that host, and use its private MagicDNS name:

```dotenv
USER_SERVICE_URL=http://upstream-host.tailnet.example:8081
ORDER_SERVICE_URL=http://upstream-host.tailnet.example:8082
```

Restrict those ports to the Tailscale interface or the VPS's Tailscale identity. Test both origins from the VPS before deployment.

## 3. Validate and obtain HTTPS

The domain must already resolve to the VPS, and TCP 80 must be reachable for the ACME HTTP-01 challenge.

```bash
make prod-validate PROD_ENV=.env.production
make prod-tls-init PROD_ENV=.env.production
```

`prod-tls-init` starts an HTTP-only Nginx bootstrap, obtains the certificate through Certbot's webroot flow, stops the bootstrap, and starts the complete production stack. Set `CERTBOT_STAGING=1` only on a disposable rehearsal; staging certificates are not browser-trusted.

For later deployments, keep the certificate volumes and run:

```bash
make prod-up PROD_ENV=.env.production
```

The deploy script validates Compose, builds the local binaries, waits for service health, and verifies the public HTTPS readiness endpoint.

## 4. Create a real API key

Create a production plan and client after the stack is healthy:

```bash
docker compose --env-file .env.production -f compose.production.yaml run --rm gateway-admin \
  create-plan --slug standard --name "Standard" --rate 20 --burst 40

docker compose --env-file .env.production -f compose.production.yaml run --rm gateway-admin \
  create-client --name "Production demo" --plan standard

docker compose --env-file .env.production -f compose.production.yaml run --rm gateway-admin \
  create-key --client CLIENT_UUID --name "Primary key"
```

Store the printed API key immediately; it is shown once. Verify it without putting it in shell history if the environment is shared:

```bash
read -rsp 'API key: ' API_KEY; echo
curl --fail --show-error -H "X-API-Key: $API_KEY" \
  "https://${GATEWAY_DOMAIN}/api/users"
unset API_KEY
```

## 5. Access monitoring privately

Grafana listens only on `127.0.0.1:3000` on the VPS. From an operator workstation:

```bash
ssh -L 3000:127.0.0.1:3000 operator@VPS_IP
```

Open `http://127.0.0.1:3000` and sign in with the credentials from `.env.production`. The provisioned **Distributed API Gateway — Production** dashboard covers traffic, average latency, status codes, rate-limit denials, usage-queue saturation, Redis memory, VPS CPU/RAM/disk, dependency health, and firing alerts.

Prometheus and exporter ports have no host mapping. The gateway's `/metrics` route is also blocked by public Nginx.

## 6. Automate renewal and backups

Install the supplied systemd units if the repository lives at `/opt/distributed-api-gateway`:

```bash
sudo cp deploy/systemd/api-gateway-* /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now api-gateway-cert-renew.timer api-gateway-backup.timer
systemctl list-timers 'api-gateway-*'
```

The backup timer creates a mode-600 custom-format PostgreSQL dump and SHA-256 checksum under `backups/`. Copy both to encrypted off-host storage. A same-host backup does not protect against VPS loss.

Test each service after installation:

```bash
sudo systemctl start api-gateway-cert-renew.service
sudo systemctl start api-gateway-backup.service
sudo journalctl -u api-gateway-backup.service --since today
```

## 7. Deployment and rollback

Before each release, create a backup and record the current commit:

```bash
git rev-parse HEAD
make prod-backup PROD_ENV=.env.production
git fetch --tags origin
git switch --detach RELEASE_COMMIT
make prod-validate PROD_ENV=.env.production
make prod-up PROD_ENV=.env.production
```

To roll back application/configuration changes, switch to the last tested commit and repeat validation and `prod-up`. Compose reuses the named database, Redis, certificate, Prometheus, and Grafana volumes. If a release contains a non-backward-compatible database migration, follow the database recovery procedure in [runbook.md](runbook.md) instead of assuming an application-only rollback is safe.

## Verification checklist

```bash
curl --fail "https://${GATEWAY_DOMAIN}/health/live"
curl --fail "https://${GATEWAY_DOMAIN}/health/ready"
curl -I "https://${GATEWAY_DOMAIN}/metrics"  # expected 404
sudo ss -lntp                              # public listeners: SSH, 80, 443
docker compose --env-file .env.production -f compose.production.yaml ps
```

Also verify API-key authentication, a deliberate `429`, Grafana data freshness, and a test alert before declaring the deployment complete.
