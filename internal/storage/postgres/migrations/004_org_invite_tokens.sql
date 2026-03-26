-- 004_org_invite_tokens.sql
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS verification_mode TEXT NOT NULL DEFAULT 'email_otp';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS invite_token_hash TEXT NOT NULL DEFAULT '';
