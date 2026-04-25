package requests_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/requests"
	"alice/internal/storage/memory"
)

func newService() *requests.Service {
	store := memory.New()
	return requests.NewService(store, store, store)
}

func makeRequest(fromAgentID, toAgentID, orgID string) core.Request {
	return core.Request{
		OrgID:       orgID,
		FromAgentID: fromAgentID,
		ToAgentID:   toAgentID,
		RequestType: "information",
		Title:       "How is the project going?",
		Content:     "Please share your latest status.",
	}
}

func TestSend(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	fromAgent := id.New("agent")
	toAgent := id.New("agent")

	saved, err := svc.Send(ctx, makeRequest(fromAgent, toAgent, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if saved.RequestID == "" {
		t.Fatal("expected non-empty RequestID")
	}
	if saved.State != core.RequestStatePending {
		t.Fatalf("expected pending state, got %s", saved.State)
	}
	if saved.ExpiresAt.IsZero() {
		t.Fatal("expected ExpiresAt to be set")
	}
	if saved.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
}

type fakeAnswerer struct {
	result requests.AutoAnswerResult
}

func (f *fakeAnswerer) Evaluate(_ context.Context, _ core.Request) requests.AutoAnswerResult {
	return f.result
}

type recordedAudit struct {
	eventKind     string
	subjectID     string
	actorAgentID  string
	targetAgentID string
	metadata      map[string]any
}

type fakeAuditRecorder struct {
	events []recordedAudit
}

func (f *fakeAuditRecorder) Record(_ context.Context, eventKind, _, subjectID, _, actorAgentID, targetAgentID, _ string, _ core.RiskLevel, _ []string, metadata map[string]any) (core.AuditEvent, error) {
	f.events = append(f.events, recordedAudit{
		eventKind:     eventKind,
		subjectID:     subjectID,
		actorAgentID:  actorAgentID,
		targetAgentID: targetAgentID,
		metadata:      metadata,
	})
	return core.AuditEvent{}, nil
}

// TestSend_AutoAnsweredEmitsAuditEvent confirms that when the gatekeeper
// auto-answers a request, the requests service records a
// `request.auto_answered` audit event with the supporting artifact IDs and
// aggregate confidence. Without the audit trail the gatekeeper would be
// acting on the user's behalf invisibly.
func TestSend_AutoAnsweredEmitsAuditEvent(t *testing.T) {
	store := memory.New()
	answerer := &fakeAnswerer{result: requests.AutoAnswerResult{
		Answered:    true,
		Summary:     "Auto-answered from 1 derived artifact.",
		ArtifactIDs: []string{"artifact_abc", "artifact_def"},
		Confidence:  0.82,
	}}
	audit := &fakeAuditRecorder{}
	svc := requests.NewService(store, store, store).
		WithAutoAnswerer(answerer).
		WithAuditRecorder(audit)

	ctx := context.Background()
	orgID := id.New("org")
	fromAgent := id.New("agent")
	toAgent := id.New("agent")

	saved, err := svc.Send(ctx, makeRequest(fromAgent, toAgent, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if saved.State != core.RequestStateAutoAnswered {
		t.Fatalf("expected auto_answered, got %s", saved.State)
	}
	if len(audit.events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(audit.events))
	}
	event := audit.events[0]
	if event.eventKind != "request.auto_answered" {
		t.Fatalf("unexpected event kind %q", event.eventKind)
	}
	if event.actorAgentID != toAgent {
		t.Fatalf("expected actor=recipient agent %q, got %q", toAgent, event.actorAgentID)
	}
	if event.targetAgentID != fromAgent {
		t.Fatalf("expected target=sender agent %q, got %q", fromAgent, event.targetAgentID)
	}
	if event.subjectID != saved.RequestID {
		t.Fatalf("expected subject=request id %q, got %q", saved.RequestID, event.subjectID)
	}
	if event.metadata["artifact_count"].(int) != 2 {
		t.Fatalf("expected artifact_count=2, got %v", event.metadata["artifact_count"])
	}
	if conf, _ := event.metadata["confidence"].(float64); conf != 0.82 {
		t.Fatalf("expected confidence=0.82, got %v", event.metadata["confidence"])
	}
}

// TestSend_AutoAnswerDeferredSkipsAuditEvent confirms no
// `request.auto_answered` event is emitted when the gatekeeper declines to
// answer.
func TestSend_AutoAnswerDeferredSkipsAuditEvent(t *testing.T) {
	store := memory.New()
	audit := &fakeAuditRecorder{}
	svc := requests.NewService(store, store, store).
		WithAutoAnswerer(&fakeAnswerer{result: requests.AutoAnswerResult{Answered: false, Reason: "no grant"}}).
		WithAuditRecorder(audit)

	ctx := context.Background()
	saved, err := svc.Send(ctx, makeRequest(id.New("agent"), id.New("agent"), id.New("org")))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if saved.State != core.RequestStatePending {
		t.Fatalf("expected pending state, got %s", saved.State)
	}
	if len(audit.events) != 0 {
		t.Fatalf("expected no audit events, got %d", len(audit.events))
	}
}

func TestListIncoming(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	recipient := id.New("agent")
	sender := id.New("agent")

	// Send two requests to recipient, one to someone else.
	if _, err := svc.Send(ctx, makeRequest(sender, recipient, orgID)); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if _, err := svc.Send(ctx, makeRequest(sender, recipient, orgID)); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	if _, err := svc.Send(ctx, makeRequest(sender, id.New("agent"), orgID)); err != nil {
		t.Fatalf("Send other: %v", err)
	}

	list, err := svc.ListIncoming(ctx, recipient, 50, 0)
	if err != nil {
		t.Fatalf("ListIncoming: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 incoming requests, got %d", len(list))
	}
}

func TestListSent(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	sender := id.New("agent")

	if _, err := svc.Send(ctx, makeRequest(sender, id.New("agent"), orgID)); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if _, err := svc.Send(ctx, makeRequest(sender, id.New("agent"), orgID)); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	// Different sender — should not appear.
	if _, err := svc.Send(ctx, makeRequest(id.New("agent"), id.New("agent"), orgID)); err != nil {
		t.Fatalf("Send other: %v", err)
	}

	list, err := svc.ListSent(ctx, sender, 50, 0)
	if err != nil {
		t.Fatalf("ListSent: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 sent requests, got %d", len(list))
	}
}

func TestRespond_Accept(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	agent := core.Agent{AgentID: toAgentID, OrgID: orgID}

	sent, err := svc.Send(ctx, makeRequest(id.New("agent"), toAgentID, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	updated, approval, err := svc.Respond(ctx, agent, sent.RequestID, core.RequestResponseAccept, "Sounds good")
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if approval != nil {
		t.Fatal("expected no approval for direct accept")
	}
	if updated.State != core.RequestStateAccepted {
		t.Fatalf("expected accepted state, got %s", updated.State)
	}
}

func TestRespond_Deny(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	agent := core.Agent{AgentID: toAgentID, OrgID: orgID}

	sent, err := svc.Send(ctx, makeRequest(id.New("agent"), toAgentID, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	updated, _, err := svc.Respond(ctx, agent, sent.RequestID, core.RequestResponseDeny, "Not now")
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if updated.State != core.RequestStateDenied {
		t.Fatalf("expected denied state, got %s", updated.State)
	}
}

func TestRespond_AlreadyClosed(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	agent := core.Agent{AgentID: toAgentID, OrgID: orgID}

	sent, err := svc.Send(ctx, makeRequest(id.New("agent"), toAgentID, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, _, err := svc.Respond(ctx, agent, sent.RequestID, core.RequestResponseAccept, ""); err != nil {
		t.Fatalf("first Respond: %v", err)
	}

	_, _, err = svc.Respond(ctx, agent, sent.RequestID, core.RequestResponseDeny, "")
	if !errors.Is(err, requests.ErrRequestAlreadyClosed) {
		t.Fatalf("expected ErrRequestAlreadyClosed, got %v", err)
	}
}

func TestRespond_NotRecipient(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	sent, err := svc.Send(ctx, makeRequest(id.New("agent"), id.New("agent"), orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	wrongAgent := core.Agent{AgentID: id.New("agent"), OrgID: orgID}
	_, _, err = svc.Respond(ctx, wrongAgent, sent.RequestID, core.RequestResponseAccept, "")
	if !errors.Is(err, requests.ErrRequestNotVisible) {
		t.Fatalf("expected ErrRequestNotVisible, got %v", err)
	}
}

func TestRespond_NotFound(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org")}
	_, _, err := svc.Respond(ctx, agent, "nonexistent", core.RequestResponseAccept, "")
	if !errors.Is(err, requests.ErrUnknownRequest) {
		t.Fatalf("expected ErrUnknownRequest, got %v", err)
	}
}

func TestRespond_RequireApproval(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	agent := core.Agent{AgentID: toAgentID, OrgID: orgID, OwnerUserID: id.New("user")}

	sent, err := svc.Send(ctx, makeRequest(id.New("agent"), toAgentID, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	updated, approval, err := svc.Respond(ctx, agent, sent.RequestID, core.RequestResponseRequireApproval, "need user consent")
	if err != nil {
		t.Fatalf("Respond with require_approval: %v", err)
	}
	if approval == nil {
		t.Fatal("expected an approval record to be created")
	}
	if approval.ApprovalID == "" {
		t.Fatal("expected non-empty ApprovalID")
	}
	if approval.SubjectID != sent.RequestID {
		t.Fatalf("approval SubjectID mismatch: got %s want %s", approval.SubjectID, sent.RequestID)
	}
	if updated.ApprovalState != core.ApprovalStatePending {
		t.Fatalf("expected request approval state pending, got %s", updated.ApprovalState)
	}
}

func newServiceWithStore() (*requests.Service, *memory.Store) {
	store := memory.New()
	return requests.NewService(store, store, store), store
}

func TestRespond_Defer(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	agent := core.Agent{AgentID: toAgentID, OrgID: orgID}

	sent, err := svc.Send(ctx, makeRequest(id.New("agent"), toAgentID, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	updated, approval, err := svc.Respond(ctx, agent, sent.RequestID, core.RequestResponseDefer, "deferring for now")
	if err != nil {
		t.Fatalf("Respond Defer: %v", err)
	}
	if approval != nil {
		t.Fatal("expected no approval for defer")
	}
	if updated.State != core.RequestStateDeferred {
		t.Fatalf("expected deferred state, got %s", updated.State)
	}
}

func TestRespond_Complete(t *testing.T) {
	svc := newService()
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	agent := core.Agent{AgentID: toAgentID, OrgID: orgID}

	sent, err := svc.Send(ctx, makeRequest(id.New("agent"), toAgentID, orgID))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	updated, approval, err := svc.Respond(ctx, agent, sent.RequestID, core.RequestResponseComplete, "all done")
	if err != nil {
		t.Fatalf("Respond Complete: %v", err)
	}
	if approval != nil {
		t.Fatal("expected no approval for complete")
	}
	if updated.State != core.RequestStateCompleted {
		t.Fatalf("expected completed state, got %s", updated.State)
	}
}

func TestRespond_ExpiredRequest(t *testing.T) {
	svc, store := newServiceWithStore()
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	now := time.Now().UTC()

	// Inject an already-expired request directly into the store.
	expired := core.Request{
		RequestID:   id.New("request"),
		OrgID:       orgID,
		FromAgentID: id.New("agent"),
		ToAgentID:   toAgentID,
		State:       core.RequestStatePending,
		RequestType: "information",
		Title:       "expired question",
		Content:     "x",
		CreatedAt:   now.Add(-2 * time.Hour),
		ExpiresAt:   now.Add(-time.Hour),
	}
	if _, err := store.SaveRequest(ctx, expired); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	agent := core.Agent{AgentID: toAgentID, OrgID: orgID}
	_, _, err := svc.Respond(ctx, agent, expired.RequestID, core.RequestResponseAccept, "")
	if !errors.Is(err, requests.ErrExpiredRequest) {
		t.Fatalf("expected ErrExpiredRequest, got %v", err)
	}
}

func TestListIncoming_ExpiredFiltered(t *testing.T) {
	store := memory.New()
	svc := requests.NewService(store, store, store)
	ctx := context.Background()

	orgID := id.New("org")
	toAgentID := id.New("agent")
	now := time.Now().UTC()

	// Save one active and one already-expired request directly into the store.
	active := core.Request{
		RequestID:   id.New("request"),
		OrgID:       orgID,
		ToAgentID:   toAgentID,
		State:       core.RequestStatePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
		RequestType: "info",
		Title:       "active",
		Content:     "x",
	}
	expired := core.Request{
		RequestID:   id.New("request"),
		OrgID:       orgID,
		ToAgentID:   toAgentID,
		State:       core.RequestStatePending,
		CreatedAt:   now.Add(-2 * time.Hour),
		ExpiresAt:   now.Add(-time.Hour),
		RequestType: "info",
		Title:       "expired",
		Content:     "x",
	}
	store.SaveRequest(ctx, active)
	store.SaveRequest(ctx, expired)

	list, err := svc.ListIncoming(ctx, toAgentID, 50, 0)
	if err != nil {
		t.Fatalf("ListIncoming: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 non-expired request, got %d", len(list))
	}
	if list[0].RequestID != active.RequestID {
		t.Fatalf("unexpected RequestID %s", list[0].RequestID)
	}
}
