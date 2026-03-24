# Agent Coordination Platform — Technical Specification v0.1

## Document status

Draft  
Audience: engineering, security, product, platform  
Scope: Reporter + Gatekeeper phases, with a path to Operator

---

## 1. Purpose

This system provides a secure coordination layer for personal AI agents used by people inside an organization.

Each person has a personal agent that:
- observes approved work signals from connected systems
- derives private working context locally
- publishes only policy-approved shareable artifacts
- communicates with other agents through a central coordination server
- answers status questions and relays requests within permission boundaries

The system starts as:
- **Reporter**: answer questions about status, blockers, commitments, and recent work
- **Gatekeeper**: relay requests, defer interruptions, require approval when necessary

The system later expands into:
- **Operator**: safely execute approved low-risk actions on behalf of users

---

## 2. Goals

### 2.1 Product goals

- Replace manual status collection with queryable agent-mediated status
- Reduce interruptions by routing requests through agents
- Preserve privacy by sharing only derived artifacts, never raw logs
- Support peer, manager, and enterprise use cases with permission boundaries
- Work with CLI-native agents including Claude Code, Codex, Gemini CLI, and OpenCode
- Start with GitHub, Jira, and Google Calendar
- Use a central Go server that starts dumb and becomes smarter over time

### 2.2 Technical goals

- Modular connector system
- MCP-native interoperability surface
- Strong prompt injection containment architecture
- Deterministic policy enforcement outside the model
- Auditable source-to-sink flow
- Local/private raw context, centrally shared derived artifacts only
- Monolith-first architecture with clear future split boundaries

### 2.3 Non-goals for v0.1

- No broad enterprise UI
- No autonomous high-risk actions
- No central storage of raw GitHub/Jira/Calendar activity streams
- No unrestricted cross-agent querying
- No model-only trust decisions
- No “always-on surveillance” behavior

---

## 3. Core principles

1. **Raw source data remains local whenever possible**
2. **Only shareable artifacts move through the coordination server**
3. **Untrusted content is treated as data, not policy**
4. **The model may propose; deterministic code decides**
5. **All cross-agent sharing is permission-checked**
6. **Every meaningful action is auditable**
7. **Reporter and Gatekeeper ship before Operator**
8. **The server starts dumb: transport, policy, audit, routing**
9. **A missing permission means deny by default**
10. **No raw logs are ever shared through cross-agent queries**

---

## 4. High-level architecture

### 4.1 Components

#### A. Edge Agent Runtime

Per-user runtime that:
- connects to approved data sources
- normalizes source events
- tags provenance and trust level
- derives private state
- generates shareable artifacts
- answers incoming cross-agent queries
- applies local sharing policy
- requests approval from the human when needed

#### B. Coordination Server

Central Go service that:
- registers agents and identities
- stores org graph and permission grants
- routes inbox/outbox messages
- stores shared artifacts
- exposes a remote MCP interface
- records audit events
- manages approval workflows
- enforces org-level policy

#### C. Security / Execution Sandbox

Execution containment layer for agent runtimes, ideally using NVIDIA OpenShell where practical:
- process sandboxing
- egress restrictions
- policy-governed tool/network access
- privacy-aware routing and controlled execution

#### D. External Systems

Initial source systems:
- GitHub
- Jira
- Google Calendar

#### E. Human Approval Surface

Initially minimal:
- CLI prompts
- local approval commands
- optional webhook callback / browser auth landing pages
- future lightweight admin/debug UI

---

## 5. Trust zones

### Zone A — Trusted policy

- org policy
- tool schemas
- permission rules
- action risk rules
- approval thresholds
- connector allowlists

### Zone B — Semi-trusted structured system data

- normalized event metadata
- identity mappings
- connector account metadata
- project/role graph
- artifact metadata

### Zone C — Untrusted content

- PR bodies
- issue descriptions
- comments
- meeting notes
- calendar descriptions
- copied text
- external user-authored text

### Zone D — Sensitive sinks

- publishing cross-agent responses
- sending requests to other agents
- editing Jira/GitHub/Calendar
- sending external communications
- changing permissions/policy

**Invariant:** Zone C must never directly control Zone D.

---

## 6. Service boundaries

The initial implementation is a **modular monolith** with separate packages and deployment roles. The boundaries below are logical first and physical later.

### 6.1 Edge Agent Runtime boundary

#### Responsibilities

- pull or receive source events from connectors
- normalize external data into internal event types
- enrich events with provenance, trust tags, and sensitivity
- derive local private working state
- derive shareable artifacts
- answer permitted queries
- request human approval where required
- publish artifacts to the coordination server
- receive and process inbound requests from the coordination server

#### Does not own

- enterprise-wide org graph
- cross-user permission records beyond local cached policy
- durable central audit storage
- company-wide routing logic

#### Trust model

- can access raw source data for its owner
- must not publish raw source content centrally
- must apply local privacy filters before publication

### 6.2 Coordination Server boundary

#### Responsibilities

- authenticate agents and users
- register agent identities
- maintain org graph
- maintain permission grants
- maintain artifact registry
- route query and request messages
- store approval state
- store audit events
- expose remote MCP tools
- enforce org-level and recipient-level policy checks

#### Does not own

- raw source system logs
- rich personal private memory
- unconstrained inference over source exhaust

#### Trust model

- trusted to route, authenticate, authorize, and audit
- not trusted as a holder of full personal work history

### 6.3 Connector boundary

Connectors are isolated modules with a stable interface.

#### Responsibilities

- authenticate to source system
- collect events by webhook and/or polling
- authenticate inbound webhook deliveries with provider signatures where available or local shared-secret validation when a relay path is used, including Google Calendar channel-token verification for local watch callbacks
- map source records into normalized events
- declare source provenance and confidence
- classify event sensitivity
- emit only event payloads needed by downstream derivation

#### Does not own

- permission grants
- cross-agent logic
- action policy
- final artifact sharing rules

### 6.4 Policy Engine boundary

#### Responsibilities

- evaluate sharing permissions
- evaluate query authorization
- evaluate risk level for proposed actions
- evaluate whether approval is required
- apply redaction rules
- evaluate role/project/scope restrictions

#### Does not own

- model inference
- connector transport
- data collection

### 6.5 Audit boundary

#### Responsibilities

