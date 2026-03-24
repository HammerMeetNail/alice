package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"alice/internal/core"
)

func (s *Store) SaveGrant(ctx context.Context, grant core.PolicyGrant) (core.PolicyGrant, error) {
	artifactTypes, err := marshalArtifactTypes(grant.AllowedArtifactTypes)
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("marshal grant artifact types: %w", err)
	}
	allowedPurposes, err := marshalQueryPurposes(grant.AllowedPurposes)
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("marshal grant purposes: %w", err)
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO policy_grants (
			policy_grant_id, org_id, grantor_user_id, grantee_user_id, scope_type, scope_ref, allowed_artifact_types,
			max_sensitivity, allowed_purposes, visibility_mode, requires_approval_above_risk, created_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7::jsonb,
			$8, $9::jsonb, $10, $11, $12, $13
		)`,
		grant.PolicyGrantID,
		grant.OrgID,
		grant.GrantorUserID,
		grant.GranteeUserID,
		grant.ScopeType,
		grant.ScopeRef,
		artifactTypes,
		grant.MaxSensitivity,
		allowedPurposes,
		grant.VisibilityMode,
		grant.RequiresApprovalAboveRisk,
		grant.CreatedAt,
		grant.ExpiresAt,
	)
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("insert policy grant: %w", err)
	}
	return grant, nil
}

func (s *Store) FindGrant(ctx context.Context, grantID string) (core.PolicyGrant, bool, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT policy_grant_id, org_id, grantor_user_id, grantee_user_id, scope_type, scope_ref, allowed_artifact_types,
		        max_sensitivity, allowed_purposes, visibility_mode, requires_approval_above_risk, created_at, expires_at, revoked_at
		FROM policy_grants
		WHERE policy_grant_id = $1`,
		grantID,
	)
	grant, err := scanGrant(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.PolicyGrant{}, false, nil
		}
		return core.PolicyGrant{}, false, err
	}
	return grant, true, nil
}

func (s *Store) RevokeGrant(ctx context.Context, grantID, grantorUserID string) (core.PolicyGrant, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE policy_grants SET revoked_at = $1
		 WHERE policy_grant_id = $2 AND grantor_user_id = $3 AND revoked_at IS NULL`,
		now, grantID, grantorUserID,
	)
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("revoke grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("revoke grant rows affected: %w", err)
	}
	if rows == 0 {
		return core.PolicyGrant{}, fmt.Errorf("grant not found or not owned by grantor")
	}
	grant, ok, err := s.FindGrant(ctx, grantID)
	if err != nil {
		return core.PolicyGrant{}, err
	}
	if !ok {
		return core.PolicyGrant{}, fmt.Errorf("grant not found after revocation")
	}
	return grant, nil
}

func (s *Store) ListGrantsForPair(ctx context.Context, grantorUserID, granteeUserID string) ([]core.PolicyGrant, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT policy_grant_id, org_id, grantor_user_id, grantee_user_id, scope_type, scope_ref, allowed_artifact_types,
		        max_sensitivity, allowed_purposes, visibility_mode, requires_approval_above_risk, created_at, expires_at, revoked_at
		FROM policy_grants
		WHERE grantor_user_id = $1 AND grantee_user_id = $2 AND revoked_at IS NULL
		AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at ASC`,
		grantorUserID,
		granteeUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("query grants for pair: %w", err)
	}
	defer rows.Close()

	grants := make([]core.PolicyGrant, 0)
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grants for pair: %w", err)
	}

	return grants, nil
}

func (s *Store) ListIncomingGrantsForUser(ctx context.Context, granteeUserID string, limit, offset int) ([]core.PolicyGrant, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT policy_grant_id, org_id, grantor_user_id, grantee_user_id, scope_type, scope_ref, allowed_artifact_types,
		        max_sensitivity, allowed_purposes, visibility_mode, requires_approval_above_risk, created_at, expires_at, revoked_at
		FROM policy_grants
		WHERE grantee_user_id = $1 AND revoked_at IS NULL
		AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3`,
		granteeUserID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query incoming grants: %w", err)
	}
	defer rows.Close()

	grants := make([]core.PolicyGrant, 0)
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incoming grants: %w", err)
	}

	return grants, nil
}

func scanGrant(scanner interface{ Scan(dest ...any) error }) (core.PolicyGrant, error) {
	var (
		grant               core.PolicyGrant
		allowedArtifactJSON []byte
		allowedPurposesJSON []byte
		expiresAt           sql.NullTime
		revokedAt           sql.NullTime
	)

	if err := scanner.Scan(
		&grant.PolicyGrantID,
		&grant.OrgID,
		&grant.GrantorUserID,
		&grant.GranteeUserID,
		&grant.ScopeType,
		&grant.ScopeRef,
		&allowedArtifactJSON,
		&grant.MaxSensitivity,
		&allowedPurposesJSON,
		&grant.VisibilityMode,
		&grant.RequiresApprovalAboveRisk,
		&grant.CreatedAt,
		&expiresAt,
		&revokedAt,
	); err != nil {
		return core.PolicyGrant{}, fmt.Errorf("scan grant: %w", err)
	}

	if err := unmarshalJSON(allowedArtifactJSON, &grant.AllowedArtifactTypes); err != nil {
		return core.PolicyGrant{}, fmt.Errorf("decode grant artifact types: %w", err)
	}
	if err := unmarshalJSON(allowedPurposesJSON, &grant.AllowedPurposes); err != nil {
		return core.PolicyGrant{}, fmt.Errorf("decode grant purposes: %w", err)
	}
	if expiresAt.Valid {
		grant.ExpiresAt = &expiresAt.Time
	}
	if revokedAt.Valid {
		grant.RevokedAt = &revokedAt.Time
	}
	return grant, nil
}
