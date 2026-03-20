# alice

Privacy-first coordination platform for personal AI agents.

`alice` is a coordination layer for teams where each person has a personal AI agent that can:
- observe approved work signals from connected systems
- derive private working context locally
- publish only policy-approved artifacts
- communicate with other agents through a central server
- answer status questions and relay requests within permission boundaries

The system starts as **Reporter + Gatekeeper** and later expands into **Operator**.

## Why this exists

Teams spend too much time:
- writing manual status updates
- asking each other for context
- interrupting people for progress checks
- digging across GitHub, Jira, calendars, docs, and chat

`alice` is designed to reduce that coordination overhead.

Instead of asking a person directly, you ask their agent.

Examples:
- “What has Sam been working on today?”
- “Who is blocked on the payments project?”
- “Ask Priya for a review on the retry PR.”
- “What changed since yesterday?”

The key design constraint is that agents should share **summaries, commitments, blockers, requests, and status deltas** — **never raw logs**.

## Core principles

1. **Raw source data stays local whenever possible**
2. **Only derived, shareable artifacts move through the server**
3. **Untrusted content is treated as data, not policy**
4. **The model may propose; deterministic code decides**
5. **All cross-agent communication is permission-checked**
6. **The system is auditable end to end**
7. **Reporter and Gatekeeper ship before Operator**
8. **The server starts dumb: routing, policy, audit, transport**
9. **A missing permission means deny by default**
10. **No raw logs are shared across agents**

## Product shape

Each user has a personal agent runtime connected to approved sources such as:
- GitHub
- Jira
- Google Calendar

That agent derives local private context, then publishes only approved artifacts such as:
- summaries
- commitments
- blockers
- status deltas
- requests

Agents communicate through a central coordination server written in Go.

## Phases

### Phase 1: Reporter
Agents can:
- observe source events
- derive shareable artifacts
- answer questions about what a user has been doing
- expose only permitted information to allowed peers and managers

### Phase 2: Gatekeeper
Agents can:
- receive requests from other agents
- triage interruptions
- accept, defer, deny, or escalate requests
- require user approval when needed

### Phase 3: Operator
Agents can:
- safely perform low-risk approved actions
- draft updates
- create tickets or comments
- propose calendar actions

High-risk actions should remain gated by deterministic policy and, where required, user approval.

## Architecture overview

`alice` has two primary runtime surfaces:

### 1. Edge Agent Runtime
A per-user runtime that:
- connects to source systems
- normalizes source events
- tags provenance and sensitivity
- derives local private state
- generates shareable artifacts
- answers incoming queries
- enforces local policy
- requests human approval where required

### 2. Coordination Server
A central Go service that:
- registers agents and identities
- stores org graph and permission grants
- routes queries and requests
- stores shared artifacts
- records audit events
- exposes MCP tools for agent clients
- enforces org-level and recipient-level policy

## Security posture

`alice` treats prompt injection as an architectural problem, not a prompt-writing problem.

Key security rules:
- external text is always treated as untrusted content
- trusted policy is kept separate from source content
- the model may generate typed proposals, not final authority
- deterministic code controls sensitive sinks
- all cross-agent publication is permission-checked
- all meaningful actions are auditable
- raw logs are not shared

Where practical, edge runtimes can be sandboxed with technologies such as OpenShell to reduce blast radius through:
- restricted network egress
- limited filesystem access
- controlled process execution
- policy-governed runtime boundaries

## Interoperability

`alice` is designed to expose an MCP-native tool surface so that different agent clients can interact with the same coordination layer.

Target clients include:
- Claude Code
- Codex
- Gemini CLI
- OpenCode

## Initial connectors

The first supported sources are:
- GitHub
- Jira
- Google Calendar

The connector model is intentionally modular so additional sources can be added over time.

Future candidates may include:
- Slack
- Linear
- Google Docs
- Notion
- email metadata
- internal task systems

## What the server stores

The coordination server is designed to store:
- agent identities
- org relationships
- permission grants
- shared artifacts
- requests and responses
- approvals
- audit events

The server is **not** intended to be the long-term home of raw GitHub, Jira, or calendar exhaust.

## What gets shared

Allowed shared units:
- summary
- commitment
- blocker
- status delta
- request

Not allowed:
- raw logs
- raw PR comment dumps
- raw Jira issue histories
- unrestricted calendar details
- unrestricted source text copied directly across agents

