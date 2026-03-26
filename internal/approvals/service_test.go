package approvals_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"alice/internal/approvals"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

func newService(store *memory.Store) *approvals.Service {
	return approvals.NewService(store, store, store, store)
}

func saveRequest(ctx context.Context, store *memory.Store, toAgentID, orgID string) core.Request {
	req := core.Request{
		RequestID:   id.New("request"),
		OrgID:       orgID,
		ToAgentID:   toAgentID,
		State:       core.RequestStatePending,
		ApprovalState: core.ApprovalStatePending,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		RequestType: "info",
		Title:       "test",
		Content:     "test",
	}
	saved, _ := store.SaveRequest(ctx, req)
	return saved
}

func savePendingApproval(ctx context.Context, store *memory.Store, agentID, orgID, subjectType, subjectID string) core.Approval {
	approval := core.Approval{
		ApprovalID:  id.New("approval"),
		OrgID:       orgID,
		AgentID:     agentID,
		OwnerUserID: id.New("user"),
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Reason:      "test approval",
		State:       core.ApprovalStatePending,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}
	saved, _ := store.SaveApproval(ctx, approval)
	return saved
}

func TestListPending(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	agentID := id.New("agent")
	orgID := id.New("org")

	savePendingApproval(ctx, store, agentID, orgID, "request", id.New("request"))
	savePendingApproval(ctx, store, agentID, orgID, "request", id.New("request"))
	// Another agent — should not appear.
	savePendingApproval(ctx, store, id.New("agent"), orgID, "request", id.New("request"))

	list, err := svc.ListPending(ctx, agentID, 50, 0)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 pending approvals, got %d", len(list))
	}
}

func TestResolve_Approve_Request(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")
	agent := core.Agent{AgentID: agentID, OrgID: orgID}

	req := saveRequest(ctx, store, agentID, orgID)
	ap := savePendingApproval(ctx, store, agentID, orgID, "request", req.RequestID)

	resolvedAp, updatedReq, err := svc.Resolve(ctx, agent, ap.ApprovalID, core.ApprovalStateApproved)
	if err != nil {
		t.Fatalf("Resolve approve: %v", err)
	}
	if resolvedAp.State != core.ApprovalStateApproved {
		t.Fatalf("expected approved state on approval, got %s", resolvedAp.State)
	}
	if updatedReq.State != core.RequestStateAccepted {
		t.Fatalf("expected accepted state on request, got %s", updatedReq.State)
	}
}

func TestResolve_Deny_Request(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")
	agent := core.Agent{AgentID: agentID, OrgID: orgID}

	req := saveRequest(ctx, store, agentID, orgID)
	ap := savePendingApproval(ctx, store, agentID, orgID, "request", req.RequestID)

	resolvedAp, updatedReq, err := svc.Resolve(ctx, agent, ap.ApprovalID, core.ApprovalStateDenied)
	if err != nil {
		t.Fatalf("Resolve deny: %v", err)
	}
	if resolvedAp.State != core.ApprovalStateDenied {
		t.Fatalf("expected denied state on approval, got %s", resolvedAp.State)
	}
	if updatedReq.State != core.RequestStateDenied {
		t.Fatalf("expected denied state on request, got %s", updatedReq.State)
	}
}

func TestResolve_AlreadyResolved(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")
	agent := core.Agent{AgentID: agentID, OrgID: orgID}

	req := saveRequest(ctx, store, agentID, orgID)
	ap := savePendingApproval(ctx, store, agentID, orgID, "request", req.RequestID)

	if _, _, err := svc.Resolve(ctx, agent, ap.ApprovalID, core.ApprovalStateApproved); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}

	_, _, err := svc.Resolve(ctx, agent, ap.ApprovalID, core.ApprovalStateDenied)
	if !errors.Is(err, approvals.ErrApprovalResolved) {
		t.Fatalf("expected ErrApprovalResolved, got %v", err)
	}
}

func TestResolve_NotOwner(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")

	req := saveRequest(ctx, store, agentID, orgID)
	ap := savePendingApproval(ctx, store, agentID, orgID, "request", req.RequestID)

	wrongAgent := core.Agent{AgentID: id.New("agent"), OrgID: orgID}
	_, _, err := svc.Resolve(ctx, wrongAgent, ap.ApprovalID, core.ApprovalStateApproved)
	if !errors.Is(err, approvals.ErrApprovalNotVisible) {
		t.Fatalf("expected ErrApprovalNotVisible, got %v", err)
	}
}

func TestResolve_NotFound(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org")}
	_, _, err := svc.Resolve(ctx, agent, "nonexistent", core.ApprovalStateApproved)
	if !errors.Is(err, approvals.ErrUnknownApproval) {
		t.Fatalf("expected ErrUnknownApproval, got %v", err)
	}
}