- immutable or append-only event recording
- source-to-sink traceability
- actor, target, policy basis, and result capture
- support security review and debugging

#### Does not own

- operational routing
- user-facing message generation

---

## 7. Deployment model

### 7.1 Initial deployment

#### Central

- 1 Go coordination server
- 1 PostgreSQL database
- optional Redis for ephemeral queue/cache
- optional object store only if needed later for encrypted blobs or snapshots

#### Per user / per workstation / per execution environment

- edge-agent runtime
- connector credentials
- local cache/state
- sandbox policy
- CLI integration via MCP-compatible tools or wrapper commands

### 7.2 Future deployment split

The monolith can later split into:
- identity service
- policy service
- message router
- artifact service
- audit service
- approval service
- connector workers

Do not split until scale or isolation demands it.

---

## 8. Protocols

### 8.1 Primary protocols

- **MCP** for agent-facing tool integration
- **HTTPS JSON API** between edge runtimes and coordination server
- **OAuth 2.0** for connector authorization where applicable
- **Webhook + polling hybrid** for connector event ingestion
- **PostgreSQL** for durable system state

### 8.2 Why MCP is the external contract

The platform should expose a remote MCP server so that multiple agent ecosystems can use the same tool surface:
- Claude Code
- Codex
- Gemini CLI
- OpenCode

This keeps the coordination layer tool-oriented and decoupled from any one model vendor.

---

## 9. Data flow

### 9.1 Reporter flow

1. Connector fetches source event
2. Edge runtime normalizes event
3. Event is tagged with provenance and trust metadata
4. Local derivation logic produces candidate artifact
5. Policy engine determines if artifact can be shared
6. Artifact is published to coordination server
7. Another agent queries for status
8. Coordination server checks org-level and grant-level permissions
9. Recipient edge runtime checks local recipient policy
10. Recipient edge runtime returns permitted artifacts only
11. Coordination server delivers response and writes audit record

### 9.2 Gatekeeper flow

1. Agent A submits request for Agent B
2. Coordination server validates route and permission
3. Agent B receives request
4. Agent B evaluates local policy and risk
5. Agent B either:
   - responds automatically
   - defers
   - asks human for approval
   - denies
6. Response is returned and audited

### 9.3 Future Operator flow

1. Untrusted content and structured context are analyzed
2. Model emits a typed proposed action
3. Deterministic policy engine checks the action
4. If required, user approval is requested
5. Approved executor performs the action
6. Result is published and audited

---

## 10. Prompt injection defense model

### 10.1 Security posture

Prompt injection is treated as an architectural class of risk, not a prompt-writing problem.

### 10.2 Rules

#### Rule 1: Content/policy separation

All external text must enter as explicitly marked untrusted content fields.

#### Rule 2: Provenance tagging

Every event and text chunk must include:
- source system
- source entity
- actor if known
- timestamp
- trust class
- sensitivity
- extraction method

#### Rule 3: Typed intent generation

The model may only produce typed outputs such as:
- `CandidateArtifact`
- `CandidateResponse`
- `CandidateAction`
- `CandidateRoutingDecision`

It may not directly call a sensitive sink.

#### Rule 4: Deterministic sink control

Only deterministic code may authorize:
- cross-agent publication
- connector writes
- permission changes
- external communication

#### Rule 5: Risk-classed sinks

Each sink is classified:

- **L0** local read-only reasoning
- **L1** publish internal artifact
- **L2** send peer request
- **L3** write to internal system
- **L4** write to external system or broad audience

#### Rule 6: Audit everything important

Every source-to-sink path records:
- source references
- model output type
- policy decision
- approval decision
- execution result

### 10.3 Required implementation constraints

- no raw concatenation of untrusted source text into system policy prompts
- no direct tool invocation based only on untrusted content
- no automatic escalation from untrusted text to L3/L4 sink
- redaction occurs before publication, not after

### 10.4 HTTP hardening requirements

These constraints apply to all HTTP endpoints on the coordination server:

- every endpoint that reads a request body must wrap `req.Body` with `http.MaxBytesReader` before decoding; suggested ceiling is 1 MiB (1 << 20 bytes); exceeding the limit must return HTTP 413
- the HTTP server must set `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, and `MaxHeaderBytes` in addition to `ReadHeaderTimeout`; suggested values: `ReadTimeout: 30s`, `WriteTimeout: 60s`, `IdleTimeout: 120s`, `MaxHeaderBytes: 1 << 20`
- every response must include security headers: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Cache-Control: no-store`
- unauthenticated endpoints (`/v1/agents/register/challenge`, `/v1/agents/register`) must be rate-limited; suggested default: 10 requests per minute per source IP
- URL path parameters must be validated against expected formats (e.g., ULID pattern) before use; manual `strings.TrimPrefix` extraction without validation is fragile and must be replaced or augmented with format checks
- authentication must use only the `Authorization: Bearer` header; undocumented alternate headers widen the attack surface and must be removed or explicitly documented and tested

---

## 11. Domain model

### 11.1 Main entities

- Organization
- User
- Agent
- ConnectorAccount
- NormalizedEvent
- Artifact
- Query
- QueryResponse
- Request
- Approval
- PolicyGrant
- AuditEvent
- ActionProposal
- DeliveryRecord

---

## 12. Schema definitions

The following are canonical wire/domain schemas. Storage schemas may differ but must preserve semantics.

### 12.1 Common types

```json
{
  "Id": "string, ULID preferred",
  "Timestamp": "RFC3339 UTC timestamp",
  "Confidence": "float 0.0..1.0"
}
```

#### Enums

```json
{
  "ArtifactType": ["summary", "commitment", "blocker", "status_delta", "request"],
  "QueryPurpose": ["status_check", "dependency_check", "handoff", "manager_update", "request_context"],
  "RiskLevel": ["L0", "L1", "L2", "L3", "L4"],
  "ApprovalState": ["not_required", "pending", "approved", "denied", "expired"],
  "Sensitivity": ["low", "medium", "high", "restricted"],
  "TrustClass": ["trusted_policy", "structured_system", "untrusted_content"],
  "DeliveryStatus": ["queued", "sent", "delivered", "failed", "expired"],
  "RequestState": ["pending", "accepted", "deferred", "denied", "completed", "expired"],
  "VisibilityMode": ["private", "explicit_grants_only", "team_scope", "manager_scope"]
}
```

