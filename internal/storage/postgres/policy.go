package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"alice/internal/core"
)

func (s *Store) SaveGrant(grant core.PolicyGrant) (core.PolicyGrant, error) {
	artifactTypes, err := marshalArtifactTypes(grant.AllowedArtifactTypes)
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("marshal grant artifact types: %w", err)
	}
	allowedPurposes, err := marshalQueryPurposes(grant.AllowedPurposes)
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("marshal grant purposes: %w", err)
	}

	_, err = s.db.ExecContext(
		context.Background(),
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

func (s *Store) ListGrantsForPair(grantorUserID, granteeUserID string) ([]core.PolicyGrant, error) {
	rows, err := s.db.QueryContext(
		context.Background(),
		`SELECT policy_grant_id, org_id, grantor_user_id, grantee_user_id, scope_type, scope_ref, allowed_artifact_types,
		        max_sensitivity, allowed_purposes, visibility_mode, requires_approval_above_risk, created_at, expires_at
		FROM policy_grants
		WHERE grantor_user_id = $1 AND grantee_user_id = $2
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

func (s *Store) ListIncomingGrantsForUser(granteeUserID string) ([]core.PolicyGrant, error) {
	rows, err := s.db.QueryContext(
		context.Background(),
		`SELECT policy_grant_id, org_id, grantor_user_id, grantee_user_id, scope_type, scope_ref, allowed_artifact_types,
		        max_sensitivity, allowed_purposes, visibility_mode, requires_approval_above_risk, created_at, expires_at
		FROM policy_grants
		WHERE grantee_user_id = $1
		ORDER BY created_at ASC`,
		granteeUserID,
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
	return grant, nil
}
