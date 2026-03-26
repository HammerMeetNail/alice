# Test Plan

## Document status

Active test engineering guide
Audience: engineering, CI/CD, future agent sessions
Created: 2026-03-25

---

## 1. Goals

- Reach **80% code coverage** across all Go packages
- Run **unit tests** via `make test` (no external dependencies) and `make test-postgres` (with PostgreSQL)
- Run **end-to-end tests** via `make e2e` (full server stack exercised over HTTP)
- All tests executable in CI on every pull request
- Tests are deterministic, parallelizable, and require no manual setup beyond `make` targets

---

## 2. Current state

### Existing test files (7)

| File | What it tests |
|------|---------------|
| `internal/agents/service_test.go` | Registration, auth, email OTP, invite tokens, admin approval |
| `internal/edge/runtime_test.go` | Edge runtime: fixtures, connectors, webhooks, OAuth, credentials |
| `internal/httpapi/router_test.go` | HTTP integration: all routes, auth, cross-org isolation |
| `internal/mcp/server_test.go` | MCP tool surface: embedded + remote modes |
| `internal/policy/service_test.go` | Grant lifecycle, revocation, allowed-peer listing |
| `internal/queries/service_test.go` | Query evaluation: grants, sensitivity, purpose, expiry |
| `internal/storage/memory/store_test.go` | Memory store: grants, approvals, requests, expiry filtering |

### Estimated current coverage

~83 test functions exist. Based on symbol analysis:
- **~65 symbols** directly tested
- **~140 symbols** indirectly tested (via HTTP/MCP integration)
- **~75 symbols** with zero coverage

Estimated line coverage: **45-55%**. The largest gaps are:
1. `internal/storage/postgres/` — 46 methods, all untested (0%)
2. `internal/app/` and `internal/app/services/` — bootstrap and DI (0%)
3. `internal/config/` — env parsing (0%)
4. `internal/requests/` — no dedicated unit tests
5. `internal/approvals/` — no dedicated unit tests
6. `internal/artifacts/` — no dedicated unit tests
7. `internal/audit/` — no dedicated unit tests
8. `internal/email/` — SMTP sender untested
9. `internal/core/` — validation functions not directly tested
10. `internal/edge/watch.go` — watch registration untested

---

## 3. Unit test plan

Unit tests run without external services. They use the in-memory storage backend.
Run with: `make test`

### 3.1 `internal/core/` — NEW FILE: `validate_test.go` ✅ DONE

**Scenarios:**

- `TestValidateOrgSlug`: valid slug, empty, too-long, invalid chars
- `TestValidateEmail`: valid email, missing @, empty
- `TestValidateAgentName`: valid name, empty, too-long
- `TestValidateClientType`: "claude_code", "opencode", invalid
- `TestValidateArtifactType`: each valid type, invalid type
- `TestValidateSensitivity`: low/medium/high/critical, invalid
- `TestValidatePurpose`: valid purpose, empty
- `TestSensitivityAllowed`: low≤high (pass), critical≤medium (fail)
- `TestRiskLevelExceeds`: low>medium (false), high>low (true), equal (false)
- `TestValidationError`: Error() returns expected string
- `TestForbiddenError`: Error() returns expected string

### 3.2 `internal/config/` — NEW FILE: `config_test.go` ✅ DONE

**Scenarios:**

- `TestFromEnvDefaults`: no env vars set → default listen addr, timeouts
- `TestFromEnvCustom`: set `ALICE_LISTEN_ADDR`, `ALICE_DATABASE_URL`, `ALICE_TOKEN_TTL`, `ALICE_SMTP_HOST` → verify parsed values
- `TestFromEnvDatabaseURL`: set/unset `ALICE_DATABASE_URL` → verify presence/absence
- `TestFromEnvSMTPConfig`: set all SMTP vars → verify SMTP config populated

### 3.3 `internal/id/` — NEW FILE: `id_test.go` ✅ DONE

**Scenarios:**

- `TestNew`: returns non-empty string
- `TestNewUniqueness`: 1000 calls produce 1000 unique values
- `TestNewFormat`: result matches expected prefix/length pattern

### 3.4 `internal/email/` — NEW FILE: `email_test.go` ✅ DONE

**Scenarios:**

