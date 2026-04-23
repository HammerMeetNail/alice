-- 010_org_graph.sql
-- Org graph: teams, team memberships, and the append-only manager
-- reporting graph that drive the `team_scope` and `manager_scope`
-- visibility modes. Cross-org edges are not permitted — every edge
-- originates and terminates within a single organization.

CREATE TABLE IF NOT EXISTS teams (
    team_id        TEXT PRIMARY KEY,
    org_id         TEXT NOT NULL REFERENCES organizations(org_id),
    name           TEXT NOT NULL,
    parent_team_id TEXT REFERENCES teams(team_id),
    created_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_teams_org_created_at
    ON teams (org_id, created_at);

CREATE TABLE IF NOT EXISTS team_members (
    team_id   TEXT NOT NULL REFERENCES teams(team_id) ON DELETE CASCADE,
    user_id   TEXT NOT NULL REFERENCES users(user_id),
    role      TEXT NOT NULL CHECK (role IN ('member', 'lead')),
    joined_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (team_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_team_members_user_team
    ON team_members (user_id, team_id);

-- Append-only manager graph. The current manager for a user is the most
-- recent row with revoked_at IS NULL. SaveManagerEdge must revoke any
-- prior active edge for the same user atomically before inserting the new
-- row; the partial unique index below enforces the "at most one active
-- edge per user" invariant at the DB level.
CREATE TABLE IF NOT EXISTS manager_edges (
    edge_id         TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(user_id),
    manager_user_id TEXT NOT NULL REFERENCES users(user_id),
    effective_at    TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    CHECK (user_id <> manager_user_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_manager_edges_active_user
    ON manager_edges (user_id) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_manager_edges_user_effective
    ON manager_edges (user_id, effective_at);