### 12.2 Organization

```json
{
  "org_id": "01H...",
  "name": "Example Corp",
  "slug": "example-corp",
  "created_at": "2026-03-18T12:00:00Z",
  "status": "active"
}
```

### 12.3 User

```json
{
  "user_id": "01H...",
  "org_id": "01H...",
  "email": "user@example.com",
  "display_name": "Jane Doe",
  "role_titles": ["Engineer"],
  "manager_user_id": "01H...",
  "created_at": "2026-03-18T12:00:00Z",
  "status": "active"
}
```

### 12.4 Agent

```json
{
  "agent_id": "01H...",
  "org_id": "01H...",
  "owner_user_id": "01H...",
  "agent_name": "jane-agent",
  "runtime_kind": "edge",
  "client_type": "claude_code",
  "public_key": "base64-encoded-public-key",
  "capabilities": ["publish_artifact", "respond_query", "request_approval"],
  "status": "active",
  "last_seen_at": "2026-03-18T12:00:00Z"
}
```

### 12.5 ConnectorAccount

```json
{
  "connector_account_id": "01H...",
  "agent_id": "01H...",
  "connector_type": "github",
  "external_account_ref": "github:user:12345",
  "scopes": ["repo:read", "pull_request:read"],
  "status": "connected",
  "created_at": "2026-03-18T12:00:00Z",
  "updated_at": "2026-03-18T12:00:00Z"
}
```

### 12.6 SourceReference

```json
{
  "source_system": "github",
  "source_type": "pull_request",
  "source_id": "repo:org/name:pr:128",
  "source_url": "https://...",
  "observed_at": "2026-03-18T12:00:00Z",
  "trust_class": "untrusted_content",
  "sensitivity": "medium"
}
```

### 12.7 NormalizedEvent

```json
{
  "event_id": "01H...",
  "agent_id": "01H...",
  "connector_type": "github",
  "event_type": "github.pr.review_requested",
  "occurred_at": "2026-03-18T11:45:00Z",
  "ingested_at": "2026-03-18T11:46:00Z",
  "project_refs": ["payments-api"],
  "actor_ref": "github:user:janedoe",
  "subject_ref": "repo:org/payments:pr:128",
  "trust_class": "structured_system",
  "sensitivity": "medium",
  "metadata": {
    "repo": "org/payments",
    "pr_number": 128,
    "reviewers": ["sam", "pat"]
  },
  "content_fragments": [
    {
      "kind": "title",
      "text": "Add retry logic to webhook consumer",
      "trust_class": "untrusted_content"
    }
  ]
}
```

### 12.8 Artifact

```json
{
  "artifact_id": "01H...",
  "org_id": "01H...",
  "owner_agent_id": "01H...",
  "owner_user_id": "01H...",
  "type": "blocker",
  "title": "Waiting on PR review",
  "content": "Blocked on review for payments retry logic PR.",
  "structured_payload": {
    "project_refs": ["payments-api"],
    "related_users": ["01H..."],
    "due_at": null
  },
  "source_refs": [
    {
      "source_system": "github",
      "source_type": "pull_request",
      "source_id": "repo:org/payments:pr:128",
      "observed_at": "2026-03-18T11:46:00Z",
      "trust_class": "structured_system",
      "sensitivity": "medium"
    }
  ],
  "visibility_mode": "explicit_grants_only",
  "sensitivity": "medium",
  "confidence": 0.92,
  "approval_state": "not_required",
  "created_at": "2026-03-18T11:47:00Z",
  "expires_at": "2026-03-19T00:00:00Z",
  "supersedes_artifact_id": null
}
```

### 12.9 Query

```json
{
  "query_id": "01H...",
  "org_id": "01H...",
  "from_agent_id": "01H...",
  "from_user_id": "01H...",
  "to_agent_id": "01H...",
  "to_user_id": "01H...",
  "purpose": "status_check",
  "question": "What has Jane been working on today?",
  "requested_types": ["summary", "blocker", "commitment", "status_delta"],
  "project_scope": ["payments-api"],
  "time_window": {
    "start": "2026-03-18T00:00:00Z",
    "end": "2026-03-18T23:59:59Z"
  },
  "risk_level": "L1",
  "created_at": "2026-03-18T12:00:00Z",
  "expires_at": "2026-03-18T12:05:00Z"
}
```

### 12.10 QueryResponse

```json
{
  "response_id": "01H...",
  "query_id": "01H...",
  "from_agent_id": "01H...",
  "to_agent_id": "01H...",
  "artifacts": ["01H...", "01H..."],
  "redactions": ["removed PR body details", "removed meeting attendee list"],
  "policy_basis": ["grant:team-payments-status", "recipient-policy:allow-summary-only"],
  "approval_state": "not_required",
  "confidence": 0.89,
  "created_at": "2026-03-18T12:00:03Z"
}
```

### 12.11 Request

```json
{
  "request_id": "01H...",
  "org_id": "01H...",
  "from_agent_id": "01H...",
  "to_agent_id": "01H...",
  "request_type": "ask_for_review",
  "title": "Review needed on payments PR",
  "content": "Can you review the retry logic PR today?",
  "structured_payload": {
    "project_refs": ["payments-api"],
    "desired_by": "2026-03-18T18:00:00Z"
  },
  "risk_level": "L2",
  "state": "pending",
  "created_at": "2026-03-18T12:10:00Z",
  "expires_at": "2026-03-19T12:10:00Z"
}
```

### 12.12 Approval

```json
{
  "approval_id": "01H...",
  "org_id": "01H...",
  "agent_id": "01H...",
  "owner_user_id": "01H...",
  "subject_type": "request",
  "subject_id": "01H...",
  "reason": "Request would disclose high-sensitivity blocker details.",
  "state": "pending",
  "created_at": "2026-03-18T12:10:03Z",
  "expires_at": "2026-03-18T14:10:03Z",
  "resolved_at": null
}
```

### 12.13 PolicyGrant

```json
{
  "policy_grant_id": "01H...",
  "org_id": "01H...",
  "grantor_user_id": "01H...",
  "grantee_user_id": "01H...",
  "scope_type": "project",
  "scope_ref": "payments-api",
  "allowed_artifact_types": ["summary", "blocker", "commitment", "status_delta"],
  "max_sensitivity": "medium",
  "allowed_purposes": ["status_check", "dependency_check"],
  "visibility_mode": "explicit_grants_only",
  "requires_approval_above_risk": "L1",
  "created_at": "2026-03-18T12:00:00Z",
  "expires_at": null
}
```