- `TestNoopSenderSend`: NoopSender.Send() returns nil, does not panic
- `TestNewSenderFromConfigNoop`: `ALICE_SMTP_HOST=noop` returns NoopSender
- `TestNewSenderFromConfigSMTP`: valid SMTP env returns SMTPSender (type assertion)
- `TestNewSenderFromConfigEmpty`: no SMTP env returns nil sender
- `TestSMTPSenderFieldsSet`: after construction, host/port/from are correct

### 3.5 `internal/agents/` — EXTEND: `service_test.go` ✅ DONE (via `service_extended_test.go`)

Existing tests are solid. Add:

- `TestFindUserByEmail_NotFound`: returns false for unknown email
- `TestFindUserByEmail_CrossOrg`: org A user not found from org B context
- `TestFindUserByID_NotFound`: returns false for unknown ID
- `TestFindAgentByUserID_NotFound`: returns false
- `TestRequireAgent_Active`: returns agent for active token
- `TestRequireAgent_Expired`: returns error for expired token
- `TestRequireAgent_PendingVerification`: returns error for unverified agent
- `TestChallengeExpired`: CompleteRegistration with expired challenge returns error
- `TestAlreadyActive`: re-completing registration for active agent returns error
- `TestListPendingAgentApprovals_Empty`: returns empty list for org with no pending agents
- `TestListPendingAgentApprovals_Pagination`: verify offset/limit work

### 3.6 `internal/artifacts/` — NEW FILE: `service_test.go` ✅ DONE

**Scenarios:**

- `TestPublishArtifact`: publish valid artifact → returns saved artifact with ID
- `TestPublishArtifact_InvalidType`: invalid artifact type → ValidationError
- `TestPublishArtifact_EmptySummary`: missing summary → ValidationError
- `TestCorrectArtifact`: owner corrects own artifact → updated artifact returned
- `TestCorrectArtifact_NotOwner`: non-owner corrects → ForbiddenError
- `TestCorrectArtifact_NotFound`: correct nonexistent artifact → error
- `TestListArtifactsByOwner`: publish 3, list returns all 3
- `TestListArtifactsByOwner_Empty`: no artifacts → empty list
- `TestListArtifactsByOwner_OtherUser`: list returns only own artifacts

### 3.7 `internal/requests/` — NEW FILE: `service_test.go` ✅ DONE

**Scenarios:**

- `TestSendRequest`: send request between two users → request saved with pending state
- `TestSendRequest_SelfRequest`: send to self → ErrSelfRequest
- `TestSendRequest_CrossOrg`: send to user in different org → error
- `TestListIncoming`: list incoming requests for a user → correct results
- `TestListIncoming_Pagination`: verify limit/offset
- `TestListIncoming_ExpiredFiltered`: expired requests excluded
- `TestListSent`: list sent requests for a user
- `TestListSent_Empty`: no sent requests → empty list
- `TestRespond_Accept`: respond with accept → state updated
- `TestRespond_Decline`: respond with decline → state updated
- `TestRespond_AlreadyResolved`: double-respond → ErrRequestAlreadyResolved
- `TestRespond_NotRecipient`: wrong user responds → ErrNotRequestRecipient
- `TestRespond_NotFound`: respond to nonexistent → ErrRequestNotFound

### 3.8 `internal/approvals/` — NEW FILE: `service_test.go` ✅ DONE (+ `service_extended_test.go` for query/expired cases)

**Scenarios:**

- `TestListPending`: list pending approvals for a user → correct results
- `TestListPending_Pagination`: verify limit/offset
- `TestListPending_ExpiredFiltered`: expired approvals excluded from list
- `TestResolve_Approve`: resolve approval as approve → state updated
- `TestResolve_Reject`: resolve as reject → state updated
- `TestResolve_AlreadyResolved`: double-resolve → ErrApprovalAlreadyResolved
- `TestResolve_NotOwner`: wrong user resolves → ErrNotDataOwner
- `TestResolve_NotFound`: resolve nonexistent → ErrApprovalNotFound
- `TestResolve_QueryNotFound`: approval for nonexistent query → ErrQueryNotFound

### 3.9 `internal/audit/` — NEW FILE: `service_test.go` ✅ DONE

**Scenarios:**

- `TestRecord`: record audit event → event saved with ID and timestamp
- `TestSummary`: record multiple events, get summary → correct counts
- `TestSummary_SinceFilter`: record events at different times, filter by since
- `TestSummary_Empty`: no events → empty summary
- `TestSummary_Pagination`: verify limit/offset on underlying listing

