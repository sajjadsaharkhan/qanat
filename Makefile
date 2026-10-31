SHELL := /bin/bash
COMPOSE := docker compose -f deploy/test/compose.test.yml
# Only test-integration sets these, so `make test` on a laptop without Docker
# skips integration tests instead of failing them (see internal/testkit/itenv).
INTEGRATION_ENV := QANAT_TEST_REDIS_ADDR=localhost:56379 \
  QANAT_TEST_POSTGRES_DSN='postgres://qanat:qanat@localhost:55432/qanat_test?sslmode=disable'

.PHONY: help test test-race test-reference test-integration test-acceptance bench \
        golden-update fixtures-check lint deps-up deps-down stack-up stack-down \
        web-test web-e2e ci sqlc local-up local-down local-logs local-reset local-demo local-demo-clean \
        prod-secrets prod-up prod-down prod-logs prod-backup prod-deploy

help: ## List targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-18s %s\n", $$1, $$2}'

SQLC := docker run --rm -v "$(CURDIR)":/src -w /src sqlc/sqlc:1.31.1

sqlc: ## Regenerate internal/store/db from SQL (commit the output)
	$(SQLC) generate

lint: ## gofmt + go vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt needed" && exit 1)
	go vet ./...
	go vet -tags reference ./...
	go vet -tags qanat ./test/acceptance/...

test: ## Unit tests, translator goldens, fixture checks (no Docker)
	go test -short ./...

test-race: ## Unit tests with the race detector
	go test -short -race ./...

test-reference: ## Acceptance suite against the in-process reference gateway
	go test -tags reference -count=1 ./test/acceptance/...

fixtures-check: ## Validate and secret-scan recorded upstream fixtures
	go test -count=1 ./test/fixtures/...

golden-update: ## Rewrite translator golden files (review the diff!)
	QANAT_UPDATE_GOLDEN=1 go test ./...

deps-up: ## Start Redis, PostgreSQL, mockupstream
	$(COMPOSE) up -d --wait

deps-down:
	$(COMPOSE) down -v

test-integration: deps-up ## Integration tests against real Redis/PostgreSQL
	$(INTEGRATION_ENV) go test -count=1 ./...

stack-up: ## Full stack: deps + gateway + control + nginx
	$(COMPOSE) --profile stack up -d --build --wait

stack-down:
	$(COMPOSE) --profile stack down -v

test-acceptance: stack-up ## Acceptance suite against the real stack through Nginx
	QANAT_ACCEPTANCE_GATEWAY=http://localhost:58080 QANAT_ACCEPTANCE_CONTROL=http://localhost:58081 \
	  QANAT_MOCKUPSTREAM_BIND=0.0.0.0:0 go test -tags qanat -count=1 ./test/acceptance/...

bench: stack-up ## Latency gate: overhead p99 < 10ms and < 10% regression
	key=$$(go run ./cmd/benchseed) && \
	go run ./cmd/loadgen -gateway http://localhost:58080 -direct http://localhost:59090 \
	  -metrics http://localhost:58082/metrics \
	  -key "$$key" -n 500 -c 50 \
	  -budget-p99 10ms -baseline bench/baseline.json -max-regression 0.10 -out bench/result.json

web-test: ## Dashboard unit/component tests
	cd web && npm run test

web-e2e: stack-up ## Dashboard Playwright tests, light + dark themes
	cd web && npm run e2e

ci: lint test test-race test-reference ## What CI runs without Docker

# --- Local run (deploy/local) -------------------------------------------------
LOCAL := docker compose -f deploy/local/compose.yml
ROOT_PW := deploy/local/secrets/root_password
MASTER_KEY := deploy/local/secrets/master_key

$(ROOT_PW):
	@mkdir -p $(dir $@) && umask 077 && openssl rand -base64 24 | tr -d '\n' > $@
	@echo "Generated a root password in $@"

$(MASTER_KEY):
	@mkdir -p $(dir $@) && umask 077 && openssl rand -base64 32 | tr -d '\n' > $@
	@echo "Generated a master key in $@ (back it up: stored credentials can't be read without it)"

