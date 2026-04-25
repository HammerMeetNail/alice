SHELL := /bin/sh

PODMAN := podman
PODMAN_COMPOSE ?= $(shell if command -v podman-compose >/dev/null 2>&1; then printf '%s' podman-compose; else printf '%s' 'podman compose'; fi)

TEST_POSTGRES_URL ?= postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable
POSTGRES_SERVICE ?= db
POSTGRES_CONTAINER_NAME ?= alice-db
POSTGRES_WAIT_TIMEOUT ?= 60

COVERAGE_THRESHOLD ?= 70
# Threshold used when all packages (including postgres) are measured.
COVERAGE_THRESHOLD_FULL ?= 75

.PHONY: local down status logs postgres-up postgres-down test test-race test-cover test-cover-postgres test-postgres e2e e2e-postgres test-all ci mailpit-ui

local:
	@$(PODMAN_COMPOSE) up --build -d
	@$(PODMAN_COMPOSE) ps

down:
	@$(PODMAN_COMPOSE) down --remove-orphans

status:
	@$(PODMAN_COMPOSE) ps

logs:
	@$(PODMAN_COMPOSE) logs -f server

postgres-up:
	@if $(PODMAN) container exists $(POSTGRES_CONTAINER_NAME) 2>/dev/null; then \
		status="$$($(PODMAN) inspect --format '{{.State.Status}}' $(POSTGRES_CONTAINER_NAME) 2>/dev/null || true)"; \
		if [ "$$status" != "running" ]; then \
			$(PODMAN) start $(POSTGRES_CONTAINER_NAME) >/dev/null; \
		fi; \
	else \
		$(PODMAN_COMPOSE) up -d $(POSTGRES_SERVICE); \
	fi
	@i=0; \
	while [ $$i -lt $(POSTGRES_WAIT_TIMEOUT) ]; do \
		status="$$($(PODMAN) inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' $(POSTGRES_CONTAINER_NAME) 2>/dev/null || true)"; \
		if [ "$$status" = "healthy" ] || [ "$$status" = "running" ]; then \
			exit 0; \
		fi; \
		i=$$((i + 1)); \
		sleep 1; \
	done; \
	echo "postgres container $(POSTGRES_CONTAINER_NAME) did not become healthy within $(POSTGRES_WAIT_TIMEOUT)s" >&2; \
	$(PODMAN) logs $(POSTGRES_CONTAINER_NAME) >&2 || true; \
	exit 1

postgres-down:
	@$(PODMAN_COMPOSE) stop $(POSTGRES_SERVICE)

test:
	@go test ./...

test-race:
	@go test -race -count=1 ./...

test-cover:
	@go test -coverprofile=coverage.out -covermode=atomic ./...
	@echo "--- Per-package coverage ---"
	@go tool cover -func=coverage.out | grep "^total:" | head -1
	@echo "--- Testable-package coverage (excluding cmd/, postgres/, app/) ---"
	@grep -v -E '^(alice/cmd/|alice/internal/storage/postgres/|alice/internal/app/)' coverage.out > coverage-testable.out || true
	@go tool cover -func=coverage-testable.out | grep "^total:" | head -1
	@total=$$(go tool cover -func=coverage-testable.out | grep "^total:" | awk '{print $$NF}' | tr -d '%'); \
	threshold=$(COVERAGE_THRESHOLD); \
	result=$$(echo "$$total < $$threshold" | bc -l 2>/dev/null || awk "BEGIN{print ($$total < $$threshold)}"); \
	if [ "$$result" = "1" ]; then \
		echo "FAIL: testable coverage $$total% is below threshold $$threshold%" >&2; \
		exit 1; \
	else \
		echo "OK: testable coverage $$total% meets threshold $$threshold%"; \
	fi

# test-cover-postgres runs coverage including internal/storage/postgres (requires
# a running Postgres instance). Used by CI to measure full package coverage.
# When ALICE_TEST_DATABASE_URL is already set (e.g. a CI service container),
# skip the local Podman postgres-up step.
test-cover-postgres:
	@[ -n "$(ALICE_TEST_DATABASE_URL)" ] || $(MAKE) postgres-up
	@ALICE_TEST_DATABASE_URL=$${ALICE_TEST_DATABASE_URL:-$(TEST_POSTGRES_URL)} go test -coverprofile=coverage.out -covermode=atomic ./...
	@echo "--- Per-package coverage (all packages including postgres) ---"
	@go tool cover -func=coverage.out | grep "^total:" | head -1
	@echo "--- Testable-package coverage (excluding cmd/, app/) ---"
	@grep -v -E '^(alice/cmd/|alice/internal/app/)' coverage.out > coverage-testable.out || true
	@go tool cover -func=coverage-testable.out | grep "^total:" | head -1
	@total=$$(go tool cover -func=coverage-testable.out | grep "^total:" | awk '{print $$NF}' | tr -d '%'); \
	threshold=$(COVERAGE_THRESHOLD_FULL); \
	result=$$(echo "$$total < $$threshold" | bc -l 2>/dev/null || awk "BEGIN{print ($$total < $$threshold)}"); \
	if [ "$$result" = "1" ]; then \
		echo "FAIL: testable coverage $$total% is below threshold $$threshold%" >&2; \
		exit 1; \
	else \
		echo "OK: testable coverage $$total% meets threshold $$threshold%"; \
	fi

test-postgres:
	@[ -n "$(ALICE_TEST_DATABASE_URL)" ] || $(MAKE) postgres-up
	@ALICE_TEST_DATABASE_URL=$${ALICE_TEST_DATABASE_URL:-$(TEST_POSTGRES_URL)} go test -race -count=1 ./...

e2e:
	@go test -tags e2e -race -count=1 -timeout 5m ./tests/e2e/...

e2e-postgres:
	@[ -n "$(ALICE_TEST_DATABASE_URL)" ] || $(MAKE) postgres-up
	@ALICE_TEST_DATABASE_URL=$${ALICE_TEST_DATABASE_URL:-$(TEST_POSTGRES_URL)} go test -tags e2e -race -count=1 -timeout 5m ./tests/e2e/...

test-all: test e2e

ci: test-cover-postgres e2e

mailpit-ui:
	@echo "http://localhost:8025"