### 3.10 `internal/policy/` — EXTEND: `service_test.go` ✅ DONE (via `service_extended_test.go`)

Add:

- `TestListGrantsForPair_Empty`: no grants → empty list
- `TestListGrantsForPair_MultipleGrants`: multiple grants returned
- `TestListGrantsForPair_ExpiredFiltered`: expired grants excluded
- `TestGrant_InvalidSensitivity`: invalid sensitivity → ValidationError
- `TestGrant_InvalidPurpose`: empty purpose → ValidationError
- `TestGrant_CrossOrg`: grant to user in other org → error

### 3.11 `internal/queries/` — EXTEND: `service_test.go` ✅ DONE (via `service_extended_test.go`)

Add:

- `TestFindResult_Found`: valid query result → returns response
- `TestFindResult_NotFound`: nonexistent query → error
- `TestFindResult_WrongRequester`: another user's query → error
- `TestEvaluate_MultipleMatchingGrants`: first matching grant wins

### 3.12 `internal/storage/memory/` — EXTEND: `store_test.go` ✅ DONE (via `store_extended_test.go`)

Add scenarios for untested repository methods:

- `TestUpsertOrganization`: create org, re-upsert same slug → same ID
- `TestFindOrganizationBySlug_NotFound`: returns false
- `TestFindOrganizationByID_NotFound`: returns false
- `TestUpsertUser_Update`: upsert same email → updates fields
- `TestFindUserByID_NotFound`: returns false
- `TestUpdateUserRole`: update role → reflected in subsequent find
- `TestUpsertAgent_Update`: upsert same ID → updates fields
- `TestFindAgentByID_NotFound`: returns false
- `TestFindAgentByUserID_NotFound`: returns false
- `TestSaveAgentRegistrationChallenge_AlreadyUsed`: ErrChallengeAlreadyUsed
- `TestRevokeAllTokensForAgent`: revoked tokens return error on find
- `TestSaveArtifact_FindByID`: round-trip save and find
- `TestListArtifactsByOwner_Multiple`: multiple artifacts returned
- `TestSaveQuery_FindQuery`: round-trip
- `TestSaveQueryResponse_FindQueryResponse`: round-trip
- `TestUpdateQueryState`: state update reflected
- `TestSaveEmailVerification_FindPending`: round-trip
- `TestMarkEmailVerified`: verified → no longer pending
- `TestIncrementVerificationAttempts`: counter incremented
- `TestSaveAgentApproval_FindPending`: round-trip
- `TestUpdateAgentApproval`: decision reflected
- `TestConnectorCursorRoundTrip`: save/load cursor
- `TestWebhookDeliveryDedup`: record delivery → HasWebhookDelivery true
- `TestWithTx_Rollback`: error in fn → changes not persisted (memory store is pass-through, but test the contract)
- `TestAppendAuditEvent_ListAuditEvents`: round-trip with since filter

### 3.13 `internal/storage/postgres/` — NEW FILE: `store_test.go` ✅ DONE (gated by ALICE_TEST_DATABASE_URL)

**Gated by build tag or env var** `ALICE_TEST_DATABASE_URL`. When set, tests run against a real PostgreSQL instance. When unset, tests are skipped.

**Pattern:** Each test creates a fresh test schema or uses transaction rollback to isolate test data. A shared `TestMain` function handles migration and cleanup.

**Scenarios (mirror the memory store tests plus PostgreSQL-specific behavior):**

- `TestMigrate`: migrations run without error on clean database
- `TestMigrate_Idempotent`: running migrations twice does not error
- `TestSaveAgentRegistrationChallenge_ConcurrentUse`: two concurrent `CompleteRegistration` attempts — exactly one succeeds (row-level locking)
- `TestUpsertOrganization`: create and re-upsert
- `TestFindOrganizationBySlug`: found and not-found cases
- `TestUpsertUser`: create and update
- `TestFindUserByEmail_OrgScoped`: correct org returns user, wrong org returns not-found
- `TestUpsertAgent`: create and update
- `TestSaveAgentToken_Find_Revoke`: full token lifecycle
- `TestSaveGrant_Find_Revoke_List`: full grant lifecycle with expiry
- `TestSaveArtifact_Find_List`: artifact CRUD
- `TestSaveQuery_Response_StateUpdate`: query lifecycle
- `TestSaveRequest_Find_List_Update`: request lifecycle
- `TestSaveApproval_Find_ListPending_Resolve`: approval lifecycle
- `TestAppendAuditEvent_List`: audit event lifecycle with since filter
- `TestSaveEmailVerification_Lifecycle`: save, find, increment attempts, verify
- `TestSaveAgentApproval_Lifecycle`: save, find pending, update
- `TestSetOrgInviteTokenHash`: set and verify
- `TestUpdateOrgVerificationMode`: update and verify
- `TestWithTx_Commit`: changes visible after commit
- `TestWithTx_Rollback`: changes not visible after error

