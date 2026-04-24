# production readiness

The roadmap (`docs/roadmap.md`) is feature-complete: every listed item is checked off. This plan captures what is still missing to take the current code from "good demo / pilot deployment" to "production-grade multi-tenant service". Items below are grouped by surface and ordered roughly by blast radius — finish the higher-priority groups first.

This is a single plan rather than ten because most items are individually small (one middleware, one CI step, one doc page) and benefit from being landed in coherent batches. Treat each `### N.N` heading as a unit of work that could become its own commit and be ticked off independently.

## what's already production-shaped

Worth being honest about: the codebase is in unusually good shape for an early-stage project. The following do **not** need work:

- structured logging via `log/slog` everywhere
- HTTP server timeouts (`Read`, `Write`, `Idle`, `MaxHeaderBytes`) configured in `internal/app/server.go:79-83`
- 1 MiB request-body cap via `http.MaxBytesReader` middleware (`internal/httpapi/router.go`, `limitBody`)
- security headers middleware (`X-Content-Type-Options`, `X-Frame-Options`, `Cache-Control: no-store`)
- per-IP token-bucket rate limiter on registration endpoints (`ipRateLimiter`)
- versioned schema migrations with `schema_migrations` table (`internal/storage/postgres/migrate.go`)
- TOCTOU-safe registration challenge consumption
- cross-org isolation enforced at the `FindUserByEmail` layer
- Ed25519 keypair flow, AES-GCM credential encryption, HMAC webhook verification
- email-OTP / invite-token / admin-approval verification modes
- `cmd/server` graceful shutdown on SIGINT/SIGTERM
- Dockerfile uses non-root user, alpine base, CGO disabled
- CI runs unit + e2e on both in-memory and PostgreSQL
- `make test-cover` enforces a 70 % testable-package coverage threshold
- audit NDJSON sink for SIEM ingestion
- admin UI ships with strict CSP, double-submit CSRF, `Secure`/`HttpOnly`/`SameSite=Strict` cookies, explicit CORS allow-list

The gaps below are about everything *around* that code: how it's deployed, observed, scaled, audited, and recovered.

---

## 1. observability

Today the server emits structured logs and writes audit events. There is no metrics endpoint, no per-request access log, no tracing, no request-id propagation. Operators have no way to answer "what is the p99 latency on `/v1/queries`?" or "which agent is responsible for this 500?".

### 1.1 Prometheus metrics endpoint

- add `/metrics` (unauthenticated, bound to a separate listener via `ALICE_METRICS_ADDR=:9090`, default disabled)
- expose Go runtime metrics, HTTP request/response counters and histograms (per route + status), DB pool stats (`sql.DBStats`), rate-limiter rejections, gatekeeper auto-answer counts, audit events emitted
- use `github.com/prometheus/client_golang` — already a stable, minimal dependency
- document scrape config in a new `docs/operations.md`

acceptance: `curl :9090/metrics` returns Prometheus exposition; new metrics show up in a fresh `go test` covering the middleware

### 1.2 Per-request access logging middleware

- wrap the router with a middleware that logs method, path, status, duration, bytes-out, agent ID (if authenticated), peer IP, request ID, at INFO
- log at WARN for 4xx auth failures and rate-limit rejections, ERROR for 5xx
- skip `/healthz` to keep logs clean

acceptance: one structured log line per request; existing tests still pass; new test asserts the line shape

### 1.3 Request ID + trace context propagation

- middleware that reads `X-Request-ID` (or generates one with `internal/id`) and injects it into the request context, response header, and every `slog` line emitted during the request
- for OpenTelemetry-ready callers, also read W3C `traceparent`/`tracestate` and bind to `context.Context` (don't ship an OTLP exporter yet — just preserve the trace context so it's available when 1.4 lands)

### 1.4 Optional OTLP tracing

- behind `ALICE_OTEL_EXPORTER_OTLP_ENDPOINT`, instrument the HTTP handlers, service-layer entry points, and storage calls with `go.opentelemetry.io/otel`
- defer until 1.1–1.3 are in place — they have higher leverage

### 1.5 Split `/healthz` into liveness and readiness

- keep `/healthz` for backwards compatibility (always 200 if the process is up)
- add `/livez` (process is alive) and `/readyz` (DB ping succeeds, optional sinks are writable)
- allows Kubernetes / load balancers to take a replica out of rotation while DB is unhealthy without killing it

