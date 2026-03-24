# implementation plan

## document status

Active implementation guide  
Audience: engineering, platform, future agent sessions  
Last updated: 2026-03-23

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
- an edge runtime path that can register, publish artifacts, derive artifacts from GitHub/Jira/calendar fixture files, bootstrap GitHub/Jira/Calendar connectors through a local OAuth loopback callback, persist bootstrapped connector credentials in a dedicated local credential store, optionally encrypt that store with a local key, refresh expired OAuth credentials when refresh tokens are available, poll live GitHub/Jira/Calendar metadata through env-backed token auth, token-file loading, or bootstrapped local credentials, page through multi-response connector APIs, retry transient 429/5xx connector failures with short backoff, accept signed local GitHub `pull_request` webhooks, accept shared-secret Jira issue webhooks, accept shared-secret Google Calendar change notifications that trigger incremental event fetches, persist webhook delivery receipts and Google Calendar channel message numbers to suppress duplicate or replayed webhook deliveries, persist local connector cursor state, persist the latest published artifact ID per logical derivation slot, persist project signal state so blocker-resolution and commitment-completion transitions can supersede stale blocker and commitment artifacts, derive project-level aggregate status/blocker/commitment artifacts plus transition-aware status deltas, retrieve watched query results, and poll incoming requests
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
- a targeted edge runtime test covering registration reuse, fixture publication, fixture-derived artifacts, replacement-aware connector publication, live GitHub/Jira/Calendar polling, signed GitHub webhook intake, shared-secret Jira webhook intake, shared-secret Google Calendar webhook intake, webhook duplicate/replay handling, connector pagination, transient connector retry behavior, connector cursor persistence, connector OAuth bootstrap, encrypted credential-store behavior, actionable re-auth errors, query-result retrieval, and incoming-request polling
- a Podman-based local container workflow through `make local` and `make down` that runs both the server and PostgreSQL, plus `make postgres-up` / `make test-postgres` helpers that bring up PostgreSQL-only test infrastructure, reuse an existing `alice-db` container when present, and wait for container health before running the PostgreSQL-backed test path

---

## 2. assumptions currently encoded in code

These are implementation choices already present in the codebase and should be treated as the current default unless deliberately changed.

- auth uses a signed Ed25519 registration challenge and short-lived bearer tokens
- the server answers queries from centrally stored derived artifacts
- access control is explicit-grant-only for now
- manager-specific visibility defaults are not implemented
- the server uses PostgreSQL when `ALICE_DATABASE_URL` is set and otherwise falls back to in-memory storage
- the public surface now includes HTTP plus a local stdio MCP server for the implemented Reporter and Gatekeeper tools, while the edge runtime now also exposes local GitHub, Jira, and Google Calendar webhook endpoints for push-based connector input
- request approvals are explicit and API-driven; no user-facing approval UI or automatic risk policy exists yet
- query time windows prefer source observation timestamps when an artifact carries source refs
- the edge runtime uses local JSON config plus artifact fixtures and a normalized event pipeline for GitHub/Jira/calendar inputs
- live polling exists for GitHub, Jira, and calendar inputs through env-backed token auth, token files, bootstrapped local credentials, and source-specific config
- the edge runtime now supports both polling and initial push paths through signed local GitHub webhooks, shared-secret Jira webhooks, and shared-secret Google Calendar change notifications, persists webhook delivery receipts plus Google Calendar channel message numbers to suppress duplicate or replayed deliveries, live connector pollers persist local cursor state, page through multi-response APIs, retry transient 429/5xx failures with short backoff, persists per-project signal state so blocker-resolution and commitment-completion transitions can supersede stale aggregate artifacts, and can now complete a local OAuth bootstrap with PKCE and callback-state validation, persist connector credentials in a dedicated local credential store with file-permission checks, optionally encrypt that store with a local key, refresh expired OAuth credentials when refresh tokens are available, and surface actionable re-auth guidance when refresh cannot proceed
- edge-derived artifacts now carry stable derivation keys, the edge runtime persists the latest published artifact ID per derivation slot, updated logical artifacts supersede prior ones, and query evaluation skips superseded artifacts
- richer project-level derivation now exists, but it is still heuristic and rule-based rather than connector-native or model-assisted

