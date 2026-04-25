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
- [x] Summariser interface scaffold for the git connector (heuristic default; LLM-backed slot reserved via `ALICE_TRACK_SUMMARISER=claude`)

## spec features

- [x] Email OTP, invite tokens, admin approval queue, verification-mode management API
- [x] Field-level artifact redaction, fine-grained audit queries
- [x] `team_scope` / `manager_scope` visibility + rich org graph
- [x] Operator phase (per-user opt-in, risk-policy-gated `acknowledge_blocker` executor, idempotent lifecycle, audit emission on every transition)
- [x] Advanced risk policy engine (per-org versioned JSON policies, admin-gated apply/history/activate, evaluator overrides the grant ladder in queries service)

## security / sandbox

- [x] OpenShell sandbox integration: `deploy/openshell/policies/` with enforce and audit-mode policies for the edge agent; setup guide at `docs/openshell.md`

## quality / operational

- [x] Test coverage threshold, e2e tracker test, audit NDJSON sink
- [x] Admin UI + CORS/CSRF (feature-flagged, email-OTP sign-in, double-submit CSRF, strict CSP)
- [x] Production readiness: metrics endpoint, access log, request-ID, livez/readyz, per-agent rate limits, HSTS, trusted-proxy IP extraction, DELETE /v1/users/me, DELETE /v1/orgs/:slug
- [x] Production readiness (continued): Prometheus metrics tests, release + nightly workflow, govulncheck CI, Dependabot, docs/operations.md, docs/incident-response.md, deploy/kubernetes/ manifests, cmd/alice-gc GC job, email SMTP tests, test-cover-postgres with 75% threshold

## usability / onboarding

- [x] CLI state file opt-in AES-256-GCM encryption via `ALICE_ENCRYPT_STATE_KEY`
- [x] Edge agent `-validate-config` and `-generate-config` flags for safe, debuggable config authoring
- [x] `alice init` guided next-steps output after registration
- [x] Connector setup guides: `docs/connectors/github.md`, `docs/connectors/jira.md`, `docs/connectors/gcal.md`

---

Start any new work from the matching plan. Plans are starting sketches; open questions in each plan are items to settle with the user before coding.
