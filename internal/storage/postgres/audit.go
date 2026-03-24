package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
)

func (s *Store) AppendAuditEvent(ctx context.Context, event core.AuditEvent) (core.AuditEvent, error) {
	policyBasis, err := marshalStringSlice(event.PolicyBasis)
	if err != nil {
		return core.AuditEvent{}, fmt.Errorf("marshal audit policy basis: %w", err)
	}
	metadata, err := marshalJSONObject(event.Metadata)
	if err != nil {
		return core.AuditEvent{}, fmt.Errorf("marshal audit metadata: %w", err)
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO audit_events (
			audit_event_id, org_id, event_kind, actor_agent_id, target_agent_id, subject_type, subject_id,
			policy_basis, decision, risk_level, created_at, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8::jsonb, $9, $10, $11, $12::jsonb
		)`,
		event.AuditEventID,
		event.OrgID,
		event.EventKind,
		nullString(event.ActorAgentID),
		nullString(event.TargetAgentID),
		event.SubjectType,
		event.SubjectID,
		policyBasis,
		event.Decision,
		nullString(string(event.RiskLevel)),
		event.CreatedAt,
		metadata,
	)
	if err != nil {
		return core.AuditEvent{}, fmt.Errorf("insert audit event: %w", err)
	}
	return event, nil
}

func (s *Store) ListAuditEvents(ctx context.Context, agentID string, since time.Time) ([]core.AuditEvent, error) {
	var sinceArg any
	if !since.IsZero() {
		sinceArg = since
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT audit_event_id, org_id, event_kind, actor_agent_id, target_agent_id, subject_type, subject_id,
		        policy_basis, decision, risk_level, created_at, metadata
		FROM audit_events
		WHERE ($1 = '' OR actor_agent_id = $1 OR target_agent_id = $1)
		  AND ($2::timestamptz IS NULL OR created_at >= $2)
		ORDER BY created_at ASC`,
		agentID,
		sinceArg,
	)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	events := make([]core.AuditEvent, 0)
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}

	return events, nil
}

func scanAuditEvent(scanner interface{ Scan(dest ...any) error }) (core.AuditEvent, error) {
	var (
		event           core.AuditEvent
		actorAgentID    sql.NullString
		targetAgentID   sql.NullString
		policyBasisJSON []byte
		riskLevel       sql.NullString
		metadataJSON    []byte
	)

	if err := scanner.Scan(
		&event.AuditEventID,
		&event.OrgID,
		&event.EventKind,
		&actorAgentID,
		&targetAgentID,
		&event.SubjectType,
		&event.SubjectID,
		&policyBasisJSON,
		&event.Decision,
		&riskLevel,
		&event.CreatedAt,
		&metadataJSON,
	); err != nil {
		return core.AuditEvent{}, fmt.Errorf("scan audit event: %w", err)
	}

	event.ActorAgentID = actorAgentID.String
	event.TargetAgentID = targetAgentID.String
	event.RiskLevel = core.RiskLevel(riskLevel.String)

	if err := unmarshalJSON(policyBasisJSON, &event.PolicyBasis); err != nil {
		return core.AuditEvent{}, fmt.Errorf("decode audit policy basis: %w", err)
	}
	if err := unmarshalJSON(metadataJSON, &event.Metadata); err != nil {
		return core.AuditEvent{}, fmt.Errorf("decode audit metadata: %w", err)
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	return event, nil
}