### security gaps identified in code review (2026-03-23)

The following gaps exist in the current implementation. Items marked **fixed** have been addressed; the rest remain open.

- ~~the edge runtime state file (`internal/edge/state.go`) stores the Ed25519 private key and bearer token in plaintext JSON; the credential store already encrypts with AES-256-GCM but that protection does not extend to the state file; the state directory is created with `0755` permissions (should be `0700`)~~ **fixed (step b, 2026-03-23)**
- ~~`FindUserByEmail` is a global lookup not scoped to an organization; grant creation, query evaluation, and request routing never validate that both parties belong to the same org, so a multi-org deployment has no tenant isolation~~ **fixed (step c, 2026-03-23)**
- ~~no HTTP endpoint uses `http.MaxBytesReader` or equivalent; all JSON decoding reads from unbounded request bodies; the webhook handlers correctly use `io.LimitReader(req.Body, 1<<20)` but the same pattern is missing from every server-side POST route~~ **fixed (step a, 2026-03-23)**
- ~~every PostgreSQL query uses `context.Background()` instead of accepting a caller-provided context; queries cannot be cancelled and have no application-level timeouts~~ **fixed (step e, 2026-03-23)**
- ~~the migration system has no `schema_migrations` version tracking table; every migration is re-executed on every startup via `CREATE TABLE IF NOT EXISTS`, which will break on the first non-idempotent migration~~ **fixed (step d, 2026-03-23)**
- the in-memory registration challenge flow has a TOCTOU race: the read-check-update for `used_at` is not atomic across method calls, allowing a concurrent `CompleteRegistration` with the same challenge to succeed twice; the PostgreSQL path is safe due to row-level locking
- ~~`X-Agent-Token` is accepted as an undocumented and untested alternate auth header alongside `Authorization: Bearer`~~ **fixed (step j, 2026-03-23)**
- ~~`Agent.Capabilities` is stored during registration but never checked by any service; any authenticated agent can perform any action~~ **fixed (step k, 2026-03-23)** (field removed)
- ~~`ResolveApproval` in the SQL layer does not include `AND state = 'pending'`, so a concurrent race can re-resolve an already-resolved approval~~ **fixed (step h, 2026-03-23)**
- ~~no rate limiting exists on any endpoint, including unauthenticated registration routes~~ **fixed (step i, 2026-03-23)**
- Jira JQL construction uses `fmt.Sprintf` with the project key from local config without validating the key matches `^[A-Z][A-Z0-9_]+$`
- ~~every PostgreSQL query uses `context.Background()` instead of accepting a caller-provided context; queries cannot be cancelled and have no application-level timeouts~~ (duplicate — fixed step e)
- no list endpoint has pagination; all return unbounded result sets
- ~~the migration system has no `schema_migrations` version tracking table; every migration is re-executed on every startup via `CREATE TABLE IF NOT EXISTS`, which will break on the first non-idempotent migration~~ (duplicate — fixed step d)
- no multi-step database operation uses explicit transactions
- ~~the HTTP server sets `ReadHeaderTimeout` (5s) but not `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, or `MaxHeaderBytes`~~ **fixed (step g, 2026-03-23)**
- ~~the codebase uses the standard `log` package everywhere; the spec calls for structured logging~~ **fixed (step m, 2026-03-23)**
- no CORS, CSRF, or security response headers are set
- ~~grant revocation (`revoke_permission` / `DELETE /v1/policy-grants/:id`) is not implemented~~ **fixed (step f, 2026-03-23)**
- `submit_correction` is not implemented
- `team_scope` and `manager_scope` visibility modes pass through without team/manager relationship logic
- `PolicyGrant.RequiresApprovalAboveRisk` is set but never checked during query evaluation
- the `Redactions` field is always empty; no redaction logic exists
- expired requests, approvals, and grants are not filtered during list/resolve operations (only expired artifacts are filtered during query evaluation)
- no unit tests exist for any service or storage package; all testing is integration-level
- no negative authorization tests exist (cross-agent access, sensitivity ceiling, purpose mismatch, cross-org)
- no token/challenge expiration tests exist
- no malformed-input tests exist

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
  - shared-secret local Google Calendar webhook intake that verifies `X-Goog-Channel-Token`, parses the calendar resource URI, and fetches incremental event changes through the saved cursor
  - persisted webhook delivery receipts and Google Calendar channel message numbers that suppress duplicate or replayed webhook deliveries before publication
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
  - persisted per-project signal state and transition-aware blocker-resolution / commitment-completion status deltas that supersede stale blocker and commitment artifacts
  - query-time filtering of superseded artifacts
  - project-level aggregate status_delta, blocker, and commitment artifacts derived from cross-source events

---

## 4. next recommended steps

The next session should address security and architectural hardening before adding new features. The steps below are ordered by priority — highest-impact security fixes first, then architectural correctness, then spec completeness.

### step a: request body size limits on all HTTP endpoints

Status: **complete** (2026-03-23)

`limitBody` middleware added to `internal/httpapi/router.go` wrapping every POST route with `http.MaxBytesReader(w, req.Body, 1<<20)`. A `writeDecodeError` helper distinguishes `*http.MaxBytesError` (413) from ordinary decode errors (400). All existing tests pass.

### step b: encrypt state file and fix directory permissions

Status: **complete** (2026-03-23)

State directory now created with `0700`. `PrivateKey` and `AccessToken` are encrypted with AES-256-GCM (reusing `credentialStoreAEAD` from `internal/edge/credentials.go`) when an encryption key is configured; a warning is printed to stderr when no key is set. Load path handles both encrypted and plaintext formats for backward compatibility. Encryption key is loaded via the same env-var/file mechanism as the credential store. All existing edge tests pass.

### step c: add cross-organization isolation

Status: **complete** (2026-03-23)

`FindUserByEmail` now accepts `orgID` as a first parameter across the repository interface, both storage implementations, and all call sites. The in-memory store indexes users by `orgID:email` composite key. The PostgreSQL store adds `AND org_id = $1` to the lookup query. All grant, query, and request handlers pass `agent.OrgID` when resolving user emails. All existing tests pass.

### step d: add migration version tracking

Status: **complete** (2026-03-23)

`internal/storage/postgres/migrate.go` now creates a `schema_migrations(version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ)` table on startup. Each migration is wrapped in a transaction: version is checked before execution and recorded after. Already-applied migrations are skipped. Migration version is extracted from the numeric filename prefix (e.g. `001_initial.sql` → 1). All existing tests pass.

### step e: add context propagation through storage layer

Status: **complete** (2026-03-23)

`context.Context` added as the first parameter to all 32 methods across the 8 repository interfaces in `internal/storage/repository.go`, both storage implementations (memory and PostgreSQL), all service layers (`agents`, `artifacts`, `policy`, `queries`, `requests`, `approvals`, `audit`), and all service interface definitions in `internal/app/services/container.go`. All HTTP handlers now pass `req.Context()` to every service call. No `context.Background()` calls remain in the PostgreSQL storage layer. All existing tests pass.

### step f: implement grant revocation

Status: **complete** (2026-03-23)

`RevokedAt *time.Time` field added to `core.PolicyGrant`. Migration `002_grant_revocation.sql` adds `revoked_at TIMESTAMPTZ` column. `FindGrant` and `RevokeGrant` methods added to the `PolicyGrantRepository` interface, both storage implementations, and the policy service. `DELETE /v1/policy-grants/:id` route added to the HTTP router with auth middleware; handler verifies the caller is the grantor (matched via `user.UserID`). `revoke_permission` MCP tool added. Both list queries (`ListGrantsForPair`, `ListIncomingGrantsForUser`) filter `revoked_at IS NULL`. `matchingGrant` in the queries service also skips revoked grants. An audit event is recorded for each revocation. All existing tests pass.

### step g: add request body size limits, HTTP timeouts, and security headers

Status: **complete** (2026-03-23)

`ReadTimeout: 30s`, `WriteTimeout: 60s`, `IdleTimeout: 120s`, `MaxHeaderBytes: 1<<20` added to the `http.Server` in `internal/app/server.go`. `securityHeaders` middleware added to `internal/httpapi/router.go` wrapping the entire mux; sets `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and `Cache-Control: no-store` on every response. All existing tests pass.

