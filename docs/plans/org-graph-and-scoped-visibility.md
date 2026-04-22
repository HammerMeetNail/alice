# org graph + scoped visibility modes

## goal

Implement the `team_scope` and `manager_scope` visibility modes from the technical spec, which requires a rich org graph that knows team membership and manager relationships.

## why

Today only `explicit_grants_only` and `public` are meaningful visibility modes. The spec defines `team_scope` ("visible to my team") and `manager_scope` ("visible to my manager chain") but they pass through without enforcement because there's no graph to evaluate against. Shipping these unlocks most of the teammate-UX the spec promised: a status artifact can be visible to your team automatically without every grant being manual.

## constraints

- deny-by-default. If the graph can't prove the viewer is in the right team / chain, access is denied, not allowed
- cross-org isolation still holds: team/manager edges only connect users within the same org
- graph edits are audit-logged like permission changes
- org admins own the graph; normal users cannot add themselves to a team or assign themselves a manager

## shape

- new tables:
  - `teams(team_id, org_id, name, parent_team_id nullable, created_at)`
  - `team_members(team_id, user_id, role, joined_at)` — role: `member` / `lead`
  - `manager_edges(user_id, manager_user_id, effective_at, revoked_at nullable)` — append-only; current manager = most recent row with `revoked_at IS NULL`
- new domain types in `internal/core/`: `Team`, `TeamMember`, `ManagerEdge`
- new repository: `storage.OrgGraphRepository` with the obvious reads (`FindTeamsForUser`, `FindManagerChain`, `FindTeamMembers`) and writes
- new service: `internal/orggraph/` with `Service.AddTeamMember`, `AssignManager`, etc. — all admin-gated
- policy evaluation changes: `queries.Service` and `artifacts.Service` both consult `OrgGraphRepository` when a grant or artifact is scoped by team/manager
- new HTTP routes under `/v1/org/teams`, `/v1/org/manager-edges`
- new MCP tools + CLI subcommands (`alice team create`, `alice team add-member`, `alice manager set`)
- for `manager_scope`, the spec says "viewer is in the owner's manager chain" — a transitive climb from owner upward; cap at reasonable depth (10) to avoid cycles

## acceptance criteria

- creating a team, adding members, and querying a team-scoped artifact all work via CLI and HTTP
- non-admin cannot modify the graph (403 on every write)
- cycle detection: attempting to create a manager-edge that would form a cycle returns an error at the service layer
- a user removed from a team immediately loses access to team-scoped artifacts (no cache-staleness window beyond the normal request)
- migration is idempotent and safe to run on an existing org with zero teams
- visibility-mode enforcement tests cover: viewer in team → allow; viewer not in team → deny; viewer in parent team → allow (if we decide parent teams inherit); viewer is manager of owner → allow; viewer is peer → deny

## open questions to settle with the user before building

- do parent teams inherit access to child teams' artifacts, or is each team a sealed scope?
- is the manager chain transitive upward only, or does a manager see peers' artifacts too?
- should team membership be time-bounded (like grants) or point-in-time?

## out of scope

- cross-org teams
- dotted-line / matrix management (single manager per edge row; multiple edges are allowed but the semantics are TBD)
- auto-discovery from HRIS (Workday, BambooHR) — future plan
