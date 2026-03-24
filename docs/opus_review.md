# Project Review — alice

**Reviewer:** Claude Opus  
**Date:** 2026-03-23  
**Scope:** Full codebase, documentation, and implementation-plan alignment  
**Verdict:** Viable path forward with actionable issues to address

---

## Executive Summary

The alice project is in strong shape for its stage of development. The architecture is sound, the security posture is unusually mature for an early implementation, and the code quality is consistent. The technical spec, threat model, and implementation plan are well-aligned with each other and with the actual code.

That said, this review identified several issues that range from security gaps to architectural concerns to spec-vs-implementation drift. None are fatal, but several should be addressed before moving further.

The findings are organized by severity: security issues first, then architectural concerns, then spec/plan drift, then minor items.

---

## 1. Security Issues

### 1.1 HIGH — Private key and access token stored in plaintext state file

**Location:** `internal/edge/state.go`

The edge runtime's Ed25519 private key and bearer token are persisted in a plaintext JSON file. The credential store already supports AES-256-GCM encryption, but this protection does not extend to the state file. The state directory is also created with `0755` permissions (world-listable), while the credential store directory correctly uses `0700`.

**Risk:** Local file read vulnerability, backup exposure, or workstation compromise leaks the agent's identity and active session.

**Recommendation:** Either encrypt the state file using the same AES-256-GCM mechanism, or at minimum use `0700` for the state directory and encrypt the private key and token fields within the state file.

### 1.2 HIGH — No cross-organization isolation enforcement

**Locations:** `internal/agents/service.go`, `internal/queries/service.go`, `internal/httpapi/router.go` (grant handler), `internal/storage/`

`FindUserByEmail` is a global lookup not scoped to an organization. The grant and query flows set `OrgID` on records but never validate that both parties belong to the same org. In the storage layer, queries look up entities by their own ID without also filtering by `org_id`.

**Risk:** In a multi-org deployment, Agent A in Org-X could potentially grant permissions to, query, or send requests to User B in Org-Y.

**Recommendation:** Add org-scoping to `FindUserByEmail` (accept orgID as a parameter), and validate org membership consistency in grant creation and query evaluation.

### 1.3 HIGH — No request body size limits on HTTP endpoints

**Location:** `internal/httpapi/router.go`

No endpoint uses `http.MaxBytesReader` or equivalent. All JSON decoding uses `json.NewDecoder(req.Body).Decode` against unbounded request bodies. The `Content`, `Question`, `StructuredPayload`, and `Metadata` fields accept arbitrarily large content.

**Risk:** Memory exhaustion via oversized POST bodies. Any unauthenticated endpoint (registration challenge/completion) is especially vulnerable.

**Recommendation:** Wrap `req.Body` with `http.MaxBytesReader` (e.g., 1MB) on all endpoints. The webhook handlers already do this correctly (`io.LimitReader(req.Body, 1<<20)`), so the pattern exists in the codebase.

### 1.4 MEDIUM — Registration challenge TOCTOU race in memory store

**Location:** `internal/agents/service.go` (`CompleteRegistration`)

The flow reads the challenge, checks `used_at`, then updates it. With the in-memory store, a mutex protects individual operations but the read-check-update sequence across multiple method calls is not atomic. Two concurrent `CompleteRegistration` calls with the same challenge could both succeed.

**Risk:** Challenge replay within the race window could issue duplicate tokens.

**Recommendation:** Make the "mark challenge used" operation atomic (compare-and-swap), or wrap the sequence in a single locked operation. The PostgreSQL path handles this via row-level locking but the memory path does not.

### 1.5 MEDIUM — `X-Agent-Token` header accepted as undocumented auth fallback

**Location:** `internal/httpapi/router.go:722-723`

`accessTokenFromRequest` accepts tokens via `Authorization: Bearer ...` or `X-Agent-Token: ...`. The alternate header is not documented, not tested, and not mentioned in any config or example. The `TestProtectedRoutesRequireBearerToken` test confirms `X-Agent-ID` alone is rejected but does not test `X-Agent-Token`.

**Risk:** Security monitoring, WAF rules, or logging that only inspects the `Authorization` header would miss tokens in `X-Agent-Token`. This also widens the attack surface.

**Recommendation:** Either remove the fallback, or document and test it explicitly.

### 1.6 MEDIUM — Agent capabilities field stored but never enforced

**Location:** `internal/core/types.go` (Agent.Capabilities), all service layers

The `Capabilities` field (e.g., `["publish_artifact", "respond_query"]`) is stored during registration but never checked by any service. Any authenticated agent can perform any action regardless of declared capabilities.