local-up: $(ROOT_PW) $(MASTER_KEY) ## Run Qanat locally: dashboard on http://localhost:3000
	$(LOCAL) up -d --build --wait
	@echo ""
	@echo "  Dashboard  http://localhost:$${QANAT_WEB_PORT:-3000}"
	@echo "  Sign in    username root, password in $(ROOT_PW)"
	@echo "  Keycloak   alice/alice (admin), bob/bob, mallory/mallory (denied); console http://localhost:$${QANAT_KEYCLOAK_PORT:-8180} admin/admin"
	@echo ""

local-down: ## Stop the local stack (keeps data)
	$(LOCAL) down

local-logs: ## Follow local stack logs
	$(LOCAL) logs -f

# demoseed runs inside the control container, so it uses the stack's own
# database, Redis and master key. Demo rows are tagged; clean removes only them.
DEMOSEED := GOOS=linux GOARCH=$$(go env GOARCH) CGO_ENABLED=0 go build -o bin/demoseed-linux ./cmd/demoseed && \
	$(LOCAL) run --rm --no-deps -v "$(CURDIR)/bin/demoseed-linux:/usr/local/bin/demoseed:ro" --entrypoint demoseed control

local-demo: ## Add demo data to the local stack (users, Plans, accounts, usage…)
	$(DEMOSEED)

local-demo-clean: ## Remove the demo data again (your own data is kept)
	$(DEMOSEED) -clean

local-reset: ## Stop the local stack and delete its data
	$(LOCAL) down -v

# --- Server deploy (deploy/prod, docs/deploy.md) ------------------------------
PROD := docker compose -f deploy/prod/compose.yml
PROD_SECRETS := deploy/prod/secrets

# The folder is 0700 so other host users can't read it; the files are 0644 so
# the containers' own users (uid 10001, postgres) can read their bind mounts.
$(PROD_SECRETS):
	@mkdir -p $@ && chmod 700 $@

$(PROD_SECRETS)/root_password $(PROD_SECRETS)/master_key: | $(PROD_SECRETS)
	@openssl rand -base64 $(if $(findstring master,$@),32,24) | tr -d '\n' > $@ && chmod 644 $@
	@echo "Generated $@"

# Hex keeps the password safe inside the DSN URL.
$(PROD_SECRETS)/postgres_password: | $(PROD_SECRETS)
	@openssl rand -hex 24 | tr -d '\n' > $@ && chmod 644 $@
	@echo "Generated $@"

$(PROD_SECRETS)/postgres_dsn: $(PROD_SECRETS)/postgres_password
	@printf 'postgres://qanat:%s@postgres:5432/qanat?sslmode=disable' "$$(cat $<)" > $@ && chmod 644 $@

$(PROD_SECRETS)/oidc_client_secret: | $(PROD_SECRETS) # empty = public Keycloak client
	@touch $@ && chmod 644 $@

PROD_SECRET_FILES := $(addprefix $(PROD_SECRETS)/,root_password master_key postgres_password postgres_dsn oidc_client_secret)

prod-secrets: $(PROD_SECRET_FILES) ## Create missing server secrets (existing ones are kept)

prod-up: ## First start: pull CI images from GHCR, create secrets, start Qanat (needs deploy/prod/.env)
	deploy/prod/deploy.sh $(TAG)
	@echo "  Sign in    username root, password in $(PROD_SECRETS)/root_password" && echo ""

prod-deploy: ## Pull CI images from GHCR and restart (TAG=<sha> to pin or roll back)
	deploy/prod/deploy.sh $(TAG)

prod-down: ## Stop the server stack (keeps data)
	$(PROD) down

prod-logs: ## Follow server stack logs
	$(PROD) logs -f

prod-backup: ## pg_dump the server database to deploy/prod/backups/
	@mkdir -p deploy/prod/backups && umask 077 && \
	  $(PROD) exec -T postgres pg_dump -U qanat -d qanat -Fc > deploy/prod/backups/qanat-$$(date +%Y%m%d-%H%M%S).dump
	@ls -t deploy/prod/backups | head -1
