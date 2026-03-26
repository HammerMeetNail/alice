package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) SaveAgentRegistrationChallenge(ctx context.Context, challenge core.AgentRegistrationChallenge) (core.AgentRegistrationChallenge, error) {
	if challenge.UsedAt != nil {
		// Atomic check-and-set: only mark as used if not already marked.
		// This prevents concurrent CompleteRegistration calls from both succeeding.
		result, err := s.db.ExecContext(ctx,
			`UPDATE agent_registration_challenges SET used_at = $2 WHERE challenge_id = $1 AND used_at IS NULL`,
			challenge.ChallengeID,
			nullTimePtr(challenge.UsedAt),
		)
		if err != nil {
			return core.AgentRegistrationChallenge{}, fmt.Errorf("mark registration challenge used: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return core.AgentRegistrationChallenge{}, fmt.Errorf("check rows affected: %w", err)
		}
		if n == 0 {
			return core.AgentRegistrationChallenge{}, storage.ErrChallengeAlreadyUsed
		}
		return challenge, nil
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO agent_registration_challenges (
			challenge_id, org_slug, owner_email, agent_name, client_type, public_key, nonce, created_at, expires_at, used_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
		ON CONFLICT (challenge_id) DO UPDATE
		SET org_slug = EXCLUDED.org_slug,
		    owner_email = EXCLUDED.owner_email,
		    agent_name = EXCLUDED.agent_name,
		    client_type = EXCLUDED.client_type,
		    public_key = EXCLUDED.public_key,
		    nonce = EXCLUDED.nonce,
		    created_at = EXCLUDED.created_at,
		    expires_at = EXCLUDED.expires_at`,
		challenge.ChallengeID,
		normalizeSlug(challenge.OrgSlug),
		normalizeEmail(challenge.OwnerEmail),
		challenge.AgentName,
		challenge.ClientType,
		challenge.PublicKey,
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

func (s *Store) FindAgentRegistrationChallenge(ctx context.Context, challengeID string) (core.AgentRegistrationChallenge, bool, error) {
	var (
		challenge core.AgentRegistrationChallenge
		usedAt    sql.NullTime
	)

	err := s.db.QueryRowContext(
		ctx,
		`SELECT challenge_id, org_slug, owner_email, agent_name, client_type, public_key, nonce, created_at, expires_at, used_at
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

	if usedAt.Valid {
		challenge.UsedAt = &usedAt.Time
	}
	return challenge, true, nil
}

func (s *Store) SaveAgentToken(ctx context.Context, token core.AgentToken) (core.AgentToken, error) {
	_, err := s.db.ExecContext(
		ctx,
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

func (s *Store) FindAgentTokenByID(ctx context.Context, tokenID string) (core.AgentToken, bool, error) {
	var (
		token     core.AgentToken
		revokedAt sql.NullTime
	)

	err := s.db.QueryRowContext(
		ctx,
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

func (s *Store) RevokeAllTokensForAgent(ctx context.Context, agentID string, revokedAt time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE agent_tokens SET revoked_at = $2 WHERE agent_id = $1 AND revoked_at IS NULL`,
		agentID, revokedAt,
	)
	if err != nil {
		return fmt.Errorf("revoke all tokens for agent: %w", err)
	}
	return nil
}

func nullTimePtr(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}