**Risk:** An agent registered with limited capabilities can still publish artifacts, create grants, send requests, etc.

**Recommendation:** Either enforce capabilities in the auth middleware or service layer, or remove the field to avoid a false sense of security.

### 1.7 MEDIUM — `ResolveApproval` does not guard against invalid state transitions at storage level

**Location:** `internal/storage/postgres/requests.go`, `internal/storage/memory/store.go`

The `UPDATE approvals SET state = $2` query does not include `AND state = 'pending'`. A resolved approval could theoretically be re-resolved (changed from "approved" to "denied" or vice versa). The application layer checks state in the approval service, but a concurrent request could race past the check.

**Risk:** Approval state corruption under concurrent access.

**Recommendation:** Add `AND state = 'pending'` to the SQL UPDATE and return a conflict error if zero rows are affected.

### 1.8 MEDIUM — No rate limiting on unauthenticated endpoints

**Location:** `internal/httpapi/router.go`

The `/v1/agents/register/challenge` and `/v1/agents/register` endpoints are unauthenticated and have no rate limiting. Authenticated endpoints also lack rate limits.

**Risk:** Denial-of-service via registration challenge flooding or query amplification.

**Recommendation:** Add rate limiting by IP or by org_slug/email combination on registration endpoints. Consider per-agent rate limits on authenticated endpoints.

### 1.9 LOW — Jira JQL construction uses string interpolation from config

**Location:** `internal/edge/jira_connector.go`

