SHELL := /bin/sh

PODMAN_COMPOSE := podman compose

TEST_POSTGRES_URL ?= postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable

.PHONY: local down status logs test test-postgres

local:
	@$(PODMAN_COMPOSE) up --build -d
	@$(PODMAN_COMPOSE) ps

down:
	@$(PODMAN_COMPOSE) down --remove-orphans

status:
	@$(PODMAN_COMPOSE) ps

logs:
	@$(PODMAN_COMPOSE) logs -f server

test:
	@go test ./...

test-postgres:
	@ALICE_TEST_DATABASE_URL=$(TEST_POSTGRES_URL) go test ./...
