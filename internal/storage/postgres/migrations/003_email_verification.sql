-- Add email_verified_at column and update status for existing agents.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;

-- Grandfather existing agents as active (status column already exists).
UPDATE agents SET status = 'active' WHERE status = '';

-- Create email_verifications table.
CREATE TABLE IF NOT EXISTS email_verifications (
    verification_id TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agents(agent_id) ON DELETE CASCADE,
    org_id          TEXT NOT NULL REFERENCES organizations(org_id),
    email           TEXT NOT NULL,
    code_hash       TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    verified_at     TIMESTAMPTZ,
    attempts        INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS email_verifications_agent_id_idx
    ON email_verifications (agent_id)
    WHERE verified_at IS NULL;
