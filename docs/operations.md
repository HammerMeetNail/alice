# Operations guide

## Minimum production topology

alice requires three components:

- **alice-server** — the stateless coordination server
- **PostgreSQL 14+** — all persistent state (agents, artifacts, grants, audit events)
- **Reverse proxy** — TLS termination (Caddy, nginx, Envoy, or a cloud load balancer)

The MCP server (`alice-mcp-server`) and edge agent (`alice-edge-agent`) are optional
per-user processes; they communicate with alice-server over HTTP(S) and carry no state
of their own.

## Required environment variables

| Variable | Default | Description |
|---|---|---|
| `ALICE_DATABASE_URL` | — | PostgreSQL DSN, e.g. `postgres://alice:secret@db:5432/alice?sslmode=require` |
| `ALICE_LISTEN_ADDR` | `:8080` | HTTP bind address for the coordination server |
| `ALICE_AUTH_TOKEN_TTL` | `15m` | Bearer token lifetime; extend for long-lived automation (e.g. `24h`) |
| `ALICE_AUTH_CHALLENGE_TTL` | `5m` | Registration challenge window |
| `ALICE_AUDIT_LOG_FILE` | — | Path to NDJSON audit log file; leave unset to write audit events to DB only |
| `ALICE_METRICS_ADDR` | — | Prometheus metrics listener, e.g. `:9090`; empty disables the endpoint |
| `ALICE_TLS_TERMINATED` | `false` | Set `true` when behind a TLS-terminating proxy; enables HSTS on all responses |
| `ALICE_TRUSTED_PROXIES` | — | Comma-separated CIDR list of trusted load-balancer IPs (see "Trusted proxies" below) |
| `ALICE_SMTP_HOST` | — | SMTP server hostname; `noop` logs OTP codes to stderr instead of sending email; empty disables email OTP |
| `ALICE_SMTP_PORT` | `587` | SMTP port |
| `ALICE_SMTP_TLS` | `true` | Use STARTTLS |
| `ALICE_SMTP_USERNAME` | — | SMTP username |
| `ALICE_SMTP_PASSWORD` | — | SMTP password |
| `ALICE_SMTP_FROM` | — | Envelope From address |
| `ALICE_ADMIN_UI_ENABLED` | `false` | Mount the browser admin UI at `/admin/*` |
| `ALICE_ADMIN_UI_ALLOWED_ORIGINS` | — | Comma-separated CORS allow-list; empty = same-origin only |
| `ALICE_ADMIN_UI_DEV_MODE` | `false` | Disable HTTPS guard and Secure cookie attribute; **never set in production** |
| `ALICE_RATE_LIMIT_AGENT_PER_MIN` | `60` | Per-agent token-bucket rate on heavy endpoints |
| `ALICE_RATE_LIMIT_ADMIN_SIGNIN_PER_MIN` | `10` | Per-IP rate on admin UI sign-in |
| `ALICE_GC_ENABLED` | — | Not yet implemented; use `cmd/alice-gc` on a cron instead |

## Sizing guidance

Single alice-server pod (commodity cloud instance):

| Metric | Idle | 1 k concurrent agents |
|---|---|---|
| CPU | < 0.05 vCPU | ~0.5 vCPU |
| Memory | ~64 MiB | ~200 MiB |
| Postgres connections | 5 (pool default) | 20–30 |

PostgreSQL storage growth (rough estimates):

- ~1 KB per agent registration row
- ~10 KB per artifact (content stored in-column)
- ~200 bytes per audit event
- Audit events accumulate indefinitely by design; use `cmd/alice-gc` or `ALICE_AUDIT_RETENTION` to prune if storage is constrained

## TLS setup

alice speaks plain HTTP. Terminate TLS at the reverse proxy and set
`ALICE_TLS_TERMINATED=true` so the server emits
`Strict-Transport-Security: max-age=63072000; includeSubDomains` on every response.

### Caddy (recommended — automatic certificate management)

```caddyfile
alice.example.com {
    reverse_proxy localhost:8080 {
        header_up X-Forwarded-Proto https
        header_up X-Forwarded-For {remote_host}
    }
}
```

Set in alice-server:

```
ALICE_TLS_TERMINATED=true
ALICE_TRUSTED_PROXIES=127.0.0.1/32
```

### nginx