The project key is inserted into JQL via `fmt.Sprintf` with only `strings.TrimSpace`. The value comes from config, not user input, but if config is user-controlled (which it is — it's a local JSON file), this could be a JQL injection vector.

**Recommendation:** Validate that the project key matches `^[A-Z][A-Z0-9_]+$` before use in JQL.

---

## 2. Architectural Concerns

### 2.1 No context propagation in storage layer

**Location:** `internal/storage/postgres/*.go`

Every PostgreSQL query uses a hardcoded `context.Background()` instead of accepting a caller-provided context. This means:
- Queries cannot be cancelled when an HTTP request is cancelled.
- There are no query timeouts at the application level.
- A slow or blocked query will hold a connection indefinitely.

**Recommendation:** Thread `context.Context` through the repository interfaces and pass it from the HTTP handler through the service layer to storage.

### 2.2 No pagination on list endpoints

**Locations:** `handleListAllowedPeers`, `handleListIncomingRequests`, `handleListPendingApprovals`, `handleAuditSummary`, `ListArtifactsByOwner`

All list operations return unbounded result sets. The storage layer queries also have no `LIMIT` or cursor-based pagination.

**Risk:** Memory exhaustion and slow responses as data grows.

**Recommendation:** Add cursor-based or offset/limit pagination to all list endpoints and their backing storage queries.

### 2.3 Migration system has no version tracking

**Location:** `internal/storage/postgres/migrate.go`

There is no `schema_migrations` table. Every migration is re-executed on every call to `Migrate()`. The current DDL uses `CREATE TABLE IF NOT EXISTS` which is idempotent, but any future migration using `ALTER TABLE`, `INSERT`, or other non-idempotent DDL will break on re-run.

**Recommendation:** Add a `schema_migrations` table that tracks which migrations have been applied. This is critical before adding any additional migration files.

### 2.4 No explicit transaction handling

**Location:** All PostgreSQL storage methods

Every database operation is a single `ExecContext` or `QueryRowContext` call with no transaction wrapping. Multi-step operations (e.g., "resolve approval then update linked request state") are not atomic.

**Risk:** Partial state updates on failure or under concurrent access.

**Recommendation:** Add transaction support for multi-step operations, particularly in the approval resolution and request response flows.

### 2.5 HTTP server missing timeout settings

**Location:** `internal/app/server.go`

Only `ReadHeaderTimeout` (5s) is set. `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, and `MaxHeaderBytes` are not configured.

**Risk:** Slow-body attacks, connection exhaustion, or unbounded header sizes.

**Recommendation:** Set all four timeout values and `MaxHeaderBytes`.

### 2.6 No structured logging

**Location:** All `cmd/` entrypoints, all internal packages

The entire codebase uses the standard `log` package. There is no structured logging (e.g., `slog`, `zap`). The tech spec (section 24) calls for structured log fields, metrics, and tracing.

**Recommendation:** Migrate to `log/slog` (standard library since Go 1.21) which provides structured key-value logging with minimal dependency overhead.

### 2.7 No CORS, CSRF, or security headers

**Location:** `internal/httpapi/router.go`

The router does not set `X-Content-Type-Options`, `X-Frame-Options`, `Strict-Transport-Security`, or CORS headers.

**Recommendation:** Add a middleware that sets standard security response headers. Even for an API-only server, `X-Content-Type-Options: nosniff` is good practice.

---

## 3. Spec/Plan Drift and Incomplete Implementations

### 3.1 Grant revocation not implemented

The technical spec defines `revoke_permission` as MCP tool 17.11 and `DELETE /v1/policy-grants/:id` as an internal route. Neither is implemented. Once a grant is created, it cannot be revoked or expired (the `ExpiresAt` field exists but is never set during `Grant()`).

**Impact:** Users cannot undo permission grants. This is a significant gap for the privacy guarantees the system promises.

### 3.2 `submit_correction` not implemented

The technical spec defines `submit_correction` (MCP tool 17.13) for correcting previously published artifacts. Not implemented.

### 3.3 Visibility modes partially implemented

The spec defines four visibility modes: `private`, `explicit_grants_only`, `team_scope`, `manager_scope`. Only `private` is actively filtered in query evaluation. `team_scope` and `manager_scope` pass through to the grant check without any team/manager relationship logic, effectively behaving identically to `explicit_grants_only`.

### 3.4 Risk-based approval not enforced for queries

`PolicyGrant.RequiresApprovalAboveRisk` is set to `L1` by default during grant creation, but the query evaluation pipeline never checks it. All queries proceed without approval regardless of risk level. This contradicts the spec's intent (section 16) that high-risk operations should require approval.

### 3.5 Redaction not implemented

The spec (section 16.2, 12.2) calls for redaction as a policy output. The query response type has a `Redactions` field but it is always set to an empty slice. No redaction logic exists. The full artifact `Content` field is returned verbatim to permitted queriers.

### 3.6 Content/policy separation not yet implemented

The spec's core security invariant (section 10) is that untrusted content must be separated from policy in model inputs. The edge runtime currently does rule-based derivation without any model involvement, so this is not yet relevant — but the derivation code does not have a provenance-tracking layer that would enforce this separation when models are introduced.

### 3.7 No expiry enforcement at query/request/approval time

Expired requests can be responded to. Expired approvals can be resolved. The `ExpiresAt` fields are set but never checked during `ListIncoming`, `Respond`, `ListPending`, or `Resolve`. Only expired *grants* and *artifacts* are filtered during query evaluation.

### 3.8 Repo layout differs from spec

The spec (section 21) proposes several packages that don't exist yet: `orggraph`, `delivery`, `normalize`, `derive`, `promptguard`, `models`, `crypto`, `config/defaults.go`, `telemetry`. The actual layout puts connector and derivation logic in `internal/edge/` and has no separate packages for several spec-defined boundaries.

This is not necessarily wrong — the edge runtime is a monolithic package that contains functionality the spec distributes across multiple packages. But it means the spec's "modular connector system" with separate `connectors/github/`, `connectors/jira/`, `connectors/gcal/` packages is not reflected in the code. All connector logic lives in `internal/edge/` as separate files within one package.

---

## 4. Test Coverage Gaps

### 4.1 No unit tests for any service or storage package

All 14 non-test internal packages (`agents`, `app`, `approvals`, `artifacts`, `audit`, `config`, `core`, `id`, `policy`, `queries`, `requests`, `storage/memory`, `storage/postgres`) have zero `_test.go` files. Testing is only through integration tests in `httpapi`, `mcp`, and `edge`.

This means:
- No unit tests for input validation logic (`core.Validate*` functions)
- No unit tests for policy evaluation, sensitivity comparison, or grant matching
- No unit tests for storage behavior (unique constraints, upsert semantics, etc.)
- No unit tests for ID generation properties

### 4.2 No negative authorization tests

There are no tests for:
- Agent A trying to read Agent B's query result
- Querying without any matching grant
- Sensitivity ceiling enforcement (artifact with `high` sensitivity blocked by grant with `max_sensitivity: medium`)
- Purpose mismatch (querying with a purpose not in the grant)
- Artifact type filtering (querying for `commitment` when grant only allows `summary`)
- Cross-org isolation

### 4.3 No token/challenge expiration tests

No test simulates or verifies token expiration or challenge expiration behavior. The TTLs are set but never tested.

### 4.4 Only GitHub OAuth/refresh/token-file tested

The Jira and GCal connectors have no tests for OAuth bootstrap, token refresh, token-from-file, or `ConnectorReauthRequiredError`. Only GitHub exercises these paths.

### 4.5 No malformed input tests

No tests submit malformed JSON, missing required fields, oversized payloads, or unexpected content types. The validation functions exist but are only tested through happy-path integration flows.

---

## 5. Minor Items

### 5.1 MCP access token env var read outside config package

`cmd/mcp-server/main.go` reads `ALICE_MCP_ACCESS_TOKEN` directly via `os.Getenv` rather than through `config.FromEnv()`. This is inconsistent with the otherwise clean config pattern.

### 5.2 No audit logging on `get_query_result`

Reading a query result (which reveals artifact content) does not generate an audit event. This is an observability gap in the data-access trail.

### 5.3 Manual path parameter extraction

URL path parameters are extracted via `strings.TrimPrefix` / `trimActionPath` instead of a proper router with path parameters. No validation that extracted IDs conform to an expected format. While the storage layer handles arbitrary strings safely, this is fragile.

### 5.4 Memory/Postgres behavioral parity gaps

- The memory store allows duplicate artifact IDs in the per-user index; Postgres would reject with a PK violation.
- Email normalization is inconsistent: Postgres normalizes the email on the returned `User` struct; memory does not.
- The memory store does not enforce FK relationships or UNIQUE constraints.

### 5.5 `json.NewEncoder(w).Encode` error ignored

In `writeJSON`, if JSON encoding fails after headers are written, the client receives a partial response. Minor robustness issue.

### 5.6 Database enum fields have no CHECK constraints

All enum-like columns (`status`, `state`, `risk_level`, `sensitivity`, `visibility_mode`) are `TEXT` without CHECK constraints. Validation depends entirely on the application layer.

### 5.7 Webhook config nesting inconsistency

GitHub webhook config puts `repositories` inside `webhook`. Jira puts `projects` inside `webhook`. GCal puts `calendars` at the connector root alongside `webhook`. This inconsistency could confuse users.

### 5.8 No `t.Parallel()` in any tests

None of the tests call `t.Parallel()`, meaning they run sequentially within each package. For the edge runtime's 28 tests (3500 lines), this could be slow.

### 5.9 Go version pinned at 1.23.2

`go.mod` specifies Go 1.23.2. The Dockerfile uses `golang:1.23-alpine`. These are compatible, but Go 1.23 is not yet released as of the time these docs reference 2026 dates. Ensure the version pins are correct for the actual build environment.

---

## 6. Positive Observations

The following patterns are notably well-done and should be preserved:

1. **Crypto usage is correct throughout.** Ed25519 for registration, SHA-256 for token hashing, AES-256-GCM for credential storage, HMAC-SHA256 for webhooks, `crypto/rand` exclusively for security-sensitive randomness, `subtle.ConstantTimeCompare` for token validation. No weak or deprecated primitives found.

2. **Webhook security is production-grade.** HMAC verification, replay protection with delivery ID dedup, sequence number tracking, body size limits, and TTL-based pruning of old delivery records.

3. **OAuth implementation follows best practices.** PKCE with S256, state parameter validation, loopback-only callback URLs, automatic token refresh with proper error handling.

4. **Error messages are properly generic.** Auth failures return generic messages that do not distinguish between unknown tokens, expired tokens, or revoked tokens. This prevents enumeration attacks.

5. **The artifact supersession model is well-designed.** Stable derivation keys, persisted latest-artifact tracking, and query-time filtering of superseded artifacts provide a clean replacement semantic.

6. **The edge runtime test suite is comprehensive.** 28 tests covering all three connector types across fixtures, live polling, webhooks, pagination, retries, OAuth bootstrap, encrypted storage, and state transitions. This is strong coverage for an early implementation.

7. **Design documents are high quality.** The technical spec, threat model, and implementation plan are thorough, internally consistent, and provide a clear roadmap. The threat model in particular is unusually detailed for a project at this stage.

8. **The monolith-first approach is correct.** All logic is in a single binary with clean package boundaries. The code is well-organized for future extraction into separate services if needed.

---

## 7. Recommended Priority Order

If addressing these findings, I would recommend this order:

1. **Request body size limits** (1.3) — trivial to fix, high impact
2. **State file encryption and directory permissions** (1.1) — protects the most sensitive local data
3. **Cross-org isolation** (1.2) — fundamental multi-tenancy correctness
4. **Migration version tracking** (2.3) — required before adding any more migrations
5. **Context propagation in storage** (2.1) — enables request cancellation and timeouts
6. **Grant revocation** (3.1) — critical for the privacy story
7. **Expiry enforcement** (3.7) — makes existing time-based fields actually work
8. **Rate limiting on registration** (1.8) — basic DoS protection
9. **Negative authorization tests** (4.2) — validates the security model
10. **Body size limits, pagination, HTTP timeouts** (2.2, 2.5) — production readiness
