# operator phase: safe execution of approved low-risk actions

## goal

Let a user's agent execute approved low-risk actions on the user's behalf (accepting a meeting, acknowledging a blocker, replying to a status check) rather than only surfacing a suggestion for the human to act on.

## why

Today the coordination server is Reporter-only: it answers questions but never acts. The spec describes an "operator phase" where approved actions execute. Without this, every `ask_for_time` request still needs a human hand-off. This is the biggest UX unlock remaining.

## why this is risky

Actions are side-effectful and often irreversible:

- accepting a meeting writes to the user's calendar
- replying to a peer leaks content
- a compromised agent can now do real damage instead of just leaking reads

The whole plan is about designing enough rails that auto-execution stays safe even when the model is confused or adversarial.

## constraints

- actions are execute-once: every action gets a unique `action_id`; replays must no-op
- every action goes through a risk classification (reuse the risk-policy engine; see `risk-policy-engine.md`)
- actions above the user's configured threshold always require explicit human approval; the operator phase only runs actions below the threshold
- every executed action is audit-logged with the request that authorised it, the approval if any, and the resulting effect (calendar event ID, reply message ID, …)
- action executors run in the user's trust domain, not on the coordination server. The server authorises; the edge agent (or MCP tracker process) executes and reports back
- actions that fail must leave the request in a recoverable state (not silently swallow the failure)

## shape

- new `Action` domain type: `ActionID`, `RequestID`, `Kind` (`accept_meeting`, `decline_meeting`, `acknowledge_blocker`, `send_reply`), `Inputs map[string]any`, `RiskLevel`, `State` (`pending`, `approved`, `executing`, `executed`, `failed`, `cancelled`), `Result map[string]any`, `ExecutedAt *time.Time`
- new repository + service + routes (`/v1/actions`, `/v1/actions/:id/execute`, …)
- action kinds are pluggable: `internal/actions/` has one file per kind with a tight interface
  ```go
  type Executor interface {
      Kind() ActionKind
      Validate(inputs map[string]any) error
      Execute(ctx context.Context, user core.User, inputs map[string]any) (map[string]any, error)
  }
  ```
- gatekeeper extension: when a request is auto-answered AND the grant + action-kind risk both permit, the gatekeeper enqueues an action instead of (or in addition to) the response message
- execution happens on the edge agent / MCP tracker. Those processes poll `/v1/actions?state=approved&assignee=me` and call the matching executor locally. Results post back via `POST /v1/actions/:id/complete`
- opt-in per user: `UserPreferences.OperatorEnabled bool` default false; per-kind opt-in granularity is preferable to a single master switch

## acceptance criteria

- enabling the operator phase is per-user; default off
- action replay via the same `action_id` is a no-op (idempotency test)
- a failed executor leaves the action in `failed` state with an error message, and the original request remains actionable by the human
- every action produces an audit event with kind `action.executed` or `action.failed`; no action ever bypasses audit
- sample executor (`accept_meeting`) has fixture-driven tests against a mocked Calendar API

## open questions to settle with the user before building

- do approvals still fan in from multiple users, or only from the action owner?
- what happens if the user is offline when an approved action is ready to execute? Retry? Expire after N hours?
- should the first action kind be something low-stakes (`acknowledge_blocker`, writes nothing externally) to prove the machinery before touching calendar or email?

## out of scope

- new action kinds beyond the first three (`accept_meeting`, `acknowledge_blocker`, `send_reply`)
- multi-step workflows (one action at a time)
- rollback / undo — executed actions stay executed; the audit trail is the recovery mechanism

## dependencies

- risk policy engine (LANDED): reuse `internal/riskpolicy.Service.Evaluate` with Inputs populated for the action kind; don't duplicate classification
- per-org gatekeeper tuning (LANDED): same "per-org override" pattern applies to per-kind risk thresholds when we need them
