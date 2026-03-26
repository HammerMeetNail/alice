SHELL := /bin/sh

PODMAN := podman
PODMAN_COMPOSE ?= $(shell if command -v podman-compose >/dev/null 2>&1; then printf '%s' podman-compose; else printf '%s' 'podman compose'; fi)

TEST_POSTGRES_URL ?= postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable
POSTGRES_SERVICE ?= db
POSTGRES_CONTAINER_NAME ?= alice-db
POSTGRES_WAIT_TIMEOUT ?= 60

COVERAGE_THRESHOLD ?= 80

.PHONY: local down status logs postgres-up postgres-down test test-race test-cover test-postgres e2e e2e-postgres test-all ci mailpit-ui

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
	@go tool cover -func=coverage.out | tail -1
	@total=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$NF}' | tr -d '%'); \
	threshold=$(COVERAGE_THRESHOLD); \
	result=$$(echo "$$total < $$threshold" | bc -l 2>/dev/null || awk "BEGIN{print ($$total < $$threshold)}"); \
	if [ "$$result" = "1" ]; then \
		echo "FAIL: coverage $$total% is below threshold $$threshold%" >&2; \
		exit 1; \
	else \
		echo "OK: coverage $$total% meets threshold $$threshold%"; \
	fi

test-postgres: postgres-up
	@ALICE_TEST_DATABASE_URL=$(TEST_POSTGRES_URL) go test -race -count=1 ./...

e2e:
	@go test -tags e2e -race -count=1 -timeout 5m ./tests/e2e/...

e2e-postgres: postgres-up
	@ALICE_TEST_DATABASE_URL=$(TEST_POSTGRES_URL) go test -tags e2e -race -count=1 -timeout 5m ./tests/e2e/...

test-all: test e2e

ci: test-cover e2e

mailpit-ui:
	@echo "http://localhost:8025"
