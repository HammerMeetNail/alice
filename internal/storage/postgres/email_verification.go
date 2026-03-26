package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) SaveEmailVerification(ctx context.Context, v core.EmailVerification) (core.EmailVerification, error) {
	// Invalidate any existing pending verification for this agent first.
	_, err := s.db.ExecContext(ctx,
		`UPDATE email_verifications
		 SET verified_at = now()
		 WHERE agent_id = $1 AND verified_at IS NULL`,
		v.AgentID,
	)
	if err != nil {
		return core.EmailVerification{}, fmt.Errorf("invalidate old email verifications: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO email_verifications
		     (verification_id, agent_id, org_id, email, code_hash, created_at, expires_at, verified_at, attempts)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		v.VerificationID,
		v.AgentID,
		v.OrgID,
		v.Email,
		v.CodeHash,
		v.CreatedAt,
		v.ExpiresAt,
		nullTimePtr(v.VerifiedAt),
		v.Attempts,
	)
	if err != nil {
		return core.EmailVerification{}, fmt.Errorf("insert email verification: %w", err)
	}
	return v, nil
}

func (s *Store) FindPendingVerification(ctx context.Context, agentID string) (core.EmailVerification, bool, error) {
	var (
		v          core.EmailVerification
		verifiedAt sql.NullTime
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT verification_id, agent_id, org_id, email, code_hash,
		        created_at, expires_at, verified_at, attempts
		 FROM email_verifications
		 WHERE agent_id = $1 AND verified_at IS NULL
		 ORDER BY created_at DESC
		 LIMIT 1`,
		agentID,
	).Scan(
		&v.VerificationID,
		&v.AgentID,
		&v.OrgID,
		&v.Email,
		&v.CodeHash,
		&v.CreatedAt,
		&v.ExpiresAt,
		&verifiedAt,
		&v.Attempts,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.EmailVerification{}, false, nil
		}
		return core.EmailVerification{}, false, fmt.Errorf("find pending email verification: %w", err)
	}
	if verifiedAt.Valid {
		v.VerifiedAt = &verifiedAt.Time
	}
	return v, true, nil
}

func (s *Store) MarkEmailVerified(ctx context.Context, verificationID string, verifiedAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE email_verifications
		 SET verified_at = $1
		 WHERE verification_id = $2 AND verified_at IS NULL`,
		verifiedAt,
		verificationID,
	)
	if err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark email verified rows affected: %w", err)
	}
	if n == 0 {
		return storage.ErrVerificationNotFound
	}
	return nil
}

func (s *Store) IncrementVerificationAttempts(ctx context.Context, verificationID string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE email_verifications
		 SET attempts = attempts + 1
		 WHERE verification_id = $1`,
		verificationID,
	)
	if err != nil {
		return fmt.Errorf("increment verification attempts: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("increment verification attempts rows affected: %w", err)
	}
	if n == 0 {
		return storage.ErrVerificationNotFound
	}
	return nil
}
