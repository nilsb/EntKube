BINARY   := server
MAIN     := ./cmd/server
GOFLAGS  := -ldflags="-s -w"

.PHONY: build run test lint tidy generate migrate-create

## build: compile the server binary
build:
	go build $(GOFLAGS) -o bin/$(BINARY) $(MAIN)

## run: run the server locally (requires DATABASE_URL etc. in environment)
run:
	go run $(MAIN)

## test: run all tests
test:
	go test ./... -race -count=1

## lint: run golangci-lint (must be installed separately)
lint:
	golangci-lint run ./...

## tidy: sync go.mod / go.sum
tidy:
	go mod tidy

## generate: run sqlc code generation (requires sqlc in PATH)
generate:
	sqlc generate

## migrate-create: create a new goose migration  (usage: make migrate-create NAME=add_clusters)
migrate-create:
	@[ -n "$(NAME)" ] || (echo "usage: make migrate-create NAME=<name>" && exit 1)
	goose -dir internal/db/migrations create $(NAME) sql