### 3.14 `internal/httpapi/` — EXTEND: `router_test.go` ✅ DONE (via `router_extended_test.go`)

Add scenarios for uncovered routes and edge cases:

- `TestHealthz`: GET /healthz returns 200 with `{"status":"ok"}`
- `TestRegisterAgent_MalformedJSON`: POST with bad JSON → 400
- `TestRegisterAgent_OversizedBody`: POST with >1MB body → 413
- `TestRegisterAgent_EmptyBody`: POST with empty body → 400
- `TestAuth_MissingHeader`: request without Authorization → 401
- `TestAuth_MalformedBearer`: "Bearer " with garbage → 401
- `TestAuth_ExpiredToken`: valid format but expired → 401
- `TestRateLimit_Unauthenticated`: exceed rate limit on registration → 429
- `TestSecurityHeaders`: verify X-Content-Type-Options, X-Frame-Options, Cache-Control
- `TestPagination_InvalidParams`: limit=-1, offset=abc → 400 or defaults
- `TestVerifyEmail_Unverified403`: unverified agent calling protected route → 403
- `TestPendingAdminApproval_403`: pending-approval agent calling protected route → 403
- `TestRejectedAgent_403`: rejected agent calling protected route → 403
- `TestRotateInviteToken_NonAdmin`: member calls rotate → 403
- `TestListPendingAgents_NonAdmin`: member calls list-pending → 403
- `TestReviewAgent_NonAdmin`: member calls review → 403
- `TestCorrectArtifact_NotOwner_403`: non-owner correction → 403
- `TestListSentRequests`: verify response shape and pagination
- `TestAuditSummary_SinceParam`: with and without since query parameter

### 3.15 `internal/mcp/` — EXTEND: `server_test.go` ✅ DONE (via `server_extended_test.go`)

Add:

- `TestToolCallWithoutAuth`: tool call before registration → error
- `TestToolCallWithExpiredToken`: expired token → error
- `TestRemoteMode_AllTools`: verify all 18 tools work through remote HTTP mode
- `TestRemoteMode_TLSCert`: custom CA accepted via `ALICE_SERVER_TLS_CA`
- `TestRemoteMode_BadURL`: unreachable server URL → connection error
- `TestToolValidation_MissingRequired`: each tool with missing required params → error

### 3.16 `internal/edge/` — EXTEND: `runtime_test.go` ✅ DONE (via `runtime_extended_test.go`)

Add scenarios for gaps:

- `TestLoadConfig_InvalidJSON`: malformed config → error
- `TestLoadConfig_MissingRequired`: missing server URL → validation error
- `TestSecrets_EncryptDecrypt`: AES-GCM round-trip
- `TestSecrets_WrongKey`: decrypt with wrong key → error
- `TestDeriveArtifacts_AllTypes`: status, blocker, commitment derivation
- `TestConnectorReauthError_Message`: error message includes connector type
- `TestCredentialStoreKeyRequiredError_Message`: helpful hint
- `TestCredentialStoreDecryptError_Message`: helpful hint
- `TestWebhookHandler_InvalidSignature`: bad GitHub signature → rejected
- `TestWebhookHandler_ReplayAttack`: duplicate delivery ID → rejected
- `TestServeWebhooks_ListenAndShutdown`: start + graceful stop (short timeout)
- `TestWatchRegistration_Success`: mock GCal API, verify watch created
- `TestWatchRegistration_ReusePeriod`: within 15min → reuse existing watch

---

## 4. End-to-end test plan

E2E tests exercise the full server stack over HTTP. They start a real server (with either memory or PostgreSQL backend), make real HTTP requests, and verify end-to-end behavior including middleware, auth, serialization, and database round-trips.