### step h: fix approval state guard and add expiry enforcement

Status: **complete** (2026-03-23)

`ResolveApproval` SQL updated to `WHERE approval_id = $1 AND state = 'pending'`; concurrent races now result in a not-found response rather than a double-resolution. Memory store `ResolveApproval` guards the same way. `ListIncomingRequests` and `ListPendingApprovals` SQL queries now filter `(expires_at IS NULL OR expires_at > NOW())`; memory store equivalents filter `!request.ExpiresAt.IsZero() && request.ExpiresAt.Before(now)`. `requests.Respond` returns `ErrExpiredRequest` (HTTP 410) when the request has expired; `approvals.Resolve` returns `ErrExpiredApproval` (HTTP 410) when the approval has expired. Grant expiry in query evaluation was already enforced by `matchingGrant`. All existing tests pass.

### step i: add rate limiting on unauthenticated endpoints

Status: **complete** (2026-03-23)

`ipRateLimiter` (per-IP token bucket, 10 req/min, burst 10) added to `internal/httpapi/router.go`. `rateLimit` middleware wraps `/v1/agents/register/challenge` and `/v1/agents/register`; returns HTTP 429 when the bucket is empty. `clientIP` helper extracts the first address from `X-Forwarded-For` or falls back to `RemoteAddr`. All existing tests pass (registration paths are called once per test, well under the limit).

