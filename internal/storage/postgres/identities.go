package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"alice/internal/core"
)

func (s *Store) UpsertOrganization(ctx context.Context, org core.Organization) (core.Organization, error) {
	var threshold sql.NullFloat64
	if org.GatekeeperConfidenceThreshold != nil {
		threshold = sql.NullFloat64{Float64: *org.GatekeeperConfidenceThreshold, Valid: true}
	}
	var lookback sql.NullInt64
	if org.GatekeeperLookbackWindow != nil {
		lookback = sql.NullInt64{Int64: int64(*org.GatekeeperLookbackWindow / time.Second), Valid: true}
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO organizations (org_id, name, slug, created_at, status, verification_mode, invite_token_hash, gatekeeper_confidence_threshold, gatekeeper_lookback_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (org_id) DO UPDATE
		SET name = EXCLUDED.name,
		    slug = EXCLUDED.slug,
		    created_at = EXCLUDED.created_at,
		    status = EXCLUDED.status,
		    verification_mode = EXCLUDED.verification_mode,
		    invite_token_hash = EXCLUDED.invite_token_hash,
		    gatekeeper_confidence_threshold = EXCLUDED.gatekeeper_confidence_threshold,
		    gatekeeper_lookback_seconds = EXCLUDED.gatekeeper_lookback_seconds`,
		org.OrgID,
		org.Name,
		normalizeSlug(org.Slug),
		org.CreatedAt,
		org.Status,
		org.VerificationMode,
		org.InviteTokenHash,
		threshold,
		lookback,
	)
	if err != nil {
		return core.Organization{}, fmt.Errorf("upsert organization: %w", err)
	}
	return org, nil
}

func (s *Store) FindOrganizationBySlug(ctx context.Context, slug string) (core.Organization, bool, error) {
	org, err := scanOrganization(s.db.QueryRowContext(
		ctx,
		orgSelectColumns+` FROM organizations WHERE slug = $1`,
		normalizeSlug(slug),
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Organization{}, false, nil
		}
		return core.Organization{}, false, fmt.Errorf("find organization by slug: %w", err)
	}
	return org, true, nil
}

func (s *Store) UpsertUser(ctx context.Context, user core.User) (core.User, error) {
	roleTitles, err := marshalStringSlice(user.RoleTitles)
	if err != nil {
		return core.User{}, fmt.Errorf("marshal user role titles: %w", err)
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO users (user_id, org_id, email, display_name, role_titles, manager_user_id, created_at, status, role)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)
		ON CONFLICT (user_id) DO UPDATE
		SET org_id = EXCLUDED.org_id,
		    email = EXCLUDED.email,
		    display_name = EXCLUDED.display_name,
		    role_titles = EXCLUDED.role_titles,
		    manager_user_id = EXCLUDED.manager_user_id,
		    created_at = EXCLUDED.created_at,
		    status = EXCLUDED.status,
		    role = EXCLUDED.role`,
		user.UserID,
		user.OrgID,
		normalizeEmail(user.Email),
		user.DisplayName,
		roleTitles,
		nullString(user.ManagerUserID),
		user.CreatedAt,
		user.Status,
		user.Role,
	)
	if err != nil {
		return core.User{}, fmt.Errorf("upsert user: %w", err)
	}
	user.Email = normalizeEmail(user.Email)
	return user, nil
}

func (s *Store) FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error) {
	var (
		user        core.User
		roleTitles  []byte
		managerUser sql.NullString
	)

	err := s.db.QueryRowContext(
		ctx,
		`SELECT user_id, org_id, email, display_name, role_titles, manager_user_id, created_at, status, role
		FROM users
		WHERE org_id = $1 AND email = $2`,
		orgID,
		normalizeEmail(email),
	).Scan(&user.UserID, &user.OrgID, &user.Email, &user.DisplayName, &roleTitles, &managerUser, &user.CreatedAt, &user.Status, &user.Role)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.User{}, false, nil
		}
		return core.User{}, false, fmt.Errorf("find user by email: %w", err)
	}

	if err := unmarshalJSON(roleTitles, &user.RoleTitles); err != nil {
		return core.User{}, false, fmt.Errorf("decode user role titles: %w", err)
	}
	user.ManagerUserID = managerUser.String
	return user, true, nil
}