**Location:** `tests/e2e/` (new directory at repo root)
**Run with:** `make e2e` (memory backend) or `make e2e-postgres` (PostgreSQL backend)
**Build tag:** `//go:build e2e`

### 4.1 Test infrastructure ✅ DONE

`tests/e2e/main_test.go` provides `newE2EServer(t)` (per-test in-process server), `registerAgent`, `doJSON`, `doJSONRaw` helpers.

A shared `TestMain` function in `tests/e2e/main_test.go` (original plan — replaced with per-test server to avoid rate-limit exhaustion):

1. Starts the coordination server on a random port using `app.NewServer(cfg)`
2. Waits for `/healthz` to return 200
3. Stores the base URL in a package-level variable
4. Runs all tests
5. Shuts down the server

A shared helper `registerAgent(t, orgSlug, email, name)` handles the full Ed25519 challenge/response flow and returns a bearer token.

### 4.2 Scenario: Full agent lifecycle ✅ DONE

**File:** `tests/e2e/agent_lifecycle_test.go`

1. Register agent A in org "acme" → get token A
2. Register agent B in org "acme" → get token B
3. Verify healthz returns 200
4. Agent A publishes a status artifact
5. Agent A grants agent B permission to read status artifacts
6. Agent B queries agent A's status → approved, artifact returned
7. Agent B queries agent A's status with wrong purpose → denied
8. Agent A revokes the grant
9. Agent B queries again → denied

### 4.3 Scenario: Request and approval flow

**File:** `tests/e2e/request_approval_test.go`

1. Register agents A and B
2. Agent A grants B permission for status
3. Agent B queries A → generates approval (high risk)
4. Agent A lists pending approvals → sees one
5. Agent A approves
6. Agent B retrieves the query result → artifact returned
7. Agent A sends a request to B
8. Agent B lists incoming requests → sees one
9. Agent B responds (accept)
10. Agent A lists sent requests → sees accepted

### 4.4 Scenario: Cross-org isolation ✅ DONE

**File:** `tests/e2e/cross_org_test.go`

1. Register agent A in org "acme"
2. Register agent C in org "globex"
3. Agent A tries to grant permission to agent C's email → 404
4. Agent A tries to query agent C → 404
5. Agent A tries to send request to agent C → 404

### 4.5 Scenario: Email verification flow

**File:** `tests/e2e/email_verification_test.go`

**Requires:** server started with `ALICE_SMTP_HOST=noop`

1. Register agent → status is `pending_email_verification`
2. Agent tries to publish artifact → 403
3. Agent calls verify-email with wrong code → error, attempts incremented
4. Agent calls verify-email with correct code → status becomes `active`
5. Agent publishes artifact → 200

### 4.6 Scenario: Invite token flow

**File:** `tests/e2e/invite_token_test.go`

1. Create org with `verification_mode=invite_token`
2. First agent registers (generates token) → gets token in response
3. Second agent tries to register without token → 403
4. Second agent registers with correct token → success
5. Admin rotates invite token → old token invalid, new token works

### 4.7 Scenario: Admin approval flow

**File:** `tests/e2e/admin_approval_test.go`

1. Create org with `verification_mode=admin_approval`
2. First agent registers → auto-approved as admin
3. Second agent registers → status `pending_admin_approval`
4. Second agent tries to publish → 403
5. Admin lists pending agents → sees second agent
6. Admin approves second agent
7. Second agent publishes → 200
8. Third agent registers → admin rejects → tokens revoked

### 4.8 Scenario: Rate limiting

**File:** `tests/e2e/rate_limit_test.go`

1. Send 20 rapid registration challenge requests from same IP
2. Verify that requests eventually get 429 Too Many Requests
3. Wait for bucket refill, verify requests succeed again

### 4.9 Scenario: Body size limits and malformed input ✅ DONE

**File:** `tests/e2e/input_validation_test.go`

1. POST /v1/agents/register/challenge with >1MB body → 413
2. POST /v1/artifacts with malformed JSON → 400
3. POST /v1/policy-grants with empty body → 400
4. GET /v1/queries/nonexistent-id → 404
5. DELETE /v1/policy-grants/nonexistent-id → appropriate error

### 4.10 Scenario: Concurrent registration race ✅ DONE

**File:** `tests/e2e/concurrent_test.go`

1. Begin registration → get challenge
2. Launch 10 goroutines all calling CompleteRegistration with the same challenge
3. Exactly 1 succeeds, rest get ErrUsedRegistrationChallenge

