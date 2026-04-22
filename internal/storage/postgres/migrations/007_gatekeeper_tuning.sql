-- 007_gatekeeper_tuning.sql
-- Per-org overrides for the gatekeeper auto-answer path. NULL means "fall
-- through to the server-wide env var, then the compile-time default".
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS gatekeeper_confidence_threshold DOUBLE PRECISION;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS gatekeeper_lookback_seconds BIGINT;
