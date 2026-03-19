SHELL := /bin/sh

PODMAN_COMPOSE := podman compose

.PHONY: local down status logs test

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
