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
- edge runtime skeleton entrypoint in `cmd/edge-agent` for local runtime bootstrap
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
  - a normalized connector event pipeline shared by fixture and live ingestion
  - fixture-driven artifact publication
  - fixture-driven GitHub, Jira, and calendar ingestion with deterministic derived artifacts
  - live GitHub polling with env-backed token auth and repository-to-project mapping
  - watched query-result retrieval
  - incoming-request polling
- end-to-end MCP test coverage for registration, artifact publish, grant creation, peer listing, query submission/result retrieval, request send/respond, and approval resolution
- targeted edge runtime test coverage for local registration reuse, fixture publication, fixture-derived artifacts, live GitHub polling, query-result retrieval, and request polling against the current server
- targeted HTTP test coverage for the permissioned query flow and request/approval flow in memory and, when configured, against PostgreSQL
- Podman-based container workflow for local execution with both the server and PostgreSQL

Current implementation assumptions:

- agent registration is a signed Ed25519 challenge flow that returns a short-lived bearer token
- access control is explicit-grant-only
- queries are answered from centrally stored derived artifacts
- the MCP surface is currently a local stdio wrapper around the existing HTTP routes and auth flow
- the first Gatekeeper request and approval flow exists, but approval policy is still explicit/manual rather than risk-engine driven
- query time windows use source observation timestamps when artifacts carry source refs
- the edge runtime uses JSON config plus local fixture files, with the first live connector path now available for GitHub polling via env-backed token auth
- Jira and Google Calendar ingestion remain fixture-driven for now
- local container runs use PostgreSQL; tests and ad hoc runs can still fall back to in-memory storage when no database URL is set

The current implementation handoff plan lives in `docs/implementation-plan.md`.

## Local development

Run these commands from the repository root:

- prerequisites: `podman` and `podman-compose`
- `make local`: build and start the local stack with Podman Compose, including PostgreSQL
- `make down`: stop the local stack
- `make status`: show container status
- `make logs`: tail server logs
- `make test`: run the Go test suite
- `make test-postgres`: run the query-flow test path against the local PostgreSQL service

The server reads `ALICE_DATABASE_URL` to decide whether to use PostgreSQL or the in-memory fallback.

For local MCP use, run `go run ./cmd/mcp-server`. The server speaks MCP over stdio, can bootstrap registration through the `register_agent` tool, and also accepts `ALICE_MCP_ACCESS_TOKEN` to start with an existing authenticated session.

For local edge runtime use, run `go run ./cmd/edge-agent -config examples/edge-agent-config.json`. The current runtime reads JSON config, persists local auth state under `.alice/`, publishes configured artifact fixtures plus deterministic artifacts derived from GitHub/Jira/calendar fixture files, and polls watched query IDs plus incoming requests.

For the first live connector path, set `ALICE_GITHUB_TOKEN` and run `go run ./cmd/edge-agent -config examples/edge-agent-github-live-config.json`. The live GitHub path polls configured repositories, filters PR metadata to the configured actor login, normalizes those events locally, and publishes only derived artifacts to the coordination server.

The server is exposed on `http://127.0.0.1:8080`, and the local PostgreSQL instance is exposed on `127.0.0.1:5432`.

## Next steps

The next recommended implementation steps are:

1. extend live connector coverage beyond GitHub to Jira and Google Calendar, with a safer auth/bootstrap path than env-token-only setup
2. deepen local derivation so the edge runtime can emit richer summaries, blockers, commitments, and status deltas from normalized events
3. add connector cursor/state handling plus stronger local policy and redaction around retained raw data

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
