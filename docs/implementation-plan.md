# implementation plan

## document status

Active implementation guide  
Audience: engineering, platform, future agent sessions  
Last updated: 2026-03-19

---

## 1. current state

The repository is no longer design-only. The current implementation includes:

- a Go module and runnable coordination server entrypoint
- a stdio MCP server entrypoint for local CLI-native clients
- canonical Go domain models for agents, auth challenges/tokens, artifacts, grants, queries, requests, approvals, and audit events
- JSON schema files for artifact, query, and policy-grant payloads
- repository interfaces for the currently implemented entities
- a PostgreSQL storage layer with embedded startup migrations
- an in-memory storage layer that remains available as a fallback when `ALICE_DATABASE_URL` is not set
- a signed registration challenge flow with short-lived bearer-token issuance for agents
- an MCP wrapper layer that maps the current tool surface onto the existing HTTP route contracts
- HTTP routes for:
  - `POST /v1/agents/register/challenge`
  - `POST /v1/agents/register`
  - `POST /v1/artifacts`
  - `POST /v1/policy-grants`
  - `GET /v1/peers`
  - `POST /v1/queries`
  - `GET /v1/queries/:id`
  - `POST /v1/requests`
  - `GET /v1/requests/incoming`
  - `POST /v1/requests/:id/respond`
  - `GET /v1/approvals`
  - `POST /v1/approvals/:id/resolve`
  - `GET /v1/audit/summary`
  - `GET /healthz`
- a targeted handler test covering the permissioned query flow against memory and, when configured, PostgreSQL
- a targeted handler test covering the request and approval flow against memory and, when configured, PostgreSQL
- a targeted MCP test covering local registration, artifact publish, grant, peer listing, query/result retrieval, request response, and approval resolution
- a Podman-based local container workflow through `make local` and `make down` that runs both the server and PostgreSQL

---

## 2. assumptions currently encoded in code

These are implementation choices already present in the codebase and should be treated as the current default unless deliberately changed.

- auth uses a signed Ed25519 registration challenge and short-lived bearer tokens
- the server answers queries from centrally stored derived artifacts
- access control is explicit-grant-only for now
- manager-specific visibility defaults are not implemented
- the server uses PostgreSQL when `ALICE_DATABASE_URL` is set and otherwise falls back to in-memory storage
- the public surface now includes HTTP plus a local stdio MCP server for the implemented Reporter and Gatekeeper tools
- request approvals are explicit and API-driven; no user-facing approval UI or automatic risk policy exists yet
- no edge runtime or connector ingestion exists yet

---

## 3. completed implementation steps

From the technical specification’s recommended order:

- step 1 is partially complete:
  - Go domain schemas exist
  - initial JSON schemas exist
- step 2 is partially complete:
  - agents
  - agent auth and token issuance
  - policy grants
  - artifacts
  - queries
  - audit
  - minimal server wiring
  - durable PostgreSQL-backed storage for the implemented entities

Not yet complete inside step 2:

- org graph and richer authorization rules

- step 3 is complete for the initial Reporter tool subset:
  - `register_agent`
  - `publish_artifact`
  - `query_peer_status`
  - `get_query_result`
  - `grant_permission`
  - `list_allowed_peers`

- step 4 is complete for the current minimal Gatekeeper flow:
  - `send_request_to_peer`
  - `list_incoming_requests`
  - `respond_to_request`
  - `list_pending_approvals`
  - `resolve_approval`

---

## 4. next recommended steps

The next session should work through these in order.

### step a: replace in-memory storage with PostgreSQL

Status: complete for the currently implemented HTTP surface

Implement:

- `internal/storage/postgres/`
- migrations for agents, users, grants, artifacts, queries, responses, and audit events
- repository interfaces so services stop depending directly on the in-memory store

Definition of done:

- `make local` runs the server and database together
- data survives server restarts
- the existing query-flow test can run against a real DB-backed repository layer

### step b: add real auth for agent registration and requests

Status: complete for the current HTTP surface

Implement:

- registration challenge flow
- server-issued short-lived tokens
- request authentication middleware

Definition of done:

- `X-Agent-ID` is no longer sufficient by itself
- agent registration results in a usable token or session credential

### step c: expose the MCP surface on top of the existing services

Status: complete for the current Reporter tool subset

Implement at minimum:

- `register_agent`
- `publish_artifact`
- `query_peer_status`
- `get_query_result`
- `grant_permission`
- `list_allowed_peers`

Definition of done:

- the existing HTTP server logic is wrapped by an MCP-facing package or handler layer
- at least one CLI-native agent client can call the MCP tools locally

### step d: add Gatekeeper request and approval flows

Status: complete for the current HTTP and local MCP surfaces

Implement:

- requests
- approvals
- request inbox and response endpoints
- audit coverage for request lifecycle events

Definition of done:

- a requester can send a request to a peer agent
- the peer can accept, defer, deny, or require approval

### step e: add the first edge runtime skeleton

Implement:

- local config loading
- agent registration with the server
- artifact publication path
- query result polling or retrieval

Use fixture-driven data first. Do not start with live GitHub/Jira/Calendar auth.

---

## 5. immediate constraints for future sessions

- keep raw source data out of central storage
- keep permission checks deny-by-default
- do not let untrusted content control sinks
- preserve the current conservative assumption that server-side querying is artifact-based until an ADR says otherwise
- update this file, `README.md`, and `AGENTS.md` whenever the implementation status materially changes

---

## 6. suggested first task for the next session

Start the first edge runtime skeleton while keeping the current HTTP and MCP server behavior stable.

Concrete first changes:

1. add a new `cmd/edge-agent/` entrypoint plus an `internal/edge/` package for local config loading and bootstrap
2. wire agent registration through the existing MCP or HTTP auth flow and persist the issued token locally for the runtime session
3. add fixture-driven artifact publication plus query/result and request inbox polling paths
4. update `README.md`, `AGENTS.md`, and this file once the edge runtime skeleton exists
