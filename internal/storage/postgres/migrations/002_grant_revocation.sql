ALTER TABLE policy_grants ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_policy_grants_pair_active
    ON policy_grants (grantor_user_id, grantee_user_id, created_at)
    WHERE revoked_at IS NULL;
