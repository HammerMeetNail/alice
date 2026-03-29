# alice

Privacy-first coordination platform for personal AI agents.

`alice` lets each person on a team run a personal agent that observes their approved work signals, derives a private working context, and shares only what they permit with other agents. Agents communicate through a central coordination server. All cross-agent access is permission-checked, auditable, and deny-by-default.

## Contents

- [How it works](#how-it-works)
- [Quick start: single user with Claude Code](#quick-start-single-user-with-claude-code)
- [Multi-user setup](#multi-user-setup)
- [Connecting with OpenCode](#connecting-with-opencode)
- [Edge agent: connecting real data sources](#edge-agent-connecting-real-data-sources)
- [Complete two-person workflow](#complete-two-person-workflow)
- [MCP tool reference](#mcp-tool-reference)
- [HTTP API reference](#http-api-reference)
- [Coordination server environment variables](#coordination-server-environment-variables)
- [MCP server environment variables](#mcp-server-environment-variables)
- [Edge agent environment variables](#edge-agent-environment-variables)
- [Edge agent config reference](#edge-agent-config-reference)
- [Local development commands](#local-development-commands)

---

## How it works

There are three binaries:

| Binary | Purpose |
|---|---|
| `cmd/server` | Shared HTTP coordination server. Stores agents, artifacts, grants, queries, requests, and audit events. Backed by PostgreSQL or in-memory. |
| `cmd/mcp-server` | Stdio MCP server for Claude Code / OpenCode. Wraps the full HTTP API as MCP tools. Can run standalone (in-memory) or share a PostgreSQL database with the coordination server so multiple users see the same state. |
| `cmd/edge-agent` | Per-user runtime. Connects to GitHub, Jira, and Google Calendar, derives artifacts locally, and publishes them to the coordination server via HTTP. |

For single-user testing, `cmd/mcp-server` alone is enough — it runs entirely in memory. For two or more users communicating, both MCP server instances point at the same PostgreSQL database, or each user runs an edge agent against the shared coordination server.

---

## Quick start: single user with Claude Code

This requires only Go 1.23.x and no database.

### 1. Clone and build

```sh
git clone https://github.com/HammerMeetNail/alice
cd alice
go build ./...
```

### 2. Build the MCP server binary

```sh
go build -o alice-mcp-server ./cmd/mcp-server
```

### 3. Add alice to Claude Code

Run this from the repository root:

```sh
claude mcp add alice \
  -e "ALICE_AUTH_TOKEN_TTL=24h" \
  -- "$(pwd)/alice-mcp-server"
```

`ALICE_AUTH_TOKEN_TTL=24h` prevents the session from expiring during a long working session (default is 15 minutes).

Claude Code stores MCP server configuration in `~/.claude.json`. The `claude mcp add` command writes the correct format automatically. You can verify the server connected with `/mcp` inside a Claude Code session.

### 4. Start a Claude Code session

Open Claude Code in your project. The `alice` MCP tools will be available. Ask Claude to register you as an agent:

> Use the alice `register_agent` tool to register me. Use org slug `my-org`, email `me@example.com`, agent name `my-agent`, and client type `mcp`.

Claude will call `register_agent`, which generates a keypair automatically, completes the Ed25519 challenge flow, and stores the session token for subsequent tool calls.

### 5. Publish an artifact

Ask Claude to publish a status summary on your behalf:

> Use alice `publish_artifact` to publish a summary artifact with title "Working on auth refactor", content "Extracting JWT validation into a shared middleware layer. Two PRs open, one awaiting review.", sensitivity `low`, visibility mode `explicit_grants_only`, and confidence 0.9.

### 6. Resume a session across restarts

The MCP server loses its in-memory state when it restarts. To resume without re-registering, pass `ALICE_MCP_ACCESS_TOKEN` set to the `access_token` value returned by your first `register_agent` call:

```sh
claude mcp remove alice
claude mcp add alice \
  -e "ALICE_MCP_ACCESS_TOKEN=atok_..." \
  -e "ALICE_AUTH_TOKEN_TTL=24h" \
  -- "$(pwd)/alice-mcp-server"
```

With in-memory storage the token is valid only for the lifetime of the process. For durable sessions across restarts, use PostgreSQL (see below).

---

## Multi-user setup

For two or more agents to communicate, each user's MCP server must connect to a shared coordination server. The coordination server is the single source of truth; each user's machine only runs the MCP client.

### 1. Start the coordination server

If you have Podman installed, start the full stack (coordination server + PostgreSQL) with one command:

```sh
make local
```

This starts the coordination server at `http://127.0.0.1:8080` and PostgreSQL at `127.0.0.1:5432`.

Alternatively, start PostgreSQL separately and run the server manually:

```sh
make postgres-up
ALICE_DATABASE_URL="postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable" go run ./cmd/server
```

### 2. Configure each user's MCP server

Build the binary first if you haven't already:

```sh
go build -o alice-mcp-server ./cmd/mcp-server
```

Each user points their MCP server at the coordination server with `ALICE_SERVER_URL`. The user's machine needs no database access. Run this from the repository root:

```sh
claude mcp add alice \
  -e "ALICE_SERVER_URL=http://127.0.0.1:8080" \
  -- "$(pwd)/alice-mcp-server"
```

Replace `http://127.0.0.1:8080` with the actual URL of your coordination server. For HTTPS with a self-signed or internal CA certificate, also pass `-e "ALICE_SERVER_TLS_CA=/path/to/ca.pem"`.

In remote mode, token TTL is controlled by the coordination server process — `ALICE_AUTH_TOKEN_TTL` must be set on the server, not on each user's MCP client:

```sh
# on the server
ALICE_AUTH_TOKEN_TTL=24h ./alice-server

# on each user machine (MCP client) — no TTL setting needed here
ALICE_SERVER_URL=https://alice.example.com ./alice-mcp-server
```

Both users' MCP servers talk to the same coordination server, so artifacts, grants, queries, and requests are visible across sessions and survive process restarts.

### Local testing shortcut (single machine only)

For local development where all users are on the same machine and a full hosted setup is not needed, you can skip `cmd/server` and point MCP server instances directly at a shared PostgreSQL database:

```sh
claude mcp add alice \
  -e "ALICE_DATABASE_URL=postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable" \
  -e "ALICE_AUTH_TOKEN_TTL=24h" \
  -- "$(pwd)/alice-mcp-server"
```

This is not representative of real use — it requires every user to have direct database access — but it is convenient for local testing without running a separate server process.

---

## Connecting with OpenCode

Build the binary first if you haven't already:

```sh
go build -o alice-mcp-server ./cmd/mcp-server
```

Add alice to OpenCode's MCP configuration. OpenCode stores its config at `~/.config/opencode/config.json`:

```json
{
  "mcpServers": {
    "alice": {
      "command": "/absolute/path/to/alice/alice-mcp-server",
      "env": {
        "ALICE_DATABASE_URL": "postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable",
        "ALICE_AUTH_TOKEN_TTL": "24h"
      }
    }
  }
}
```

After restarting OpenCode, the alice tools appear in the tool list. The registration and usage flow is identical to Claude Code — ask the assistant to call `register_agent` with your details to begin.

---

## Edge agent: connecting real data sources

The edge agent runs locally per user, connects to GitHub, Jira, and Google Calendar, derives artifacts from your activity, and publishes them to the shared coordination server. This is how alice learns about your actual work without sending raw data to the server.

### Prerequisites

- The coordination server must be running and reachable (e.g. `make local` or a standalone `go run ./cmd/server`)
- Go 1.23.x

### 1. Create a config file

Copy the fixture example and adapt it:

```sh
cp examples/edge-agent-config.json my-edge-config.json
```

Edit `my-edge-config.json`:

```json
{
  "agent": {
    "org_slug": "my-org",
    "owner_email": "alice@example.com",
    "agent_name": "alice-agent",
    "client_type": "edge_agent"
  },
  "server": {
    "base_url": "http://127.0.0.1:8080"
  },
  "connectors": {
    "github": {
      "fixture_file": "examples/github-fixtures.json"
    },
    "jira": {
      "fixture_file": "examples/jira-fixtures.json"
    },
    "gcal": {
      "fixture_file": "examples/gcal-fixtures.json"
    }
  },
  "runtime": {
    "state_file": ".alice/alice-state.json",
    "poll_incoming_requests": true
  }
}
```

### 2. Run the edge agent

```sh
go run ./cmd/edge-agent -config my-edge-config.json
```

On first run the edge agent:
1. Registers with the coordination server using an Ed25519 keypair (generated and saved to `state_file`)
2. Reads the connector fixture files
3. Derives summary, blocker, and commitment artifacts from the fixture data
4. Publishes those artifacts to the coordination server
5. Polls for incoming requests if `poll_incoming_requests` is true

Subsequent runs reuse the saved keypair and token, skipping re-registration.

### 3. Connect a live GitHub source

Instead of fixtures, point the edge agent at your actual GitHub repositories:

```sh
export ALICE_GITHUB_TOKEN="ghp_your_personal_access_token"
go run ./cmd/edge-agent -config examples/edge-agent-github-live-config.json
```

Edit `examples/edge-agent-github-live-config.json` to set your org slug, email, actor login, and the repositories you want to observe:

```json
{
  "agent": {
    "org_slug": "my-org",
    "owner_email": "alice@example.com",
    "agent_name": "alice-agent",
    "client_type": "edge_agent"
  },
  "server": { "base_url": "http://127.0.0.1:8080" },
  "connectors": {
    "github": {
      "enabled": true,
      "token_env_var": "ALICE_GITHUB_TOKEN",
      "actor_login": "alice",
      "repositories": [
        { "name": "my-org/payments-api", "project_refs": ["payments-api"] }
      ]
    }
  },
  "runtime": {
    "state_file": ".alice/alice-live-state.json",
    "poll_incoming_requests": true
  }
}
```

### 4. Connect Jira and Google Calendar

Set the corresponding env vars and use the live config examples:

```sh
# Jira (API token auth)
export ALICE_JIRA_TOKEN="your_jira_api_token"
go run ./cmd/edge-agent -config examples/edge-agent-jira-live-config.json

# Google Calendar (OAuth — run bootstrap first)
go run ./cmd/edge-agent -config examples/edge-agent-gcal-oauth-config.json -bootstrap-connector gcal
# Then poll:
go run ./cmd/edge-agent -config examples/edge-agent-gcal-live-config.json
```

### 5. OAuth bootstrap

For connectors that use OAuth (GitHub, Jira, Google Calendar), run the bootstrap once to authorize and persist credentials:

```sh
go run ./cmd/edge-agent -config examples/edge-agent-github-oauth-config.json -bootstrap-connector github
```

The bootstrap mode:
1. Prints a browser authorization URL
2. Starts a local loopback callback server
3. Exchanges the authorization code with PKCE
4. Saves the resulting tokens to the credentials file (derived from `state_file` path)

On subsequent runs the edge agent loads the saved credentials automatically and refreshes them when expired.

To encrypt the credentials file at rest, set `ALICE_EDGE_CREDENTIAL_KEY`:

```sh
export ALICE_EDGE_CREDENTIAL_KEY="a-long-random-secret-key"
go run ./cmd/edge-agent -config my-edge-config.json
```

---

## Complete two-person workflow

This example shows Alice and Bob communicating through alice. One coordination server runs (with PostgreSQL) and each user's MCP server connects to it over HTTP.

### Setup

Start the coordination server:

```sh
make local
```

Both Alice and Bob configure their MCP server (run from the repository root):

```sh
claude mcp add alice \
  -e "ALICE_SERVER_URL=http://127.0.0.1:8080" \
  -- "$(pwd)/alice-mcp-server"
```

### Step 1 — Alice registers and publishes

In Alice's Claude Code session:

> Register me as an alice agent. Org slug: `example-corp`, email: `alice@example.com`, agent name: `alice-agent`, client type: `mcp`.

> Publish a summary artifact: title "Finishing payments retry PR", content "PR #128 is in review. Unblocked and targeting merge by EOD. Project: payments-api.", sensitivity `low`, visibility mode `explicit_grants_only`, confidence 0.9. Include a project_refs structured payload with value `["payments-api"]`.

### Step 2 — Bob registers and grants Alice access

In Bob's Claude Code session:

> Register me. Org: `example-corp`, email: `bob@example.com`, agent name: `bob-agent`, client type: `mcp`.

> Publish a summary artifact: title "Reviewing auth middleware", content "Reviewing Alice's JWT extraction PR. Should have feedback by tomorrow.", sensitivity `low`, visibility mode `explicit_grants_only`, confidence 0.85.

> Grant alice@example.com permission to query my status. Use scope type `project`, scope ref `payments-api`, allowed artifact types `["summary", "blocker"]`, max sensitivity `medium`, allowed purposes `["status_check"]`.

### Step 3 — Alice queries Bob's status

In Alice's Claude Code session:

> Query bob@example.com's peer status. Purpose: `status_check`, question: "What is Bob working on today?", requested types: `["summary"]`, project scope: `["payments-api"]`, time window: last 24 hours.

> Get the result of that query.

The response includes Bob's summary artifact. If the grant's `requires_approval_above_risk` threshold is exceeded, the response includes an `approval_state: pending` field and Bob receives an approval request.

### Step 4 — Alice sends Bob a request

In Alice's Claude Code session:

> Send bob@example.com a request. Type: `ask_for_review`, title: "Review needed on PR #128", content: "Can you finish your review of the payments retry PR today? It's blocking the sprint."

### Step 5 — Bob responds

In Bob's Claude Code session:

> List my incoming requests.

> Respond to that request with `accepted` and message "Will review this afternoon."

### Step 6 — Check the audit trail and sent requests

Either user can inspect the audit log:

> Use alice `get_audit_summary` to show my recent activity.

Alice can see the status of her sent request:

> Use alice `list_sent_requests` to show my outbound requests and their responses.

---

## MCP tool reference

All tools are available through the MCP stdio server. Call `register_agent` first to establish a session.

### `register_agent`

Register an agent and start an authenticated session. A keypair is generated automatically — you do not need to provide key material.

| Parameter | Required | Description |
|---|---|---|
| `org_slug` | yes | Organization slug (e.g. `example-corp`) |
| `owner_email` | yes | Agent owner email |
| `agent_name` | yes | Human-readable agent name |
| `client_type` | yes | Client type (e.g. `mcp`, `edge_agent`) |
| `public_key` | no | Base64-encoded Ed25519 public key. Auto-generated if omitted. |
| `private_key` | no | Base64-encoded Ed25519 private key. Auto-generated if omitted. |
| `invite_token` | no | Invite token required when the org's verification mode includes `invite_token` |
| `challenge_id` | no | Challenge ID for two-step completion |
| `challenge_signature` | no | Base64-encoded signature for two-step completion |

Registration always completes in a single call — the tool generates a keypair, submits the challenge, signs it internally, and stores the returned token automatically.

When the org's verification mode requires it, registration may not immediately result in an `active` agent:

- **Email OTP** (`email_otp` mode, requires SMTP on the server): the tool returns status `pending_email_verification`. Call `verify_email` with the code sent to your address, or `resend_verification_email` to request a new code.
- **Admin approval** (`admin_approval` mode): after any other verification completes, status is `pending_admin_approval`. An org admin must call `review_agent` to approve before the agent can act.
- **First registration** in a new org: a `first_invite_token` is returned in the response. Save this value — it is shown only once and must be passed as `invite_token` by all subsequent registrants.

If the org's mode includes `invite_token`, pass the shared token as the `invite_token` parameter below.

### `publish_artifact`

Publish a shareable artifact for the authenticated agent.

| Parameter | Required | Description |
|---|---|---|
| `artifact` | yes | Artifact object (see artifact schema below) |

**Artifact fields:**

| Field | Type | Description |
|---|---|---|
| `type` | string | `summary`, `blocker`, `commitment`, or `status_delta` |
| `title` | string | Short human-readable title |
| `content` | string | Artifact content text |
| `sensitivity` | string | `low`, `medium`, or `high` |
| `visibility_mode` | string | `explicit_grants_only` (only value currently enforced) |
| `confidence` | number | Confidence score 0.0–1.0 |
| `source_refs` | array | Source references (see below) |
| `structured_payload` | object | Optional. Include `project_refs: [...]` to scope to projects |

**Source reference fields:**

| Field | Type | Description |
|---|---|---|
| `source_system` | string | `github`, `jira`, `gcal`, `manual`, etc. |
| `source_type` | string | `pull_request`, `issue`, `event`, `manual`, etc. |
| `source_id` | string | Unique identifier within the source system |
| `observed_at` | string | RFC3339 timestamp of when the signal was observed |
| `trust_class` | string | `structured_system`, `unstructured_user`, etc. |
| `sensitivity` | string | Sensitivity of this specific source signal |

### `submit_correction`

Publish a corrected version of a previously published artifact. Only the original owner can correct an artifact. Creates a new artifact that supersedes the original.

| Parameter | Required | Description |
|---|---|---|
| `artifact_id` | yes | ID of the artifact being corrected |
| `artifact` | yes | Corrected artifact payload |

### `grant_permission`

Grant another user permission to query your artifacts.

| Parameter | Required | Description |
|---|---|---|
| `grantee_user_email` | yes | Email of the user being granted access |
| `scope_type` | yes | `project` or `global` |
| `scope_ref` | no | Project key when `scope_type` is `project` |
| `allowed_artifact_types` | yes | Array of types: `summary`, `blocker`, `commitment`, `status_delta` |
| `max_sensitivity` | yes | Maximum sensitivity the grantee can see: `low`, `medium`, or `high` |
| `allowed_purposes` | yes | Array of purposes: `status_check`, `dependency_check`, `planning` |

### `revoke_permission`

Revoke a grant you previously created. Only the grantor can revoke.

| Parameter | Required | Description |
|---|---|---|
| `policy_grant_id` | yes | Grant ID returned by `grant_permission` |

### `list_allowed_peers`

List peers that have granted the authenticated agent access to their artifacts. Returns each peer's email, allowed purposes, and allowed artifact types.

### `query_peer_status`

Submit a permission-checked status query to another agent. The query is evaluated immediately and the response is returned synchronously.

| Parameter | Required | Description |
|---|---|---|
| `to_user_email` | yes | Target user email |
| `purpose` | yes | `status_check`, `dependency_check`, or `planning` |
| `question` | yes | Natural-language question |
| `requested_types` | yes | Array of artifact types to retrieve |
| `project_scope` | no | Array of project refs to narrow scope |
| `time_window` | yes | Object with `start` and `end` (RFC3339 strings) |

Returns a `query_id`. If a grant requires approval above a risk threshold, the response includes `approval_state: pending`.

### `get_query_result`

Retrieve the current result for a previously submitted query.

| Parameter | Required | Description |
|---|---|---|
| `query_id` | yes | Query ID from `query_peer_status` |

Returns the query state and `response.artifacts` array when the query is complete.

### `send_request_to_peer`

Send a Gatekeeper request to another agent.

| Parameter | Required | Description |
|---|---|---|
| `to_user_email` | yes | Recipient user email |
| `request_type` | yes | e.g. `ask_for_review`, `ask_for_update`, `ask_for_decision` |
| `title` | yes | Request title |
| `content` | yes | Request body |
| `structured_payload` | no | Optional object (e.g. `{"project_refs": ["payments-api"]}`) |

### `list_incoming_requests`

List pending requests directed to the authenticated agent. Returns requests that have not expired and have not been responded to.

### `list_sent_requests`

List requests sent by the authenticated agent, including their current state and any response message from the recipient.

### `get_audit_summary`

Retrieve audit events visible to the authenticated agent (events where the agent is actor or target).

| Parameter | Required | Description |
|---|---|---|
| `since` | no | RFC3339 timestamp — only return events after this time |

### `respond_to_request`

Respond to an incoming request.

| Parameter | Required | Description |
|---|---|---|
| `request_id` | yes | Request ID from `list_incoming_requests` |
| `response` | yes | `accepted`, `deferred`, `denied`, `completed`, or `require_approval` |
| `message` | no | Optional response message |

When `response` is `require_approval`, an approval record is created and returned with an `approval_id`.

### `list_pending_approvals`

List approvals waiting for the authenticated agent to resolve.

### `resolve_approval`

Resolve a pending approval.

| Parameter | Required | Description |
|---|---|---|
| `approval_id` | yes | Approval ID |
| `decision` | yes | `approved` or `denied` |

### `verify_email`

Submit an email OTP code to activate an agent that is in `pending_email_verification` status.

| Parameter | Required | Description |
|---|---|---|
| `code` | yes | 6-digit OTP code sent to the agent owner's email address |

### `resend_verification_email`

Request a new email OTP code. Rate-limited to one resend per 60 seconds.

### `rotate_invite_token`

Generate a new invite token for the caller's org. The new raw token is returned once and replaces the previous one immediately. Restricted to org admins.

### `list_pending_agents`

List agents in the caller's org that are waiting for admin approval (`pending_admin_approval` status). Restricted to org admins. Supports pagination (`limit`, `cursor`).

### `review_agent`

Approve or reject an agent awaiting admin approval. Restricted to org admins. Rejection immediately revokes all bearer tokens for the agent.

| Parameter | Required | Description |
|---|---|---|
| `agent_id` | yes | ID of the agent to review |
| `decision` | yes | `approved` or `rejected` |
| `reason` | no | Optional explanation recorded in the audit log |

---

## HTTP API reference

The coordination server (`cmd/server`) exposes these endpoints at `http://127.0.0.1:8080` by default. All authenticated routes require `Authorization: Bearer <token>`.

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/healthz` | none | Health check |
| `POST` | `/v1/agents/register/challenge` | none | Begin registration — returns a challenge to sign. Accepts optional `invite_token`. Returns `first_invite_token` on org creation. |
| `POST` | `/v1/agents/register` | none | Complete registration — returns a bearer token |
| `POST` | `/v1/agents/verify-email` | required | Submit OTP code to activate a `pending_email_verification` agent |
| `POST` | `/v1/agents/resend-verification` | required | Resend email OTP (rate-limited to once per 60 s) |
| `POST` | `/v1/artifacts` | required | Publish an artifact |
| `POST` | `/v1/artifacts/:id/correct` | required | Publish a correction superseding a prior artifact |
| `POST` | `/v1/policy-grants` | required | Create a permission grant |
| `DELETE` | `/v1/policy-grants/:id` | required | Revoke a permission grant |
| `GET` | `/v1/peers` | required | List allowed peers |
| `POST` | `/v1/queries` | required | Submit a peer status query |
| `GET` | `/v1/queries/:id` | required | Retrieve a query result |
| `POST` | `/v1/requests` | required | Send a request to a peer |
| `GET` | `/v1/requests/incoming` | required | List incoming requests |
| `GET` | `/v1/requests/sent` | required | List sent requests |
| `POST` | `/v1/requests/:id/respond` | required | Respond to a request |
| `GET` | `/v1/approvals` | required | List pending approvals |
| `POST` | `/v1/approvals/:id/resolve` | required | Resolve an approval |
| `GET` | `/v1/audit/summary` | required | List audit events for the authenticated agent. Accepts `?since=<RFC3339>` |
| `POST` | `/v1/orgs/rotate-invite-token` | required (admin) | Rotate the org invite token; returns the new raw token once |
| `GET` | `/v1/orgs/pending-agents` | required (admin) | List agents awaiting admin approval |
| `POST` | `/v1/orgs/agents/:id/review` | required (admin) | Approve or reject an agent awaiting admin approval |

List endpoints accept `?limit=N&cursor=<opaque>` for pagination. Default limit is 50, maximum is 200.

---

## Coordination server environment variables

Set these on the `cmd/server` process.

| Variable | Default | Description |
|---|---|---|
| `ALICE_DATABASE_URL` | _(none — uses in-memory)_ | PostgreSQL connection string. When unset the server uses an in-memory store that resets on restart. |
| `ALICE_LISTEN_ADDR` | `:8080` | TCP address the HTTP server binds to |
| `ALICE_DEFAULT_ORG_NAME` | `Alice Development Org` | Display name used when auto-creating an org on first registration |
| `ALICE_AUTH_CHALLENGE_TTL` | `5m` | How long a registration challenge is valid before it expires |
| `ALICE_AUTH_TOKEN_TTL` | `15m` | How long a bearer token is valid after issuance. In remote mode this must be set on the server, not on each user's MCP client. |
| `ALICE_SMTP_HOST` | _(none — OTP disabled)_ | SMTP server hostname for email OTP verification. Set to `noop` to log OTP codes to stderr instead of sending email (useful for development). |
| `ALICE_SMTP_PORT` | `587` | SMTP server port. |
| `ALICE_SMTP_USERNAME` | _(none)_ | SMTP authentication username. |
| `ALICE_SMTP_PASSWORD` | _(none)_ | SMTP authentication password. |
| `ALICE_SMTP_FROM` | _(none)_ | From address for OTP emails. |
| `ALICE_SMTP_TLS` | `true` | Set `false` to disable STARTTLS (e.g. for local mail catchers). |
| `ALICE_EMAIL_OTP_TTL` | `10m` | How long an OTP code is valid after issuance. |
| `ALICE_EMAIL_OTP_MAX_ATTEMPTS` | `5` | Maximum wrong-code attempts before the verification record is locked. |

## MCP server environment variables

Set these on the `cmd/mcp-server` process (per user machine).

| Variable | Default | Description |
|---|---|---|
| `ALICE_SERVER_URL` | _(none — uses embedded mode)_ | URL of a remote coordination server (e.g. `https://alice.example.com`). When set the MCP server forwards all calls over HTTP instead of running an embedded stack. No local database access is required. |
| `ALICE_SERVER_TLS_CA` | _(none — uses system roots)_ | Path to a PEM file containing additional CA certificates to trust when connecting to `ALICE_SERVER_URL`. Use this for self-signed or internal CA certificates. |
| `ALICE_MCP_ACCESS_TOKEN` | _(none)_ | Pre-load an existing bearer token into the MCP server on startup, skipping the registration step. |
| `ALICE_DATABASE_URL` | _(none)_ | PostgreSQL connection string. Only needed when running the MCP server in standalone mode (without `ALICE_SERVER_URL`) and you want durable storage shared across MCP server instances. |
| `ALICE_AUTH_TOKEN_TTL` | `15m` | Token TTL in embedded (local) mode only. In remote mode, set this on the coordination server instead. |

## Edge agent environment variables

Set these when running the `cmd/edge-agent` process.

| Variable | Default | Description |
|---|---|---|
| `ALICE_GITHUB_TOKEN` | _(none)_ | GitHub personal access token used by the edge agent's GitHub connector. |
| `ALICE_JIRA_TOKEN` | _(none)_ | Jira API token used by the edge agent's Jira connector. |
| `ALICE_GCAL_TOKEN` | _(none)_ | Google Calendar OAuth access token used by the edge agent's calendar connector. |
| `ALICE_EDGE_CREDENTIAL_KEY` | _(none)_ | AES-GCM encryption key for the connector credentials file at rest. |

---

## Edge agent config reference

The edge agent reads a JSON config file passed via `-config`. All paths in the config are resolved relative to the config file's directory unless they are absolute.

### `agent`

| Field | Description |
|---|---|
| `org_slug` | Organization slug. Must match the slug used when registering via MCP or the HTTP API. |
| `owner_email` | Agent owner email. |
| `agent_name` | Human-readable agent name. |
| `client_type` | Client type identifier (e.g. `edge_agent`). |
| `invite_token` | Optional. Invite token required when the org's verification mode includes `invite_token`. |

### `server`

| Field | Description |
|---|---|
| `base_url` | Base URL of the coordination server (e.g. `http://127.0.0.1:8080`). |

### `runtime`

| Field | Description |
|---|---|
| `state_file` | Path to the persisted edge state file (keypair, token, connector cursors). Created automatically on first run. |
| `credentials_file` | Optional. Path to the connector credentials file. Defaults to `<state_file_stem>.credentials.json`. |
| `credentials_key_env_var` | Env var holding the AES-GCM encryption key for the credentials file. Defaults to `ALICE_EDGE_CREDENTIAL_KEY`. |
| `credentials_key_file` | Optional path to a file containing the encryption key. |
| `artifact_fixture_file` | Optional path to a JSON file with manually authored artifacts to publish on each run. |
| `query_watch_ids` | Array of query IDs to check for results on each run. |
| `poll_incoming_requests` | If `true`, the agent polls for and logs incoming requests on each run. |

### `connectors.github`

| Field | Description |
|---|---|
| `enabled` | Set `true` to enable live polling. |
| `fixture_file` | Path to a GitHub fixture file for offline testing. |
| `api_base_url` | GitHub API base URL. Defaults to `https://api.github.com`. |
| `token_env_var` | Env var holding a GitHub personal access token. Defaults to `ALICE_GITHUB_TOKEN`. |
| `token_file` | Optional path to a file containing the token. |
| `actor_login` | GitHub username of the agent owner. Only PRs authored or reviewed by this user are ingested. |
| `repositories` | Array of `{name, project_refs}` objects specifying which repositories to observe. |
| `oauth` | OAuth config block for GitHub OAuth App bootstrap (client_id, scopes, callback_url, etc.). |
| `webhook.enabled` | Set `true` to enable the local GitHub webhook intake server. |
| `webhook.listen_addr` | Address for the webhook listener. Defaults to `127.0.0.1:8788`. |
| `webhook.secret_env_var` | Env var holding the HMAC webhook secret. Defaults to `ALICE_GITHUB_WEBHOOK_SECRET`. |

### `connectors.jira`

| Field | Description |
|---|---|
| `enabled` | Set `true` to enable live polling. |
| `fixture_file` | Path to a Jira fixture file. |
| `api_base_url` | Jira instance base URL (required for live polling). |
| `token_env_var` | Env var holding a Jira API token. Defaults to `ALICE_JIRA_TOKEN`. |
| `actor_email` | Jira account email of the agent owner. Only issues assigned to this user are ingested. |
| `projects` | Array of `{key, project_refs}` objects. Project keys must match `^[A-Z][A-Z0-9_]+$`. |
| `webhook.enabled` | Set `true` to enable the local Jira webhook intake server. |
| `webhook.listen_addr` | Address for the webhook listener. Defaults to `127.0.0.1:8789`. |
| `webhook.secret_env_var` | Env var holding the shared secret. Defaults to `ALICE_JIRA_WEBHOOK_SECRET`. |

### `connectors.gcal`

| Field | Description |
|---|---|
| `enabled` | Set `true` to enable live polling. |
| `fixture_file` | Path to a Google Calendar fixture file. |
| `api_base_url` | Calendar API base URL. Defaults to `https://www.googleapis.com/calendar/v3`. |
| `token_env_var` | Env var holding a Google OAuth access token. Defaults to `ALICE_GCAL_TOKEN`. |
| `calendars` | Array of `{id, project_refs, category}` objects identifying which calendars to observe. |
| `oauth` | OAuth config block for Google OAuth bootstrap. |
| `webhook.enabled` | Set `true` to enable the local Google Calendar webhook intake server. |
| `webhook.listen_addr` | Address for the webhook listener. Defaults to `127.0.0.1:8790`. |
| `webhook.secret_env_var` | Env var holding the channel token secret. Defaults to `ALICE_GCAL_WEBHOOK_SECRET`. |
| `webhook.callback_url` | Public URL that Google will deliver change notifications to. |
| `webhook.channel_id_prefix` | Prefix for generated channel IDs. Defaults to `alice-edge-gcal`. |
| `webhook.requested_ttl_seconds` | Requested channel lifetime in seconds. Google may return a shorter TTL. |

To register provider-side push watches for Google Calendar (so Google pushes changes rather than you polling):

```sh
go run ./cmd/edge-agent -config my-gcal-config.json -register-watches gcal
```

---

## Local development commands

Run from the repository root. Requires Podman and `podman-compose` (or `podman compose`).

| Command | Description |
|---|---|
| `make local` | Build and start the full stack (server + PostgreSQL) with Podman Compose |
| `make down` | Stop the full stack |
| `make postgres-up` | Start only PostgreSQL, reusing an existing container when present |
| `make postgres-down` | Stop only the PostgreSQL container |
| `make status` | Show container status |
| `make logs` | Tail coordination server logs |
| `make test` | Run the full Go test suite (in-memory storage) |
| `make test-race` | Run the test suite with the race detector enabled |
| `make test-cover` | Run tests with coverage report; fails if coverage is below 80% |
| `make test-postgres` | Start PostgreSQL and run the test suite with `ALICE_TEST_DATABASE_URL` set |
| `make e2e` | Run end-to-end tests using an in-process HTTP server (no external deps) |
| `make e2e-postgres` | Start PostgreSQL and run e2e tests against it |
| `make test-all` | Run unit tests followed by e2e tests |
| `make ci` | Run coverage check and e2e tests (full CI pipeline locally) |
| `make mailpit-ui` | Print the Mailpit web UI URL (`http://localhost:8025`) for inspecting OTP emails during local development |

### Running without containers

Start the server with in-memory storage:

```sh
go run ./cmd/server
```

Start the server with PostgreSQL (requires a running instance):

```sh
ALICE_DATABASE_URL="postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable" go run ./cmd/server
```

Build and run the MCP server standalone (use the binary rather than `go run` when registering with Claude Code):

```sh
go build -o alice-mcp-server ./cmd/mcp-server
./alice-mcp-server
```

Run the edge agent with the fixture example:

```sh
go run ./cmd/edge-agent -config examples/edge-agent-config.json
```

Run unit tests:

```sh
go test ./...
# or with race detector:
go test -race -count=1 ./...
# or via make:
make test
make test-race
```

Run unit tests against PostgreSQL:

```sh
ALICE_TEST_DATABASE_URL="postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable" go test -race -count=1 ./...
# or via make (starts PostgreSQL automatically):
make test-postgres
```

Run end-to-end tests (in-process server, no external dependencies):

```sh
go test -tags e2e -race -count=1 -timeout 5m ./tests/e2e/...
# or via make:
make e2e
```

Run end-to-end tests against PostgreSQL:

```sh
ALICE_TEST_DATABASE_URL="postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable" go test -tags e2e -race -count=1 -timeout 5m ./tests/e2e/...
# or via make (starts PostgreSQL automatically):
make e2e-postgres
```

---

## Core principles

1. Raw source data stays local — edge runtimes derive summaries, not raw logs
2. Only derived artifacts move through the server
3. All cross-agent access is permission-checked and deny-by-default
4. Every query, grant, and request is auditable
5. The model may propose; deterministic code decides
6. Untrusted content is treated as data, never as policy