acceptance: `/readyz` returns 503 when `db.PingContext` fails; CLI/MCP/edge unaffected

---

## 2. security hardening

The reviewed gaps from `docs/opus_review.md` have largely been closed, but there are production-specific items that didn't surface in a code-level review.

### 2.1 Rate limiting beyond registration

- the current `ipRateLimiter` only protects `/v1/agents/register{,/challenge}`; authenticated endpoints have no per-agent or per-org cap
- add a per-agent token bucket on the heavy paths (`POST /v1/queries`, `POST /v1/requests`, `POST /v1/artifacts`) with sensible defaults (e.g. 60/min) and `ALICE_RATE_LIMIT_*` overrides
- add a per-IP cap on the unauthenticated `/admin/sign-in*` endpoints (currently relies on email-OTP mass-mail throttling only)
- decide policy on `X-Forwarded-For`: today the limiter uses `req.RemoteAddr`, which means a single load-balancer IP shares one bucket across all clients. Add a `ALICE_TRUSTED_PROXIES` env (CIDR list) and only honour `X-Forwarded-For` when `req.RemoteAddr` is in the list, taking the right-most untrusted hop as the client

acceptance: tests that exceed the cap return 429 with `Retry-After`; documented behaviour for proxy deployments

### 2.2 HSTS + content-security-policy on JSON API

- add `Strict-Transport-Security: max-age=63072000; includeSubDomains` to all responses when `ALICE_TLS_TERMINATED=true` (or `X-Forwarded-Proto: https`)
- the admin UI already sets a strict CSP; the JSON API does not need one but should set `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'` so any accidental HTML rendering can't execute

### 2.3 Bootstrap-admin clarification

- "first registrant in an org is auto-approved and assigned the `admin` role" creates a land-grab window when org slugs are guessable
- options to consider with the user before coding:
  - require an explicit `ALICE_ADMIN_BOOTSTRAP_TOKEN` env on the server that the first registrant must present, OR
  - move org creation behind an admin-only endpoint and disallow auto-create, OR
  - keep the current behaviour but document the trust model and recommend pre-creating orgs out-of-band before exposing the server publicly
- this is a doc + small policy change, not a deep refactor

### 2.4 Dependency + container vulnerability scanning in CI

- add `govulncheck ./...` as a CI job (fast, official, low signal-to-noise)
- add `trivy image` (or `grype`) on the published container image once 4.1 lands
- enable Dependabot for `gomod` and `github-actions` (one PR adds `.github/dependabot.yml`)

### 2.5 Secret-rotation runbook

- document rotation procedure for: server bearer tokens (`ALICE_AUTH_TOKEN_TTL`), admin invite tokens (already supported via `rotate-invite-token`), SMTP credentials, AES-GCM credential keys (`ALICE_EDGE_CREDENTIAL_KEY`), webhook shared secrets, GitHub/Jira/GCal connector tokens
- short doc, no code changes — just a section in `docs/operations.md`

### 2.6 Audit-log tamper resistance (defer / open question)

- the NDJSON sink is owner-readable but unsigned. For deployments that treat audit log as system-of-record (auditor trust), consider hash-chaining each line (`prev_sha256`) or shipping every event to an append-only external sink (e.g. S3 Object Lock, CloudWatch).
- open question for the user: is local NDJSON + external ship-out via Filebeat/Vector enough, or do we want chain-of-custody built into the format? Default to "external sink is enough" until someone asks otherwise.

---

## 3. deployment + lifecycle

`compose.yml` is the only deployment artefact. Adopters running on Kubernetes, ECS, Nomad, or anything else have to reverse-engineer the env-var contract from the README.

### 3.1 Release workflow + container publishing

- add `.github/workflows/release.yml` triggered on tag `v*`:
  - build multi-arch container (`linux/amd64`, `linux/arm64`)
  - push to `ghcr.io/<org>/alice-server`, `…/alice-mcp-server`, `…/alice-edge-agent`
  - attach checksums + SBOM (Syft) + signature (Cosign keyless via OIDC)
- add a smaller `nightly.yml` that publishes `:nightly` tag from `main`

### 3.2 Reverse-proxy + TLS deployment guide

- the server speaks plain HTTP. Production deployments must front it with Caddy / nginx / Envoy / cloud LB.
- add a `docs/operations.md` section with a worked example for at least one reverse proxy (Caddy is fewest moving parts), covering: TLS certificate provisioning, `X-Forwarded-Proto`/`-For` headers, the `ALICE_TRUSTED_PROXIES` setting from 2.1, HSTS, and websocket considerations (none today, but future-proofing for `/admin/*`)