### 12.14 ActionProposal

```json
{
  "action_proposal_id": "01H...",
  "agent_id": "01H...",
  "proposal_type": "jira.create_comment",
  "risk_level": "L3",
  "input_sources": ["01H...", "01H..."],
  "proposed_payload": {
    "issue_key": "PAY-123",
    "comment": "Need review from payments team."
  },
  "policy_decision": "pending",
  "approval_state": "pending",
  "created_at": "2026-03-18T12:30:00Z"
}
```

### 12.15 AuditEvent

```json
{
  "audit_event_id": "01H...",
  "org_id": "01H...",
  "event_kind": "query.response.sent",
  "actor_agent_id": "01H...",
  "target_agent_id": "01H...",
  "subject_type": "query",
  "subject_id": "01H...",
  "source_refs": ["01H...", "01H..."],
  "policy_basis": ["grant:team-payments-status"],
  "decision": "allow",
  "risk_level": "L1",
  "created_at": "2026-03-18T12:00:03Z",
  "metadata": {
    "redaction_count": 2
  }
}
```

### 12.16 DeliveryRecord

```json
{
  "delivery_id": "01H...",
  "subject_type": "request",
  "subject_id": "01H...",
  "destination_agent_id": "01H...",
  "status": "delivered",
  "attempt_count": 1,
  "last_attempt_at": "2026-03-18T12:10:01Z",
  "error": null
}
```

---

## 13. Storage model

Use PostgreSQL initially.

### 13.1 Required tables

- `organizations`
- `users`
- `agents`
- `connector_accounts`
- `normalized_events_local_index` (optional central metadata index only; no raw bodies)
- `artifacts`
- `artifact_source_refs`
- `queries`
- `query_responses`
- `requests`
- `approvals`
- `policy_grants`
- `audit_events`
- `delivery_records`
- `agent_sessions`
- `oauth_states`
- `idempotency_keys`

### 13.2 Storage rules

- central DB stores artifact content and metadata, not raw source logs
- raw connector payloads remain local or are discarded after derivation depending on local retention policy
- audit events are append-only
- permissions are versioned
- derived artifacts may supersede earlier artifacts
- expired artifacts should not be returned for new queries unless explicitly requested
- all storage methods must accept `context.Context` as the first parameter; callers must pass the request context from the HTTP handler through the service layer so that queries can be cancelled and timed out
- all list queries must support pagination (cursor-based or offset/limit); unbounded `SELECT` without `LIMIT` is not acceptable
- multi-step state changes (e.g., resolve approval + update linked request, mark challenge used + create token) must be wrapped in a database transaction
- migrations must be tracked in a `schema_migrations` table; `CREATE TABLE IF NOT EXISTS` is only acceptable for the initial bootstrap; all subsequent migrations must use explicit version tracking and must not be re-executed
- enum-like columns (`status`, `state`, `risk_level`, `sensitivity`, `visibility_mode`) should have `CHECK` constraints at the database level, not rely solely on application validation
- the memory store must maintain behavioral parity with the PostgreSQL store for all tested paths, including duplicate-ID rejection, email normalization, and unique-constraint enforcement

---

## 14. Connector architecture

### 14.1 Connector interface

```go
type Connector interface {
    Name() string
    AuthScheme() AuthScheme
    Start(ctx context.Context) error
    Poll(ctx context.Context) ([]NormalizedEvent, error)
    HandleWebhook(ctx context.Context, payload []byte, headers map[string]string) ([]NormalizedEvent, error)
    Normalize(ctx context.Context, raw any) ([]NormalizedEvent, error)
    ClassifySensitivity(ctx context.Context, event *NormalizedEvent) (Sensitivity, error)
}
```

### 14.2 Connector responsibilities

#### GitHub connector

Inputs:
- PR opened/updated
- review requested/submitted
- issue assigned/updated
- comments
- repo activity relevant to the user

Candidate artifacts:
- status_delta
- blocker
- commitment
- summary

#### Jira connector

Inputs:
- issue assigned
- issue state transitioned
- issue commented
- due date changed
- sprint placement changed

Candidate artifacts:
- commitment
- blocker
- summary
- status_delta

#### Google Calendar connector

Inputs:
- event created/updated
- event started/ended
- focus time / OOO blocks
- accepted/declined events

Candidate artifacts:
- status_delta
- summary
- availability context
- blocker if user is fully booked or unavailable

---

## 15. Artifact derivation rules

Derivation happens locally inside the edge runtime.

### 15.1 Candidate artifact generation

The derivation engine may create candidate artifacts from:
- one normalized event
- multiple correlated events
- temporal aggregation over a time window

### 15.2 Required constraints

- candidate artifacts must reference source_refs
- candidate artifacts must carry confidence
- candidate artifacts must carry sensitivity
- candidate artifacts must be typed
- candidate artifacts must be short and shareable
- no artifact may include raw log dumps

### 15.3 Example derivation

Input:
- Jira issue transitioned to “In Review”
- GitHub PR opened for linked issue
- calendar shows engineering review sync

Output:
- `status_delta`: “Moved PAY-123 into review and opened the linked PR.”
- `commitment`: “Expecting review feedback this afternoon.”
- `blocker`: only if another event indicates dependency or stalled state

---

## 16. Policy engine

### 16.1 Policy inputs

- requester identity
- recipient identity
- org relationship
- explicit policy grants
- artifact type
- artifact sensitivity
- project scope
- query purpose
- action risk level
- local recipient settings

### 16.2 Policy outputs

- allow
- deny
- allow with redaction
- require approval
- allow subset only

### 16.3 Policy evaluation order

1. authenticate actor
2. confirm org relationship
3. check explicit deny rules
4. resolve grants for requester → recipient
5. evaluate purpose
6. evaluate artifact type and sensitivity
7. evaluate project scope
8. evaluate risk
9. determine redactions
10. decide allow / deny / approval

### 16.4 Example rule set

- peer with explicit project grant may query `summary`, `blocker`, `commitment`, `status_delta` up to `medium` sensitivity
- manager may query the same plus broader time windows for direct reports
- no one may query `restricted` artifacts without explicit grant plus approval
- raw source content is never returnable regardless of grant

