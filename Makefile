.PHONY: fmt vet test test-integration build up down

fmt:
	gofmt -w cmd internal migrations

vet:
	go vet ./...

test:
	go test -race -count=1 ./...

# Tests create an isolated schema, never truncate the configured database.
test-integration:
	@test -n "$$TEST_DATABASE_URL" || (echo 'Set TEST_DATABASE_URL to a PostgreSQL test database'; exit 1)
	go test -race -count=1 ./internal/httpapi -run TestIntegration -v

build:
	go build -o bin/api ./cmd/api

up:
	docker compose up --build -d

down:
	docker compose down
