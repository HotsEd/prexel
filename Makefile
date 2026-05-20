# Prexel dev Makefile. Everything routes through docker-compose so contributors
# don't need a local Go toolchain or Node install.

COMPOSE := docker compose -f deployments/docker-compose.yml
CTL     := $(COMPOSE) run --rm prexel-cli
MIGRATE := $(COMPOSE) exec prexel migrate -path /workspace/migrations -database "sqlite:///var/lib/prexel/prexel.db"

.PHONY: help up down restart logs ps ctl test lint tidy \
        migrate-up migrate-down migrate-new \
        web-shell go-shell build-prod

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

up: ## Start the dev stack (prexel + web + caddy).
	$(COMPOSE) up -d --build

down: ## Stop and remove the dev stack.
	$(COMPOSE) down

restart: ## Restart the prexel service (forces fresh air run).
	$(COMPOSE) restart prexel

logs: ## Tail logs for all services.
	$(COMPOSE) logs -f --tail=200

ps: ## Show running services.
	$(COMPOSE) ps

ctl: ## Run a prexel subcommand inside prexel-cli. Usage: make ctl ARGS="version"
	$(CTL) $(ARGS)

test: ## Run the Go test suite.
	$(COMPOSE) exec -T prexel sh -c "cd /workspace && go test ./..."

lint: ## Run golangci-lint over the backend.
	$(COMPOSE) exec -T prexel sh -c "cd /workspace && golangci-lint run ./..."

tidy: ## Run go mod tidy.
	$(COMPOSE) exec -T prexel sh -c "cd /workspace && go mod tidy"

migrate-up: ## Apply pending migrations.
	$(MIGRATE) up

migrate-down: ## Roll back the latest migration.
	$(MIGRATE) down 1

migrate-new: ## Create a new migration pair. Usage: make migrate-new NAME=add_foo
	@test -n "$(NAME)" || (echo "set NAME=..." && exit 1)
	$(COMPOSE) exec prexel migrate create -ext sql -dir /workspace/migrations -seq $(NAME)

web-shell: ## Open a shell in the web (Vite) container.
	$(COMPOSE) exec web sh

go-shell: ## Open a shell in the prexel (Go) container.
	$(COMPOSE) exec prexel bash

build-prod: ## Build the production image (smoke test of build/package/Dockerfile).
	docker build -f build/package/Dockerfile -t prexel:local .
