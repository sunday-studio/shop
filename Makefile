COMPOSE := docker compose -f deploy/docker-compose.yml
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

.PHONY: hosted-kit-test e2e e2e-hosted restore-drill offsite-up offsite-down offsite-sync offsite-pull setup help gen test vet lint build run up down logs ps clean backup token spike-up spike-down spike-status spike-ocr spike-label spike-score

help: ## list targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

gen: ## regenerate sqlc + openapi code
	cd api && go generate ./...

test: ## run Go tests
	cd api && go test ./...

vet: ## go vet + gofmt check
	cd api && go vet ./... && test -z "$$(gofmt -l .)"

e2e: ## end-to-end test in an isolated docker stack (project shop-e2e, port 18480); never touches the live stack
	@docker image inspect shop-ocr:ppocrv5 >/dev/null 2>&1 || docker build -t shop-ocr:ppocrv5 --build-arg OCR_ENGINE=ppocrv5 ocr
	cd api && go test -tags e2e -count=1 -v -timeout 40m ./e2e/

e2e-hosted: ## end-to-end test of the whole hosted paid product (fake Apple, real OCR) in an isolated docker stack (project shop-e2e-hosted, ports 18500/18501); never touches the live stack
	@docker image inspect shop-ocr:ppocrv5 >/dev/null 2>&1 || docker build -t shop-ocr:ppocrv5 --build-arg OCR_ENGINE=ppocrv5 ocr
	cd api && go test -tags e2ehosted -count=1 -v -timeout 60m ./e2ehosted/

restore-drill: ## restore drill in an isolated docker stack (project shop-drill, port 18481): backup, wipe, restore (also from an off-site copy), verify; never touches the live stack
	cd api && go test -tags drill -count=1 -v -timeout 30m ./drill/

hosted-kit-test: ## test the hosted server kit (deploy/hosted) in an isolated stack (project shop-hosted-test, port 18490); never touches the live stack
	deploy/hosted/test/run.sh

HOSTED_RUN_ADMIN_TOKEN ?= sa_hosted_run_local
HOSTED_RUN_PORT ?= 18495
HOSTED_RUN_ADMIN_PORT ?= 18496

.PHONY: hosted-run
hosted-run: ## run the hosted server (cmd/shop-hosted) locally in a throwaway folder against the in-process fake Apple: public API on 127.0.0.1:18495, admin API on 127.0.0.1:18496 (token sa_hosted_run_local; fake Apple helpers at /admin/v1/dev/*, see docs/BACKEND-STATUS.md); never touches the live stack
	cd api && go build -o bin/shop-hosted ./cmd/shop-hosted
	@d=$$(mktemp -d "$${TMPDIR:-/tmp}/shop-hosted-run.XXXXXX"); trap 'rm -rf "$$d"' EXIT INT TERM; \
	echo "hosted-run: data in $$d (removed on exit); public http://127.0.0.1:$(HOSTED_RUN_PORT) admin http://127.0.0.1:$(HOSTED_RUN_ADMIN_PORT) token $(HOSTED_RUN_ADMIN_TOKEN)"; \
	cd api && SHOP_MODE=hosted SHOP_DATA_DIR="$$d" SHOP_ADDR=127.0.0.1:$(HOSTED_RUN_PORT) ADMIN_ADDR=127.0.0.1:$(HOSTED_RUN_ADMIN_PORT) \
	ADMIN_TOKEN_HASH=$$(printf %s '$(HOSTED_RUN_ADMIN_TOKEN)' | shasum -a 256 | cut -d' ' -f1) SHOP_APPLE_MODE=fake SHOP_APPLE_FAKE_ACK=i-know-this-is-a-test SHOP_APPLE_ENVIRONMENT=sandbox \
	OCR_URL=$${OCR_URL:-http://127.0.0.1:1} ./bin/shop-hosted serve

.PHONY: isolation
isolation: ## household isolation tests (build tag isolation): two households, every endpoint and MCP tool, plus the process-state scan, then the same suite against the hosted service (two accounts, control database, plan gate) and a household member; fails with a "not isolated: ..." checklist
	cd api && go test -tags isolation -count=1 -run 'TestIsolation' ./internal/httpapi/
	cd api && ISOLATION_WORLD=hosted go test -tags isolation -count=1 -run 'TestIsolation' ./internal/httpapi/

offsite-up: ## start the optional off-site copy (rclone) after each backup; needs OFFSITE_REMOTE, see docs/operations.md
	$(COMPOSE) --profile offsite up -d offsite

offsite-down: ## stop the optional off-site copy
	$(COMPOSE) --profile offsite stop offsite

offsite-sync: ## copy the backup folder off-site now (also needs OFFSITE_REMOTE)
	$(COMPOSE) --profile offsite run --rm offsite once

offsite-pull: ## bring the off-site copy back into the backup folder (first step of a restore from off-site)
	$(COMPOSE) --profile offsite run --rm offsite pull

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

backup: ## run a backup now (to SHOP_BACKUP_DIR, default ./backups)
	$(COMPOSE) run --rm backup backup

setup: ## first run: start the stack and create the app pairing password
	$(MAKE) up
	@sleep 3
	@$(COMPOSE) exec api shop password generate

token: ## create an API token: make token NAME=assistant
	@$(COMPOSE) exec api shop token create $(NAME)

SPIKE := cd api && go run ./cmd/shop-spike
SPIKE_COMPOSE := docker compose -f deploy/docker-compose.spike.yml

spike-up: ## start the spike OCR engines (except paddleocr-vl, see docs/spike.md)
	$(SPIKE_COMPOSE) up -d --build ppocrv5 ppocrv5-longside ppocrv5-server tesseract doctr

spike-down: ## stop all spike engines
	$(SPIKE_COMPOSE) down

spike-status: ## images, labels and OCR cache per engine
	$(SPIKE) status

spike-ocr: ## OCR spike/receipts with every running engine (cached)
	$(SPIKE) ocr

spike-label: ## label receipts at http://127.0.0.1:8090
	$(SPIKE) label

spike-score: ## score engines + parser, write docs/spike-report.md
	$(SPIKE) score

clean: ## remove local build output
	rm -rf api/bin
