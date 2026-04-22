# tracker connectors inside the MCP server process

## goal

Add Jira, GitHub API, and Google Calendar pollers to the MCP server process so users who run `cmd/mcp-server` get teammate-visible status artifacts automatically, without running the separate `cmd/edge-agent`.

## why

The MCP server already hosts a built-in local git tracker (`internal/tracker/`). The edge agent (`cmd/edge-agent/`) is a richer runtime that can poll GitHub/Jira/Calendar, but it's a separate process and a separate setup. For most users a single CLI-native binary with connectors baked in is better UX. Keep `cmd/edge-agent` for users who want webhook intake or OAuth bootstrap flows; the MCP tracker stays simple (poll-only).

## constraints

- do not duplicate connector logic. Reuse `internal/edge` packages where possible — pollers, normalizers, derivation — so a bug fix in one path fixes both
- MCP tracker keeps its "silent publish in the background" contract: no UI prompts, no blocking on errors, structured-log failures and retry on the next tick
- credentials live on the user's machine. Match the existing `ALICE_TRACK_*` env-var pattern; do not require a separate credential store
- preserve dedup / supersedes-chain behaviour from the git tracker — repeated polls that return the same state must not re-publish

## shape

- `internal/tracker/` grows a `Connector` interface. Each connector (`git`, `github`, `jira`, `calendar`) implements `Poll(ctx) ([]core.Artifact, error)`
- the tracker loop iterates enabled connectors each tick, dedups, publishes new artifacts
- new env vars: `ALICE_TRACK_GITHUB_TOKEN`, `ALICE_TRACK_JIRA_BASE_URL` + `ALICE_TRACK_JIRA_TOKEN`, `ALICE_TRACK_CALENDAR_CREDENTIALS` (path to OAuth JSON)
- `ALICE_TRACK_CONNECTORS` comma list (e.g. `git,github,jira`) selects which connectors to run; defaults to `git` only (preserves current behaviour)
- GitHub connector: use the same polling code the edge agent uses (refactor into `internal/edge/connectors/github` if it isn't already shared) — issues assigned to user + recently-updated PRs authored by user
- Jira connector: issues assigned to user in a configurable JQL (reuse the edge-agent project-key validation regex)
- Calendar connector: upcoming + recently-updated events on the primary calendar; derive `commitment` artifacts for events with clear titles and `status_delta` only when the week's load changes meaningfully

## acceptance criteria

- enabling a new connector via env var is a one-line change; no code edit needed
- each connector has fixture-based unit tests (mirror `internal/edge/connector_test.go`)
- turning connectors on/off does not affect the git tracker
- integration test: run MCP server with `ALICE_TRACK_CONNECTORS=git,github` against a mock HTTP GitHub server, confirm status_delta artifacts appear in the artifact repo
- failing connector (e.g. Jira base URL unreachable) logs at WARN and does not stop other connectors from polling

## out of scope

- webhook intake (that's what `cmd/edge-agent -serve-webhooks` exists for)
- OAuth bootstrap UX in the MCP server — use `cmd/edge-agent -bootstrap-connector` to mint credentials, then pass the credential file path via env var
- new connector types (Linear, Asana, …) — add them after the framework lands

## dependencies

None — ships independently.