## Repository status

This repository is in early implementation. The current codebase includes an initial Go coordination server scaffold alongside the design documents and a containerized local development workflow.

The initial implementation target is:
- a modular monolith
- Go coordination server
- edge agent runtime
- MCP tool surface
- PostgreSQL storage
- GitHub/Jira/Google Calendar connectors
- Reporter and Gatekeeper flows

## Current implementation

Implemented now:

- Go coordination server entrypoint and HTTP health endpoint
- MCP stdio server entrypoint in `cmd/mcp-server` for local CLI-native tool access
- edge runtime skeleton entrypoint in `cmd/edge-agent` for local runtime bootstrap, polling, and webhook intake
- domain models for agents, artifacts, grants, queries, requests, approvals, and audit events
- registration challenge and short-lived bearer-token auth for agent registration and authenticated requests
- JSON schemas for artifact, query, and policy-grant payloads
- repository interfaces plus PostgreSQL-backed storage with embedded startup migrations, including auth, request, and approval tables
- in-memory storage fallback when `ALICE_DATABASE_URL` is not set
- HTTP routes for registration challenge, registration completion, artifact publish, permission grants, peer listing, query submit/result, request submit/inbox/respond, approval list/resolve, and audit summary
- MCP tools for Reporter and the first Gatekeeper slice, including request send/respond and approval list/resolve
- local edge runtime support for:
  - JSON config loading
  - persisted Ed25519 keypair and bearer-token state
  - persisted connector cursor state for live pollers
  - a dedicated local connector credential store with 0600 permission enforcement
  - optional AES-GCM encryption for the connector credential store from an env var or local key file
  - local OAuth-style connector bootstrap with PKCE, loopback callbacks, and persisted connector credentials
  - automatic refresh-token exchange for expired stored OAuth credentials when refresh tokens are available
  - connector secret loading from env vars, local token files, or the dedicated credential store
  - a normalized connector event pipeline shared by fixture and live ingestion
  - fixture-driven artifact publication
  - fixture-driven GitHub, Jira, and calendar ingestion with deterministic derived artifacts
  - live GitHub polling with env-backed token auth, token-file auth, or bootstrapped state tokens plus repository-to-project mapping
  - signed local GitHub webhook intake for `pull_request` events through `cmd/edge-agent -serve-webhooks`
  - live Jira polling with env-backed token auth, token-file auth, or bootstrapped state tokens plus project scoping and assignee filtering
  - shared-secret local Jira webhook intake for issue events through `cmd/edge-agent -serve-webhooks`
  - live Google Calendar polling with env-backed token auth, token-file auth, or bootstrapped state tokens plus calendar-scoped event ingestion
  - shared-secret local Google Calendar webhook intake that verifies `X-Goog-Channel-Token`, parses the calendar resource URI, and fetches incremental event changes through the saved cursor
  - live connector pagination across GitHub, Jira, and Google Calendar API pages
  - transient connector retry/backoff for 429, 502, 503, and 504 responses
  - project-level aggregate status deltas, blockers, and commitments derived from cross-source signals
  - stable derivation keys plus persisted latest-artifact tracking for replacement-aware edge publication of connector-derived artifacts
  - watched query-result retrieval
  - incoming-request polling
- end-to-end MCP test coverage for registration, artifact publish, grant creation, peer listing, query submission/result retrieval, request send/respond, and approval resolution
- targeted edge runtime test coverage for local registration reuse, fixture publication, fixture-derived artifacts, replacement-aware edge publication, live GitHub/Jira/Calendar polling, signed GitHub webhook intake, shared-secret Jira webhook intake, shared-secret Google Calendar webhook intake, connector pagination, transient connector retry behavior, connector cursor persistence, connector OAuth bootstrap, encrypted credential-store round trips, credential-store permission checks, refresh-token renewal, actionable re-auth errors, query-result retrieval, and request polling against the current server
- targeted HTTP test coverage for the permissioned query flow and request/approval flow in memory and, when configured, against PostgreSQL
- Podman-based container workflow for local execution with both the server and PostgreSQL

Current implementation assumptions:

- agent registration is a signed Ed25519 challenge flow that returns a short-lived bearer token
- access control is explicit-grant-only
- queries are answered from centrally stored derived artifacts
- the MCP surface is currently a local stdio wrapper around the existing HTTP routes and auth flow
- the first Gatekeeper request and approval flow exists, but approval policy is still explicit/manual rather than risk-engine driven
- query time windows use source observation timestamps when artifacts carry source refs
- the edge runtime uses JSON config plus local fixture files, with live polling now available for GitHub, Jira, and Google Calendar via env vars, token files, or locally bootstrapped OAuth credentials
- the edge runtime now supports both polling and initial push paths through signed local GitHub webhooks, shared-secret Jira webhooks, and shared-secret Google Calendar change notifications, live connector pollers persist local cursor state, page through multi-response APIs, retry transient 429/5xx failures with short backoff, and the edge runtime stores bootstrapped connector credentials in a dedicated local credential file with strict permission checks, optional AES-GCM encryption, and automatic refresh-token renewal when refresh tokens are available
- edge-derived artifacts now carry stable derivation keys, the edge runtime persists the latest published artifact ID per derivation slot, updated summaries, blockers, commitments, and status deltas supersede older logical artifacts, and superseded artifacts are hidden from query results
- richer project-level derivation now exists, but it is still heuristic and rule-based rather than connector-native or model-assisted
- local container runs use PostgreSQL; tests and ad hoc runs can still fall back to in-memory storage when no database URL is set

The current implementation handoff plan lives in `docs/implementation-plan.md`.

## Local development

Run these commands from the repository root:

- prerequisites: `podman` and `podman-compose`
- `make local`: build and start the local stack with Podman Compose, including PostgreSQL
- `make down`: stop the local stack
- `make postgres-up`: start only the PostgreSQL container and wait for it to become ready
- `make postgres-down`: stop only the PostgreSQL container
- `make status`: show container status
- `make logs`: tail server logs
- `make test`: run the Go test suite
- `make test-postgres`: start the PostgreSQL container if needed, wait for health, and run the Go test suite with `ALICE_TEST_DATABASE_URL` set

The server reads `ALICE_DATABASE_URL` to decide whether to use PostgreSQL or the in-memory fallback.

For local MCP use, run `go run ./cmd/mcp-server`. The server speaks MCP over stdio, can bootstrap registration through the `register_agent` tool, and also accepts `ALICE_MCP_ACCESS_TOKEN` to start with an existing authenticated session.

For local edge runtime use, run `go run ./cmd/edge-agent -config examples/edge-agent-config.json`. The current runtime reads JSON config, persists local auth state under `.alice/`, publishes configured artifact fixtures plus deterministic artifacts derived from GitHub/Jira/calendar fixture files, and polls watched query IDs plus incoming requests.

For local connector bootstrap use, run `go run ./cmd/edge-agent -config examples/edge-agent-github-oauth-config.json -bootstrap-connector github` or the equivalent Jira/Google Calendar OAuth example. The bootstrap mode prints a provider authorization URL, waits on a localhost callback, exchanges the code with PKCE, and persists the resulting connector credential into a dedicated local credentials file derived from the state path or set explicitly through `runtime.credentials_file`.

If you also set `ALICE_EDGE_CREDENTIAL_KEY` or configure `runtime.credentials_key_file`, that dedicated credentials file is AES-GCM encrypted at rest. If the runtime later encounters an expired stored connector credential that cannot be refreshed, the CLI now prints the exact `-bootstrap-connector` command to rerun for that connector.

For live connector use:

- set `ALICE_GITHUB_TOKEN` or bootstrap GitHub OAuth first, then run `go run ./cmd/edge-agent -config examples/edge-agent-github-live-config.json` for GitHub repository polling
- set `ALICE_JIRA_TOKEN` or bootstrap Jira OAuth first, then run `go run ./cmd/edge-agent -config examples/edge-agent-jira-live-config.json` for Jira project polling
- set `ALICE_GCAL_TOKEN` or bootstrap Google Calendar OAuth first, then run `go run ./cmd/edge-agent -config examples/edge-agent-gcal-live-config.json` for Google Calendar polling

Each live connector path persists a local last-seen cursor in the edge state file so subsequent runs can narrow polling and avoid republishing stale events. The live connector configs also accept `token_file` as a local-file alternative to env vars, and the OAuth bootstrap path stores connector credentials in a separate `0600` credentials file so later polls can reuse them without process env injection. When a stored OAuth credential expires and includes a refresh token, the runtime now refreshes it automatically before polling. If the credential store is encrypted, the same key must be present on later runs through `ALICE_EDGE_CREDENTIAL_KEY` or `runtime.credentials_key_file`.

