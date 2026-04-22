# roadmap

Quick status view. Every unchecked item points at a plan in `docs/plans/` that a future session can pick up cold. Completed work lives in git history.

---

## CLI and gatekeeper (primary surface)

- [x] First-class `alice` CLI (full HTTP surface, persisted session, untrusted-data framing on list output)
- [x] Gatekeeper auto-answer for informational request types with purpose-fallback, confidence gating, audit emission
- [x] Server-wide configurable gatekeeper threshold / lookback via `ALICE_GATEKEEPER_*` env vars
- [x] `alice inbox --watch`, `alice completion bash|zsh|fish`, Claude Code skill + hardened hook regex
- [x] Per-org gatekeeper tuning (confidence threshold + lookback window, admin-gated CLI / MCP / HTTP)

## tracker evolution

- [x] Local git tracker in MCP server with dedup, supersedes chains, configurable interval, state persistence
- [x] Additional tracker connectors (Jira, GitHub API, Google Calendar) via `ALICE_TRACK_CONNECTORS`
- [ ] Model-assisted artifact derivation (LLM summarisation) — [plans/model-assisted-derivation.md](plans/model-assisted-derivation.md)

## spec features

- [x] Email OTP, invite tokens, admin approval queue, verification-mode management API
- [x] Field-level artifact redaction, fine-grained audit queries
- [ ] `team_scope` / `manager_scope` visibility + rich org graph — [plans/org-graph-and-scoped-visibility.md](plans/org-graph-and-scoped-visibility.md)
- [ ] Operator phase — [plans/operator-phase.md](plans/operator-phase.md)
- [ ] Advanced risk policy engine — [plans/risk-policy-engine.md](plans/risk-policy-engine.md)

## quality / operational

- [x] Test coverage threshold, e2e tracker test, audit NDJSON sink
- [ ] Admin UI + CORS/CSRF — [plans/admin-ui.md](plans/admin-ui.md)

---

Start any new work from the matching plan. Plans are starting sketches; open questions in each plan are items to settle with the user before coding.
