SHELL := /bin/bash
-include .env
export

COMPOSE := docker compose -f deploy/docker-compose.yml
CONFIG  := configs/config.yaml

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: build
build: ## Build all binaries
	go build ./...

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: test
test: ## Run network-free tests (set TEST_DATABASE_URL to include DB tests)
	go test ./...

.PHONY: up
up: ## Start Postgres (pgvector)
	$(COMPOSE) up -d
	@echo "waiting for postgres..." && until $(COMPOSE) exec -T postgres pg_isready -U bepilot -d bepilot >/dev/null 2>&1; do sleep 1; done
	@echo "postgres ready on localhost:5432"

.PHONY: down
down: ## Stop Postgres
	$(COMPOSE) down

.PHONY: reset-db
reset-db: ## Drop and recreate the database volume
	$(COMPOSE) down -v && $(MAKE) up

.PHONY: migrate
migrate: ## Apply database migrations
	go run ./cmd/server -config $(CONFIG) -migrate-only

.PHONY: seed
seed: ## Create a dev user and print a fresh API key
	go run ./cmd/seed -config $(CONFIG)

.PHONY: skills-sync
skills-sync: ## Sync skills/ into Postgres + embeddings
	go run ./cmd/skills-sync -config $(CONFIG)

SWAG := go run github.com/swaggo/swag/cmd/swag@v1.16.6

.PHONY: swag
swag: ## Regenerate the OpenAPI spec from handler annotations (docs/)
	$(SWAG) init -g cmd/server/main.go -o docs --parseInternal --parseDepth 2
	@echo "regenerated docs/{docs.go,swagger.json,swagger.yaml}"

.PHONY: docs
docs: swag ## Alias for `swag`; then run `make test` to check route/spec drift
	@echo "run 'make test' — TestRoutesMatchOpenAPISpec catches un-annotated routes"

.PHONY: run
run: ## Run the API server
	go run ./cmd/server -config $(CONFIG)

.PHONY: dev
dev: up migrate ## Bring up DB, migrate, then run the server
	$(MAKE) run
