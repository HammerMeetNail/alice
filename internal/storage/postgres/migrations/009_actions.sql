-- 009_actions.sql
-- Operator-phase actions: approved work the user's agent may execute on
-- the user's behalf. Each action points at the request that authorised it
-- and records its own lifecycle state so replays, failures, and expiries
-- are audit-visible.
CREATE TABLE IF NOT EXISTS actions (
    action_id      TEXT PRIMARY KEY,
    org_id         TEXT NOT NULL REFERENCES organizations(org_id),
    request_id     TEXT REFERENCES requests(request_id),
    owner_user_id  TEXT NOT NULL REFERENCES users(user_id),
    owner_agent_id TEXT NOT NULL REFERENCES agents(agent_id),
    kind           TEXT NOT NULL,
    inputs         JSONB NOT NULL DEFAULT '{}'::jsonb,
    risk_level     TEXT NOT NULL,
    state          TEXT NOT NULL,
    result         JSONB NOT NULL DEFAULT '{}'::jsonb,
    failure_reason TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    executed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_actions_owner_state_created_at
    ON actions (owner_user_id, state, created_at);

CREATE INDEX IF NOT EXISTS idx_actions_state_expires_at
    ON actions (state, expires_at);

-- Per-user opt-in for the operator phase. Defaults to false so the feature
-- is explicitly enabled; existing deployments are never silently promoted
-- from Reporter-only to operator-capable.
ALTER TABLE users ADD COLUMN IF NOT EXISTS operator_enabled BOOLEAN NOT NULL DEFAULT FALSE;
