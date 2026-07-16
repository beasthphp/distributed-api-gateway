PROD_ENV ?= .env.production

.PHONY: test vet run migrate bootstrap up down logs smoke prod-validate prod-tls-init prod-up prod-down prod-logs prod-renew prod-backup

test:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/gateway

migrate:
	go run ./cmd/gateway-admin migrate

bootstrap:
	go run ./cmd/gateway-admin bootstrap

up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f gateway

smoke:
	sh scripts/smoke-test.sh

prod-validate:
	sh scripts/validate-production.sh $(PROD_ENV)

prod-tls-init:
	sh scripts/init-tls.sh $(PROD_ENV)

prod-up:
	sh scripts/deploy-production.sh $(PROD_ENV)

prod-down:
	docker compose --env-file $(PROD_ENV) -f compose.production.yaml down

prod-logs:
	docker compose --env-file $(PROD_ENV) -f compose.production.yaml logs -f --tail=200 gateway nginx

prod-renew:
	sh scripts/renew-tls.sh $(PROD_ENV)

prod-backup:
	sh scripts/backup-production.sh $(PROD_ENV)
