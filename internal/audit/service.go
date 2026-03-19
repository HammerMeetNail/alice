package audit

import (
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

type Service struct {
	store *memory.Store
}

func NewService(store *memory.Store) *Service {
	return &Service{store: store}
}

func (s *Service) Record(eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) core.AuditEvent {
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
	return s.store.AppendAuditEvent(event)
}

func (s *Service) Summary(agentID string, since time.Time) []core.AuditEvent {
	return s.store.ListAuditEvents(agentID, since)
}
