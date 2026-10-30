SHELL := /bin/bash
COMPOSE := docker compose -f deploy/test/compose.test.yml
# Only test-integration sets these, so `make test` on a laptop without Docker
# skips integration tests instead of failing them (see internal/testkit/itenv).
INTEGRATION_ENV := QANAT_TEST_REDIS_ADDR=localhost:56379 \
  QANAT_TEST_POSTGRES_DSN='postgres://qanat:qanat@localhost:55432/qanat_test?sslmode=disable'

.PHONY: help test test-race test-reference test-integration test-acceptance bench \
        golden-update fixtures-check lint deps-up deps-down stack-up stack-down \
        web-test web-e2e ci sqlc

help: ## List targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-18s %s\n", $$1, $$2}'

SQLC := docker run --rm -v "$(CURDIR)":/src -w /src sqlc/sqlc:1.31.1

sqlc: ## Regenerate internal/store/db from SQL (commit the output)
	$(SQLC) generate

lint: ## gofmt + go vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt needed" && exit 1)
	go vet ./...
	go vet -tags reference ./...

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
	  go test -tags qanat -count=1 ./test/acceptance/...

bench: stack-up ## Latency gate: overhead p99 < 10ms and < 10% regression
	go run ./cmd/loadgen -gateway http://localhost:58080 -direct http://localhost:59090 \
	  -key "$${QANAT_BENCH_KEY:?seed a key first}" -n 500 -c 50 \
	  -budget-p99 10ms -baseline bench/baseline.json -max-regression 0.10 -out bench/result.json

web-test: ## Dashboard unit/component tests
	cd web && npm run test

web-e2e: stack-up ## Dashboard Playwright tests, light + dark themes
	cd web && npm run e2e

ci: lint test test-race test-reference ## What CI runs without Docker
