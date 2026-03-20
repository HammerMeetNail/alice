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
- a local edge runtime skeleton entrypoint with JSON config loading, persisted local state, and an initial webhook-server mode
- canonical Go domain models for agents, auth challenges/tokens, artifacts, grants, queries, requests, approvals, and audit events
- JSON schema files for artifact, query, and policy-grant payloads
- repository interfaces for the currently implemented entities
- a PostgreSQL storage layer with embedded startup migrations
- an in-memory storage layer that remains available as a fallback when `ALICE_DATABASE_URL` is not set
- a signed registration challenge flow with short-lived bearer-token issuance for agents
- an MCP wrapper layer that maps the current tool surface onto the existing HTTP route contracts
- a normalized edge connector event layer shared by fixture and live connector ingestion
- an edge runtime path that can register, publish artifacts, derive artifacts from GitHub/Jira/calendar fixture files, bootstrap GitHub/Jira/Calendar connectors through a local OAuth loopback callback, persist bootstrapped connector credentials in a dedicated local credential store, optionally encrypt that store with a local key, refresh expired OAuth credentials when refresh tokens are available, poll live GitHub/Jira/Calendar metadata through env-backed token auth, token-file loading, or bootstrapped local credentials, page through multi-response connector APIs, retry transient 429/5xx connector failures with short backoff, accept signed local GitHub `pull_request` webhooks, accept shared-secret Jira issue webhooks, persist local connector cursor state, persist the latest published artifact ID per logical derivation slot, derive project-level aggregate status/blocker/commitment artifacts, supersede older logical connector-derived artifacts through stable derivation keys, retrieve watched query results, and poll incoming requests
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
- a targeted edge runtime test covering registration reuse, fixture publication, fixture-derived artifacts, replacement-aware connector publication, live GitHub/Jira/Calendar polling, signed GitHub webhook intake, shared-secret Jira webhook intake, connector pagination, transient connector retry behavior, connector cursor persistence, connector OAuth bootstrap, encrypted credential-store behavior, actionable re-auth errors, query-result retrieval, and incoming-request polling
- a Podman-based local container workflow through `make local` and `make down` that runs both the server and PostgreSQL, plus `make postgres-up` / `make test-postgres` helpers that bring up PostgreSQL-only test infrastructure and wait for container health before running the PostgreSQL-backed test path

---

## 2. assumptions currently encoded in code

These are implementation choices already present in the codebase and should be treated as the current default unless deliberately changed.

- auth uses a signed Ed25519 registration challenge and short-lived bearer tokens
- the server answers queries from centrally stored derived artifacts
- access control is explicit-grant-only for now
- manager-specific visibility defaults are not implemented
- the server uses PostgreSQL when `ALICE_DATABASE_URL` is set and otherwise falls back to in-memory storage
- the public surface now includes HTTP plus a local stdio MCP server for the implemented Reporter and Gatekeeper tools, while the edge runtime now also exposes local GitHub and Jira webhook endpoints for push-based connector input
- request approvals are explicit and API-driven; no user-facing approval UI or automatic risk policy exists yet
- query time windows prefer source observation timestamps when an artifact carries source refs
- the edge runtime uses local JSON config plus artifact fixtures and a normalized event pipeline for GitHub/Jira/calendar inputs
- live polling exists for GitHub, Jira, and calendar inputs through env-backed token auth, token files, bootstrapped local credentials, and source-specific config
- the edge runtime now supports both polling and initial push paths through signed local GitHub webhooks plus shared-secret Jira webhooks, live connector pollers persist local cursor state, page through multi-response APIs, retry transient 429/5xx failures with short backoff, and the edge runtime can now complete a local OAuth bootstrap with PKCE and callback-state validation, persist connector credentials in a dedicated local credential store with file-permission checks, optionally encrypt that store with a local key, refresh expired OAuth credentials when refresh tokens are available, and surface actionable re-auth guidance when refresh cannot proceed
- edge-derived artifacts now carry stable derivation keys, the edge runtime persists the latest published artifact ID per derivation slot, updated logical artifacts supersede prior ones, and query evaluation skips superseded artifacts
- richer project-level derivation now exists, but it is still heuristic and rule-based rather than connector-native or model-assisted

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

- step 5 is partially complete:
  - fixture-driven GitHub ingestion
  - fixture-driven Jira ingestion
  - fixture-driven calendar ingestion
  - deterministic local artifact derivation from connector fixtures
  - a normalized event layer shared by fixture and live connector ingestion
  - live GitHub polling via env-backed token auth and repository mapping
  - signed local GitHub webhook intake for `pull_request` events with HMAC verification
  - live Jira polling via env-backed token auth and project scoping
  - shared-secret local Jira webhook intake for `jira:issue_created` and `jira:issue_updated`
  - live calendar polling via env-backed token auth and calendar scoping
  - pagination across live GitHub, Jira, and calendar connector APIs
  - transient retry/backoff handling for 429, 502, 503, and 504 connector responses
  - persisted connector cursor state for incremental live polling
  - connector secret loading via env vars or token files
  - local OAuth bootstrap flows for GitHub, Jira, and calendar connectors through a loopback callback
  - persisted connector credentials in a dedicated local credential store reused by live pollers
  - dedicated local connector credential storage separate from the general edge state file
  - optional AES-GCM encryption for the local connector credential store
  - automatic refresh-token exchange for expired stored OAuth credentials
  - actionable connector re-auth errors surfaced to the edge-agent CLI
  - stable derivation keys, persisted latest-derived-artifact tracking, and replacement-aware publication for connector-derived artifacts
  - query-time filtering of superseded artifacts
  - project-level aggregate status_delta, blocker, and commitment artifacts derived from cross-source events

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

Status: complete for the current local runtime skeleton

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

Build on the current paginated, replacement-aware edge runtime with the remaining push path and richer derivation.

Concrete first changes:

1. add a Google Calendar push path so polling is no longer the only incremental input mechanism for calendar data
2. deepen derivation beyond the current project-level heuristics with richer blocker-resolution and commitment-completion signals
3. harden local operator workflows further with rotation tooling and safer credential-key management
4. keep raw source content local and continue publishing only derived artifacts through the existing runtime client