### step j: remove or document the X-Agent-Token header fallback

Status: **complete** (2026-03-23)

`X-Agent-Token` fallback removed from `accessTokenFromRequest` in `internal/httpapi/router.go`. Only `Authorization: Bearer` is now accepted. All existing tests pass.

### step k: enforce capabilities or remove the field

Status: **complete** (2026-03-23)

`Capabilities` field removed from `core.Agent` and `core.AgentRegistrationChallenge`. Removed from the `capabilities []string` parameter on `BeginRegistration` (service and interface), the HTTP registration request struct, `edge/config.go` `AgentConfig`, the edge runtime registration call, and the MCP `register_agent` tool schema and handler. PostgreSQL `UpsertAgent` and `SaveAgentRegistrationChallenge` no longer write the column (DB column retains its `'[]'::jsonb` default); SELECT queries no longer read it. The DB column can be dropped in a future migration once confirmed safe. All existing tests pass.

### step l: add unit tests for services, storage, and negative authorization

Status: **complete** (2026-03-23)

Unit test files added in `internal/agents/`, `internal/policy/`, `internal/queries/`, and `internal/storage/memory/`. Tests cover:

- **agents**: full registration flow, expired challenge rejection, used-challenge rejection, invalid signature rejection, valid/invalid token authentication, missing-field validation
- **policy**: grant creation with valid and invalid inputs, grantor-only revocation enforcement, peer listing
- **queries**: no-grant denial, matching grant succeeds, wrong purpose/artifact type/sensitivity ceiling returns empty result, revoked grant denied, expired grant returns empty result, project scope matching
- **memory store**: grant revocation idempotency and ownership guard, approval state guard (no double-resolution), expired request/approval filtering from list queries, org-scoped user lookup isolation

All new tests pass alongside all existing integration tests.

### step m: add structured logging

Status: **complete** (2026-03-23)

Replaced all `log.Printf` / `log.Fatalf` / `log.Fatal` calls with `log/slog` (standard library since Go 1.21). Four files updated:

- `cmd/server/main.go`: `log.Fatalf` → `slog.Error` + `os.Exit(1)`, `log.Printf` → `slog.Info` with `"addr"` key
- `cmd/mcp-server/main.go`: same pattern
- `cmd/edge-agent/main.go`: all CLI error paths converted; contextual hints included as `"hint"` key-value pairs; bootstrap prompt URLs use `"url"` and `"connector"` keys
- `internal/httpapi/router.go`: all non-fatal audit-record error logs converted to `slog.Error` with `"op"` key identifying the operation and `"err"` key for the error

