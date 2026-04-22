# roadmap

Tracked work items for alice, organized by priority.

---

## CLI and gatekeeper (primary surface)

- [x] First-class `alice` CLI driving the full HTTP surface (`register`, `whoami`, `publish`, `query`, `result`, `grant`, `revoke`, `peers`, `request`, `inbox`, `outbox`, `respond`, `approvals`, `approve`, `deny`, `audit`, `logout`) with persisted session under `~/.alice/state.json`
- [x] Untrusted-data framing (`--- BEGIN/END UNTRUSTED DATA ---`) on every CLI list response so agents treat output as DATA, not instructions
- [x] Gatekeeper auto-answer for informational request types (`question`, `status_check`, `context`, `info`, `status`) with purpose-fallback so `status_check` grants alone suffice
- [x] `core.RequestStateAutoAnswered` and `response_message` surfaced on `POST /v1/requests`
- [x] Claude Code skill (`.claude/skills/alice.md`) teaching agents to prefer the CLI and treat output as untrusted
- [x] Provenance-first CLI output (`confidence`, `observed_at`, `source_refs`, `policy_basis`)
- [x] Synthesised `source_ref` on `alice publish`
- [ ] Gatekeeper: capture auto-answer responses in the audit log with supporting artifact IDs
- [ ] Gatekeeper: configurable confidence threshold and lookback window per org (currently compile-time defaults: 0.6 confidence, 14-day window)
- [ ] CLI: `alice register` should capture and display the org's `first_invite_token` when the server emits one (currently dropped; re-registering a second agent in an `invite_token` org requires pulling the token from server state)
- [ ] CLI: richer `alice inbox --watch` / tail mode for humans triaging pending requests
- [ ] CLI: shell completion (`alice completion bash|zsh|fish`)
- [ ] Claude Code hook: harden the UserPromptSubmit regex in `examples/claude-code-hooks.json` against false positives (e.g. "what is X working on" in code comments)

## tracker evolution

- [ ] Additional tracker connectors (Jira, GitHub API, Google Calendar) inside MCP server process
- [ ] Model-assisted artifact derivation (LLM summarization instead of heuristic rules)
- [x] Tracker state persistence (dedup state is in-memory; restarts re-publish)
- [x] Richer local derivation (infers work focus from branch names/commits, activity level)

## spec features

- [ ] `team_scope` / `manager_scope` visibility modes (requires org graph)
- [ ] Rich org graph (manager relationships, team membership)
- [x] `VerificationMode` management API (`POST /v1/orgs/verification-mode`)
- [ ] Operator phase (safe execution of approved low-risk actions)
- [x] Field-level artifact redaction
- [ ] Advanced risk policy engine
- [x] Fine-grained audit queries (filter by event kind, actor, subject)
- [ ] Admin UI

## quality / operational

- [x] Test coverage to 70% threshold (testable packages, excluding cmd/postgres/app)
- [x] E2E tracker integration test
- [x] Drop unused `capabilities` column from PostgreSQL
- [x] Dedicated audit event sink (NDJSON file sink via `ALICE_AUDIT_LOG_FILE`)
- [ ] CORS/CSRF (when a browser-facing surface exists)