### 16.5 Required enforcement rules

The following policy behaviors are specified in the schema but must actually be enforced at runtime. Any field that is stored but unenforced creates a false sense of security and must either be enforced or removed.

#### Cross-org isolation

Every grant, query, and request path must validate that both the acting agent and the target user/agent belong to the same organization. `FindUserByEmail` must accept an org ID and scope its lookup accordingly. A global email lookup without org scoping is a cross-tenant data leak vector.

#### Sensitivity ceiling

When evaluating a query against a grant, the grant's `max_sensitivity` must be compared against each candidate artifact's `sensitivity`. Artifacts with sensitivity exceeding the grant's ceiling must be excluded from the response. The sensitivity ordering is: `low` < `medium` < `high` < `restricted`.

#### Purpose filtering

The grant's `allowed_purposes` must be compared against the query's `purpose`. A query whose purpose is not in the grant's allowed list must be denied.

#### Artifact type filtering

The grant's `allowed_artifact_types` must be compared against each candidate artifact's `type`. Artifacts with types not in the grant's allowed list must be excluded.

#### Risk-based approval

The grant's `requires_approval_above_risk` must be compared against the query's `risk_level`. When the query's risk level exceeds the threshold, an approval record must be created and the query must wait for approval before returning results. This is specified in the PolicyGrant schema (section 12.13) but is not currently enforced.

#### Visibility modes

All four visibility modes must be enforced:

- `private`: artifact is never returned in cross-agent queries (currently enforced)
- `explicit_grants_only`: artifact is returned only when a matching grant exists (currently enforced)
- `team_scope`: artifact is returned to any user on the same team without requiring an explicit per-user grant (requires team membership resolution from org graph — not yet implemented)
- `manager_scope`: artifact is returned to the user's direct manager without requiring an explicit grant (requires manager relationship resolution from org graph — not yet implemented)

#### Grant revocation

Grants must be revocable via `DELETE /v1/policy-grants/:id` and the `revoke_permission` MCP tool. Once revoked, a grant must not match any subsequent query. The revocation must be audited.

#### Grant expiry

When `PolicyGrant.ExpiresAt` is set and the current time exceeds it, the grant must not match any query. Expiry must be checked at evaluation time, not only at creation time.

#### Request/approval expiry

Expired requests must not appear in `list_incoming_requests`. Expired approvals must not be resolvable. The `ExpiresAt` fields on requests and approvals must be checked at list time and at resolve time.

#### Redaction

Before returning artifacts in a query response, the policy engine must apply redaction rules. The `Redactions` field on `QueryResponse` must list what was redacted and why. At minimum, redaction must:

- strip raw source text fragments that appear in artifact content
- strip fields that exceed the grant's sensitivity ceiling
- record each redaction action in the response metadata

#### Capabilities enforcement

If the `Agent.Capabilities` field is retained, it must be checked before allowing each operation. An agent registered with only `["publish_artifact"]` must not be able to create grants, send requests, or resolve approvals. If capability enforcement is not yet needed, the field should be removed from the Agent schema to avoid a false sense of restriction.

---

## 17. MCP tool definitions

The coordination server exposes a remote MCP server. Tools below are the public agent contract for Reporter + Gatekeeper.

### 17.1 `register_agent`

Registers an edge runtime with the coordination server.

#### Input

```json
{
  "org_slug": "example-corp",
  "owner_email": "jane@example.com",
  "agent_name": "jane-agent",
  "client_type": "claude_code",
  "public_key": "base64-encoded-public-key",
  "capabilities": ["publish_artifact", "respond_query", "request_approval"]
}
```

#### Output

```json
{
  "agent_id": "01H...",
  "org_id": "01H...",
  "status": "active"
}
```

#### Side effects

- creates or updates agent identity
- writes audit event

---

### 17.2 `publish_artifact`

Publishes a shareable artifact to the coordination server.

#### Input

```json
{
  "artifact": {
    "type": "summary",
    "title": "Working on payments retry logic",
    "content": "Focused on webhook retry improvements and review follow-up.",
    "structured_payload": {
      "project_refs": ["payments-api"]
    },
    "source_refs": [
      {
        "source_system": "github",
        "source_type": "pull_request",
        "source_id": "repo:org/payments:pr:128",
        "observed_at": "2026-03-18T11:46:00Z",
        "trust_class": "structured_system",
        "sensitivity": "medium"
      }
    ],
    "visibility_mode": "explicit_grants_only",
    "sensitivity": "medium",
    "confidence": 0.91,
    "expires_at": "2026-03-19T00:00:00Z"
  }
}
```

#### Output

```json
{
  "artifact_id": "01H...",
  "stored": true
}
```

#### Policy

- server validates schema and ownership
- server does not accept raw-source artifact types

---

### 17.3 `query_peer_status`

Requests permitted status artifacts from another agent.

#### Input

```json
{
  "to_user_email": "sam@example.com",
  "purpose": "status_check",
  "question": "What has Sam been working on today?",
  "requested_types": ["summary", "blocker", "commitment", "status_delta"],
  "project_scope": ["payments-api"],
  "time_window": {
    "start": "2026-03-18T00:00:00Z",
    "end": "2026-03-18T23:59:59Z"
  }
}
```

#### Output

```json
{
  "query_id": "01H...",
  "status": "queued"
}
```

#### Delivery semantics

- async
- result retrieved via `get_query_result` or pushed by callback in future versions

---

### 17.4 `get_query_result`

Retrieves the current result for a previously submitted query.

#### Input

```json
{
  "query_id": "01H..."
}
```

#### Output

```json
{
  "query_id": "01H...",
  "state": "completed",
  "response": {
    "artifacts": [
      {
        "artifact_id": "01H...",
        "type": "summary",
        "title": "Finishing test fixes",
        "content": "Working on post-review fixes for the payments retry PR.",
        "sensitivity": "medium",
        "confidence": 0.87
      }
    ],
    "redactions": [],
    "policy_basis": ["grant:team-payments-status"]
  }
}
```

---

### 17.5 `send_request_to_peer`

Sends a Gatekeeper request to another agent.

#### Input

```json
{
  "to_user_email": "sam@example.com",
  "request_type": "ask_for_review",
  "title": "Need review today",
  "content": "Can you review the payments retry PR today?",
  "structured_payload": {
    "project_refs": ["payments-api"],
    "desired_by": "2026-03-18T18:00:00Z"
  }
}
```

