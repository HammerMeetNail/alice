package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"alice/internal/core"
)

func (s *Store) UpsertOrganization(org core.Organization) (core.Organization, error) {
	_, err := s.db.ExecContext(
		context.Background(),
		`INSERT INTO organizations (org_id, name, slug, created_at, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id) DO UPDATE
		SET name = EXCLUDED.name,
		    slug = EXCLUDED.slug,
		    created_at = EXCLUDED.created_at,
		    status = EXCLUDED.status`,
		org.OrgID,
		org.Name,
		normalizeSlug(org.Slug),
		org.CreatedAt,
		org.Status,
	)
	if err != nil {
		return core.Organization{}, fmt.Errorf("upsert organization: %w", err)
	}
	return org, nil
}

func (s *Store) FindOrganizationBySlug(slug string) (core.Organization, bool, error) {
	var org core.Organization
	err := s.db.QueryRowContext(
		context.Background(),
		`SELECT org_id, name, slug, created_at, status
		FROM organizations
		WHERE slug = $1`,
		normalizeSlug(slug),
	).Scan(&org.OrgID, &org.Name, &org.Slug, &org.CreatedAt, &org.Status)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Organization{}, false, nil
		}
		return core.Organization{}, false, fmt.Errorf("find organization by slug: %w", err)
	}
	return org, true, nil
}

func (s *Store) UpsertUser(user core.User) (core.User, error) {
	roleTitles, err := marshalStringSlice(user.RoleTitles)
	if err != nil {
		return core.User{}, fmt.Errorf("marshal user role titles: %w", err)
	}

	_, err = s.db.ExecContext(
		context.Background(),
		`INSERT INTO users (user_id, org_id, email, display_name, role_titles, manager_user_id, created_at, status)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE
		SET org_id = EXCLUDED.org_id,
		    email = EXCLUDED.email,
		    display_name = EXCLUDED.display_name,
		    role_titles = EXCLUDED.role_titles,
		    manager_user_id = EXCLUDED.manager_user_id,
		    created_at = EXCLUDED.created_at,
		    status = EXCLUDED.status`,
		user.UserID,
		user.OrgID,
		normalizeEmail(user.Email),
		user.DisplayName,
		roleTitles,
		nullString(user.ManagerUserID),
		user.CreatedAt,
		user.Status,
	)
	if err != nil {
		return core.User{}, fmt.Errorf("upsert user: %w", err)
	}
	user.Email = normalizeEmail(user.Email)
	return user, nil
}

func (s *Store) FindUserByEmail(email string) (core.User, bool, error) {
	var (
		user        core.User
		roleTitles  []byte
		managerUser sql.NullString
	)

	err := s.db.QueryRowContext(
		context.Background(),
		`SELECT user_id, org_id, email, display_name, role_titles, manager_user_id, created_at, status
		FROM users
		WHERE email = $1`,
		normalizeEmail(email),
	).Scan(&user.UserID, &user.OrgID, &user.Email, &user.DisplayName, &roleTitles, &managerUser, &user.CreatedAt, &user.Status)
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

func (s *Store) FindUserByID(userID string) (core.User, bool, error) {
	var (
		user        core.User
		roleTitles  []byte
		managerUser sql.NullString
	)

	err := s.db.QueryRowContext(
		context.Background(),
		`SELECT user_id, org_id, email, display_name, role_titles, manager_user_id, created_at, status
		FROM users
		WHERE user_id = $1`,
		userID,
	).Scan(&user.UserID, &user.OrgID, &user.Email, &user.DisplayName, &roleTitles, &managerUser, &user.CreatedAt, &user.Status)
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

func (s *Store) UpsertAgent(agent core.Agent) (core.Agent, error) {
	capabilities, err := marshalStringSlice(agent.Capabilities)
	if err != nil {
		return core.Agent{}, fmt.Errorf("marshal agent capabilities: %w", err)
	}

	_, err = s.db.ExecContext(
		context.Background(),
		`INSERT INTO agents (agent_id, org_id, owner_user_id, agent_name, runtime_kind, client_type, public_key, capabilities, status, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
		ON CONFLICT (agent_id) DO UPDATE
		SET org_id = EXCLUDED.org_id,
		    owner_user_id = EXCLUDED.owner_user_id,
		    agent_name = EXCLUDED.agent_name,
		    runtime_kind = EXCLUDED.runtime_kind,
		    client_type = EXCLUDED.client_type,
		    public_key = EXCLUDED.public_key,
		    capabilities = EXCLUDED.capabilities,
		    status = EXCLUDED.status,
		    last_seen_at = EXCLUDED.last_seen_at`,
		agent.AgentID,
		agent.OrgID,
		agent.OwnerUserID,
		agent.AgentName,
		agent.RuntimeKind,
		agent.ClientType,
		agent.PublicKey,
		capabilities,
		agent.Status,
		agent.LastSeenAt,
	)
	if err != nil {
		return core.Agent{}, fmt.Errorf("upsert agent: %w", err)
	}
	return agent, nil
}

func (s *Store) FindAgentByID(agentID string) (core.Agent, bool, error) {
	var (
		agent        core.Agent
		capabilities []byte
	)

	err := s.db.QueryRowContext(
		context.Background(),
		`SELECT agent_id, org_id, owner_user_id, agent_name, runtime_kind, client_type, public_key, capabilities, status, last_seen_at
		FROM agents
		WHERE agent_id = $1`,
		agentID,
	).Scan(&agent.AgentID, &agent.OrgID, &agent.OwnerUserID, &agent.AgentName, &agent.RuntimeKind, &agent.ClientType, &agent.PublicKey, &capabilities, &agent.Status, &agent.LastSeenAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Agent{}, false, nil
		}
		return core.Agent{}, false, fmt.Errorf("find agent by id: %w", err)
	}

	if err := unmarshalJSON(capabilities, &agent.Capabilities); err != nil {
		return core.Agent{}, false, fmt.Errorf("decode agent capabilities: %w", err)
	}
	return agent, true, nil
}

func (s *Store) FindAgentByUserID(userID string) (core.Agent, bool, error) {
	var (
		agent        core.Agent
		capabilities []byte
	)

	err := s.db.QueryRowContext(
		context.Background(),
		`SELECT agent_id, org_id, owner_user_id, agent_name, runtime_kind, client_type, public_key, capabilities, status, last_seen_at
		FROM agents
		WHERE owner_user_id = $1`,
		userID,
	).Scan(&agent.AgentID, &agent.OrgID, &agent.OwnerUserID, &agent.AgentName, &agent.RuntimeKind, &agent.ClientType, &agent.PublicKey, &capabilities, &agent.Status, &agent.LastSeenAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Agent{}, false, nil
		}
		return core.Agent{}, false, fmt.Errorf("find agent by user id: %w", err)
	}

	if err := unmarshalJSON(capabilities, &agent.Capabilities); err != nil {
		return core.Agent{}, false, fmt.Errorf("decode agent capabilities: %w", err)
	}
	return agent, true, nil
}

func nullString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}