### 3.3 Kubernetes manifests (or Helm chart)

- ship a minimal `deploy/kubernetes/` directory with: `Deployment` (server, 2 replicas, anti-affinity), `Service`, `Ingress` (nginx + cert-manager annotations as comments), `ConfigMap` + `Secret` examples, `NetworkPolicy` (deny-all egress except DB + SMTP), `PodDisruptionBudget`, `HorizontalPodAutoscaler`
- skip Helm until someone asks — raw manifests cover the 80 % case and are easier to read

### 3.4 Multi-replica safety: migration locking

- `Migrate()` runs on every server start. With multiple replicas behind a rolling deployment, two pods will race the same migration.
- wrap the migration loop in a `pg_advisory_lock(<constant>)` so only one replica runs migrations at a time
- alternative: extract migrations into a separate `cmd/alice-migrate` binary and run it as a Kubernetes `Job` / init container; cheaper to reason about, but more deployment work for adopters

### 3.5 Graceful shutdown / drain documentation

- `cmd/server/main.go` already calls `Shutdown` with `cfg.ShutdownTimeout` (5 s default). Document this in `operations.md` along with the recommended Kubernetes settings: `terminationGracePeriodSeconds: 30`, no `preStop` hook needed, lifecycle expectations for in-flight requests.

### 3.6 Backup + restore documentation

- write a short `docs/operations.md` section: PostgreSQL `pg_dump --format=custom` schedule, what to exclude (the `audit_events` table can dominate), `pg_restore` procedure, point-in-time-recovery hints (WAL archiving), retention policy guidance, audit-log file rotation (`logrotate` example).
- no code change required.

### 3.7 Data-retention + GC

