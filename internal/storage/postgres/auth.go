package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
)

func (s *Store) SaveAgentRegistrationChallenge(challenge core.AgentRegistrationChallenge) (core.AgentRegistrationChallenge, error) {
	capabilities, err := marshalStringSlice(challenge.Capabilities)
	if err != nil {
		return core.AgentRegistrationChallenge{}, fmt.Errorf("marshal challenge capabilities: %w", err)
	}

	_, err = s.db.ExecContext(
		context.Background(),
		`INSERT INTO agent_registration_challenges (
			challenge_id, org_slug, owner_email, agent_name, client_type, public_key, capabilities, nonce, created_at, expires_at, used_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10, $11
		)
		ON CONFLICT (challenge_id) DO UPDATE
		SET org_slug = EXCLUDED.org_slug,
		    owner_email = EXCLUDED.owner_email,
		    agent_name = EXCLUDED.agent_name,
		    client_type = EXCLUDED.client_type,
		    public_key = EXCLUDED.public_key,
		    capabilities = EXCLUDED.capabilities,
		    nonce = EXCLUDED.nonce,
		    created_at = EXCLUDED.created_at,
		    expires_at = EXCLUDED.expires_at,
		    used_at = EXCLUDED.used_at`,
		challenge.ChallengeID,
		normalizeSlug(challenge.OrgSlug),
		normalizeEmail(challenge.OwnerEmail),
		challenge.AgentName,
		challenge.ClientType,
		challenge.PublicKey,
		capabilities,
		challenge.Nonce,
		challenge.CreatedAt,
		challenge.ExpiresAt,
		nullTimePtr(challenge.UsedAt),
	)
	if err != nil {
		return core.AgentRegistrationChallenge{}, fmt.Errorf("upsert registration challenge: %w", err)
	}

	challenge.OrgSlug = normalizeSlug(challenge.OrgSlug)
	challenge.OwnerEmail = normalizeEmail(challenge.OwnerEmail)
	return challenge, nil
}

func (s *Store) FindAgentRegistrationChallenge(challengeID string) (core.AgentRegistrationChallenge, bool, error) {
	var (
		challenge        core.AgentRegistrationChallenge
		capabilitiesJSON []byte
		usedAt           sql.NullTime
	)

	err := s.db.QueryRowContext(
		context.Background(),
		`SELECT challenge_id, org_slug, owner_email, agent_name, client_type, public_key, capabilities, nonce, created_at, expires_at, used_at
		FROM agent_registration_challenges
		WHERE challenge_id = $1`,
		challengeID,
	).Scan(
		&challenge.ChallengeID,
		&challenge.OrgSlug,
		&challenge.OwnerEmail,
		&challenge.AgentName,
		&challenge.ClientType,
		&challenge.PublicKey,
		&capabilitiesJSON,
		&challenge.Nonce,
		&challenge.CreatedAt,
		&challenge.ExpiresAt,
		&usedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.AgentRegistrationChallenge{}, false, nil
		}
		return core.AgentRegistrationChallenge{}, false, fmt.Errorf("find registration challenge: %w", err)
	}

	if err := unmarshalJSON(capabilitiesJSON, &challenge.Capabilities); err != nil {
		return core.AgentRegistrationChallenge{}, false, fmt.Errorf("decode challenge capabilities: %w", err)
	}
	if usedAt.Valid {
		challenge.UsedAt = &usedAt.Time
	}
	return challenge, true, nil
}

func (s *Store) SaveAgentToken(token core.AgentToken) (core.AgentToken, error) {
	_, err := s.db.ExecContext(
		context.Background(),
		`INSERT INTO agent_tokens (
			token_id, agent_id, token_hash, issued_at, expires_at, last_used_at, revoked_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		ON CONFLICT (token_id) DO UPDATE
		SET agent_id = EXCLUDED.agent_id,
		    token_hash = EXCLUDED.token_hash,
		    issued_at = EXCLUDED.issued_at,
		    expires_at = EXCLUDED.expires_at,
		    last_used_at = EXCLUDED.last_used_at,
		    revoked_at = EXCLUDED.revoked_at`,
		token.TokenID,
		token.AgentID,
		token.TokenHash,
		token.IssuedAt,
		token.ExpiresAt,
		token.LastUsedAt,
		nullTimePtr(token.RevokedAt),
	)
	if err != nil {
		return core.AgentToken{}, fmt.Errorf("upsert agent token: %w", err)
	}
	return token, nil
}

func (s *Store) FindAgentTokenByID(tokenID string) (core.AgentToken, bool, error) {
	var (
		token     core.AgentToken
		revokedAt sql.NullTime
	)

	err := s.db.QueryRowContext(
		context.Background(),
		`SELECT token_id, agent_id, token_hash, issued_at, expires_at, last_used_at, revoked_at
		FROM agent_tokens
		WHERE token_id = $1`,
		tokenID,
	).Scan(
		&token.TokenID,
		&token.AgentID,
		&token.TokenHash,
		&token.IssuedAt,
		&token.ExpiresAt,
		&token.LastUsedAt,
		&revokedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.AgentToken{}, false, nil
		}
		return core.AgentToken{}, false, fmt.Errorf("find agent token: %w", err)
	}

	if revokedAt.Valid {
		token.RevokedAt = &revokedAt.Time
	}
	return token, true, nil
}

func nullTimePtr(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}