#### Output

```json
{
  "request_id": "01H...",
  "state": "pending"
}
```

---

### 17.6 `list_incoming_requests`

Lists pending requests for the current agent.

#### Input

```json
{}
```

#### Output

```json
{
  "requests": [
    {
      "request_id": "01H...",
      "from_user_email": "jane@example.com",
      "request_type": "ask_for_review",
      "title": "Need review today",
      "state": "pending",
      "created_at": "2026-03-18T12:10:00Z"
    }
  ]
}
```

---

### 17.7 `respond_to_request`

Responds to an incoming request.

#### Input

```json
{
  "request_id": "01H...",
  "response": "accepted",
  "message": "I can review this after the 3 PM sync."
}
```

#### Output

```json
{
  "request_id": "01H...",
  "state": "accepted"
}
```

#### Allowed response values

- `accepted`
- `deferred`
- `denied`
- `completed`
- `require_approval`

---

### 17.8 `list_pending_approvals`

Lists approvals requiring human action for the current agent.

#### Input

```json
{}
```

#### Output

```json
{
  "approvals": [
    {
      "approval_id": "01H...",
      "subject_type": "request",
      "subject_id": "01H...",
      "reason": "Would disclose high-sensitivity blocker details.",
      "created_at": "2026-03-18T12:10:03Z",
      "expires_at": "2026-03-18T14:10:03Z"
    }
  ]
}
```

---

### 17.9 `resolve_approval`

Resolves a pending approval.

#### Input

```json
{
  "approval_id": "01H...",
  "decision": "approved"
}
```

#### Output

```json
{
  "approval_id": "01H...",
  "state": "approved"
}
```

#### Allowed decision values

- `approved`
- `denied`

---

### 17.10 `grant_permission`

Creates or updates a permission grant.

#### Input

```json
{
  "grantee_user_email": "sam@example.com",
  "scope_type": "project",
  "scope_ref": "payments-api",
  "allowed_artifact_types": ["summary", "blocker", "commitment", "status_delta"],
  "max_sensitivity": "medium",
  "allowed_purposes": ["status_check", "dependency_check"]
}
```

#### Output

```json
{
  "policy_grant_id": "01H..."
}
```

---

### 17.11 `revoke_permission`

Revokes an existing permission grant.

#### Input

```json
{
  "policy_grant_id": "01H..."
}
```

#### Output

```json
{
  "revoked": true
}
```

---

### 17.12 `list_allowed_peers`

Lists peers the current agent may query or share with.

#### Input

```json
{}
```

#### Output

```json
{
  "peers": [
    {
      "user_email": "sam@example.com",
      "allowed_purposes": ["status_check", "dependency_check"],
      "allowed_artifact_types": ["summary", "blocker", "commitment", "status_delta"]
    }
  ]
}
```

---

### 17.13 `submit_correction`

Lets the user or agent correct a previously published artifact.

#### Input

```json
{
  "artifact_id": "01H...",
  "correction": "This should say blocked on review, not implementation."
}
```

#### Output

```json
{
  "accepted": true
}
```

---

### 17.14 `get_audit_summary`

Returns a constrained audit summary for the current agent.

#### Input

```json
{
  "since": "2026-03-18T00:00:00Z"
}
```

#### Output

```json
{
  "events": [
    {
      "event_kind": "query.response.sent",
      "created_at": "2026-03-18T12:00:03Z",
      "target_user_email": "alex@example.com",
      "subject_type": "query",
      "decision": "allow"
    }
  ]
}
```

---

## 18. Internal server APIs

MCP is the public tool surface. Internally, the coordination server should also expose standard service interfaces.

### 18.1 Suggested internal HTTP routes

```text
POST   /v1/agents/register/challenge   [implemented]
POST   /v1/agents/register             [implemented]
POST   /v1/artifacts                    [implemented]
GET    /v1/artifacts/:id                [not implemented]
POST   /v1/queries                      [implemented]
GET    /v1/queries/:id                  [implemented]
POST   /v1/requests                     [implemented]
GET    /v1/requests/incoming            [implemented]
POST   /v1/requests/:id/respond         [implemented]
GET    /v1/approvals                    [implemented]
POST   /v1/approvals/:id/resolve        [implemented]
POST   /v1/policy-grants                [implemented]
DELETE /v1/policy-grants/:id            [not implemented — required for grant revocation]
GET    /v1/peers                        [implemented]
GET    /v1/audit/summary                [implemented]
POST   /v1/connectors/:type/oauth/start [not implemented]
GET    /v1/connectors/:type/oauth/callback [not implemented]
POST   /v1/connectors/:type/webhook     [not implemented — server-side; edge runtime has local webhook endpoints]
```

---

## 19. Auth and identity

### 19.1 Identity model

- user identity belongs to an org
- each user may have one or more agent sessions but one primary personal agent
- agent auth uses signed credentials or short-lived tokens bound to registered public keys

### 19.2 Recommended auth for v0.1

- user bootstrap via email/OAuth-backed account creation
- agent registration via signed challenge
- server-issued short-lived access tokens for edge agents
- connector auth via OAuth where supported
- per-agent credential encryption at rest

---

## 20. OpenShell integration strategy

OpenShell is a security containment layer, not the sole prompt injection solution.

### 20.1 Initial role

Use OpenShell around edge agent runtimes where feasible to provide:
- sandboxed execution
- process policy
- controlled network egress
- restricted filesystem access
- model routing constraints

### 20.2 Initial policy goals

Allow outbound only to:
- coordination server
- GitHub API
- Jira API
- Google Calendar API
- model endpoints
- explicit auth callback endpoints as needed

Deny by default:
- arbitrary internet access
- local secret directory traversal
- shelling out to unapproved binaries
- high-risk file writes outside working directories

### 20.3 Separation of concerns

OpenShell handles:
- execution boundary
- egress containment
- local access restriction

The platform still must implement:
- content/policy separation
- typed intent generation
- deterministic sink gating
- audit logging
- approval rules

---

## 21. Initial repo layout

This is the recommended monorepo layout.