func (s *Store) FindUserByID(ctx context.Context, userID string) (core.User, bool, error) {
	var (
		user        core.User
		roleTitles  []byte
		managerUser sql.NullString
	)

	err := s.db.QueryRowContext(
		ctx,
		`SELECT user_id, org_id, email, display_name, role_titles, manager_user_id, created_at, status, role
		FROM users
		WHERE user_id = $1`,
		userID,
	).Scan(&user.UserID, &user.OrgID, &user.Email, &user.DisplayName, &roleTitles, &managerUser, &user.CreatedAt, &user.Status, &user.Role)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.User{}, false, nil
		}
		return core.User{}, false, fmt.Errorf("find user by id: %w", err)
	}

	if err := unmarshalJSON(roleTitles, &user.RoleTitles); err != nil {
		return core.User{}, false, fmt.Errorf("decode user role titles: %w", err)
	}
	user.ManagerUserID = managerUser.String
	return user, true, nil
}

func (s *Store) UpdateUserRole(ctx context.Context, userID, role string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE users SET role = $2 WHERE user_id = $1`,
		userID, role,
	)
	if err != nil {
		return fmt.Errorf("update user role: %w", err)
	}
	return nil
}

func (s *Store) UpsertAgent(ctx context.Context, agent core.Agent) (core.Agent, error) {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO agents (agent_id, org_id, owner_user_id, agent_name, runtime_kind, client_type, public_key, status, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (agent_id) DO UPDATE
		SET org_id = EXCLUDED.org_id,
		    owner_user_id = EXCLUDED.owner_user_id,
		    agent_name = EXCLUDED.agent_name,
		    runtime_kind = EXCLUDED.runtime_kind,
		    client_type = EXCLUDED.client_type,
		    public_key = EXCLUDED.public_key,
		    status = EXCLUDED.status,
		    last_seen_at = EXCLUDED.last_seen_at`,
		agent.AgentID,
		agent.OrgID,
		agent.OwnerUserID,
		agent.AgentName,
		agent.RuntimeKind,
		agent.ClientType,
		agent.PublicKey,
		agent.Status,
		agent.LastSeenAt,
	)
	if err != nil {
		return core.Agent{}, fmt.Errorf("upsert agent: %w", err)
	}
	return agent, nil
}

func (s *Store) FindAgentByID(ctx context.Context, agentID string) (core.Agent, bool, error) {
	var agent core.Agent

	err := s.db.QueryRowContext(
		ctx,
		`SELECT agent_id, org_id, owner_user_id, agent_name, runtime_kind, client_type, public_key, status, last_seen_at
		FROM agents
		WHERE agent_id = $1`,
		agentID,
	).Scan(&agent.AgentID, &agent.OrgID, &agent.OwnerUserID, &agent.AgentName, &agent.RuntimeKind, &agent.ClientType, &agent.PublicKey, &agent.Status, &agent.LastSeenAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Agent{}, false, nil
		}
		return core.Agent{}, false, fmt.Errorf("find agent by id: %w", err)
	}
	return agent, true, nil
}

func (s *Store) FindAgentByUserID(ctx context.Context, userID string) (core.Agent, bool, error) {
	var agent core.Agent

	err := s.db.QueryRowContext(
		ctx,
		`SELECT agent_id, org_id, owner_user_id, agent_name, runtime_kind, client_type, public_key, status, last_seen_at
		FROM agents
		WHERE owner_user_id = $1`,
		userID,
	).Scan(&agent.AgentID, &agent.OrgID, &agent.OwnerUserID, &agent.AgentName, &agent.RuntimeKind, &agent.ClientType, &agent.PublicKey, &agent.Status, &agent.LastSeenAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Agent{}, false, nil
		}
		return core.Agent{}, false, fmt.Errorf("find agent by user id: %w", err)
	}
	return agent, true, nil
}

func nullString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}
