package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
)

func (s *Store) SaveRequest(ctx context.Context, request core.Request) (core.Request, error) {
	structuredPayload, err := marshalJSONObject(request.StructuredPayload)
	if err != nil {
		return core.Request{}, fmt.Errorf("marshal request structured payload: %w", err)
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO requests (
			request_id, org_id, from_agent_id, from_user_id, to_agent_id, to_user_id, request_type, title, content,
			structured_payload, risk_level, state, approval_state, response_message, created_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10::jsonb, $11, $12, $13, $14, $15, $16
		)
		ON CONFLICT (request_id) DO UPDATE
		SET org_id = EXCLUDED.org_id,
		    from_agent_id = EXCLUDED.from_agent_id,
		    from_user_id = EXCLUDED.from_user_id,
		    to_agent_id = EXCLUDED.to_agent_id,
		    to_user_id = EXCLUDED.to_user_id,
		    request_type = EXCLUDED.request_type,
		    title = EXCLUDED.title,
		    content = EXCLUDED.content,
		    structured_payload = EXCLUDED.structured_payload,
		    risk_level = EXCLUDED.risk_level,
		    state = EXCLUDED.state,
		    approval_state = EXCLUDED.approval_state,
		    response_message = EXCLUDED.response_message,
		    created_at = EXCLUDED.created_at,
		    expires_at = EXCLUDED.expires_at`,
		request.RequestID,
		request.OrgID,
		request.FromAgentID,
		request.FromUserID,
		request.ToAgentID,
		request.ToUserID,
		request.RequestType,
		request.Title,
		request.Content,
		structuredPayload,
		request.RiskLevel,
		request.State,
		request.ApprovalState,
		nullString(request.ResponseMessage),
		request.CreatedAt,
		request.ExpiresAt,
	)
	if err != nil {
		return core.Request{}, fmt.Errorf("upsert request: %w", err)
	}
	return request, nil
}

func (s *Store) FindRequest(ctx context.Context, requestID string) (core.Request, bool, error) {
	request, err := scanRequestRow(s.db.QueryRowContext(
		ctx,
		`SELECT request_id, org_id, from_agent_id, from_user_id, to_agent_id, to_user_id, request_type, title, content,
		        structured_payload, risk_level, state, approval_state, response_message, created_at, expires_at
		FROM requests
		WHERE request_id = $1`,
		requestID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Request{}, false, nil
		}
		return core.Request{}, false, fmt.Errorf("find request: %w", err)
	}
	return request, true, nil
}

func (s *Store) ListIncomingRequests(ctx context.Context, toAgentID string, limit, offset int) ([]core.Request, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT request_id, org_id, from_agent_id, from_user_id, to_agent_id, to_user_id, request_type, title, content,
		        structured_payload, risk_level, state, approval_state, response_message, created_at, expires_at
		FROM requests
		WHERE to_agent_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3`,
		toAgentID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query incoming requests: %w", err)
	}
	defer rows.Close()

	requests := make([]core.Request, 0)
	for rows.Next() {
		request, err := scanRequestRow(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate requests: %w", err)
	}
	return requests, nil
}

func (s *Store) UpdateRequestState(ctx context.Context, requestID string, state core.RequestState, approvalState core.ApprovalState, responseMessage string) (core.Request, bool, error) {
	request, err := scanRequestRow(s.db.QueryRowContext(
		ctx,
		`UPDATE requests
		SET state = $2,
		    approval_state = $3,
		    response_message = $4
		WHERE request_id = $1
		RETURNING request_id, org_id, from_agent_id, from_user_id, to_agent_id, to_user_id, request_type, title, content,
		          structured_payload, risk_level, state, approval_state, response_message, created_at, expires_at`,
		requestID,
		state,
		approvalState,
		nullString(responseMessage),
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Request{}, false, nil
		}
		return core.Request{}, false, fmt.Errorf("update request state: %w", err)
	}
	return request, true, nil
}

func (s *Store) SaveApproval(ctx context.Context, approval core.Approval) (core.Approval, error) {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO approvals (
			approval_id, org_id, agent_id, owner_user_id, subject_type, subject_id, reason, state, created_at, expires_at, resolved_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
		ON CONFLICT (approval_id) DO UPDATE
		SET org_id = EXCLUDED.org_id,
		    agent_id = EXCLUDED.agent_id,
		    owner_user_id = EXCLUDED.owner_user_id,
		    subject_type = EXCLUDED.subject_type,
		    subject_id = EXCLUDED.subject_id,
		    reason = EXCLUDED.reason,
		    state = EXCLUDED.state,
		    created_at = EXCLUDED.created_at,
		    expires_at = EXCLUDED.expires_at,
		    resolved_at = EXCLUDED.resolved_at`,
		approval.ApprovalID,
		approval.OrgID,
		approval.AgentID,
		approval.OwnerUserID,
		approval.SubjectType,
		approval.SubjectID,
		approval.Reason,
		approval.State,
		approval.CreatedAt,
		approval.ExpiresAt,
		nullTimePtr(approval.ResolvedAt),
	)
	if err != nil {
		return core.Approval{}, fmt.Errorf("upsert approval: %w", err)
	}
	return approval, nil
}

func (s *Store) FindApproval(ctx context.Context, approvalID string) (core.Approval, bool, error) {
	approval, err := scanApprovalRow(s.db.QueryRowContext(
		ctx,
		`SELECT approval_id, org_id, agent_id, owner_user_id, subject_type, subject_id, reason, state, created_at, expires_at, resolved_at
		FROM approvals
		WHERE approval_id = $1`,
		approvalID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Approval{}, false, nil
		}
		return core.Approval{}, false, fmt.Errorf("find approval: %w", err)
	}
	return approval, true, nil
}

func (s *Store) ListPendingApprovals(ctx context.Context, agentID string, limit, offset int) ([]core.Approval, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT approval_id, org_id, agent_id, owner_user_id, subject_type, subject_id, reason, state, created_at, expires_at, resolved_at
		FROM approvals
		WHERE agent_id = $1 AND state = $2 AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at ASC
		LIMIT $3 OFFSET $4`,
		agentID,
		core.ApprovalStatePending,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending approvals: %w", err)
	}
	defer rows.Close()

	approvals := make([]core.Approval, 0)
	for rows.Next() {
		approval, err := scanApprovalRow(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate approvals: %w", err)
	}
	return approvals, nil
}

func (s *Store) ResolveApproval(ctx context.Context, approvalID string, state core.ApprovalState, resolvedAt time.Time) (core.Approval, bool, error) {
	approval, err := scanApprovalRow(s.db.QueryRowContext(
		ctx,
		`UPDATE approvals
		SET state = $2,
		    resolved_at = $3
		WHERE approval_id = $1 AND state = 'pending'
		RETURNING approval_id, org_id, agent_id, owner_user_id, subject_type, subject_id, reason, state, created_at, expires_at, resolved_at`,
		approvalID,
		state,
		resolvedAt,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Approval{}, false, nil
		}
		return core.Approval{}, false, fmt.Errorf("resolve approval: %w", err)
	}
	return approval, true, nil
}

func scanRequestRow(scanner interface{ Scan(dest ...any) error }) (core.Request, error) {
	var (
		request         core.Request
		structuredJSON  []byte
		responseMessage sql.NullString
	)

	if err := scanner.Scan(
		&request.RequestID,
		&request.OrgID,
		&request.FromAgentID,
		&request.FromUserID,
		&request.ToAgentID,
		&request.ToUserID,
		&request.RequestType,
		&request.Title,
		&request.Content,
		&structuredJSON,
		&request.RiskLevel,
		&request.State,
		&request.ApprovalState,
		&responseMessage,
		&request.CreatedAt,
		&request.ExpiresAt,
	); err != nil {
		return core.Request{}, err
	}

	if err := unmarshalJSON(structuredJSON, &request.StructuredPayload); err != nil {
		return core.Request{}, fmt.Errorf("decode request structured payload: %w", err)
	}
	if request.StructuredPayload == nil {
		request.StructuredPayload = map[string]any{}
	}
	request.ResponseMessage = responseMessage.String
	return request, nil
}

func scanApprovalRow(scanner interface{ Scan(dest ...any) error }) (core.Approval, error) {
	var (
		approval   core.Approval
		resolvedAt sql.NullTime
	)

	if err := scanner.Scan(
		&approval.ApprovalID,
		&approval.OrgID,
		&approval.AgentID,
		&approval.OwnerUserID,
		&approval.SubjectType,
		&approval.SubjectID,
		&approval.Reason,
		&approval.State,
		&approval.CreatedAt,
		&approval.ExpiresAt,
		&resolvedAt,
	); err != nil {
		return core.Approval{}, err
	}

	if resolvedAt.Valid {
		approval.ResolvedAt = &resolvedAt.Time
	}
	return approval, nil
}
