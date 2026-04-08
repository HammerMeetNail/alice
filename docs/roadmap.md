# roadmap

Tracked work items for alice, organized by priority.

---

## tracker evolution

- [ ] Additional tracker connectors (Jira, GitHub API, Google Calendar) inside MCP server process
- [ ] Model-assisted artifact derivation (LLM summarization instead of heuristic rules)
- [x] Tracker state persistence (dedup state is in-memory; restarts re-publish)
- [ ] Richer local derivation (e.g. "working on auth refactor" vs raw git state)

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
- [ ] Dedicated audit event sink (beyond slog)
- [ ] CORS/CSRF (when a browser-facing surface exists)
