CREATE TABLE IF NOT EXISTS organizations (
    org_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    user_id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES organizations(org_id),
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    role_titles JSONB NOT NULL DEFAULT '[]'::jsonb,
    manager_user_id TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agents (
    agent_id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES organizations(org_id),
    owner_user_id TEXT NOT NULL UNIQUE REFERENCES users(user_id),
    agent_name TEXT NOT NULL,
    runtime_kind TEXT NOT NULL,
    client_type TEXT NOT NULL,
    public_key TEXT NOT NULL,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS artifacts (
    artifact_id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES organizations(org_id),
    owner_agent_id TEXT NOT NULL REFERENCES agents(agent_id),
    owner_user_id TEXT NOT NULL REFERENCES users(user_id),
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    structured_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    visibility_mode TEXT NOT NULL,
    sensitivity TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL,
    approval_state TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    supersedes_artifact_id TEXT
);

CREATE TABLE IF NOT EXISTS policy_grants (
    policy_grant_id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES organizations(org_id),
    grantor_user_id TEXT NOT NULL REFERENCES users(user_id),
    grantee_user_id TEXT NOT NULL REFERENCES users(user_id),
    scope_type TEXT NOT NULL,
    scope_ref TEXT NOT NULL,
    allowed_artifact_types JSONB NOT NULL DEFAULT '[]'::jsonb,
    max_sensitivity TEXT NOT NULL,
    allowed_purposes JSONB NOT NULL DEFAULT '[]'::jsonb,
    visibility_mode TEXT NOT NULL,
    requires_approval_above_risk TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS queries (
    query_id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES organizations(org_id),
    from_agent_id TEXT NOT NULL REFERENCES agents(agent_id),
    from_user_id TEXT NOT NULL REFERENCES users(user_id),
    to_agent_id TEXT NOT NULL REFERENCES agents(agent_id),
    to_user_id TEXT NOT NULL REFERENCES users(user_id),
    purpose TEXT NOT NULL,
    question TEXT NOT NULL,
    requested_types JSONB NOT NULL DEFAULT '[]'::jsonb,
    project_scope JSONB NOT NULL DEFAULT '[]'::jsonb,
    time_window_start TIMESTAMPTZ NOT NULL,
    time_window_end TIMESTAMPTZ NOT NULL,
    risk_level TEXT NOT NULL,
    state TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS query_responses (
    response_id TEXT PRIMARY KEY,
    query_id TEXT NOT NULL UNIQUE REFERENCES queries(query_id) ON DELETE CASCADE,
    from_agent_id TEXT NOT NULL REFERENCES agents(agent_id),
    to_agent_id TEXT NOT NULL REFERENCES agents(agent_id),
    artifacts JSONB NOT NULL DEFAULT '[]'::jsonb,
    redactions JSONB NOT NULL DEFAULT '[]'::jsonb,
    policy_basis JSONB NOT NULL DEFAULT '[]'::jsonb,
    approval_state TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
    audit_event_id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES organizations(org_id),
    event_kind TEXT NOT NULL,
    actor_agent_id TEXT,
    target_agent_id TEXT,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    policy_basis JSONB NOT NULL DEFAULT '[]'::jsonb,
    decision TEXT NOT NULL,
    risk_level TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_artifacts_owner_created_at
    ON artifacts (owner_user_id, created_at);

CREATE INDEX IF NOT EXISTS idx_policy_grants_pair_created_at
    ON policy_grants (grantor_user_id, grantee_user_id, created_at);

CREATE INDEX IF NOT EXISTS idx_policy_grants_grantee_created_at
    ON policy_grants (grantee_user_id, created_at);

CREATE INDEX IF NOT EXISTS idx_queries_to_user_created_at
    ON queries (to_user_id, created_at);

CREATE INDEX IF NOT EXISTS idx_audit_events_actor_created_at
    ON audit_events (actor_agent_id, created_at);

CREATE INDEX IF NOT EXISTS idx_audit_events_target_created_at
    ON audit_events (target_agent_id, created_at);
