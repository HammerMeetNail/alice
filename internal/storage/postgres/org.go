package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

// rowScanner is the subset of *sql.Row / *sql.Rows that Scan can operate on.
// It lets scanOrganization serve both QueryRow and Query paths.
type rowScanner interface {
	Scan(dest ...any) error
}

// orgSelectColumns is the canonical column list for reading an organization.
// Kept here so FindOrganizationByID, FindOrganizationBySlug, and FindOrgBySlug
// stay in sync when the schema grows.
const orgSelectColumns = `SELECT org_id, name, slug, created_at, status, verification_mode, invite_token_hash, gatekeeper_confidence_threshold, gatekeeper_lookback_seconds`

func scanOrganization(row rowScanner) (core.Organization, error) {
	var (
		org       core.Organization
		threshold sql.NullFloat64
		lookback  sql.NullInt64
	)
	if err := row.Scan(&org.OrgID, &org.Name, &org.Slug, &org.CreatedAt, &org.Status, &org.VerificationMode, &org.InviteTokenHash, &threshold, &lookback); err != nil {
		return core.Organization{}, err
	}
	if threshold.Valid {
		v := threshold.Float64
		org.GatekeeperConfidenceThreshold = &v
	}
	if lookback.Valid {
		d := time.Duration(lookback.Int64) * time.Second
		org.GatekeeperLookbackWindow = &d
	}
	return org, nil
}

func (s *Store) FindOrganizationByID(ctx context.Context, orgID string) (core.Organization, bool, error) {
	org, err := scanOrganization(s.db.QueryRowContext(
		ctx,
		orgSelectColumns+` FROM organizations WHERE org_id = $1`,
		orgID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Organization{}, false, nil
		}
		return core.Organization{}, false, fmt.Errorf("find organization by id: %w", err)
	}
	return org, true, nil
}

func (s *Store) FindOrgBySlug(ctx context.Context, slug string) (core.Organization, error) {
	org, err := scanOrganization(s.db.QueryRowContext(
		ctx,
		orgSelectColumns+` FROM organizations WHERE slug = $1`,
		normalizeSlug(slug),
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Organization{}, storage.ErrOrgNotFound
		}
		return core.Organization{}, fmt.Errorf("find org by slug: %w", err)
	}
	return org, nil
}

func (s *Store) UpdateOrgVerificationMode(ctx context.Context, orgID, mode string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE organizations SET verification_mode = $2 WHERE org_id = $1`,
		orgID, mode,
	)
	if err != nil {
		return fmt.Errorf("update org verification mode: %w", err)
	}
	return nil
}

func (s *Store) SetOrgInviteTokenHash(ctx context.Context, orgID, hash string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE organizations SET invite_token_hash = $2 WHERE org_id = $1`,
		orgID, hash,
	)
	if err != nil {
		return fmt.Errorf("set org invite token hash: %w", err)
	}
	return nil
}

func (s *Store) UpdateGatekeeperTuning(ctx context.Context, orgID string, threshold *float64, window *time.Duration) error {
	var thresholdArg sql.NullFloat64
	if threshold != nil {
		thresholdArg = sql.NullFloat64{Float64: *threshold, Valid: true}
	}
	var windowArg sql.NullInt64
	if window != nil {
		windowArg = sql.NullInt64{Int64: int64(*window / time.Second), Valid: true}
	}
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE organizations
		SET gatekeeper_confidence_threshold = $2,
		    gatekeeper_lookback_seconds = $3
		WHERE org_id = $1`,
		orgID, thresholdArg, windowArg,
	)
	if err != nil {
		return fmt.Errorf("update gatekeeper tuning: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrOrgNotFound
	}
	return nil
}
