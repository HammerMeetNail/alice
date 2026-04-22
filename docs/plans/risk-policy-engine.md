# advanced risk policy engine

## goal

Replace the current fixed `RiskLevel` field + threshold check with an evaluable policy engine so orgs can express rules like "any request touching a customer record requires approval" or "actions on weekends require explicit re-auth" without a code change.

## why

Today risk is a simple `low | medium | high | critical` ladder compared against `Grant.RequiresApprovalAboveRisk`. It's fine for a prototype but not expressive enough for real customers — risk is multi-dimensional (who, what, when, data class, caller location). A tiny policy language lets admins codify rules, reviewable in audit, without shipping code.

## constraints

- policy evaluation is deterministic and side-effect free
- evaluation is fast enough for the synchronous request path (sub-millisecond typical)
- policies are versioned: a request / action records the `policy_version_id` it was evaluated against, so audits stay reconstructible
- default policy is the current ladder — existing deployments keep working without changes
- policy errors fail closed: if a policy is malformed or times out, the request is denied

## shape

- policy is a named, versioned document per org. Schema candidates, in order of preference:
  1. small custom JSON rule language — easiest to validate, slowest to evolve
  2. CEL (common expression language) — mature, fast, Go library exists, eval-only
  3. OPA / Rego — most expressive, heaviest dependency
  
  CEL is the recommended starting point: expressive enough, fast, safe, and stays embedded
- domain types: `Policy` (ID, OrgID, Name, Version, Source, CreatedAt, CreatedBy, ActiveAt), `PolicyEvaluation` (PolicyID, Version, Inputs, Decision, Reasons)
- evaluator is a new package `internal/riskpolicy/` with `Evaluator.Decide(ctx, inputs) (Decision, error)`
- `queries.Service` and (later) the operator phase both call the evaluator; they no longer short-circuit on `Grant.RequiresApprovalAboveRisk` alone
- HTTP + CLI: `alice org policy apply policy.cel`, `alice org policy history`
- audit event `policy.evaluated` records the decision for every request above the evaluation threshold (don't log every hello-world query — cardinality blows up)

## acceptance criteria

- default policy produces the same outcomes as today's ladder on the existing test corpus
- custom policy example: "deny any query with purpose=status_check where recipient has `sensitivity=critical` artifacts" evaluates correctly against fixtures
- malformed policy fails validation at apply time, not at evaluation time
- policy change is audit-logged with diff; admins can roll back to a prior version
- per-query evaluation latency stays under 1ms p99 for a policy with ~50 rules

## open questions to settle with the user before building

- CEL vs. a domestic mini-language — CEL adds a dependency. Worth it?
- policy granularity: one policy per org, or one per `{ kind, risk_class }` pair?
- how do we bootstrap the first policy: `alice org policy apply` against what default template?

## out of scope

- UI for editing policies (admin-ui.md plan covers this later)
- ML-derived policy suggestions
- cross-org policy sharing

## dependencies

- operator-phase.md — shares the evaluator. Build this plan first, then the operator phase can reuse it directly.
