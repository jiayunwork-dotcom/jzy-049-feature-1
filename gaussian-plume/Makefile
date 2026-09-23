.PHONY: build test test-race vet fmt tidy up down integration

build:
	go build -o bin/plume-server ./cmd/server

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	GOTOOLCHAIN=local go mod tidy

up:
	docker compose up --build

down:
	docker compose down

# 需要一个可达的 PostgreSQL 16（例如 compose 起的 db）。
integration:
	DATABASE_URL=postgres://plume:plume@localhost:5432/plume?sslmode=disable \
		go test -count=1 ./internal/store/
