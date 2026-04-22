package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) SavePolicy(ctx context.Context, policy core.RiskPolicy) (core.RiskPolicy, error) {
	var activeAt sql.NullTime
	if policy.ActiveAt != nil {
		activeAt = sql.NullTime{Time: *policy.ActiveAt, Valid: true}
	}
	var createdBy sql.NullString
	if policy.CreatedByUserID != "" {
		createdBy = sql.NullString{String: policy.CreatedByUserID, Valid: true}
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO risk_policies (policy_id, org_id, name, version, source, created_at, created_by_user_id, active_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)`,
		policy.PolicyID,
		policy.OrgID,
		policy.Name,
		policy.Version,
		policy.Source,
		policy.CreatedAt,
		createdBy,
		activeAt,
	)
	if err != nil {
		return core.RiskPolicy{}, fmt.Errorf("insert risk policy: %w", err)
	}
	return policy, nil
}

func (s *Store) FindActivePolicyForOrg(ctx context.Context, orgID string) (core.RiskPolicy, bool, error) {
	policy, err := scanRiskPolicy(s.db.QueryRowContext(
		ctx,
		riskPolicySelectColumns+` FROM risk_policies WHERE org_id = $1 AND active_at IS NOT NULL ORDER BY active_at DESC LIMIT 1`,
		orgID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.RiskPolicy{}, false, nil
		}
		return core.RiskPolicy{}, false, fmt.Errorf("find active risk policy: %w", err)
	}
	return policy, true, nil
}

func (s *Store) FindPolicyByID(ctx context.Context, policyID string) (core.RiskPolicy, bool, error) {
	policy, err := scanRiskPolicy(s.db.QueryRowContext(
		ctx,
		riskPolicySelectColumns+` FROM risk_policies WHERE policy_id = $1`,
		policyID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.RiskPolicy{}, false, nil
		}
		return core.RiskPolicy{}, false, fmt.Errorf("find risk policy by id: %w", err)
	}
	return policy, true, nil
}

func (s *Store) ListPoliciesForOrg(ctx context.Context, orgID string, limit, offset int) ([]core.RiskPolicy, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(
		ctx,
		riskPolicySelectColumns+` FROM risk_policies WHERE org_id = $1 ORDER BY version DESC LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list risk policies: %w", err)
	}
	defer rows.Close()

	policies := make([]core.RiskPolicy, 0)
	for rows.Next() {
		policy, err := scanRiskPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan risk policy: %w", err)
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate risk policies: %w", err)
	}
	return policies, nil
}

func (s *Store) ActivatePolicy(ctx context.Context, policyID string, activeAt time.Time) error {
	tx, err := s.rawDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin activate tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var orgID string
	if err := tx.QueryRowContext(ctx, `SELECT org_id FROM risk_policies WHERE policy_id = $1`, policyID).Scan(&orgID); err != nil {
		if err == sql.ErrNoRows {
			return storage.ErrRiskPolicyNotFound
		}
		return fmt.Errorf("lookup policy org: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE risk_policies SET active_at = NULL WHERE org_id = $1 AND active_at IS NOT NULL`, orgID); err != nil {
		return fmt.Errorf("deactivate prior policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE risk_policies SET active_at = $2 WHERE policy_id = $1`, policyID, activeAt); err != nil {
		return fmt.Errorf("activate policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit activate tx: %w", err)
	}
	return nil
}

func (s *Store) NextPolicyVersionForOrg(ctx context.Context, orgID string) (int, error) {
	var max sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(version) FROM risk_policies WHERE org_id = $1`, orgID,
	).Scan(&max); err != nil {
		return 0, fmt.Errorf("compute next policy version: %w", err)
	}
	if !max.Valid {
		return 1, nil
	}
	return int(max.Int64) + 1, nil
}

const riskPolicySelectColumns = `SELECT policy_id, org_id, name, version, source, created_at, created_by_user_id, active_at`

func scanRiskPolicy(row rowScanner) (core.RiskPolicy, error) {
	var (
		policy    core.RiskPolicy
		createdBy sql.NullString
		activeAt  sql.NullTime
		source    []byte
	)
	if err := row.Scan(&policy.PolicyID, &policy.OrgID, &policy.Name, &policy.Version, &source, &policy.CreatedAt, &createdBy, &activeAt); err != nil {
		return core.RiskPolicy{}, err
	}
	policy.Source = string(source)
	if createdBy.Valid {
		policy.CreatedByUserID = createdBy.String
	}
	if activeAt.Valid {
		t := activeAt.Time
		policy.ActiveAt = &t
	}
	return policy, nil
}
