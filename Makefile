.PHONY: test vet run migrate bootstrap up down logs smoke

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
