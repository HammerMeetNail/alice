# implementation plan

## document status

Active implementation guide  
Audience: engineering, platform, future agent sessions  
Last updated: 2026-03-18

---

## 1. current state

The repository is no longer design-only. The current implementation includes:

- a Go module and runnable coordination server entrypoint
- canonical Go domain models for agents, artifacts, grants, queries, and audit events
- JSON schema files for artifact, query, and policy-grant payloads
- an in-memory storage layer
- HTTP routes for:
  - `POST /v1/agents/register`
  - `POST /v1/artifacts`
  - `POST /v1/policy-grants`
  - `GET /v1/peers`
  - `POST /v1/queries`
  - `GET /v1/queries/:id`
  - `GET /v1/audit/summary`
  - `GET /healthz`
- a targeted handler test covering the permissioned query flow
- a Podman-based local container workflow through `make local` and `make down`

---

## 2. assumptions currently encoded in code

These are implementation choices already present in the codebase and should be treated as the current default unless deliberately changed.

- auth is temporary header-based auth using `X-Agent-ID`
- the server answers queries from centrally stored derived artifacts
- access control is explicit-grant-only for now
- manager-specific visibility defaults are not implemented
- storage is in-memory only; no PostgreSQL persistence exists yet
- the public surface is HTTP only; MCP has not been implemented yet
- no edge runtime, connector ingestion, approvals, or Gatekeeper request flows exist yet

---

## 3. completed implementation steps

From the technical specification’s recommended order:

- step 1 is partially complete:
  - Go domain schemas exist
  - initial JSON schemas exist
- step 2 is partially complete:
  - agents
  - policy grants
  - artifacts
  - queries
  - audit
  - minimal server wiring

Not yet complete inside step 2:

- real auth and token issuance
- durable storage
- org graph and richer authorization rules

---

## 4. next recommended steps

The next session should work through these in order.

### step a: replace in-memory storage with PostgreSQL

Implement:

- `internal/storage/postgres/`
- migrations for agents, users, grants, artifacts, queries, responses, and audit events
- repository interfaces so services stop depending directly on the in-memory store

Definition of done:

- `make local` runs the server and database together
- data survives server restarts
- the existing query-flow test can run against a real DB-backed repository layer

### step b: add real auth for agent registration and requests

Implement:

- registration challenge flow
- server-issued short-lived tokens
- request authentication middleware

Definition of done:

- `X-Agent-ID` is no longer sufficient by itself
- agent registration results in a usable token or session credential

### step c: expose the MCP surface on top of the existing services

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

Start with PostgreSQL persistence and keep the current HTTP behavior stable.

Concrete first changes:

1. add a database service to `compose.yml`
2. add migrations for the currently implemented entities
3. define repository interfaces for the existing services
4. swap the in-memory store behind those interfaces without changing the route contracts
