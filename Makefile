.PHONY: test vet run up down logs smoke

test:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/gateway

up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f gateway

smoke:
	sh scripts/smoke-test.sh
