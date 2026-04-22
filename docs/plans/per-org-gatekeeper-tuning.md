# per-org gatekeeper tuning

## goal

Let each org override the gatekeeper's confidence threshold and lookback window, so a cautious org can raise the bar without affecting a permissive tenant on the same coordination server.

## why

Server-wide env vars (`ALICE_GATEKEEPER_CONFIDENCE_THRESHOLD`, `ALICE_GATEKEEPER_LOOKBACK_WINDOW`) are already wired and cover the single-tenant case. Multi-tenant deployments need per-org values. An admin should be able to say "our org never auto-answers below 0.8 confidence" independently of what the server operator picked.

## constraints

- compile-time defaults (0.6 / 14d) remain the final fallback; env overrides remain the middle layer; per-org overrides are the narrowest layer
- org admins only — regular agents cannot change these values
- absent per-org value → fall through to env → fall through to compile-time default
- migration must tolerate existing rows (add nullable columns or sensible defaults)

## shape

- `core.Organization` gains `GatekeeperConfidenceThreshold *float64` and `GatekeeperLookbackWindow *time.Duration` (pointer so "unset" is distinguishable from "zero")
- migration adds two nullable columns on `organizations`
- `storage.OrganizationRepository` gains `UpdateGatekeeperTuning(ctx, orgID, threshold *float64, window *time.Duration)`
- `gatekeeper.Service.Evaluate` pulls the org from the repo on the request path (or the caller passes it in via options) and merges org → env → default
- new HTTP route `POST /v1/orgs/gatekeeper-tuning` (admin-only, like `rotate-invite-token`)
- new CLI subcommand `alice org tuning --confidence 0.8 --lookback 720h` (or fold into an existing admin command if one exists)
- new MCP tool `set_gatekeeper_tuning` for parity

## acceptance criteria

- migration runs cleanly on a database that already has orgs
- admin can set, clear, and read the per-org values via CLI + HTTP
- gatekeeper tests cover: no override → env used; env unset → default used; per-org set → used regardless of env; clamping on out-of-range values
- non-admin call returns 403
- audit event recorded on each tuning change

## out of scope

- per-user overrides
- live-reloading existing in-flight evaluations when tuning changes
- UI for editing tuning (handled by the admin-ui plan)
