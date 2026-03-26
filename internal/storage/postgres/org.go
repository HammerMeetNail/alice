package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) FindOrganizationByID(ctx context.Context, orgID string) (core.Organization, bool, error) {
	var org core.Organization
	err := s.db.QueryRowContext(
		ctx,
		`SELECT org_id, name, slug, created_at, status, verification_mode, invite_token_hash
		FROM organizations
		WHERE org_id = $1`,
		orgID,
	).Scan(&org.OrgID, &org.Name, &org.Slug, &org.CreatedAt, &org.Status, &org.VerificationMode, &org.InviteTokenHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Organization{}, false, nil
		}
		return core.Organization{}, false, fmt.Errorf("find organization by id: %w", err)
	}
	return org, true, nil
}

func (s *Store) FindOrgBySlug(ctx context.Context, slug string) (core.Organization, error) {
	var org core.Organization
	err := s.db.QueryRowContext(
		ctx,
		`SELECT org_id, name, slug, created_at, status, verification_mode, invite_token_hash
		FROM organizations
		WHERE slug = $1`,
		normalizeSlug(slug),
	).Scan(&org.OrgID, &org.Name, &org.Slug, &org.CreatedAt, &org.Status, &org.VerificationMode, &org.InviteTokenHash)
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