```text
agent-coordination/
├── README.md
├── LICENSE
├── Makefile
├── .gitignore
├── docs/
│   ├── technical-spec.md
│   ├── threat-model.md
│   ├── mcp-contract.md
│   ├── connector-guides/
│   │   ├── github.md
│   │   ├── jira.md
│   │   └── gcal.md
│   └── adr/
│       ├── 0001-modular-monolith.md
│       ├── 0002-mcp-public-contract.md
│       ├── 0003-local-raw-data-central-derived-artifacts.md
│       └── 0004-prompt-injection-source-sink-model.md
├── cmd/
│   ├── server/
│   │   └── main.go
│   ├── edge-agent/
│   │   └── main.go
│   └── cli/
│       └── main.go
├── internal/
│   ├── app/
│   │   ├── server.go
│   │   └── edge.go
│   ├── auth/
│   │   ├── service.go
│   │   ├── tokens.go
│   │   └── keys.go
│   ├── orggraph/
│   │   ├── service.go
│   │   └── model.go
│   ├── policy/
│   │   ├── engine.go
│   │   ├── grants.go
│   │   ├── redaction.go
│   │   └── risk.go
│   ├── artifacts/
│   │   ├── service.go
│   │   ├── derive.go
│   │   ├── publish.go
│   │   └── model.go
│   ├── queries/
│   │   ├── service.go
│   │   ├── dispatch.go
│   │   └── model.go
│   ├── requests/
│   │   ├── service.go
│   │   ├── inbox.go
│   │   └── model.go
│   ├── approvals/
│   │   ├── service.go
│   │   └── model.go
│   ├── audit/
│   │   ├── service.go
│   │   ├── writer.go
│   │   └── model.go
│   ├── delivery/
│   │   ├── service.go
│   │   └── retry.go
│   ├── mcp/
│   │   ├── server.go
│   │   ├── tools.go
│   │   ├── schemas.go
│   │   └── handlers/
│   │       ├── register_agent.go
│   │       ├── publish_artifact.go
│   │       ├── query_peer_status.go
│   │       ├── get_query_result.go
│   │       ├── send_request_to_peer.go
│   │       ├── list_incoming_requests.go
│   │       ├── respond_to_request.go
│   │       ├── list_pending_approvals.go
│   │       ├── resolve_approval.go
│   │       ├── grant_permission.go
│   │       ├── revoke_permission.go
│   │       ├── list_allowed_peers.go
│   │       ├── submit_correction.go
│   │       └── get_audit_summary.go
│   ├── connectors/
│   │   ├── connector.go
│   │   ├── github/
│   │   │   ├── client.go
│   │   │   ├── oauth.go
│   │   │   ├── webhooks.go
│   │   │   ├── poller.go
│   │   │   └── normalize.go
│   │   ├── jira/
│   │   │   ├── client.go
│   │   │   ├── oauth.go
│   │   │   ├── webhooks.go
│   │   │   ├── poller.go
│   │   │   └── normalize.go
│   │   └── gcal/
│   │       ├── client.go
│   │       ├── oauth.go
│   │       ├── poller.go
│   │       └── normalize.go
│   ├── normalize/
│   │   ├── events.go
│   │   ├── provenance.go
│   │   └── sensitivity.go
│   ├── derive/
│   │   ├── candidate_artifacts.go
│   │   ├── summarizer.go
│   │   ├── blockers.go
│   │   ├── commitments.go
│   │   └── status_deltas.go
│   ├── promptguard/
│   │   ├── input_partition.go
│   │   ├── source_tags.go
│   │   ├── intent_types.go
│   │   ├── sink_gate.go
│   │   └── validators.go
│   ├── models/
│   │   ├── client.go
│   │   ├── prompts.go
│   │   └── typed_outputs.go
│   ├── storage/
│   │   ├── postgres/
│   │   │   ├── db.go
│   │   │   ├── migrations/
│   │   │   └── repositories/
│   │   └── memory/
│   │       └── ...
│   ├── httpapi/
│   │   ├── router.go
│   │   ├── middleware.go
│   │   └── handlers/
│   ├── crypto/
│   │   ├── envelope.go
│   │   └── keys.go
│   ├── config/
│   │   ├── config.go
│   │   └── defaults.go
│   └── telemetry/
│       ├── logs.go
│       ├── metrics.go
│       └── tracing.go
├── api/
│   ├── openapi/
│   │   └── coordination.yaml
│   ├── jsonschema/
│   │   ├── artifact.json
│   │   ├── query.json
│   │   ├── request.json
│   │   ├── approval.json
│   │   └── policy_grant.json
│   └── mcp/
│       └── tools.json
├── scripts/
│   ├── dev-up.sh
│   ├── seed-dev.sh
│   └── gen-schemas.sh
├── deploy/
│   ├── docker/
│   │   ├── server.Dockerfile
│   │   └── edge-agent.Dockerfile
│   ├── k8s/
│   └── openshell/
│       ├── policies/
│       └── profiles/
├── test/
│   ├── integration/
│   ├── e2e/
│   ├── fixtures/
│   └── security/
│       ├── prompt_injection_cases/
│       └── policy_cases/
└── examples/
    ├── edge-agent-config.yaml
    ├── mcp-client-config-claude-code.json
    ├── mcp-client-config-codex.json
    ├── mcp-client-config-gemini-cli.json
    └── mcp-client-config-opencode.json
```

---

## 22. Initial package ownership

### 22.1 Server-side packages

- `auth`
- `orggraph`
- `policy`
- `artifacts`
- `queries`
- `requests`
- `approvals`
- `audit`
- `delivery`
- `mcp`
- `httpapi`
- `storage`

### 22.2 Edge-side packages

- `connectors`
- `normalize`
- `derive`
- `promptguard`
- `models`
- local portions of `policy`

---

## 23. Minimal config model

### 23.1 Server config

```yaml
server:
  listen_addr: ":8080"
  public_base_url: "https://coord.example.com"

database:
  dsn: "postgres://..."

auth:
  issuer: "coordination-server"
  token_ttl: "15m"

mcp:
  enabled: true

policy:
  default_visibility_mode: "explicit_grants_only"
  default_max_sensitivity: "medium"

audit:
  retain_days: 365
```

### 23.2 Edge agent config

