# FizzBuzz — Full End-to-End Alice Demo

This example demonstrates the complete alice workflow: an OpenCode agent builds a small web app while automatically publishing status updates to the alice coordination server at every milestone.

## What you'll see

A self-contained demo showing:

1. **OpenCode + alice MCP tools** — the agent auto-loads the alice skill and uses native tools
2. **Status publishing at task boundaries** — `status_delta` artifacts published when the agent starts work, hits milestones, and completes the task
3. **Queryable teammate state** — after the build, another agent (or teammate) can query "what was built?" and get a permission-checked response from the published artifacts

## Prerequisites

- [Go](https://go.dev/dl/) installed (to build the MCP server)
- [OpenCode](https://opencode.ai) installed and configured with an LLM provider
- Node.js or Python (to serve the HTML page — any HTTP server works)

## Quick start

### 1. Build the MCP server

```bash
cd /path/to/alice
make build-mcp-server
```

This produces `bin/alice-mcp-server` — a stdio MCP server that OpenCode connects to via the `opencode.json` config in this repo.

### 2. Launch OpenCode from the repo root

```bash
cd /path/to/alice
opencode
```

When OpenCode starts:
- It loads `opencode.json` and connects to the alice MCP server
- It discovers `.claude/skills/alice/SKILL.md` and registers the `alice` skill
- The skill is listed in the available skills (`/skills`)

### 3. Run the demo setup

```bash
./examples/fizzbuzz/demo.sh
```

This builds binaries and starts Postgres + the coordination server. Then launch OpenCode yourself:

```bash
opencode
```

### 4. In the OpenCode TUI, type this prompt

```
Create a fizzbuzz web app at examples/fizzbuzz/index.html.
Count 1-100 with "Fizz" for multiples of 3, "Buzz" for 5, "FizzBuzz" for 15.
Use nice CSS: dark background, glassmorphism card, color-coded cells.
```

That's it. The agent's alice skill triggers automatically — no need to mention alice, `register_agent`, or `publish_artifact` in your prompt.

### 5. What happens

The agent will:

| Step | Action | Alice Event |
|------|--------|-------------|
| Register | Calls `register_agent` to establish a session | `agent.registered` audit event |
| Start | Publishes `status_delta`: "Started: building fizzbuzz web app" | Artifact published with `confidence: 1.0` |
| Build | Creates `examples/fizzbuzz/index.html` with HTML/CSS/JS | File written to disk |
| Complete | Publishes `status_delta`: "Completed: fizzbuzz web app" | Artifact published with `confidence: 1.0` |

The status updates are visible to any teammate with the right grants. From another session, they can run:

```bash
alice query --to dave@example.com --purpose status_check --types status_delta
```

Or from OpenCode:

```
Use the alice query_peer_status tool to check what dave@example.com is working on
```

The response will include the published artifacts with their `observed_at` timestamps and `confidence` scores.

### 5. Serve the result

```bash
# Using Python
python3 -m http.server 8080 -d examples/fizzbuzz

# Using Node.js
npx serve examples/fizzbuzz

# Open http://localhost:8080 in a browser
```

## Viewing published artifacts

Once the agent has published artifacts, you can view them in two ways:

### Via the HTTP API

Artifacts use `explicit_grants_only` visibility by default. Before you can query them — even your own — you need a self-grant. The full workflow:

**1. Register or re-register to get a fresh bearer token**

```bash
alice register --org demo --email demo@example.com --agent "opencode-agent"
```

This saves a session to `~/.alice/state.json` containing the `access_token`.

**2. Create a self-grant (required — no implicit self-access)**

```bash
alice grant --to demo@example.com \
  --types status_delta,summary,commitment,blocker \
  --sensitivity low \
  --purposes status_check
```

**3. Query your artifacts**

```bash
curl -s http://localhost:8080/v1/queries -X POST \
  -H "Authorization: Bearer $(jq -r '.access_token' ~/.alice/state.json)" \
  -H "Content-Type: application/json" \
  -d '{
    "to_user_email": "demo@example.com",
    "purpose": "status_check",
    "requested_types": ["status_delta", "summary", "commitment", "blocker"],
    "time_window": {
      "start": "2026-04-26T00:00:00Z",
      "end": "2026-04-27T00:00:00Z"
    },
    "question": "Show recent work"
  }' | jq .
```

The response contains a `query_id`. Retrieve the full result including artifact content:

```bash
# Replace with the query_id from the response above
curl -s "http://localhost:8080/v1/queries/query_20260427T023501..." \
  -H "Authorization: Bearer $(jq -r '.access_token' ~/.alice/state.json)" | jq .
```

**Alternatively, use the CLI** which handles all of the above in one command:

```bash
alice register --org demo --email demo@example.com --agent "opencode-agent"
alice grant --to demo@example.com --types status_delta,summary,commitment,blocker --sensitivity low --purposes status_check
alice query --to demo@example.com --purpose status_check --types status_delta,summary,commitment,blocker
```

The same grant-and-query pattern applies when querying a teammate — except the grant must come from *them* to *you*, not a self-grant.

### Via direct PostgreSQL access

When the server is backed by PostgreSQL (the default in `make local` and `demo.sh`), artifacts are stored in the `artifacts` table:

```bash
PGPASSWORD=alice psql -h 127.0.0.1 -U alice -d alice \
  -c "SELECT artifact_id, type, title, content, confidence, created_at
      FROM artifacts
      ORDER BY created_at;"
```

To get a compact summary with agent info:

```bash
PGPASSWORD=alice psql -h 127.0.0.1 -U alice -d alice \
  -c "SELECT a.type, a.title, ag.agent_name, ag.owner_user_id AS email,
             a.confidence, a.created_at
      FROM artifacts a
      JOIN agents ag ON ag.agent_id = a.owner_agent_id
      ORDER BY a.created_at;"
```

All artifact content and timestamps are stored as-is — no redaction is applied at the storage layer (redaction happens at the API query layer based on grants).

## The output

`examples/fizzbuzz/index.html` — a self-contained fizzbuzz web app with:

- Dark gradient background with glassmorphism card layout
- 100-cell grid with staggered fade-in animations
- Color-coded tiles: blue (Fizz), green (Buzz), purple/pink (FizzBuzz)
- Hover effects with scale and shadow transforms
- Legend component

## How it works (under the hood)

```
┌──────────┐    stdio MCP    ┌──────────────────┐    HTTP API    ┌───────────────┐
│ OpenCode │ ◄─────────────► │ alice-mcp-server │ ◄────────────► │ alice server  │
│  (agent) │  39 MCP tools   │   (bin/alice-    │  register,    │  (make local  │
│          │                 │    mcp-server)    │  publish,     │   or embedded  │
│          │                 │                  │  query, ...   │   in-memory)   │
└──────────┘                 └──────────────────┘               └───────────────┘
        │                            │                                  │
   skill loads                  embedded mode                     in-memory store
   from .claude/             (no DB needed for                   (persists for
   skills/alice/              demo sessions)                     session lifetime)
```

- **`opencode.json`** configures the MCP server as a local stdio transport
- **`.claude/skills/alice/SKILL.md`** tells the agent to use alice tools instead of guessing
- **`bin/alice-mcp-server`** is the Go binary that speaks MCP stdio on one side and the alice HTTP API on the other
- The agent gets 39 tools (`publish_artifact`, `query_peer_status`, `send_request_to_peer`, etc.) listed in the skill's interface table

## Running with a real coordination server

The demo works standalone with the MCP server's embedded in-memory store. For multi-user scenarios, start the full coordination server:

```bash
make local         # starts PostgreSQL + alice server via Podman
```

Then set the MCP server to talk to it:

```bash
export ALICE_SERVER_URL=http://localhost:8080
opencode
```

Now status published by one agent is visible to any teammate's agent.

## What's next

- Try the [edge agent config examples](../) for GitHub, Jira, and Google Calendar live polling
- Read `.claude/skills/alice/SKILL.md` to understand the agent instructions
- Create your own example and submit a PR
