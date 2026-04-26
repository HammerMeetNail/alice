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
