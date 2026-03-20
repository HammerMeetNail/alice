SHELL := /bin/sh

PODMAN := podman
PODMAN_COMPOSE ?= $(shell if command -v podman-compose >/dev/null 2>&1; then printf '%s' podman-compose; else printf '%s' 'podman compose'; fi)

TEST_POSTGRES_URL ?= postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable
POSTGRES_SERVICE ?= db
POSTGRES_CONTAINER_NAME ?= alice-db
POSTGRES_WAIT_TIMEOUT ?= 60

.PHONY: local down status logs postgres-up postgres-down test test-postgres

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
	@$(PODMAN_COMPOSE) up -d $(POSTGRES_SERVICE)
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

test-postgres: postgres-up
	@ALICE_TEST_DATABASE_URL=$(TEST_POSTGRES_URL) go test ./...
