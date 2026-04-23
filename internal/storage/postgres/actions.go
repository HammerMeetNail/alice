package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) SaveAction(ctx context.Context, action core.Action) (core.Action, error) {
	inputs, err := json.Marshal(valueOrEmptyMap(action.Inputs))
	if err != nil {
		return core.Action{}, fmt.Errorf("marshal action inputs: %w", err)
	}
	result, err := json.Marshal(valueOrEmptyMap(action.Result))
	if err != nil {
		return core.Action{}, fmt.Errorf("marshal action result: %w", err)
	}

	var requestID sql.NullString
	if strings.TrimSpace(action.RequestID) != "" {
		requestID = sql.NullString{String: action.RequestID, Valid: true}
	}
	var executedAt sql.NullTime
	if action.ExecutedAt != nil {
		executedAt = sql.NullTime{Time: *action.ExecutedAt, Valid: true}
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO actions (action_id, org_id, request_id, owner_user_id, owner_agent_id, kind, inputs, risk_level, state, result, failure_reason, created_at, expires_at, executed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10::jsonb, $11, $12, $13, $14)
		ON CONFLICT (action_id) DO UPDATE
		SET inputs = EXCLUDED.inputs,
		    state = EXCLUDED.state,
		    result = EXCLUDED.result,
		    failure_reason = EXCLUDED.failure_reason,
		    executed_at = EXCLUDED.executed_at`,
		action.ActionID,
		action.OrgID,
		requestID,
		action.OwnerUserID,
		action.OwnerAgentID,
		string(action.Kind),
		inputs,
		string(action.RiskLevel),
		string(action.State),
		result,
		action.FailureReason,
		action.CreatedAt,
		action.ExpiresAt,
		executedAt,
	)
	if err != nil {
		return core.Action{}, fmt.Errorf("save action: %w", err)
	}
	return action, nil
}

func (s *Store) FindActionByID(ctx context.Context, actionID string) (core.Action, bool, error) {
	action, err := scanAction(s.db.QueryRowContext(
		ctx,
		actionSelectColumns+` FROM actions WHERE action_id = $1`,
		actionID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Action{}, false, nil
		}
		return core.Action{}, false, fmt.Errorf("find action by id: %w", err)
	}
	return action, true, nil
}

func (s *Store) ListActions(ctx context.Context, filter storage.ActionFilter) ([]core.Action, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	conditions := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if filter.OwnerUserID != "" {
		args = append(args, filter.OwnerUserID)
		conditions = append(conditions, fmt.Sprintf("owner_user_id = $%d", len(args)))
	}
	if filter.State != "" {
		args = append(args, string(filter.State))
		conditions = append(conditions, fmt.Sprintf("state = $%d", len(args)))
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf("%s FROM actions%s ORDER BY created_at DESC LIMIT $%d OFFSET $%d",
			actionSelectColumns, where, len(args)-1, len(args)),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	defer rows.Close()

	actions := make([]core.Action, 0)
	for rows.Next() {
		action, err := scanAction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan action: %w", err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate actions: %w", err)
	}
	return actions, nil
}

func (s *Store) UpdateActionState(ctx context.Context, action core.Action) (core.Action, error) {
	tx, err := s.rawDB.BeginTx(ctx, nil)
	if err != nil {
		return core.Action{}, fmt.Errorf("begin update-action tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentState string
	err = tx.QueryRowContext(ctx, `SELECT state FROM actions WHERE action_id = $1 FOR UPDATE`, action.ActionID).Scan(&currentState)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Action{}, storage.ErrActionNotFound
		}
		return core.Action{}, fmt.Errorf("lookup action state: %w", err)
	}
	switch core.ActionState(currentState) {
	case core.ActionStateExecuted, core.ActionStateFailed, core.ActionStateCancelled, core.ActionStateExpired:
		return core.Action{}, storage.ErrActionInTerminalState
	}

	result, err := json.Marshal(valueOrEmptyMap(action.Result))
	if err != nil {
		return core.Action{}, fmt.Errorf("marshal action result: %w", err)
	}
	inputs, err := json.Marshal(valueOrEmptyMap(action.Inputs))
	if err != nil {
		return core.Action{}, fmt.Errorf("marshal action inputs: %w", err)
	}
	var executedAt sql.NullTime
	if action.ExecutedAt != nil {
		executedAt = sql.NullTime{Time: *action.ExecutedAt, Valid: true}
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE actions
		SET state = $2, result = $3::jsonb, inputs = $4::jsonb, failure_reason = $5, executed_at = $6
		WHERE action_id = $1`,
		action.ActionID,
		string(action.State),
		result,
		inputs,
		action.FailureReason,
		executedAt,
	); err != nil {
		return core.Action{}, fmt.Errorf("update action state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return core.Action{}, fmt.Errorf("commit update-action tx: %w", err)
	}
	return action, nil
}

// --- User preferences ---

func (s *Store) SetOperatorEnabled(ctx context.Context, userID string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET operator_enabled = $2 WHERE user_id = $1`, userID, enabled)
	if err != nil {
		return fmt.Errorf("set operator_enabled: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

const actionSelectColumns = `SELECT action_id, org_id, request_id, owner_user_id, owner_agent_id, kind, inputs, risk_level, state, result, failure_reason, created_at, expires_at, executed_at`

func scanAction(row rowScanner) (core.Action, error) {
	var (
		action     core.Action
		requestID  sql.NullString
		inputs     []byte
		result     []byte
		executedAt sql.NullTime
	)
	if err := row.Scan(
		&action.ActionID,
		&action.OrgID,
		&requestID,
		&action.OwnerUserID,
		&action.OwnerAgentID,
		&action.Kind,
		&inputs,
		&action.RiskLevel,
		&action.State,
		&result,
		&action.FailureReason,
		&action.CreatedAt,
		&action.ExpiresAt,
		&executedAt,
	); err != nil {
		return core.Action{}, err
	}
	if requestID.Valid {
		action.RequestID = requestID.String
	}
	if executedAt.Valid {
		t := executedAt.Time
		action.ExecutedAt = &t
	}
	if len(inputs) > 0 {
		if err := json.Unmarshal(inputs, &action.Inputs); err != nil {
			return core.Action{}, fmt.Errorf("decode inputs: %w", err)
		}
	}
	if len(result) > 0 {
		if err := json.Unmarshal(result, &action.Result); err != nil {
			return core.Action{}, fmt.Errorf("decode result: %w", err)
		}
	}
	return action, nil
}

func valueOrEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
