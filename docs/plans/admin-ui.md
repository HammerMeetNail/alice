# admin UI

## goal

A browser-facing admin surface for org admins: review pending agent approvals, manage invite tokens, inspect audit events, edit policies, and configure per-org gatekeeper tuning. This is the first browser surface — CORS/CSRF get built as part of the same work.

## why

Everything admins do today happens through the CLI or MCP tools. That's fine for engineering-heavy orgs but a hard sell for anyone else. A minimal web UI — sign in, see pending approvals, act on them — closes the gap for regular-team deployment.

## constraints

- no new trust boundary: the UI calls the same HTTP API the CLI does, no admin-only back channel
- browser-side auth is short-lived session cookies issued after a first-party sign-in flow (email OTP, same mechanism agents use). No password storage
- CSRF tokens on every state-changing request; CORS allow-list is explicit, not `*`
- ship with a tight Content-Security-Policy; no inline scripts, no eval, no remote CDN for core assets
- every browser-issued change is audit-logged with actor user + session ID so it's indistinguishable from CLI activity downstream
- no server-side rendering with untrusted content — artifact / request content renders as text, never as HTML

## shape

- new package `internal/webui/` serving `GET /admin/*` from embedded static assets (Go `embed` + a small SPA or server-rendered templates — pick the smaller option that gets the first screen shipped)
- new package `internal/websession/` for cookie-based session lifecycle: sign in via email OTP, issue a signed session cookie with Secure + HttpOnly + SameSite=Strict, 24h TTL, rotate on privilege change
- CSRF: double-submit cookie pattern. Every mutating request carries `X-CSRF-Token` matching the `csrf` cookie
- CORS: read allow-list from config. Empty list disables CORS entirely (same-origin only, the safe default)
- routes, in priority order for v1:
  1. sign-in / sign-out
  2. pending agent approvals (list, approve, reject)
  3. org invite token (view, rotate)
  4. audit event browser (filter by kind / actor / date)
- feature-gate with env var `ALICE_ADMIN_UI_ENABLED` — off by default so existing deployments don't suddenly expose a new surface

## acceptance criteria

- all four v1 routes implemented and exercised by Playwright / Cypress-style tests or at minimum HTTP-level tests that post a CSRF-protected form
- CSRF missing → 403
- CORS request from a non-allow-listed origin → preflight denied
- CSP header present on every HTML response; no inline scripts in shipped HTML
- session cookie is `Secure` + `HttpOnly` + `SameSite=Strict`; cookie only issued over HTTPS (reject plaintext in non-dev mode)
- `ALICE_ADMIN_UI_ENABLED=false` → `/admin/*` returns 404 and no session endpoints are registered
- every mutation produces the same audit event a CLI mutation would

## open questions to settle with the user before building

- SPA (React/Vue/Svelte) vs. server-rendered Go templates + htmx — the latter is much less code and ships sooner
- self-host the UI on the coordination server port, or serve it from a separate port / separate binary?
- what's the sign-in story for a user who's never registered as an agent? Register a browser-only principal, or require an existing agent session?

## out of scope

- per-user self-service UI (this plan is admin-only)
- organization creation / billing
- rich dashboards / analytics
- i18n
- SSO / SAML

## dependencies

None block it, but several plans get UI screens once this ships:

- per-org-gatekeeper-tuning.md → tuning editor screen
- risk-policy-engine.md → policy editor screen
- org graph + scoped visibility (landed) → team / manager management screen

Ship the admin-ui skeleton first, add per-feature screens as those plans land.