- nothing in the schema is cleaned up today. Long-running deployments will accumulate: expired registration challenges, expired bearer tokens, expired email-verification records, completed/expired requests, expired grants, audit events.
- decide policy with the user. Default proposal:
  - delete expired registration challenges + tokens nightly
  - delete `email_verifications` rows older than 24 h
  - keep audit events forever (they're cheap and the system-of-record); offer `ALICE_AUDIT_RETENTION` to opt into deletion if needed
  - keep completed requests / approvals indefinitely (they're part of the audit trail)
- implement as a single `cmd/alice-gc` job that can run on a cron, or as a goroutine inside `cmd/server` gated by `ALICE_GC_ENABLED=true`. Prefer the explicit job — one less thing to monitor inside the hot path.

### 3.8 Privacy / right-to-erasure

- the project markets itself as "privacy-first" but has no documented user / org deletion flow. For GDPR-relevant deployments this is a gap.
- add `DELETE /v1/users/me` (soft-delete: clear PII fields, revoke tokens, mark account `deleted`, retain audit-event references by ID only) and `DELETE /v1/orgs/:slug` (admin-only, similar treatment for the whole org).
- decide with the user whether soft-delete is enough or whether we need hard-delete with audit-event scrubbing.

---

## 4. test + quality

Coverage from `go test ./...` today:

| package | coverage |
|---|---|
| `internal/core` | 93.9 % |
| `internal/policy` | 92.9 % |
| `internal/websession` / `webui` | 87 % / 84 % |
| `internal/tracker` | 85.6 % |
| `internal/orggraph` / `gatekeeper` / `queries` | 81–84 % |
| `internal/requests` / `riskpolicy` / `storage/memory` / `id` | 75–79 % |
| `internal/cli` / `mcp` | 68–73 % |
| `internal/edge` / `httpapi` / `config` | 62–66 % |
| `internal/email` | **19.5 %** |
| `internal/storage/postgres` | **0 %** (covered indirectly via `make test-postgres`) |

The Makefile excludes `cmd/`, `internal/storage/postgres/`, and `internal/app/` from the 70 % threshold, which masks two real gaps.

### 4.1 Add unit tests for `internal/email`

- `SMTPSender` has no test coverage; mock-or-fake the `smtp.Client` and assert STARTTLS is requested, From/To/Subject are wired, OTP body contains the code
- raises confidence in the OTP path which is now load-bearing for the admin UI

### 4.2 Bring `internal/storage/postgres` into the threshold

- `make test-postgres` already exercises this; the issue is the `make test-cover` excludes it for the threshold check. Drop the exclusion and run coverage with PostgreSQL in CI so the percentage is honest.

### 4.3 Load-testing artefacts

- add `tests/load/` with a small `k6` or `vegeta` script that hammers the registration → publish → query path; document expected throughput on a single server pod
- not a CI gate — a tool for capacity planning

### 4.4 Race detector + `t.Parallel()` audit

- CI already runs with `-race`. The previous review noted no `t.Parallel()` calls anywhere; that's fine for correctness but slows the suite. Optional cleanup, not a blocker.

---

## 5. operational documentation

Today's docs (`README.md`, `technical-spec.md`, `threat-model.md`) are excellent for contributors and security reviewers. They are not enough for an SRE who has to run alice in production.

### 5.1 `docs/operations.md`

Single page covering: minimum production topology (server + Postgres + reverse proxy), required env vars, sizing guidance (rough CPU/memory per 1k agents), TLS setup (cross-link to 3.2), backup + restore (3.6), retention + GC (3.7), secret rotation (2.5), upgrade procedure, log + metric collection.

### 5.2 `docs/incident-response.md`

Short runbooks for:
- compromised bearer token / private key
- compromised admin invite token (use `rotate-invite-token`)
- DB corruption / restore from backup
- registration spam / abuse spike
- audit-log gap (sink failure)

### 5.3 SLO + capacity targets

State explicit availability and latency SLOs (e.g. 99.9 % availability, p95 query < 200 ms). Without them, observability work in section 1 has no targets.

---

## 6. small / deferred

Items worth recording but not worth blocking on:

- `Capabilities` field on `Agent` is stored at registration but never enforced (carry-over from `opus_review.md` 1.6). Either delete or wire into authorisation. Low impact today because all agents are equal.
- `internal/tracker/summariser.go` reserves `ALICE_TRACK_SUMMARISER=claude` but the LLM-backed summariser is not implemented. Land when a real use-case shows up.
- `cmd/mcp-server/main.go` reads `ALICE_MCP_ACCESS_TOKEN` directly via `os.Getenv` instead of going through `config.FromEnv()`. Cosmetic.
- the `X-Agent-Token` header is accepted as an undocumented `Authorization: Bearer` alternative. Pick one and remove the other (probably remove `X-Agent-Token`).

---

## suggested execution order

If picking off this plan one chunk at a time, this is roughly how a session would batch it:

1. **observability (1.1, 1.2, 1.3, 1.5)** — without metrics + access logs, every later operational decision is a guess. Two to three commits, mostly middleware.
2. **rate limiting + HSTS (2.1, 2.2)** — small, finishes the security middleware story.
3. **release workflow + container publishing (3.1)** — unblocks anyone who wants to deploy without `git clone` + `go build`.
4. **operations doc (5.1) + reverse-proxy guide (3.2) + secret rotation (2.5) + backup/restore (3.6)** — single doc PR; touches no code.
5. **migration locking (3.4)** — one-line `pg_advisory_lock` plus a test; unblocks multi-replica deployments.
6. **dependency + vuln scanning (2.4)** — three CI jobs.
7. **email + postgres test coverage (4.1, 4.2)** — pulls the testable-package average up honestly.
8. **k8s manifests (3.3) + GC job (3.7)** — once the above are green, deploy-to-prod-shaped users have everything they need.
9. **bootstrap-admin clarification (2.3) + privacy / erasure (3.8)** — design discussion with the user before implementation.
10. **incident-response + SLO docs (5.2, 5.3)** — close out the doc surface.

## acceptance criteria (whole plan)

- `/metrics`, `/livez`, `/readyz` all live and documented
- per-route HTTP and DB metrics visible in Prometheus
- per-request access log line emitted with request ID
- per-agent rate limits enforced with documented overrides
- HSTS emitted when TLS-terminated; documented `ALICE_TRUSTED_PROXIES` semantics
- container images published on tag with SBOM + signature
- `docs/operations.md` and `docs/incident-response.md` exist and cover deployment, backup, rotation, incident handling
- minimal Kubernetes manifests under `deploy/kubernetes/` boot a working server + Postgres + ingress
- `pg_advisory_lock` protects multi-replica migrations
- `govulncheck` and Dependabot run in CI; container scan runs on release
- `internal/email` and `internal/storage/postgres` both contribute to the coverage threshold; threshold raised to 75 % once they're honest
- documented user / org deletion flow (or explicit decision that current behaviour is the deliberate end state)
