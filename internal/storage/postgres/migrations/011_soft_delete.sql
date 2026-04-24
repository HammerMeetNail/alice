-- Replace the global UNIQUE constraint on users.email with a partial unique
-- index so that multiple soft-deleted rows (email = '[deleted]', status =
-- 'deleted') can coexist without violating uniqueness.
--
-- organizations.slug intentionally keeps its original full UNIQUE constraint.
-- Org deletion is terminal: a deleted slug must never be silently reused
-- by a new registration.

-- users.email: allow many '[deleted]' values but keep uniqueness for live rows.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;
CREATE UNIQUE INDEX IF NOT EXISTS users_email_active_unique
    ON users (email)
    WHERE status != 'deleted';
