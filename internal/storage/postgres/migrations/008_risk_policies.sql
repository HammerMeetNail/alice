-- 008_risk_policies.sql
-- Versioned per-org risk policies. Exactly one policy may be active per org
-- at a time (enforced by a partial unique index); older versions stay in the
-- table so admins can audit history and roll back by reactivating a prior
-- version.
CREATE TABLE IF NOT EXISTS risk_policies (
    policy_id          TEXT PRIMARY KEY,
    org_id             TEXT NOT NULL REFERENCES organizations(org_id),
    name               TEXT NOT NULL,
    version            INTEGER NOT NULL,
    source             JSONB NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL,
    created_by_user_id TEXT,
    active_at          TIMESTAMPTZ,
    UNIQUE (org_id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_risk_policies_one_active_per_org
    ON risk_policies (org_id)
    WHERE active_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_risk_policies_org_created_at
    ON risk_policies (org_id, created_at);
