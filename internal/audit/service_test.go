package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"alice/internal/audit"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

func TestRecord(t *testing.T) {
	svc := audit.NewService(memory.New())
	ctx := context.Background()

	orgID := id.New("org")
	actorID := id.New("agent")

	event, err := svc.Record(ctx, "query.evaluated", "query", id.New("query"),
		orgID, actorID, id.New("agent"), "allowed",
		core.RiskLevelL1, []string{"grant_123"}, nil)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if event.AuditEventID == "" {
		t.Fatal("expected non-empty AuditEventID")
	}
	if event.OrgID != orgID {
		t.Fatalf("OrgID mismatch: got %s want %s", event.OrgID, orgID)
	}
	if event.ActorAgentID != actorID {
		t.Fatalf("ActorAgentID mismatch: got %s want %s", event.ActorAgentID, actorID)
	}
	if event.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
}

func TestSummary(t *testing.T) {
	svc := audit.NewService(memory.New())
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")

	for i := 0; i < 3; i++ {
		if _, err := svc.Record(ctx, "query.evaluated", "query", id.New("query"),
			orgID, agentID, id.New("agent"), "allowed", core.RiskLevelL0, nil, nil); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	events, err := svc.Summary(ctx, agentID, time.Time{}, 50, 0, audit.SummaryFilter{})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
}

func TestSummary_SinceFilter(t *testing.T) {
	store := memory.New()
	svc := audit.NewService(store)
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")

	// Record an event with an old timestamp by inserting directly.
	oldEvent := core.AuditEvent{
		AuditEventID:  id.New("audit"),
		OrgID:         orgID,
		ActorAgentID:  agentID,
		EventKind:     "old.event",
		SubjectType:   "query",
		SubjectID:     id.New("query"),
		Decision:      "allowed",
		CreatedAt:     time.Now().UTC().Add(-2 * time.Hour),
	}
	store.AppendAuditEvent(ctx, oldEvent)

	// Record a recent event via the service.
	if _, err := svc.Record(ctx, "recent.event", "query", id.New("query"),
		orgID, agentID, "", "allowed", core.RiskLevelL0, nil, nil); err != nil {
		t.Fatalf("Record: %v", err)
	}

	since := time.Now().UTC().Add(-30 * time.Minute)
	events, err := svc.Summary(ctx, agentID, since, 50, 0, audit.SummaryFilter{})
	if err != nil {
		t.Fatalf("Summary with since: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 recent event, got %d", len(events))
	}
	if events[0].EventKind != "recent.event" {
		t.Fatalf("unexpected event kind: %s", events[0].EventKind)
	}
}

func TestSummary_FilterByEventKind(t *testing.T) {
	svc := audit.NewService(memory.New())
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")

	svc.Record(ctx, "query.evaluated", "query", id.New("q"), orgID, agentID, "", "allowed", core.RiskLevelL0, nil, nil)
	svc.Record(ctx, "agent.registered", "agent", id.New("a"), orgID, agentID, "", "allowed", core.RiskLevelL0, nil, nil)
	svc.Record(ctx, "query.evaluated", "query", id.New("q2"), orgID, agentID, "", "denied", core.RiskLevelL0, nil, nil)

	events, err := svc.Summary(ctx, agentID, time.Time{}, 50, 0, audit.SummaryFilter{EventKind: "query.evaluated"})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 query events, got %d", len(events))
	}

	events, err = svc.Summary(ctx, agentID, time.Time{}, 50, 0, audit.SummaryFilter{Decision: "denied"})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 denied event, got %d", len(events))
	}

	events, err = svc.Summary(ctx, agentID, time.Time{}, 50, 0, audit.SummaryFilter{SubjectType: "agent"})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 agent event, got %d", len(events))
	}
}

func TestJSONSink(t *testing.T) {
	var buf bytes.Buffer
	sink := audit.NewJSONSink(&buf)

	event := core.AuditEvent{
		AuditEventID: "audit_test",
		EventKind:    "test.event",
		OrgID:        "org_1",
		ActorAgentID: "agent_1",
		Decision:     "allowed",
		CreatedAt:    time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
	}

	if err := sink.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "test.event") {
		t.Fatalf("expected event kind in output, got %q", output)
	}

	var decoded core.AuditEvent
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode NDJSON line: %v", err)
	}
	if decoded.AuditEventID != "audit_test" {
		t.Fatalf("AuditEventID mismatch: %s", decoded.AuditEventID)
	}
}

func TestRecord_WithSink(t *testing.T) {
	var buf bytes.Buffer
	sink := audit.NewJSONSink(&buf)
	svc := audit.NewService(memory.New(), sink)
	ctx := context.Background()

	_, err := svc.Record(ctx, "test.event", "query", id.New("query"),
		id.New("org"), id.New("agent"), "", "allowed", core.RiskLevelL0, nil, nil)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("expected sink to receive event")
	}
	if !strings.Contains(buf.String(), "test.event") {
		t.Fatalf("expected event kind in sink output")
	}
}