### 4.11 Scenario: MCP over HTTP (remote mode)

**File:** `tests/e2e/mcp_remote_test.go`

1. Start coordination server on random port
2. Create MCP server with `WithServerURL` pointing at test server
3. Send `register_agent` tool call via MCP JSON-RPC → success
4. Send `publish_artifact` → success
5. Send `grant_permission` + `query_peer_status` + `get_query_result` → full flow
6. Verify all 18 tools respond without transport errors

### 4.12 Scenario: Edge runtime integration

**File:** `tests/e2e/edge_runtime_test.go`

1. Start coordination server on random port
2. Create edge runtime config pointing at test server
3. Bootstrap via fixture → artifacts published
4. Verify artifacts visible via HTTP API query
5. Run again with updated fixtures → artifacts superseded correctly

---

## 5. How to run tests

### Makefile targets

| Target | What it does |
|--------|-------------|
| `make test` | Run unit tests (memory backend, no external deps) |
| `make test-postgres` | Start PostgreSQL, run unit tests with `ALICE_TEST_DATABASE_URL` |
| `make test-cover` | Run unit tests with coverage report, fail if <80% |
| `make e2e` | Run e2e tests against memory backend |
| `make e2e-postgres` | Start PostgreSQL, run e2e tests against PostgreSQL backend |
| `make test-all` | Run both unit and e2e tests |
| `make ci` | Full CI pipeline: lint, test, e2e, coverage check |

### Commands

```bash
# Unit tests (fast, no deps)
make test

# Unit tests with coverage
make test-cover

# Unit tests with PostgreSQL
make test-postgres

# E2E tests (memory backend)
make e2e

# E2E tests (PostgreSQL backend)
make e2e-postgres

# Everything
make test-all
```

---

## 6. CI configuration ✅ DONE

### GitHub Actions workflow: `.github/workflows/test.yml`

```yaml
name: test
on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: make test-cover

  unit-tests-postgres:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: alice
          POSTGRES_PASSWORD: alice
          POSTGRES_DB: alice
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go test -race -count=1 ./...
        env:
          ALICE_TEST_DATABASE_URL: postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable

  e2e-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: make e2e

  e2e-tests-postgres:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16
        env:
          POSTGRES_USER: alice
          POSTGRES_PASSWORD: alice
          POSTGRES_DB: alice
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go test -tags e2e -race -count=1 ./tests/e2e/...
        env:
          ALICE_TEST_DATABASE_URL: postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable
```

All four jobs run in parallel. PR merges are blocked if any job fails.

---

## 7. Coverage measurement strategy

### How coverage is measured

```bash
go test -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out | tail -1
```

The `make test-cover` target:
1. Runs all unit tests with `-coverprofile`
2. Prints per-function coverage
3. Extracts the total coverage percentage
4. Fails the build if total is below 80%

### Coverage exclusions

The following are excluded from the 80% target (via `//go:generate` comments or separate binary packages):

- `cmd/*/main.go` — thin entry points, tested via e2e
- `internal/edge/github_connector.go` — requires live GitHub API
- `internal/edge/jira_connector.go` — requires live Jira API
- `internal/edge/gcal_connector.go` — requires live Google Calendar API

These files are excluded because they make real external HTTP calls. They are tested indirectly by the edge runtime tests which use `httptest.NewServer` to mock the external APIs.

### Per-package coverage targets

| Package | Current est. | Target | Priority |
|---------|-------------|--------|----------|
| `internal/core` | 0% (direct) | 90% | P1 |
| `internal/config` | 0% | 70% | P2 |
| `internal/id` | 0% | 90% | P2 |
| `internal/email` | 10% | 70% | P2 |
| `internal/agents` | 70% | 85% | P1 |
| `internal/artifacts` | 0% (direct) | 80% | P1 |
| `internal/requests` | 0% (direct) | 80% | P1 |
| `internal/approvals` | 0% (direct) | 80% | P1 |
| `internal/audit` | 0% (direct) | 80% | P1 |
| `internal/policy` | 80% | 90% | P1 |
| `internal/queries` | 75% | 90% | P1 |
| `internal/storage/memory` | 50% | 85% | P1 |
| `internal/storage/postgres` | 0% | 80% | P1 |
| `internal/httpapi` | 65% | 85% | P1 |
| `internal/mcp` | 70% | 85% | P1 |
| `internal/edge` | 60% | 75% | P2 |
| `internal/app` | 0% | 60% | P3 |

