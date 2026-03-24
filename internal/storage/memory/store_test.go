package memory_test

import (
	"context"
	"testing"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

func TestGrantRevocation(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	grantorUserID := id.New("user")
	granteeUserID := id.New("user")

	grant := core.PolicyGrant{
		PolicyGrantID:        id.New("grant"),
		OrgID:                id.New("org"),
		GrantorUserID:        grantorUserID,
		GranteeUserID:        granteeUserID,
		ScopeType:            "project",
		ScopeRef:             "proj1",
		AllowedArtifactTypes: []core.ArtifactType{core.ArtifactTypeSummary},
		MaxSensitivity:       core.SensitivityLow,
		AllowedPurposes:      []core.QueryPurpose{core.QueryPurposeStatusCheck},
		CreatedAt:            time.Now().UTC(),
	}
	saved, err := store.SaveGrant(ctx, grant)
	if err != nil {
		t.Fatalf("SaveGrant: %v", err)
	}

	// Grant appears in list before revocation
	grants, err := store.ListGrantsForPair(ctx, grantorUserID, granteeUserID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("pre-revocation: expected 1 grant, got %d err=%v", len(grants), err)
	}

	// Revoke
	revoked, err := store.RevokeGrant(ctx, saved.PolicyGrantID, grantorUserID)
	if err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("RevokedAt should be set after revocation")
	}

	// Grant no longer appears in list after revocation
	grants, err = store.ListGrantsForPair(ctx, grantorUserID, granteeUserID)
	if err != nil || len(grants) != 0 {
		t.Fatalf("post-revocation: expected 0 grants, got %d err=%v", len(grants), err)
	}

	// Non-grantor cannot revoke
	_, err = store.RevokeGrant(ctx, saved.PolicyGrantID, id.New("user"))
	if err == nil {
		t.Fatal("expected error when non-grantor tries to revoke")
	}

	// Re-revocation also errors
	_, err = store.RevokeGrant(ctx, saved.PolicyGrantID, grantorUserID)
	if err == nil {
		t.Fatal("expected error when revoking already-revoked grant")
	}
}

func TestApprovalStateGuard(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	approval := core.Approval{
		ApprovalID:  id.New("approval"),
		OrgID:       id.New("org"),
		AgentID:     id.New("agent"),
		OwnerUserID: id.New("user"),
		SubjectType: "request",
		SubjectID:   id.New("request"),
		Reason:      "Test approval",
		State:       core.ApprovalStatePending,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}
	store.SaveApproval(ctx, approval)

	// First resolution succeeds
	now := time.Now().UTC()
	resolved, ok, err := store.ResolveApproval(ctx, approval.ApprovalID, core.ApprovalStateApproved, now)
	if err != nil || !ok {
		t.Fatalf("first resolution failed: ok=%v err=%v", ok, err)
	}
	if resolved.State != core.ApprovalStateApproved {
		t.Fatalf("expected approved, got %s", resolved.State)
	}

	// Second resolution returns not-found (state is no longer pending)
	_, ok, err = store.ResolveApproval(ctx, approval.ApprovalID, core.ApprovalStateDenied, now)
	if err != nil || ok {
		t.Fatalf("second resolution should return not-found: ok=%v err=%v", ok, err)
	}
}

func TestExpiredRequestsFilteredFromList(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agentID := id.New("agent")
	now := time.Now().UTC()

	activeRequest := core.Request{
		RequestID:   id.New("request"),
		OrgID:       id.New("org"),
		ToAgentID:   agentID,
		State:       core.RequestStatePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
	expiredRequest := core.Request{
		RequestID:   id.New("request"),
		OrgID:       id.New("org"),
		ToAgentID:   agentID,
		State:       core.RequestStatePending,
		CreatedAt:   now.Add(-2 * time.Hour),
		ExpiresAt:   now.Add(-time.Hour), // already expired
	}

	store.SaveRequest(ctx, activeRequest)
	store.SaveRequest(ctx, expiredRequest)

	requests, err := store.ListIncomingRequests(ctx, agentID)
	if err != nil {
		t.Fatalf("ListIncomingRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected 1 active request, got %d", len(requests))
	}
	if requests[0].RequestID != activeRequest.RequestID {
		t.Fatalf("unexpected request ID %s", requests[0].RequestID)
	}
}

func TestExpiredApprovalsFilteredFromList(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agentID := id.New("agent")
	now := time.Now().UTC()

	activeApproval := core.Approval{
		ApprovalID: id.New("approval"),
		AgentID:    agentID,
		State:      core.ApprovalStatePending,
		CreatedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
	}
	expiredApproval := core.Approval{
		ApprovalID: id.New("approval"),
		AgentID:    agentID,
		State:      core.ApprovalStatePending,
		CreatedAt:  now.Add(-2 * time.Hour),
		ExpiresAt:  now.Add(-time.Hour), // already expired
	}

	store.SaveApproval(ctx, activeApproval)
	store.SaveApproval(ctx, expiredApproval)

	approvals, err := store.ListPendingApprovals(ctx, agentID)
	if err != nil {
		t.Fatalf("ListPendingApprovals: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected 1 active approval, got %d", len(approvals))
	}
	if approvals[0].ApprovalID != activeApproval.ApprovalID {
		t.Fatalf("unexpected approval ID %s", approvals[0].ApprovalID)
	}
}

func TestOrgIsolation_UserLookup(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	orgA := id.New("org")
	orgB := id.New("org")

	userA := core.User{
		UserID: id.New("user"),
		OrgID:  orgA,
		Email:  "alice@example.com",
	}
	userB := core.User{
		UserID: id.New("user"),
		OrgID:  orgB,
		Email:  "alice@example.com", // same email, different org
	}
	store.UpsertUser(ctx, userA)
	store.UpsertUser(ctx, userB)

	// Lookup in org A should return org A's user
	found, ok, err := store.FindUserByEmail(ctx, orgA, "alice@example.com")
	if err != nil || !ok || found.UserID != userA.UserID {
		t.Fatalf("org A lookup: ok=%v err=%v userID=%s", ok, err, found.UserID)
	}

	// Lookup in org B should return org B's user
	found, ok, err = store.FindUserByEmail(ctx, orgB, "alice@example.com")
	if err != nil || !ok || found.UserID != userB.UserID {
		t.Fatalf("org B lookup: ok=%v err=%v userID=%s", ok, err, found.UserID)
	}

	// Lookup with unknown org returns not found
	_, ok, err = store.FindUserByEmail(ctx, id.New("org"), "alice@example.com")
	if err != nil || ok {
		t.Fatalf("unknown org lookup should return not found: ok=%v err=%v", ok, err)
	}
}