```yaml
agent:
  org_slug: "example-corp"
  owner_email: "jane@example.com"
  agent_name: "jane-agent"
  client_type: "claude_code"

server:
  base_url: "https://coord.example.com"

connectors:
  github:
    enabled: true
  jira:
    enabled: true
  gcal:
    enabled: true

privacy:
  raw_retention_days: 7
  publish_default_visibility: "explicit_grants_only"

model:
  provider: "anthropic"
  model: "claude-sonnet"

security:
  sandbox_profile: "default"
  allow_network_to:
    - "coord.example.com"
    - "api.github.com"
    - "your-jira-instance.atlassian.net"
    - "www.googleapis.com"
```

---

## 24. Observability

### 24.1 Required metrics

- artifacts published per hour
- query allow/deny counts
- request accept/defer/deny counts
- approval required rate
- connector ingestion lag
- delivery failure rate
- policy evaluation latency
- redaction rate
- artifact supersession rate

### 24.2 Required logs

All logging must use structured key-value output (e.g., `log/slog` with JSON format). The standard `log` package must not be used in production code paths. Log entries must include contextual fields such as `agent_id`, `org_id`, `request_id`, and `error` where available. Sensitive fields (tokens, keys, credentials, raw source content) must never appear in log output.

Required log events:

- auth success/failure
- agent registration
- connector auth/callback result
- query dispatch/complete
- request dispatch/complete
- approval create/resolve
- policy allow/deny/redact
- delivery retry/failure

### 24.3 Tracing

Trace across:
- connector ingest
- normalization
- derivation
- publish
- query dispatch
- response
- audit write

---

## 25. Testing requirements

**Current state (2026-03-23):** All testing is integration-level in `internal/httpapi/`, `internal/mcp/`, and `internal/edge/`. No unit tests exist for any service or storage package. The edge runtime test suite is comprehensive (28 tests, ~3500 lines), but the server-side packages have zero `_test.go` files. Negative authorization tests, token/challenge expiration tests, and malformed-input tests do not exist.

### 25.1 Unit tests

Required unit test coverage (must exist before MVP):

- policy engine: grant matching, sensitivity ceiling, purpose filtering, artifact type filtering, project scope matching, risk-based approval threshold, grant expiry, cross-org denial
- redaction rules: raw source text stripping, sensitivity-based field removal, redaction metadata recording
- schema validation: all `core.Validate*` functions with both valid and invalid inputs, including missing required fields, invalid enum values, oversized content, and empty strings
- connector normalization: event type mapping, provenance tagging, sensitivity classification
- sink gating: typed output validation, risk level checking
- MCP handlers: each tool with valid and invalid inputs
- storage: behavioral parity between memory and PostgreSQL stores for duplicate IDs, email normalization, unique constraints, and FK-like relationships
- ID generation: uniqueness, sortability, format conformance

### 25.2 Integration tests

- agent registration
- artifact publish
- query routing
- request workflow
- approval workflow
- connector auth flow

### 25.3 Security tests

Required negative/adversarial tests (none currently exist):

- prompt injection fixtures: PR body, Jira comment, and calendar description containing instruction-injection strings must not influence model outputs or policy decisions
- malformed content payloads: oversized JSON, missing required fields, unexpected content types, and binary payloads must be rejected with appropriate HTTP error codes
- privilege escalation attempts: Agent A querying Agent B's data without a grant; querying with a purpose not in the grant; querying for artifact types not in the grant; querying across org boundaries
- permission bypass attempts: using an expired grant, using a revoked grant, querying with sensitivity above the grant ceiling, resolving an already-resolved approval
- raw log leakage tests: artifacts must not contain raw source text; responses must not include fields above the sensitivity ceiling
- sink gate bypass attempts: untrusted content must not produce direct tool invocations or sink writes
- token/challenge expiration: expired bearer tokens must be rejected; expired registration challenges must not complete; expired requests and approvals must not be resolvable
- cross-org isolation: agents in different orgs must not be able to grant, query, or send requests to each other
- rate limiting: rapid unauthenticated requests must be throttled

### 25.4 End-to-end tests

- two-agent reporter flow
- three-agent dependency flow
- gatekeeper request + approval flow
- redacted response flow

---

## 26. MVP acceptance criteria

The MVP is complete when all of the following are true:

1. A user can register a personal agent
2. A user can connect GitHub, Jira, and Google Calendar
3. The edge runtime can derive summaries, blockers, commitments, and status deltas
4. The coordination server stores only derived artifacts centrally
5. A user can grant another user permission to query approved artifact types
6. A query returns only permitted artifacts
7. Every query and response is auditable
8. A user can send a request to another user’s agent
9. The recipient agent can accept, defer, deny, or require approval
10. Prompt injection protections enforce content/policy separation and deterministic sink control

---

## 27. Recommended implementation order

### Step 1

Define domain schemas and JSON schemas.

### Step 2

Build the Go coordination server with:
- auth
- agents
- policy grants
- artifacts
- queries
- audit

### Step 3

Expose the MCP server with:
- register_agent
- publish_artifact
- query_peer_status
- get_query_result
- grant_permission
- list_allowed_peers

### Step 4

Build edge runtime with:
- local config
- registration
- artifact publication
- query handling

### Step 5

Add connectors:
- GitHub
- Jira
- Google Calendar

### Step 6

Add Gatekeeper flows:
- send_request_to_peer
- list_incoming_requests
- respond_to_request
- approvals

### Step 7

Add sandbox policy and OpenShell integration

### Step 8

Add security fixtures and policy test suite

---

## 28. Open questions for v0.2

- should agents support multiple concurrent clients per user
- how should manager visibility differ from peer visibility by default
- should there be project-level org admins
- should the server support push callbacks or remain poll-based first
- should artifact contents be encrypted with recipient-specific keys
- how much local history should edge agents retain
- should there be a lightweight web audit UI before Operator phase
- what is the exact approval UX for CLI-native agents

---

## 29. Summary

This specification defines a privacy-aware, MCP-native coordination platform with:
- local/private per-user raw context
- central Go coordination server
- modular connectors for GitHub, Jira, and Google Calendar
- Reporter and Gatekeeper functionality first
- strong prompt injection containment through content/policy separation, deterministic sink gating, and auditability
- a monolith-first repo structure designed to evolve without a rewrite

The design is intentionally conservative:
- dumb server first
- read-only source collection first
- publish derived artifacts only
- no raw logs
- permissioned cross-agent interaction only
- operator actions later and only with typed proposals plus policy enforcement