For local GitHub webhook intake, set `ALICE_GITHUB_WEBHOOK_SECRET`, run `go run ./cmd/edge-agent -config examples/edge-agent-github-webhook-config.json -serve-webhooks`, and configure GitHub to deliver `pull_request` events to `http://127.0.0.1:8788/webhooks/github`. The current webhook path verifies `X-Hub-Signature-256`, maps repositories to project refs, ignores unrelated pull requests, and publishes derived artifacts directly without polling the GitHub API.

For local Jira webhook intake, set `ALICE_JIRA_WEBHOOK_SECRET`, run `go run ./cmd/edge-agent -config examples/edge-agent-jira-webhook-config.json -serve-webhooks`, and deliver issue webhooks to `http://127.0.0.1:8789/webhooks/jira` with `Authorization: Bearer $ALICE_JIRA_WEBHOOK_SECRET`. The current Jira path accepts `jira:issue_created` and `jira:issue_updated`, maps issue keys to configured projects, ignores unrelated assignees, and publishes derived artifacts directly without polling the Jira API.

For local Google Calendar webhook intake, set both `ALICE_GCAL_TOKEN` and `ALICE_GCAL_WEBHOOK_SECRET`, run `go run ./cmd/edge-agent -config examples/edge-agent-gcal-webhook-config.json -serve-webhooks`, and point the Calendar watch callback at `http://127.0.0.1:8790/webhooks/gcal`. The current Calendar path verifies `X-Goog-Channel-Token`, parses `X-Goog-Resource-URI` to identify the configured calendar, ignores unconfigured calendars and initial `sync` notifications, and performs an incremental events fetch through the saved calendar cursor before publishing derived artifacts.

The server is exposed on `http://127.0.0.1:8080`, and the local PostgreSQL instance is exposed on `127.0.0.1:5432`.

## Next steps

The next recommended implementation steps are:

1. harden webhook replay and duplicate-delivery handling across GitHub, Jira, and Google Calendar push paths
2. deepen derivation beyond the current project-level heuristics with richer blocker-resolution and commitment-completion signals
3. harden local operator workflows further with rotation tooling and safer credential-key management

Use `docs/implementation-plan.md` as the source of truth for the current step-by-step handoff.

## Planned repository layout

```text
alice/
├── cmd/
│   ├── server/
│   ├── edge-agent/
│   └── cli/
├── docs/
│   ├── technical-spec.md
│   ├── threat-model.md
│   └── adr/
├── internal/
│   ├── auth/
│   ├── orggraph/
│   ├── policy/
│   ├── artifacts/
│   ├── queries/
│   ├── requests/
│   ├── approvals/
│   ├── audit/
│   ├── delivery/
│   ├── mcp/
│   ├── connectors/
│   ├── normalize/
│   ├── derive/
│   ├── promptguard/
│   ├── models/
│   ├── storage/
│   ├── httpapi/
│   ├── crypto/
│   ├── config/
│   └── telemetry/
├── api/
│   ├── openapi/
│   ├── jsonschema/
│   └── mcp/
├── deploy/
├── scripts/
└── test/
```

## MVP goals

The MVP is successful when:
1. a user can register a personal agent
2. a user can connect GitHub, Jira, and Google Calendar
3. the edge runtime can derive summaries, blockers, commitments, and status deltas
4. the coordination server stores only derived artifacts centrally
5. a user can grant another user permission to query approved artifact types
6. a query returns only permitted artifacts
7. every query and response is auditable
8. a user can send a request to another user’s agent
9. the recipient agent can accept, defer, deny, or require approval
10. prompt injection protections enforce content/policy separation and deterministic sink control

## Non-goals for the first version

- no broad enterprise UI
- no high-risk autonomous actions
- no raw source log sharing
- no unrestricted cross-agent querying
- no policy decisions delegated entirely to the model
- no “manager spyware” posture

## Development priorities

1. domain schemas
2. Go coordination server
3. MCP tool surface
4. edge runtime
5. GitHub/Jira/Google Calendar connectors
6. Gatekeeper request flows
7. sandboxing and security policy
8. prompt injection and policy test fixtures

## Project vision

`alice` aims to become a trusted coordination layer where every person has a personal agent that:
- understands their work through approved sources
- shares only what they permit
- helps teammates coordinate without constant interruption
- maintains security, privacy, and auditability by default
