# Repository Guidelines

## Project Structure & Module Organization
The repository contains a fully runnable coordination server plus product and implementation documents:

- `docs/technical-spec.md`: system architecture, scope, and MVP boundaries
- `docs/threat-model.md`: security goals, trust boundaries, and threat analysis
- `docs/implementation-plan.md`: current implementation status, encoded assumptions, and the next recommended steps
- `cmd/server/`: coordination server HTTP entrypoint
- `cmd/mcp-server/`: stdio MCP entrypoint for Claude Code and OpenCode
- `cmd/edge-agent/`: per-user edge runtime with four operating modes: default poll-and-publish, `-bootstrap-connector` (OAuth PKCE), `-register-watches` (provider-side push channel setup), and `-serve-webhooks` (webhook intake server)
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
- `examples/`: runnable local example configs plus artifact fixtures, connector fixtures, live polling examples, webhook intake examples, and OAuth bootstrap examples for GitHub, Jira, and Google Calendar
- `api/jsonschema/`: machine-readable schema files

Keep the implementation plan, `README.md`, and this file aligned whenever the codebase meaningfully changes.

## Build, Test, and Development Commands
Run these commands from the repository root:

- `make local`: build and start the current server stack with Podman Compose
- `make down`: stop the Podman Compose stack
- `make postgres-up`: start only the PostgreSQL container, reusing an existing `alice-db` container when present, and wait for it to become ready
- `make postgres-down`: stop only the PostgreSQL container
- `make status`: show container status
- `make logs`: tail server container logs
- `make test`: run the Go test suite
- `make test-postgres`: start or reuse the PostgreSQL container, wait for health, and run the Go test suite with `ALICE_TEST_DATABASE_URL` set
- `git diff -- README.md AGENTS.md docs/ examples/`: inspect documentation and example-config changes before committing

Podman is the expected local container runtime for this repository, and the default local stack includes PostgreSQL.

## Coding Style & Naming Conventions
Use Markdown with clear ATX headings (`#`, `##`, `###`) and short, direct paragraphs. Match the existing style:

- sentence-case section headings
- concise bullet lists for requirements and scope
- lowercase, hyphen-separated filenames such as `technical-spec.md`

Prefer explicit security and architecture terminology over shorthand. When changing scope, update both the spec and threat model if the decision affects trust boundaries or data handling.

## Testing Guidelines
Run `make test` for code changes. For documentation changes, review for consistency, broken cross-references, and contradictions between `docs/technical-spec.md`, `docs/threat-model.md`, and `docs/implementation-plan.md`.

New security enforcement must be tested at both the unit level (service layer) and the HTTP level (`internal/httpapi/router_test.go`). Key test patterns in use:

- **TOCTOU / concurrent registration**: concurrent calls to `CompleteRegistration` must result in exactly one success and one `ErrUsedRegistrationChallenge`
- **Expired token rejection**: `AuthenticateAgent` must return an error for tokens issued with a negative TTL
- **Expired grant filtering**: `Evaluate` must return `ErrPermissionDenied` (not empty results) when all grants are expired, because filtering happens at the storage layer
- **ForbiddenError → 403**: attempting to correct an artifact you do not own must return HTTP 403
- **Cross-org isolation**: a user in org A cannot query, grant, or request against a user in org B; all such attempts must return HTTP 404

## Commit & Pull Request Guidelines
Existing history uses short, imperative commit subjects. Follow that pattern: one-line, imperative, capitalized, no trailing period.

PRs should include a brief summary, the reason for the change, and the affected documents. Link the relevant issue when one exists. Include screenshots only when a PR adds rendered diagrams or other visual assets.
