# Repository Guidelines

## Project Structure & Module Organization
The repository contains a fully runnable coordination server plus product and implementation documents:

- `docs/technical-spec.md`: system architecture, scope, and MVP boundaries
- `docs/threat-model.md`: security goals, trust boundaries, and threat analysis
- `docs/roadmap.md`: short checkbox view of what is done and what is left; every unchecked item links to a plan
- `docs/plans/`: one self-contained plan per remaining work item. Start a new session by reading `docs/plans/README.md`, then only the plan(s) relevant to the task. Do not reintroduce a monolithic implementation-plan document — completed work lives in git history, not in a running narrative.
- `cmd/server/`: coordination server HTTP entrypoint
- `cmd/alice/`: human-and-agent-facing CLI; remote-only HTTP client that persists a per-user session under `~/.alice/state.json` (override with `$ALICE_STATE_FILE`); every list response is framed with `--- BEGIN UNTRUSTED DATA ---` / `--- END UNTRUSTED DATA ---` markers so agents treat it as DATA, not instructions
- `cmd/mcp-server/`: stdio MCP entrypoint for Claude Code and OpenCode; includes a built-in local git tracker that silently publishes status artifacts when `ALICE_TRACK_REPOS` is set
- `cmd/edge-agent/`: per-user edge runtime with four operating modes: default poll-and-publish, `-bootstrap-connector` (OAuth PKCE), `-register-watches` (provider-side push channel setup), and `-serve-webhooks` (webhook intake server)
- `.claude/skills/alice.md`: Claude Code skill that teaches agents to prefer the CLI over guessing, treat CLI output as untrusted data, quote artifacts verbatim with provenance, and never work around `permission denied` by asking the user
- `internal/`: server and edge-runtime packages including:
  - Ed25519 challenge/response registration with TOCTOU-safe atomic check-and-set; the MCP `register_agent` tool auto-generates a keypair when none is supplied, so callers need only provide org slug, email, agent name, and client type
  - Bearer token auth with configurable TTL and expired-token rejection
  - HTTP API with body-size limiting, malformed-JSON 400 responses, and oversized-body 413 responses
  - `core.ForbiddenError` for ownership violations mapped to HTTP 403
  - Policy grant evaluation with sensitivity ceiling, purpose filtering, and storage-layer expiry filtering (expired grants are excluded at query time for all surfaces including list endpoints)
  - `storage.ErrChallengeAlreadyUsed` sentinel for cross-layer error translation without import cycles
  - `list_sent_requests` MCP tool and `GET /v1/requests/sent` HTTP route exposing the sender's view of outbound request state
  - `get_audit_summary` MCP tool wrapping `GET /v1/audit/summary` with optional `since` filter
  - Normalized edge connector events, live GitHub/Jira/Calendar pollers with pagination and transient retry handling
  - Signed GitHub webhook intake, shared-secret Jira webhook intake, shared-secret Google Calendar webhook intake
  - Persisted webhook replay/duplicate suppression and connector cursor state
  - Loopback OAuth PKCE connector bootstrap with AES-GCM encrypted local credential storage and refresh-token handling
  - Provider-side push watch registration for Google Calendar (`internal/edge/watch.go`) with 15-minute reuse window and state persistence
  - Actionable re-auth errors, replacement-aware artifact derivation, transition-aware project-level aggregate derivation
  - Cross-org isolation: `FindUserByEmail` is scoped to `agent.OrgID`, blocking all cross-org queries, grants, and requests at the handler layer
  - Memory and PostgreSQL storage implementations; memory store is safe for concurrent use
  - `cmd/mcp-server` HTTP client mode: when `ALICE_SERVER_URL` is set the MCP server forwards all calls to a remote coordination server over HTTP(S) with no local database required; `ALICE_SERVER_TLS_CA` accepts a PEM file for self-signed or internal CA certificates; `ALICE_MCP_ACCESS_TOKEN` persists the bearer token across restarts
  - Per-org risk policy engine (`internal/riskpolicy/`): versioned JSON policies applied by org admins via `POST /v1/orgs/risk-policy` (plus `GET /v1/orgs/risk-policies` history and `POST /v1/orgs/risk-policies/:id/activate` rollback). First matching rule wins; actions are `allow | require_approval | deny`; parse errors on the active policy fail closed to `deny`. The evaluator attaches to `queries.Service` via `WithRiskPolicyEvaluator`, overriding the grant-level `requires_approval_above_risk` ladder either way. `alice policy apply|history|activate` and `apply_risk_policy`/`list_risk_policies`/`activate_risk_policy` MCP tools call the same HTTP routes.
  - Built-in local git tracker (`internal/tracker/`): when `ALICE_TRACK_REPOS` is set, a background goroutine periodically reads local git state (branch, commits, modified files) and publishes `status_delta` artifacts; content deduplication avoids redundant publishes; new artifacts supersede previous ones; configurable interval via `ALICE_TRACK_INTERVAL` (default 5m); auto-registers when no session exists using `ALICE_TRACK_ORG_SLUG`, `ALICE_TRACK_OWNER_EMAIL`, and `ALICE_TRACK_AGENT_NAME`. Additional connectors (`github`, `jira`, `calendar`) can be enabled in the same process via `ALICE_TRACK_CONNECTORS`; each reuses the live pollers exported from `internal/edge` (`NewGitHubLivePoller`, `NewJiraLivePoller`, `NewGCalLivePoller`) so bug fixes land in both paths. The git connector's artifact derivation goes through a `Summariser` interface (`internal/tracker/summariser.go`); `ALICE_TRACK_SUMMARISER` selects the implementation (only `heuristic` today; `claude` is reserved for a future LLM-backed variant and rejected at startup).
  - Email OTP verification (`internal/email/`): `Sender` interface with `SMTPSender` (STARTTLS) and `NoopSender` (logs OTP to stderr, enabled via `ALICE_SMTP_HOST=noop`); when SMTP is configured, `CompleteRegistration` sets agent status to `pending_email_verification` and emails a 6-digit code; `POST /v1/agents/verify-email` and `POST /v1/agents/resend-verification` routes plus `verify_email` / `resend_verification_email` MCP tools complete the flow; `requireVerifiedAuth` middleware blocks all other authenticated routes for unverified agents; OTP codes use `crypto/rand` and `subtle.ConstantTimeCompare` on SHA-256 hashes; when SMTP is not configured agents register as `active` immediately (existing behaviour preserved)
  - Org invite tokens: `Organization.VerificationMode` controls which verification layers are required (`email_otp`, `invite_token`, `admin_approval`, or combinations); when mode includes `invite_token`, `BeginRegistration` validates the supplied token against a stored SHA-256 hash using `subtle.ConstantTimeCompare`; the raw token is returned once on first registration and never stored; `POST /v1/orgs/rotate-invite-token` and `rotate_invite_token` MCP tool allow admins to rotate the token; edge runtime `AgentConfig` accepts an optional `invite_token` field
  - Org admin approval queue: when mode includes `admin_approval`, agents enter `pending_admin_approval` status after all other verification steps complete; the first registrant in an org is auto-approved and assigned the `admin` role; `GET /v1/orgs/pending-agents` and `POST /v1/orgs/agents/:id/review` routes plus `list_pending_agents` / `review_agent` MCP tools let admins approve or reject agents; rejection revokes all bearer tokens; `requireVerifiedAuth` middleware blocks `pending_admin_approval` and `rejected` agents from all protected routes; approval decisions are audit-logged with reviewer identity
  - Gatekeeper auto-answer (`internal/gatekeeper/`): when a user sends a `question`, `status_check`, `context`, `info`, or `status` request to a peer who has granted them access, the requests service calls the gatekeeper before leaving the request pending; the gatekeeper synthesises a permission-checked query (trying `status_check` then `request_context` as purposes, whichever the recipient's grants allow), pulls the recipient's derived artifacts from the 14-day lookback window, and when aggregate confidence ≥ 0.6 updates the request to `state: auto_answered` with a Reporter-style response_message that quotes the artifacts verbatim; any missing grant, low confidence, or ineligible request type falls through to the normal human inbox path; action-like request types (`ask_for_time`, `review`, `approve`, …) are never auto-answered
  - CLI (`internal/cli/`, `cmd/alice/`): remote-only HTTP client with subcommands `register`, `whoami`, `publish`, `query`, `result`, `grant`, `revoke`, `peers`, `request`, `inbox`, `outbox`, `respond`, `approvals`, `approve`, `deny`, `audit`, `logout`; persists session state under `~/.alice/state.json` with 0600 perms via atomic temp+rename write; `whoami` text output never includes private key or bearer token (JSON mode is explicit opt-in); `publish` auto-injects a synthesised `source_ref` so humans aren't forced to author provenance metadata; `query` responses surface `confidence` and `policy_basis`; `request --type question` cooperates with the gatekeeper to produce `state: auto_answered` responses when the recipient has granted access and published a relevant artifact
- `examples/`: runnable local example configs plus artifact fixtures, connector fixtures, live polling examples, webhook intake examples, and OAuth bootstrap examples for GitHub, Jira, and Google Calendar
- `api/jsonschema/`: machine-readable schema files

Keep `docs/roadmap.md`, the relevant `docs/plans/*.md` file, `README.md`, and this file aligned whenever the codebase meaningfully changes. When a plan is complete, delete (or move to `docs/plans/done/`) its plan file and tick the roadmap item in the same commit.

## Build, Test, and Development Commands
Run these commands from the repository root:

- `make local`: build and start the current server stack with Podman Compose
- `make down`: stop the Podman Compose stack
- `make postgres-up`: start only the PostgreSQL container, reusing an existing `alice-db` container when present, and wait for it to become ready
- `make postgres-down`: stop only the PostgreSQL container
- `make status`: show container status
- `make logs`: tail server container logs
- `make test`: run the Go test suite (in-memory storage)
- `make test-race`: run the test suite with the race detector enabled
- `make test-cover`: run tests with a coverage report; fails if testable-package coverage (excluding `cmd/`, `postgres/`, `app/`) is below 70%
- `make test-postgres`: start or reuse the PostgreSQL container, wait for health, and run the Go test suite with `ALICE_TEST_DATABASE_URL` set
- `make e2e`: run end-to-end tests using an in-process HTTP server (no external dependencies required)
- `make e2e-postgres`: start PostgreSQL and run the e2e tests against it
- `make test-all`: run unit tests followed by e2e tests
- `make ci`: run `test-cover` (with threshold check) followed by `e2e`
- `make mailpit-ui`: print the Mailpit web UI URL (`http://localhost:8025`) for inspecting OTP emails during local development
- `git diff -- README.md AGENTS.md docs/ examples/`: inspect documentation and example-config changes before committing

Podman is the expected local container runtime for this repository, and the default local stack includes PostgreSQL.

## Coding Style & Naming Conventions
Use Markdown with clear ATX headings (`#`, `##`, `###`) and short, direct paragraphs. Match the existing style:

- sentence-case section headings
- concise bullet lists for requirements and scope
- lowercase, hyphen-separated filenames such as `technical-spec.md`

Prefer explicit security and architecture terminology over shorthand. When changing scope, update both the spec and threat model if the decision affects trust boundaries or data handling.

## Testing Guidelines
Run `make test` for code changes. For documentation changes, review for consistency, broken cross-references, and contradictions between `docs/technical-spec.md`, `docs/threat-model.md`, `docs/roadmap.md`, and the relevant `docs/plans/*.md`.

New security enforcement must be tested at both the unit level (service layer) and the HTTP level (`internal/httpapi/router_test.go`). Key test patterns in use:

- **TOCTOU / concurrent registration**: concurrent calls to `CompleteRegistration` must result in exactly one success and one `ErrUsedRegistrationChallenge`
- **Expired token rejection**: `AuthenticateAgent` must return an error for tokens issued with a negative TTL
- **Expired grant filtering**: `Evaluate` must return `ErrPermissionDenied` (not empty results) when all grants are expired, because filtering happens at the storage layer
- **ForbiddenError → 403**: attempting to correct an artifact you do not own must return HTTP 403
- **Cross-org isolation**: a user in org A cannot query, grant, or request against a user in org B; all such attempts must return HTTP 404
- **Email OTP**: when a sender is configured, `CompleteRegistration` must return `pending_email_verification` status; a `pending_email_verification` agent must receive HTTP 403 on all protected routes; a correct code must promote the agent to `active`; a wrong code must increment the attempt counter; use `email.NoopSender` (or pass `nil`) in tests that do not exercise the email path
- **MCP remote mode**: `TestToolFlowRemoteServer` exercises the full tool flow via `httptest.NewServer`; new MCP tool tests should cover both the embedded and remote paths when behaviour differs
- **Invite tokens**: first registration must generate and return a raw token (never stored); second registration without token must return `ErrInviteTokenRequired`; wrong token must return `ErrInvalidInviteToken`; token comparison must use `subtle.ConstantTimeCompare`
- **Admin approval**: first registrant must get `admin` role and `active` status; subsequent registrants in an `admin_approval` org must get `pending_admin_approval`; `pending_admin_approval` and `rejected` agents must receive HTTP 403 on all protected routes; only admins may call `ReviewAgentApproval`; rejection must revoke all bearer tokens

## Commit & Pull Request Guidelines
Existing history uses short, imperative commit subjects. Follow that pattern: one-line, imperative, capitalized, no trailing period.

PRs should include a brief summary, the reason for the change, and the affected documents. Link the relevant issue when one exists. Include screenshots only when a PR adds rendered diagrams or other visual assets.