---

## 8. Implementation order

Work is ordered by impact on coverage and priority:

### Phase 1: Core unit tests (target: 65% overall)

1. `internal/core/validate_test.go` — new file, quick wins
2. `internal/artifacts/service_test.go` — new file
3. `internal/requests/service_test.go` — new file
4. `internal/approvals/service_test.go` — new file
5. `internal/audit/service_test.go` — new file
6. Extend `internal/storage/memory/store_test.go` — cover remaining methods
7. Extend `internal/agents/service_test.go` — cover remaining methods

### Phase 2: PostgreSQL + HTTP hardening (target: 75% overall)

8. `internal/storage/postgres/store_test.go` — new file, gated by env var
9. Extend `internal/httpapi/router_test.go` — cover edge cases
10. `internal/config/config_test.go` — new file
11. `internal/id/id_test.go` — new file
12. `internal/email/email_test.go` — new file

### Phase 3: E2E tests (target: 80% overall)

13. Set up `tests/e2e/` infrastructure (TestMain, helpers)
14. Agent lifecycle e2e
15. Request/approval e2e
16. Cross-org isolation e2e
17. Email verification e2e
18. Invite token e2e
19. Admin approval e2e
20. Rate limiting and input validation e2e
21. Concurrent registration e2e
22. MCP remote mode e2e
23. Edge runtime integration e2e

### Phase 4: Polish

24. Extend `internal/mcp/server_test.go`
25. Extend `internal/edge/runtime_test.go`
26. Add CI workflow file
27. Update Makefile with all new targets
28. Run coverage report and address any remaining gaps

---

## 9. Test conventions

### Naming

- Test functions: `Test<Unit>_<Scenario>` e.g. `TestGrant_InvalidSensitivity`
- Sub-tests: `t.Run("descriptive name", func(t *testing.T) { ... })`
- Table-driven tests preferred for validation functions

### Helpers

- `testutil.RegisterAgent(t, store)` — creates org, user, agent, returns IDs and token
- `testutil.MustRegister(t, baseURL)` — e2e helper, full HTTP registration flow
- `testutil.SkipIfNoPostgres(t)` — skips test if `ALICE_TEST_DATABASE_URL` not set

### Assertions

Use standard library `testing` package. No external assertion libraries (consistent with existing codebase which has zero external test dependencies).

### Parallelism

- Unit tests: `t.Parallel()` on all tests that use their own memory store instance
- E2E tests: sequential within each file (shared server state), files can run in parallel
- PostgreSQL tests: use transaction rollback or unique schema per test for isolation

### Race detection

All test targets include `-race` flag. CI enforces this.

---

## 10. New test file summary

| File (new) | Package | Test count est. |
|------------|---------|----------------|
| `internal/core/validate_test.go` | core | 11 |
| `internal/config/config_test.go` | config | 4 |
| `internal/id/id_test.go` | id | 3 |
| `internal/email/email_test.go` | email | 5 |
| `internal/artifacts/service_test.go` | artifacts | 9 |
| `internal/requests/service_test.go` | requests | 13 |
| `internal/approvals/service_test.go` | approvals | 9 |
| `internal/audit/service_test.go` | audit | 4 |
| `internal/storage/postgres/store_test.go` | postgres | 21 |
| `tests/e2e/main_test.go` | e2e | 0 (infra) |
| `tests/e2e/agent_lifecycle_test.go` | e2e | 1 (multi-step) |
| `tests/e2e/request_approval_test.go` | e2e | 1 |
| `tests/e2e/cross_org_test.go` | e2e | 1 |
| `tests/e2e/email_verification_test.go` | e2e | 1 |
| `tests/e2e/invite_token_test.go` | e2e | 1 |
| `tests/e2e/admin_approval_test.go` | e2e | 1 |
| `tests/e2e/rate_limit_test.go` | e2e | 1 |
| `tests/e2e/input_validation_test.go` | e2e | 1 |
| `tests/e2e/concurrent_test.go` | e2e | 1 |
| `tests/e2e/mcp_remote_test.go` | e2e | 1 |
| `tests/e2e/edge_runtime_test.go` | e2e | 1 |

**Total new test functions: ~90**
**Total after additions: ~173** (existing 83 + new 90)
