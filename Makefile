COMPOSE := docker compose -f deploy/docker-compose.yml
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

.PHONY: help gen test vet lint build run up down logs ps clean

help: ## list targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

gen: ## regenerate sqlc + openapi code
	cd api && go generate ./...

test: ## run Go tests
	cd api && go test ./...

vet: ## go vet + gofmt check
	cd api && go vet ./... && test -z "$$(gofmt -l .)"

lint: ## golangci-lint (via go run, no install needed)
	cd api && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0 run

build: ## build the shop binary into api/bin
	cd api && go build -ldflags "-X shop/internal/version.Version=$(VERSION) -X shop/internal/version.Commit=$(COMMIT)" -o bin/shop ./cmd/shop

run: build ## run the API locally against ./data (OCR at localhost:8000)
	cd api && SHOP_DATA_DIR=./data ./bin/shop serve

up: ## build + start the docker stack
	SHOP_VERSION=$(VERSION) SHOP_COMMIT=$(COMMIT) $(COMPOSE) up -d --build

down: ## stop the docker stack
	$(COMPOSE) down

logs: ## follow stack logs
	$(COMPOSE) logs -f

ps: ## stack status
	$(COMPOSE) ps

clean: ## remove local build output
	rm -rf api/bin
