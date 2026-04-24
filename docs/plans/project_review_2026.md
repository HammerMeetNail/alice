# project review 2026

## status

This document reflects a verification pass after the follow-up fixes completed in this session.

Current state:

- the previously identified high-severity implementation issues are closed
- most medium-severity usability and trust issues from the review are also closed
- the remaining work is now small and mostly polish-level: one partial framing inconsistency and one shell-completion mismatch

## verified fixes

### verified: invite-token rotation is admin-only

Evidence:

- `internal/agents/service.go`: `RotateInviteToken` now requires the caller's owner user to be an org admin
- tests exist at service, HTTP, and MCP layers:
  - `internal/agents/service_test.go: TestRotateInviteToken_NonAdminDenied`
  - `internal/httpapi/router_test.go: TestRotateInviteToken_NonAdminForbidden`
  - `internal/mcp/server_extended_test.go: TestRotateInviteTokenMCP_NonAdminDenied`

### verified: `alice respond` enum/help drift is fixed

Evidence:

- `internal/cli/commands.go`: `cmdRespond` now defaults to `accepted`
- `mapResponseAlias()` maps friendly aliases such as `accept`, `defer`, and `decline` to canonical server values

### verified: CLI project scoping uses `project_refs`

Evidence:

- `internal/cli/commands.go`: `publish --project` now emits `structured_payload.project_refs`
- `internal/queries/service.go` reads `project_refs`, so CLI and query evaluation now match

### verified: approval-gated queries now persist `pending_approval`

Evidence:

- `internal/queries/service.go`: the approval branch now calls `UpdateQueryState(..., QueryStatePendingApproval)`
- `internal/httpapi/router_test.go: TestQueryApprovalPendingStatus` now checks both:
  - initial `POST /v1/queries` returns `pending_approval`
  - follow-up `GET /v1/queries/:id` also returns persisted `pending_approval`

Why it matters:

- the earlier API inconsistency between initial POST and later GET is closed
- `alice query --wait` will now observe the real state rather than stale `queued`

### verified: global CLI flags work before or after the subcommand

Evidence:

- `internal/cli/commands.go`: `splitGlobalArgs()` now accepts global flags in either position

### verified: tracker and edge-agent preview flows exist

Evidence:

- `internal/tracker/tracker.go`: `ALICE_TRACK_DRY_RUN=true` logs what would be published without sending it
- `cmd/edge-agent/main.go`: `-dry-run` calls `PreviewArtifacts()`
- `internal/edge/runtime.go`: `PreviewArtifacts()` collects artifacts without register/publish side effects

### verified: admin UI trusts forwarded HTTPS only from trusted proxies

Evidence:

- `internal/webui/handler.go`: `enforceHTTPS()` now gates `X-Forwarded-Proto: https` through `isTrustedProxy()`
- `internal/webui/webui_test.go` covers both trusted and untrusted proxy behavior

### verified: edge state is encrypted by default

Evidence:

- `internal/edge/state.go`: `SaveStateWithOptions()` now errors unless encryption is configured or plaintext is explicitly opted into
- `internal/edge/config.go`: `runtime.allow_plaintext_state` exists as the insecure opt-in

### verified: README now reflects the major behavior changes

Evidence:

- `README.md:96` now correctly states that `whoami` never emits the private key or bearer token, even in JSON
- `README.md:393` now documents `ALICE_TRACK_DRY_RUN` inline and no longer says the tracker "silently monitors"
- `README.md:963` adds an `ALICE_TRACK_DRY_RUN` env-var row
- `README.md:976` documents edge-agent `-dry-run`
- `README.md:1011` documents `allow_plaintext_state` as an insecure opt-in
- `README.md:231` now lists `manager set|revoke|chain`

### verified: admin pending-agent UI now shows useful context

Evidence:

- `internal/core/types.go`: `core.AgentApproval` now includes `AgentName`, `OwnerEmail`, and `ClientType`
- `internal/agents/service.go`: `ListPendingAgentApprovals()` enriches approvals with agent/user context
- `internal/webui/templates/pending.html` now displays agent name, owner email, client type, and requested time
- `internal/webui/static/css/app.css` adds `code.small` styling for the subdued agent ID label
- `internal/agents/service_test.go: TestListPendingAgentApprovals_EnrichedContext`

## remaining findings

### 1. low: untrusted-data framing is still not fully consistent across all CLI list commands

What is still open:

- several important list surfaces were updated to `untrusted=true`, including peers, audit events, actions list, approvals, and risk policy history
- but the org-graph list commands still render with `untrusted=false`

Evidence:

- fixed:
  - `internal/cli/commands.go`: `cmdPeers`, `cmdAudit`, `cmdListApprovals`, `cmdActions(list)`, `cmdPolicyHistory`
- still not aligned:
  - `internal/cli/orggraph.go:61` team list
  - `internal/cli/orggraph.go:129` team members
  - `internal/cli/orggraph.go:209` manager chain

Why it matters:

- the repo guidance and README frame list output broadly as untrusted data
- the implementation is now much closer to that promise, but not fully consistent yet

Recommended fix:

- switch the remaining `EmitList(..., false)` org-graph calls to `true`, or narrow the docs to exclude those commands explicitly

### 2. low: bash completion still has one stale request flag

What is still open:

- the bash completion entry for `request` still advertises `--expires`
- the actual CLI flag is `--expires-in`

Evidence:

- `internal/cli/commands.go:1520`
- actual request flag definition is `--expires-in` in `cmdSendRequest`

Why it matters:

- this is small, but it is exactly the kind of drift that causes avoidable user confusion

Recommended fix:

- update the bash completion entry for `request` to `--expires-in`

## product assessment

The project is now in a much better place relative to the stated goal.

Why:

- the product has moved further from surveillance-by-default and closer to explicit, inspectable assistance
- preview/dry-run support now exists on both tracker and edge-agent paths
- the docs now describe secret handling and preview behavior much more honestly
- admin review now has enough context to be usable in practice

The remaining gaps are no longer architectural or trust-breaking. They are polish issues.

## verification notes

Focused verification during this pass:

- `go test ./internal/httpapi -run TestQueryApprovalPendingStatus`
- `go test ./internal/agents -run TestListPendingAgentApprovals_EnrichedContext`
- `go test ./internal/cli -run 'TestCLIEndToEnd|TestCompletionCommand|TestCompletionSubcommandsInSync'`

Additional verification by direct code inspection:

- README updates for `whoami`, tracker dry-run, edge-agent dry-run, and `allow_plaintext_state`
- persisted `pending_approval` query state
- admin pending-agent UI enrichment
- current `EmitList(..., false)` call sites in CLI org-graph commands
- remaining bash completion mismatch for `request`

## current summary

Nearly all findings from the earlier project review are now complete.

Closed:

- invite-token authorization
- CLI response alias drift
- CLI project scoping payload shape
- approval-gated query state persistence
- global flag placement
- tracker and edge-agent preview support
- admin UI trusted-proxy handling
- edge-state encryption default
- README lag on the major reviewed items
- admin approval UI context

Still open:

- untrusted-data framing is not yet applied to all org-graph list commands
- bash completion for `request` still advertises `--expires` instead of `--expires-in`
