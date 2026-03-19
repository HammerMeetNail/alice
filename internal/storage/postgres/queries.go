package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"alice/internal/core"
)

func (s *Store) SaveQuery(query core.Query) (core.Query, error) {
	requestedTypes, err := marshalArtifactTypes(query.RequestedTypes)
	if err != nil {
		return core.Query{}, fmt.Errorf("marshal query requested types: %w", err)
	}
	projectScope, err := marshalStringSlice(query.ProjectScope)
	if err != nil {
		return core.Query{}, fmt.Errorf("marshal query project scope: %w", err)
	}

	_, err = s.db.ExecContext(
		context.Background(),
		`INSERT INTO queries (
			query_id, org_id, from_agent_id, from_user_id, to_agent_id, to_user_id, purpose, question,
			requested_types, project_scope, time_window_start, time_window_end, risk_level, state, created_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9::jsonb, $10::jsonb, $11, $12, $13, $14, $15, $16
		)`,
		query.QueryID,
		query.OrgID,
		query.FromAgentID,
		query.FromUserID,
		query.ToAgentID,
		query.ToUserID,
		query.Purpose,
		query.Question,
		requestedTypes,
		projectScope,
		query.TimeWindow.Start,
		query.TimeWindow.End,
		query.RiskLevel,
		query.State,
		query.CreatedAt,
		query.ExpiresAt,
	)
	if err != nil {
		return core.Query{}, fmt.Errorf("insert query: %w", err)
	}
	return query, nil
}

func (s *Store) SaveQueryResponse(response core.QueryResponse) (core.QueryResponse, error) {
	artifacts, err := marshalQueryArtifacts(response.Artifacts)
	if err != nil {
		return core.QueryResponse{}, fmt.Errorf("marshal query artifacts: %w", err)
	}
	redactions, err := marshalStringSlice(response.Redactions)
	if err != nil {
		return core.QueryResponse{}, fmt.Errorf("marshal query redactions: %w", err)
	}
	policyBasis, err := marshalStringSlice(response.PolicyBasis)
	if err != nil {
		return core.QueryResponse{}, fmt.Errorf("marshal query policy basis: %w", err)
	}

	_, err = s.db.ExecContext(
		context.Background(),
		`INSERT INTO query_responses (
			response_id, query_id, from_agent_id, to_agent_id, artifacts, redactions, policy_basis, approval_state, confidence, created_at
		) VALUES (
			$1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8, $9, $10
		)
		ON CONFLICT (query_id) DO UPDATE
		SET response_id = EXCLUDED.response_id,
		    from_agent_id = EXCLUDED.from_agent_id,
		    to_agent_id = EXCLUDED.to_agent_id,
		    artifacts = EXCLUDED.artifacts,
		    redactions = EXCLUDED.redactions,
		    policy_basis = EXCLUDED.policy_basis,
		    approval_state = EXCLUDED.approval_state,
		    confidence = EXCLUDED.confidence,
		    created_at = EXCLUDED.created_at`,
		response.ResponseID,
		response.QueryID,
		response.FromAgentID,
		response.ToAgentID,
		artifacts,
		redactions,
		policyBasis,
		response.ApprovalState,
		response.Confidence,
		response.CreatedAt,
	)
	if err != nil {
		return core.QueryResponse{}, fmt.Errorf("upsert query response: %w", err)
	}
	return response, nil
}

func (s *Store) UpdateQueryState(queryID string, state core.QueryState) (core.Query, bool, error) {
	query, err := scanQueryRow(s.db.QueryRowContext(
		context.Background(),
		`UPDATE queries
		SET state = $2
		WHERE query_id = $1
		RETURNING query_id, org_id, from_agent_id, from_user_id, to_agent_id, to_user_id, purpose, question,
		          requested_types, project_scope, time_window_start, time_window_end, risk_level, state, created_at, expires_at`,
		queryID,
		state,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Query{}, false, nil
		}
		return core.Query{}, false, fmt.Errorf("update query state: %w", err)
	}
	return query, true, nil
}

func (s *Store) FindQuery(queryID string) (core.Query, bool, error) {
	query, err := scanQueryRow(s.db.QueryRowContext(
		context.Background(),
		`SELECT query_id, org_id, from_agent_id, from_user_id, to_agent_id, to_user_id, purpose, question,
		        requested_types, project_scope, time_window_start, time_window_end, risk_level, state, created_at, expires_at
		FROM queries
		WHERE query_id = $1`,
		queryID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Query{}, false, nil
		}
		return core.Query{}, false, fmt.Errorf("find query: %w", err)
	}
	return query, true, nil
}

func (s *Store) FindQueryResponse(queryID string) (core.QueryResponse, bool, error) {
	response, err := scanQueryResponseRow(s.db.QueryRowContext(
		context.Background(),
		`SELECT response_id, query_id, from_agent_id, to_agent_id, artifacts, redactions, policy_basis, approval_state, confidence, created_at
		FROM query_responses
		WHERE query_id = $1`,
		queryID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.QueryResponse{}, false, nil
		}
		return core.QueryResponse{}, false, fmt.Errorf("find query response: %w", err)
	}
	return response, true, nil
}

func scanQueryRow(scanner interface{ Scan(dest ...any) error }) (core.Query, error) {
	var (
		query              core.Query
		requestedTypesJSON []byte
		projectScopeJSON   []byte
	)

	if err := scanner.Scan(
		&query.QueryID,
		&query.OrgID,
		&query.FromAgentID,
		&query.FromUserID,
		&query.ToAgentID,
		&query.ToUserID,
		&query.Purpose,
		&query.Question,
		&requestedTypesJSON,
		&projectScopeJSON,
		&query.TimeWindow.Start,
		&query.TimeWindow.End,
		&query.RiskLevel,
		&query.State,
		&query.CreatedAt,
		&query.ExpiresAt,
	); err != nil {
		return core.Query{}, err
	}

	if err := unmarshalJSON(requestedTypesJSON, &query.RequestedTypes); err != nil {
		return core.Query{}, fmt.Errorf("decode query requested types: %w", err)
	}
	if err := unmarshalJSON(projectScopeJSON, &query.ProjectScope); err != nil {
		return core.Query{}, fmt.Errorf("decode query project scope: %w", err)
	}
	return query, nil
}

func scanQueryResponseRow(scanner interface{ Scan(dest ...any) error }) (core.QueryResponse, error) {
	var (
		response        core.QueryResponse
		artifactsJSON   []byte
		redactionsJSON  []byte
		policyBasisJSON []byte
	)

	if err := scanner.Scan(
		&response.ResponseID,
		&response.QueryID,
		&response.FromAgentID,
		&response.ToAgentID,
		&artifactsJSON,
		&redactionsJSON,
		&policyBasisJSON,
		&response.ApprovalState,
		&response.Confidence,
		&response.CreatedAt,
	); err != nil {
		return core.QueryResponse{}, err
	}

	if err := unmarshalJSON(artifactsJSON, &response.Artifacts); err != nil {
		return core.QueryResponse{}, fmt.Errorf("decode query response artifacts: %w", err)
	}
	if err := unmarshalJSON(redactionsJSON, &response.Redactions); err != nil {
		return core.QueryResponse{}, fmt.Errorf("decode query response redactions: %w", err)
	}
	if err := unmarshalJSON(policyBasisJSON, &response.PolicyBasis); err != nil {
		return core.QueryResponse{}, fmt.Errorf("decode query response policy basis: %w", err)
	}
	return response, nil
}
