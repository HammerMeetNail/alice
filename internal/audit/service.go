package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

// Sink receives audit events for external delivery. Implementations must be
// safe for concurrent use. Errors from Emit are logged but do not block the
// primary storage path.
type Sink interface {
	Emit(ctx context.Context, event core.AuditEvent) error
}

// JSONSink writes newline-delimited JSON audit events to the given writer.
type JSONSink struct {
	w   io.Writer
	enc *json.Encoder
}

// NewJSONSink creates a Sink that writes NDJSON to w.
func NewJSONSink(w io.Writer) *JSONSink {
	return &JSONSink{w: w, enc: json.NewEncoder(w)}
}

func (s *JSONSink) Emit(_ context.Context, event core.AuditEvent) error {
	return s.enc.Encode(event)
}

type Service struct {
	repo  storage.AuditRepository
	sinks []Sink
}

func NewService(repo storage.AuditRepository, sinks ...Sink) *Service {
	return &Service{repo: repo, sinks: sinks}
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

	for _, sink := range s.sinks {
		if sinkErr := sink.Emit(ctx, saved); sinkErr != nil {
			slog.Warn("audit sink emit failed", "sink", fmt.Sprintf("%T", sink), "err", sinkErr)
		}
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
