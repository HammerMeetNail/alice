package audit

import (
	"context"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

// SummaryFilter holds optional filter criteria for the audit summary.
type SummaryFilter struct {
	EventKind   string
	SubjectType string
	Decision    string
}

type Service struct {
	repo storage.AuditRepository
}

func NewService(repo storage.AuditRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Record(ctx context.Context, eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) (core.AuditEvent, error) {
	event := core.AuditEvent{
		AuditEventID:  id.New("audit"),
		OrgID:         orgID,
		EventKind:     eventKind,
		ActorAgentID:  actorAgentID,
		TargetAgentID: targetAgentID,
		SubjectType:   subjectType,
		SubjectID:     subjectID,
		PolicyBasis:   policyBasis,
		Decision:      decision,
		RiskLevel:     riskLevel,
		CreatedAt:     time.Now().UTC(),
		Metadata:      metadata,
	}
	saved, err := s.repo.AppendAuditEvent(ctx, event)
	if err != nil {
		return core.AuditEvent{}, fmt.Errorf("append audit event: %w", err)
	}
	return saved, nil
}

func (s *Service) Summary(ctx context.Context, agentID string, since time.Time, limit, offset int, filter SummaryFilter) ([]core.AuditEvent, error) {
	return s.repo.ListAuditEvents(ctx, storage.AuditFilter{
		AgentID:     agentID,
		Since:       since,
		EventKind:   filter.EventKind,
		SubjectType: filter.SubjectType,
		Decision:    filter.Decision,
		Limit:       limit,
		Offset:      offset,
	})
}
