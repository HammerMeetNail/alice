# Repository Guidelines

## Project Structure & Module Organization
The repository is in early implementation. The root now contains runnable server code plus the product and implementation documents:

- `docs/technical-spec.md`: system architecture, scope, and MVP boundaries
- `docs/threat-model.md`: security goals, trust boundaries, and threat analysis
- `docs/implementation-plan.md`: current implementation status, encoded assumptions, and the next recommended steps
- `cmd/server/`: coordination server HTTP entrypoint
- `cmd/mcp-server/`: stdio MCP entrypoint for local tool clients
- `cmd/edge-agent/`: local edge runtime skeleton entrypoint plus webhook-server mode
- `internal/`: current server and edge-runtime packages including auth, HTTP API, MCP, Gatekeeper flows, normalized edge connector events, live GitHub/Jira/Calendar pollers with pagination and transient retry handling, signed GitHub webhook intake, connector cursor state, loopback OAuth connector bootstrap, encrypted local connector credential storage, refresh-token handling, actionable re-auth errors, persisted latest-derived-artifact tracking, replacement-aware artifact derivation, project-level aggregate derivation, and memory/PostgreSQL implementations
- `examples/`: runnable local example configs plus artifact fixtures, connector fixtures, live polling examples, webhook intake examples, and OAuth bootstrap examples for GitHub, Jira, and Google Calendar
- `api/jsonschema/`: current machine-readable schema files

Keep the implementation plan, `README.md`, and this file aligned whenever the codebase meaningfully changes.

## Build, Test, and Development Commands
Run these commands from the repository root:

- `make local`: build and start the current server stack with Podman Compose
- `make down`: stop the Podman Compose stack
- `make status`: show container status
- `make logs`: tail server container logs
- `make test`: run the Go test suite
- `make test-postgres`: run the query-flow test path against the local PostgreSQL service
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

## Commit & Pull Request Guidelines
Existing history uses short, imperative commit subjects such as `Initial commit` and `Add readme, tech-spec, threat model`. Follow that pattern: one-line, imperative, capitalized, no trailing period.

PRs should include a brief summary, the reason for the change, and the affected documents. Link the relevant issue when one exists. Include screenshots only when a PR adds rendered diagrams or other visual assets.
