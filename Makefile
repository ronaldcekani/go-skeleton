SHELL := /bin/bash
BIN := $(CURDIR)/bin
TOOLS_MOD := tools/go.mod
APP := go-skeleton

export PATH := $(BIN):$(PATH)

.PHONY: help tools generate generate-openapi generate-proto lint-proto build run migrate migrate-down migrate-status test vet tidy up down clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

tools: ## Build the pinned code generators into ./bin
	GOBIN=$(BIN) go install -modfile=$(TOOLS_MOD) \
		github.com/bufbuild/buf/cmd/buf \
		github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
		google.golang.org/protobuf/cmd/protoc-gen-go \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc

generate: generate-openapi generate-proto ## Regenerate HTTP and gRPC code

generate-openapi: tools ## Generate the HTTP server from api/openapi/openapi.yaml
	oapi-codegen -config api/openapi/oapi-codegen.yaml api/openapi/openapi.yaml

generate-proto: tools ## Generate the gRPC server from api/proto
	buf generate

lint-proto: tools ## Lint the proto files
	buf lint

build: ## Build the binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN)/$(APP) ./cmd/app

run: ## Run the HTTP + gRPC servers
	go run ./cmd/app --mode=app --config=config/config.yaml

migrate: ## Apply all pending DB migrations
	go run ./cmd/app --mode=migration --config=config/config.yaml

migrate-down: ## Roll back the last DB migration
	go run ./cmd/app --mode=migration --migration-command=down --config=config/config.yaml

migrate-status: ## Print DB migration status
	go run ./cmd/app --mode=migration --migration-command=status --config=config/config.yaml

test: ## Run tests (set TEST_POSTGRES_DSN to include the Postgres tests)
	go test -race ./...

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy both modules
	go mod tidy
	cd tools && go mod tidy

up: ## Start Postgres, Redis, Kafka and the observability stack
	docker compose up -d

down: ## Stop the local stack
	docker compose down

clean: ## Remove build output
	rm -rf $(BIN)