`import "log"` removed from all four files. `grep -r '"log"' cmd/ internal/ | grep -v '_test.go'` now returns zero matches. All existing tests pass.

### step n: add explicit transaction handling for multi-step operations

Status: not started

Implement:

- add a `BeginTx` / `CommitTx` / `RollbackTx` pattern (or a `WithTx(ctx, fn)` helper) to the PostgreSQL store
- wrap `ResolveApproval` in a transaction (resolve approval + update linked request state)
- wrap `RespondToRequest` in a transaction when it also creates an approval
- consider wrapping `CompleteRegistration` in a transaction (mark challenge used + create token)

Definition of done:

- multi-step operations are atomic
- a test verifies that a failure midway through a transaction rolls back all changes

### step o: add pagination to list endpoints

Status: not started

Implement:

- add `limit` and `cursor` (or `offset`) query parameters to all list endpoints: `/v1/peers`, `/v1/requests/incoming`, `/v1/approvals`, `/v1/audit/summary`
- add corresponding pagination parameters to the storage interface methods
- default limit: 50; maximum limit: 200
- return a `next_cursor` field in the response when more results exist

Definition of done:

- a test creates >50 artifacts/requests/approvals and confirms that pagination returns the correct subsets
- all existing tests still pass

### step p: implement remaining spec features

Status: not started

Lower-priority spec features to implement after hardening:

- **redaction logic:** add a redaction engine that applies rules from policy grants before returning artifacts in query responses; populate the `Redactions` field on `QueryResponse`
- **`submit_correction`:** implement the MCP tool and HTTP route for correcting previously published artifacts
- **risk-based approval:** check `PolicyGrant.RequiresApprovalAboveRisk` during query evaluation and create an approval record when the query's risk level exceeds the threshold
- **visibility modes:** implement `team_scope` and `manager_scope` by resolving team membership and manager relationships from the org graph
- **Jira JQL validation:** validate that the Jira project key from config matches `^[A-Z][A-Z0-9_]+$` before interpolating into JQL

Definition of done:

- each feature has tests
- the technical spec's feature list matches the implementation

---

## 5. immediate constraints for future sessions

- keep raw source data out of central storage
- keep permission checks deny-by-default
- do not let untrusted content control sinks
- preserve the current conservative assumption that server-side querying is artifact-based until an ADR says otherwise
- all HTTP endpoints that read a request body must enforce a size limit via `http.MaxBytesReader`
- all storage methods must accept `context.Context` and pass it to database queries
- all list endpoints must support pagination; unbounded result sets are not acceptable
- every new migration must be tracked in a `schema_migrations` table; do not rely on `IF NOT EXISTS` for non-idempotent DDL
- multi-step state changes must be wrapped in database transactions
- cross-org isolation must be validated on every grant, query, and request path
- no new fields should be stored but unenforced; either enforce the field or remove it
- structured logging (`log/slog`) must be used for all new code; migrate existing `log` calls when touching a file
- update this file, `README.md`, and `AGENTS.md` whenever the implementation status materially changes

---

## 6. suggested first task for the next session

Steps a through k are complete. The next session should implement step l (unit tests) and then continue with steps m–p.

Concrete next changes:

1. add unit tests for service and storage packages, negative authorization, token/challenge expiry, and input validation (step l)
2. replace `log.Printf` / `log.Fatalf` with structured `log/slog` (step m)
3. add `WithTx` transaction wrapping to multi-step PostgreSQL operations (step n)
4. add `limit`/`cursor` pagination to list endpoints (step o)
5. implement remaining spec features: redaction, `submit_correction`, risk-based approval, visibility modes, Jira JQL validation (step p)

### previously completed steps (for reference)

The following steps from earlier sessions are complete:

- PostgreSQL storage layer with embedded migrations
- signed registration challenge flow with bearer-token issuance
- MCP surface for Reporter and Gatekeeper tools
- Gatekeeper request and approval flows
- edge runtime skeleton with fixture, polling, and webhook-based connector ingestion
- OAuth bootstrap, encrypted credential storage, and token refresh for GitHub, Jira, and Google Calendar connectors
- replacement-aware artifact derivation with project-level aggregation