```nginx
server {
    listen 443 ssl http2;
    server_name alice.example.com;

    ssl_certificate     /etc/letsencrypt/live/alice.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/alice.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header Host              $host;
    }
}
```

## Trusted proxies

Rate limits extract the client IP from `req.RemoteAddr` by default. When the server
runs behind a load balancer, every request arrives from a single proxy IP, so all
clients share one rate-limit bucket.

Set `ALICE_TRUSTED_PROXIES` to the CIDR range(s) of your load balancer(s). When
`RemoteAddr` is in that list, alice uses the **rightmost untrusted hop** in
`X-Forwarded-For` as the effective client IP.

```
ALICE_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12
```

Never list `0.0.0.0/0` — that would allow any client to spoof its IP.

## Bootstrap-admin trust model

The first agent to register in a new org slug is automatically assigned the `admin`
role and set to `active` status, regardless of the org's verification mode.

**Trust model:** org slugs are effectively a shared secret during the registration
window. If your server is publicly reachable and someone guesses the slug before your
intended admin registers, they receive the admin role.

Mitigations (choose one or combine):

1. **Pre-create the org via a migration** — insert the organization row before
   exposing the server. The first `BeginRegistration` call for that slug still creates
   the admin, but the slug can be a long random value.
2. **Use `admin_approval` verification mode** — even the first registrant enters a
   `pending_admin_approval` queue; approval requires out-of-band confirmation.
   Enable with: `POST /v1/orgs/{slug}/verification-mode {"mode":"admin_approval"}`.
3. **Network isolation** — restrict registration endpoints to your internal network
   during initial setup using a firewall or `ALICE_TRUSTED_PROXIES` + an allowlist at
   the reverse proxy layer.

The recommended posture for production: configure the org slug as a long random value
(e.g. `company-f8a3c12d`) and document it only in your internal runbook.

## Graceful shutdown / drain

alice-server handles `SIGINT` and `SIGTERM` by calling `http.Server.Shutdown` with a
5-second drain window (configurable via `ALICE_SHUTDOWN_TIMEOUT`). In-flight requests
complete normally; new connections are refused immediately.

Kubernetes recommended settings:

```yaml
terminationGracePeriodSeconds: 30
```

No `preStop` hook is required. The 30-second `terminationGracePeriodSeconds` gives
Kubernetes time to route traffic away and allows the 5-second drain to complete
comfortably.

## Upgrade procedure

alice uses a sequential migration system (`internal/storage/postgres/migrate.go`).
Migrations run on startup and are protected by a `pg_advisory_lock` so only one
replica executes them during a rolling deployment.

1. Deploy the new image with a rolling update strategy (`maxUnavailable: 0`,
   `maxSurge: 1`).
2. The first new pod acquires the advisory lock, runs any new migrations, and releases
   the lock.
3. Remaining pods start normally once the lock is free; no migration work is repeated.
4. Roll back by redeploying the previous image tag. **Migrations are forward-only**;
   if a migration must be reverted, write a new migration that undoes the change.

## Metrics and Prometheus

Start the metrics listener by setting `ALICE_METRICS_ADDR`:

```
ALICE_METRICS_ADDR=:9090
```

The endpoint is **unauthenticated**. Bind it to a non-public interface or protect it
with a network policy. It must not be exposed behind the same virtual host as the API.

Prometheus `scrape_config` example:

```yaml
scrape_configs:
  - job_name: alice
    static_configs:
      - targets: ['alice-server:9090']
```

Key metrics:

| Metric | Type | Description |
|---|---|---|
| `alice_http_requests_total` | counter | Requests by method, route, status |
| `alice_http_request_duration_seconds` | histogram | Latency by method, route |
| `alice_http_response_size_bytes` | histogram | Response body size |
| `alice_rate_limit_rejections_total` | counter | 429s by limiter name |
| `alice_gatekeeper_auto_answers_total` | counter | Requests auto-answered by gatekeeper |
| `alice_audit_events_total` | counter | Audit events by kind |
| `alice_db_pool_open_connections` | gauge | Open DB connections (in-use + idle) |
| `alice_db_pool_in_use_connections` | gauge | DB connections currently in use |
| `alice_db_pool_wait_total` | counter | Times a new connection was waited for |

## Log collection

alice writes structured JSON logs via `log/slog` to `stdout`. In Kubernetes, ship them
to your aggregation stack with Fluent Bit or Vector:

