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

// saveQueryAndResponse saves a query + response pair into the store for testing query-type approvals.
func saveQueryAndResponse(ctx context.Context, store *memory.Store, queryID, orgID string) {
	q := core.Query{
		QueryID: queryID,
		OrgID:   orgID,
		State:   core.QueryStateQueued,
	}
	resp := core.QueryResponse{
		ResponseID:    id.New("resp"),
		QueryID:       queryID,
		ApprovalState: core.ApprovalStatePending,
	}
	store.SaveQuery(ctx, q)
	store.SaveQueryResponse(ctx, resp)
}

func TestResolve_Approve_Query(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")
	agent := core.Agent{AgentID: agentID, OrgID: orgID}

	queryID := id.New("query")
	saveQueryAndResponse(ctx, store, queryID, orgID)
	ap := savePendingApproval(ctx, store, agentID, orgID, "query", queryID)

	resolvedAp, _, err := svc.Resolve(ctx, agent, ap.ApprovalID, core.ApprovalStateApproved)
	if err != nil {
		t.Fatalf("Resolve query approve: %v", err)
	}
	if resolvedAp.State != core.ApprovalStateApproved {
		t.Fatalf("expected approved state, got %s", resolvedAp.State)
	}

	// Verify query state was updated to completed.
	q, ok, _ := store.FindQuery(ctx, queryID)
	if !ok || q.State != core.QueryStateCompleted {
		t.Fatalf("expected query state completed, got %s ok=%v", q.State, ok)
	}
}

func TestResolve_Deny_Query(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	orgID := id.New("org")
	agentID := id.New("agent")
	agent := core.Agent{AgentID: agentID, OrgID: orgID}

	queryID := id.New("query")
	saveQueryAndResponse(ctx, store, queryID, orgID)
	ap := savePendingApproval(ctx, store, agentID, orgID, "query", queryID)

	_, _, err := svc.Resolve(ctx, agent, ap.ApprovalID, core.ApprovalStateDenied)
	if err != nil {
		t.Fatalf("Resolve query deny: %v", err)
	}

	// Verify query state was updated to denied.
	q, ok, _ := store.FindQuery(ctx, queryID)
	if !ok || q.State != core.QueryStateDenied {
		t.Fatalf("expected query state denied, got %s ok=%v", q.State, ok)
	}
}

func TestResolve_ExpiredApproval(t *testing.T) {
	store := memory.New()
	svc := newService(store)
	ctx := context.Background()

	agentID := id.New("agent")
	orgID := id.New("org")
	agent := core.Agent{AgentID: agentID, OrgID: orgID}

	// Create an already-expired approval.
	expired := core.Approval{
		ApprovalID:  id.New("approval"),
		OrgID:       orgID,
		AgentID:     agentID,
		OwnerUserID: id.New("user"),
		SubjectType: "request",
		SubjectID:   id.New("request"),
		Reason:      "test",
		State:       core.ApprovalStatePending,
		CreatedAt:   time.Now().UTC().Add(-2 * time.Hour),
		ExpiresAt:   time.Now().UTC().Add(-time.Hour), // already expired
	}
	store.SaveApproval(ctx, expired)

	_, _, err := svc.Resolve(ctx, agent, expired.ApprovalID, core.ApprovalStateApproved)
	if !errors.Is(err, approvals.ErrExpiredApproval) {
		t.Fatalf("expected ErrExpiredApproval, got %v", err)
	}
}
