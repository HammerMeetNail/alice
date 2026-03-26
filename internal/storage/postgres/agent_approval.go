package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) SaveAgentApproval(ctx context.Context, approval core.AgentApproval) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO agent_approvals (approval_id, agent_id, org_id, requested_at, reviewed_by, reviewed_at, decision, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (approval_id) DO UPDATE
		SET reviewed_by = EXCLUDED.reviewed_by,
		    reviewed_at = EXCLUDED.reviewed_at,
		    decision = EXCLUDED.decision,
		    reason = EXCLUDED.reason`,
		approval.ApprovalID,
		approval.AgentID,
		approval.OrgID,
		approval.RequestedAt,
		nullString(approval.ReviewedBy),
		nullTime(approval.ReviewedAt),
		nullString(approval.Decision),
		nullString(approval.Reason),
	)
	if err != nil {
		return fmt.Errorf("save agent approval: %w", err)
	}
	return nil
}

func (s *Store) FindPendingAgentApprovals(ctx context.Context, orgID string, limit, offset int) ([]core.AgentApproval, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT approval_id, agent_id, org_id, requested_at, reviewed_by, reviewed_at, decision, reason
		FROM agent_approvals
		WHERE org_id = $1 AND (decision IS NULL OR decision = '')
		ORDER BY requested_at ASC
		LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("find pending agent approvals: %w", err)
	}
	defer rows.Close()

	approvals := make([]core.AgentApproval, 0)
	for rows.Next() {
		var (
			a          core.AgentApproval
			reviewedBy sql.NullString
			reviewedAt sql.NullTime
			decision   sql.NullString
			reason     sql.NullString
		)
		if err := rows.Scan(&a.ApprovalID, &a.AgentID, &a.OrgID, &a.RequestedAt, &reviewedBy, &reviewedAt, &decision, &reason); err != nil {
			return nil, fmt.Errorf("scan agent approval: %w", err)
		}
		a.ReviewedBy = reviewedBy.String
		if reviewedAt.Valid {
			a.ReviewedAt = &reviewedAt.Time
		}
		a.Decision = decision.String
		a.Reason = reason.String
		approvals = append(approvals, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent approvals: %w", err)
	}

	sort.SliceStable(approvals, func(i, j int) bool {
		return approvals[i].RequestedAt.Before(approvals[j].RequestedAt)
	})
	return approvals, nil
}

func (s *Store) FindAgentApprovalByAgentID(ctx context.Context, agentID string) (core.AgentApproval, error) {
	var (
		a          core.AgentApproval
		reviewedBy sql.NullString
		reviewedAt sql.NullTime
		decision   sql.NullString
		reason     sql.NullString
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT approval_id, agent_id, org_id, requested_at, reviewed_by, reviewed_at, decision, reason
		FROM agent_approvals
		WHERE agent_id = $1
		ORDER BY requested_at DESC
		LIMIT 1`,
		agentID,
	).Scan(&a.ApprovalID, &a.AgentID, &a.OrgID, &a.RequestedAt, &reviewedBy, &reviewedAt, &decision, &reason)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.AgentApproval{}, storage.ErrAgentApprovalNotFound
		}
		return core.AgentApproval{}, fmt.Errorf("find agent approval by agent id: %w", err)
	}
	a.ReviewedBy = reviewedBy.String
	if reviewedAt.Valid {
		a.ReviewedAt = &reviewedAt.Time
	}
	a.Decision = decision.String
	a.Reason = reason.String
	return a, nil
}

func (s *Store) UpdateAgentApproval(ctx context.Context, approvalID, decision, reason, reviewedBy string, reviewedAt time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE agent_approvals SET decision = $2, reason = $3, reviewed_by = $4, reviewed_at = $5 WHERE approval_id = $1`,
		approvalID, decision, reason, reviewedBy, reviewedAt,
	)
	if err != nil {
		return fmt.Errorf("update agent approval: %w", err)
	}
	return nil
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