```yaml
# Fluent Bit filter — parse alice JSON logs
[FILTER]
    Name   parser
    Match  kube.alice-*
    Parser json
    Key_Name log
```

Audit events are also written to `ALICE_AUDIT_LOG_FILE` (NDJSON) if set. Rotate this
file with `logrotate`:

```
/var/log/alice/audit.log {
    daily
    rotate 90
    compress
    missingok
    notifempty
    postrotate
        # alice re-opens the file on next write after rotation
    endscript
}
```

## Backup and restore

### Backup

```bash
# Full backup (recommended for restores)
pg_dump --format=custom --file=alice-$(date +%Y%m%d).dump $ALICE_DATABASE_URL

# Exclude audit_events if the table is large and covered by a separate SIEM
pg_dump --format=custom \
        --exclude-table=audit_events \
        --file=alice-no-audit-$(date +%Y%m%d).dump \
        $ALICE_DATABASE_URL
```

Schedule with cron or a Kubernetes `CronJob`. Daily backups retained for 30 days is a
reasonable starting point.

### Point-in-time recovery

Enable WAL archiving on your PostgreSQL instance for PITR. With managed services
(RDS, Cloud SQL, Supabase), PITR is available out of the box. Set a retention window
(e.g. 7 days) to limit storage cost.

### Restore

```bash
# Stop all alice-server replicas first to avoid split-brain.

# Drop and re-create the database
psql -c "DROP DATABASE alice" $PG_DSN
psql -c "CREATE DATABASE alice" $PG_DSN

# Restore
pg_restore --dbname=alice --no-owner --no-privileges alice-YYYYMMDD.dump

# Restart alice-server — migrations are idempotent and safe to re-run
```

## Data retention and GC

alice accumulates rows that are safe to prune over time:

| Table | Retention policy |
|---|---|
| `agent_registration_challenges` | Delete where `expires_at < now` |
| `agent_tokens` | Delete where `expires_at < now` or `revoked_at IS NOT NULL` |
| `email_verifications` | Delete where `expires_at < now` |
| `audit_events` | Keep forever by default; set `ALICE_AUDIT_RETENTION` (e.g. `90d`) to opt into deletion |

Run `cmd/alice-gc` on a nightly cron or Kubernetes `CronJob`:

```bash
ALICE_DATABASE_URL=postgres://... /app/alice-gc
```

Optional: `ALICE_AUDIT_RETENTION=90d` deletes audit events older than 90 days.

## Secret rotation

### Bearer tokens (`ALICE_AUTH_TOKEN_TTL`)

Tokens are short-lived (default 15 m). To invalidate all active tokens immediately:

1. Hard-delete rows from `agent_tokens` where `expires_at > now() AND revoked_at IS NULL` directly in the database (emergency-only; no dedicated HTTP endpoint exists for bulk revocation).

### Admin invite token

```bash
# Via HTTP (no dedicated alice CLI subcommand):
curl -X POST https://<server>/v1/orgs/rotate-invite-token \
  -H "Authorization: Bearer <admin-token>"
# or via MCP: rotate_invite_token
```

The old token is invalidated immediately. Distribute the new token to the next
registrant out-of-band.

### SMTP credentials

1. Update credentials at your SMTP provider.
2. Set the new `ALICE_SMTP_USERNAME` / `ALICE_SMTP_PASSWORD` in your deployment
   secret and roll the pods.
3. Confirm OTP delivery with a test registration.

### AES-GCM credential key (`ALICE_EDGE_CREDENTIAL_KEY`)

Used by the edge agent to encrypt connector OAuth tokens at rest.

1. Generate a new 32-byte key: `openssl rand -hex 32`.
2. Re-encrypt stored credentials: decrypt with the old key, re-encrypt with the new
   key, write back. There is no built-in rotation CLI today — add a migration or do
   this manually before rotating the key in the environment.
3. Roll the edge-agent pods.

### Webhook shared secrets

Update the secret at the provider (GitHub, Jira, Google Calendar) and in the
edge-agent environment simultaneously. There is a brief window where webhook deliveries
may be rejected; providers retry, so no events are lost.

### GitHub / Jira / GCal connector tokens

OAuth access tokens are refreshed automatically by the edge agent using stored refresh
tokens. To force re-authentication, delete the stored credentials file and re-run
`alice-edge-agent -bootstrap-connector`.
