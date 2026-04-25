package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
	"alice/internal/storage/postgres"
)

// openTestStore opens a postgres store for testing, or skips if ALICE_TEST_DATABASE_URL is not set.
func openTestStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("ALICE_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("ALICE_TEST_DATABASE_URL not set; skipping postgres tests")
	}
	ctx := context.Background()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		store.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestMigrate(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	// Running migrations a second time should be idempotent.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

// --- Organization ---

func TestUpsertOrganization_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org := core.Organization{
		OrgID:  id.New("org"),
		Name:   "Acme",
		Slug:   "acme-" + id.New("slug"),
		Status: "active",
	}
	saved, err := store.UpsertOrganization(ctx, org)
	if err != nil || saved.OrgID != org.OrgID {
		t.Fatalf("UpsertOrganization: %v", err)
	}

	found, ok, err := store.FindOrganizationBySlug(ctx, org.Slug)
	if err != nil || !ok || found.OrgID != org.OrgID {
		t.Fatalf("FindOrganizationBySlug: ok=%v err=%v", ok, err)
	}

	_, ok, err = store.FindOrganizationBySlug(ctx, "doesnotexist-"+id.New("x"))
	if err != nil || ok {
		t.Fatalf("non-existent slug should return not-found: ok=%v err=%v", ok, err)
	}
}

// --- User ---

func TestUpsertUser_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org := core.Organization{OrgID: id.New("org"), Name: "Test", Slug: "sluguser-" + id.New("s"), Status: "active"}
	store.UpsertOrganization(ctx, org)

	user := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "alice-" + id.New("x") + "@example.com", Role: core.UserRoleMember}
	if _, err := store.UpsertUser(ctx, user); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	found, ok, err := store.FindUserByEmail(ctx, org.OrgID, user.Email)
	if err != nil || !ok || found.UserID != user.UserID {
		t.Fatalf("FindUserByEmail: ok=%v err=%v", ok, err)
	}

	// FindUserByEmail must be org-scoped.
	_, ok, err = store.FindUserByEmail(ctx, id.New("otherorg"), user.Email)
	if err != nil || ok {
		t.Fatalf("FindUserByEmail cross-org should return not-found: ok=%v err=%v", ok, err)
	}
}

// --- Agent token lifecycle ---

func TestSaveAgentToken_Find_Revoke_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org := core.Organization{OrgID: id.New("org"), Slug: "tok-" + id.New("s"), Name: "T", Status: "active"}
	store.UpsertOrganization(ctx, org)
	user := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "tok-" + id.New("x") + "@ex.com"}
	store.UpsertUser(ctx, user)
	agent := core.Agent{AgentID: id.New("agent"), OrgID: org.OrgID, OwnerUserID: user.UserID, Status: core.AgentStatusActive}
	store.UpsertAgent(ctx, agent)

	now := time.Now().UTC()
	tok := core.AgentToken{
		TokenID:   id.New("tok"),
		AgentID:   agent.AgentID,
		TokenHash: "hash" + id.New("h"),
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}
	saved, err := store.SaveAgentToken(ctx, tok)
	if err != nil {
		t.Fatalf("SaveAgentToken: %v", err)
	}

	found, ok, err := store.FindAgentTokenByID(ctx, saved.TokenID)
	if err != nil || !ok {
		t.Fatalf("FindAgentTokenByID: ok=%v err=%v", ok, err)
	}
	if found.RevokedAt != nil {
		t.Fatal("token should not be revoked yet")
	}

	if err := store.RevokeAllTokensForAgent(ctx, agent.AgentID, time.Now().UTC()); err != nil {
		t.Fatalf("RevokeAllTokensForAgent: %v", err)
	}

	found, ok, err = store.FindAgentTokenByID(ctx, saved.TokenID)
	if err != nil || !ok || found.RevokedAt == nil {
		t.Fatalf("after revocation: ok=%v err=%v revoked=%v", ok, err, found.RevokedAt)
	}
}

// --- Grant lifecycle ---

func TestSaveGrant_Find_Revoke_List_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org := core.Organization{OrgID: id.New("org"), Slug: "grant-" + id.New("s"), Name: "T", Status: "active"}
	store.UpsertOrganization(ctx, org)
	grantor := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "grantor-" + id.New("x") + "@ex.com", Role: core.UserRoleMember}
	grantee := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "grantee-" + id.New("x") + "@ex.com", Role: core.UserRoleMember}
	store.UpsertUser(ctx, grantor)
	store.UpsertUser(ctx, grantee)

	grantorUserID := grantor.UserID
	granteeUserID := grantee.UserID
	orgID := org.OrgID

	grant := core.PolicyGrant{
		PolicyGrantID:        id.New("grant"),
		OrgID:                orgID,
		GrantorUserID:        grantorUserID,
		GranteeUserID:        granteeUserID,
		ScopeType:            "project",
		ScopeRef:             "proj-" + id.New("p"),
		AllowedArtifactTypes: []core.ArtifactType{core.ArtifactTypeSummary},
		MaxSensitivity:       core.SensitivityLow,
		AllowedPurposes:      []core.QueryPurpose{core.QueryPurposeStatusCheck},
		VisibilityMode:       core.VisibilityModeExplicitGrantsOnly,
		CreatedAt:            time.Now().UTC(),
	}
	saved, err := store.SaveGrant(ctx, grant)
	if err != nil {
		t.Fatalf("SaveGrant: %v", err)
	}

	grants, err := store.ListGrantsForPair(ctx, grantorUserID, granteeUserID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("ListGrantsForPair: len=%d err=%v", len(grants), err)
	}

	revoked, err := store.RevokeGrant(ctx, saved.PolicyGrantID, grantorUserID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("RevokeGrant: %v revokedAt=%v", err, revoked.RevokedAt)
	}

	// Revoked grant must not appear in list.
	grants, err = store.ListGrantsForPair(ctx, grantorUserID, granteeUserID)
	if err != nil || len(grants) != 0 {
		t.Fatalf("after revocation ListGrantsForPair: len=%d err=%v", len(grants), err)
	}

	// Re-revocation must fail.
	_, err = store.RevokeGrant(ctx, saved.PolicyGrantID, grantorUserID)
	if err == nil {
		t.Fatal("expected error re-revoking already revoked grant")
	}
}

// --- Artifact lifecycle ---

func TestSaveArtifact_Find_List_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org := core.Organization{OrgID: id.New("org"), Slug: "art-" + id.New("s"), Name: "T", Status: "active"}
	store.UpsertOrganization(ctx, org)
	user := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "artuser-" + id.New("x") + "@ex.com", Role: core.UserRoleMember}
	store.UpsertUser(ctx, user)
	agent := core.Agent{AgentID: id.New("agent"), OrgID: org.OrgID, OwnerUserID: user.UserID, Status: core.AgentStatusActive}
	store.UpsertAgent(ctx, agent)

	userID := user.UserID
	orgID := org.OrgID

	artifact := core.Artifact{
		ArtifactID:     id.New("artifact"),
		OrgID:          orgID,
		OwnerUserID:    userID,
		OwnerAgentID:   agent.AgentID,
		Type:           core.ArtifactTypeSummary,
		Title:          "Test artifact",
		Content:        "Content here",
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Confidence:     0.8,
		ApprovalState:  core.ApprovalStateNotRequired,
		CreatedAt:      time.Now().UTC(),
		SourceRefs: []core.SourceReference{
			{SourceSystem: "test", SourceType: "manual", SourceID: "1", ObservedAt: time.Now().UTC()},
		},
	}
	saved, err := store.SaveArtifact(ctx, artifact)
	if err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	found, ok, err := store.FindArtifactByID(ctx, saved.ArtifactID)
	if err != nil || !ok || found.ArtifactID != saved.ArtifactID {
		t.Fatalf("FindArtifactByID: ok=%v err=%v", ok, err)
	}

	list, err := store.ListArtifactsByOwner(ctx, userID)
	if err != nil || len(list) == 0 {
		t.Fatalf("ListArtifactsByOwner: len=%d err=%v", len(list), err)
	}
}

// --- Registration challenge - concurrent use ---

func TestRegistrationChallenge_ConcurrentUse_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	ch := core.AgentRegistrationChallenge{
		ChallengeID: id.New("challenge"),
		CreatedAt:   now,
		ExpiresAt:   now.Add(5 * time.Minute),
	}
	store.SaveAgentRegistrationChallenge(ctx, ch)

	usedAt := now
	ch.UsedAt = &usedAt
	_, err := store.SaveAgentRegistrationChallenge(ctx, ch)
	if err != nil {
		t.Fatalf("first use should succeed: %v", err)
	}

	_, err = store.SaveAgentRegistrationChallenge(ctx, ch)
	if !errors.Is(err, storage.ErrChallengeAlreadyUsed) {
		t.Fatalf("expected ErrChallengeAlreadyUsed, got %v", err)
	}
}

// --- Audit events ---

func TestAppendAuditEvent_List_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org := core.Organization{OrgID: id.New("org"), Slug: "audit-" + id.New("s"), Name: "T", Status: "active"}
	store.UpsertOrganization(ctx, org)

	agentID := id.New("agent")
	now := time.Now().UTC()

	old := core.AuditEvent{
		AuditEventID: id.New("audit"),
		OrgID:        org.OrgID,
		ActorAgentID: agentID,
		EventKind:    "test.old",
		SubjectType:  "query",
		SubjectID:    id.New("q"),
		Decision:     "allow",
		CreatedAt:    now.Add(-2 * time.Hour),
	}
	recent := core.AuditEvent{
		AuditEventID: id.New("audit"),
		OrgID:        org.OrgID,
		ActorAgentID: agentID,
		EventKind:    "test.recent",
		SubjectType:  "query",
		SubjectID:    id.New("q"),
		Decision:     "allow",
		CreatedAt:    now,
	}
	if _, err := store.AppendAuditEvent(ctx, old); err != nil {
		t.Fatalf("AppendAuditEvent old: %v", err)
	}
	if _, err := store.AppendAuditEvent(ctx, recent); err != nil {
		t.Fatalf("AppendAuditEvent recent: %v", err)
	}

	since := now.Add(-time.Hour)
	events, err := store.ListAuditEvents(ctx, storage.AuditFilter{AgentID: agentID, Since: since, Limit: 50})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	// Only the recent event should pass the since filter.
	found := false
	for _, e := range events {
		if e.AuditEventID == recent.AuditEventID {
			found = true
		}
		if e.AuditEventID == old.AuditEventID {
			t.Fatal("old event should be filtered by since")
		}
	}
	if !found {
		t.Fatal("recent event not found after since filter")
	}
}

// --- WithTx rollback ---

func TestWithTx_Rollback_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	orgID := id.New("org")
	org := core.Organization{OrgID: orgID, Slug: "tx-rb-" + id.New("s"), Name: "T", Status: "active"}

	sentinel := errors.New("sentinel")
	err := store.WithTx(ctx, func(tx storage.StoreTx) error {
		if _, err := tx.UpsertOrganization(ctx, org); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error from WithTx, got %v", err)
	}

	// The org must not have been committed.
	_, ok, err := store.FindOrganizationByID(ctx, orgID)
	if err != nil {
		t.Fatalf("FindOrganizationByID after rollback: %v", err)
	}
	if ok {
		t.Fatal("org should not exist after transaction rollback")
	}
}

// --- WithTx commit ---

func TestWithTx_Commit_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org := core.Organization{OrgID: id.New("org"), Slug: "tx-ok-" + id.New("s"), Name: "T", Status: "active"}
	err := store.WithTx(ctx, func(tx storage.StoreTx) error {
		_, err := tx.UpsertOrganization(ctx, org)
		return err
	})
	if err != nil {
		t.Fatalf("WithTx commit: %v", err)
	}

	_, ok, err := store.FindOrganizationByID(ctx, org.OrgID)
	if err != nil || !ok {
		t.Fatalf("org should exist after successful commit: ok=%v err=%v", ok, err)
	}
}

// mkOrgUserAgent is a test helper that inserts an org, user, and agent and
// returns them. It fails the test immediately if any insert fails.
func mkOrgUserAgent(t *testing.T, store interface {
	UpsertOrganization(context.Context, core.Organization) (core.Organization, error)
	UpsertUser(context.Context, core.User) (core.User, error)
	UpsertAgent(context.Context, core.Agent) (core.Agent, error)
}) (core.Organization, core.User, core.Agent) {
	t.Helper()
	ctx := context.Background()

	org := core.Organization{OrgID: id.New("org"), Slug: "slug-" + id.New("s"), Name: "T", Status: "active"}
	if _, err := store.UpsertOrganization(ctx, org); err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}
	user := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: id.New("u") + "@example.com", Role: core.UserRoleMember}
	if _, err := store.UpsertUser(ctx, user); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	agent := core.Agent{AgentID: id.New("agent"), OrgID: org.OrgID, OwnerUserID: user.UserID, Status: core.AgentStatusActive}
	if _, err := store.UpsertAgent(ctx, agent); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	return org, user, agent
}

// --- Queries ---

func TestSaveQuery_Find_Update_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, user, agent := mkOrgUserAgent(t, store)

	now := time.Now().UTC()
	q := core.Query{
		QueryID:        id.New("query"),
		OrgID:          org.OrgID,
		FromAgentID:    agent.AgentID,
		FromUserID:     user.UserID,
		ToAgentID:      agent.AgentID,
		ToUserID:       user.UserID,
		Purpose:        core.QueryPurposeStatusCheck,
		Question:       "What is the status?",
		RequestedTypes: []core.ArtifactType{core.ArtifactTypeSummary},
		ProjectScope:   []string{"proj-a"},
		TimeWindow:     core.TimeWindow{Start: now.Add(-24 * time.Hour), End: now},
		RiskLevel:      core.RiskLevelL0,
		State:          core.QueryStateQueued,
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	}

	saved, err := store.SaveQuery(ctx, q)
	if err != nil {
		t.Fatalf("SaveQuery: %v", err)
	}

	found, ok, err := store.FindQuery(ctx, saved.QueryID)
	if err != nil || !ok {
		t.Fatalf("FindQuery: ok=%v err=%v", ok, err)
	}
	if found.Purpose != core.QueryPurposeStatusCheck {
		t.Errorf("Purpose: got %q want %q", found.Purpose, core.QueryPurposeStatusCheck)
	}
	if len(found.RequestedTypes) != 1 {
		t.Errorf("RequestedTypes: got %v", found.RequestedTypes)
	}

	// FindQuery not-found path.
	_, ok, err = store.FindQuery(ctx, id.New("nope"))
	if err != nil || ok {
		t.Fatalf("FindQuery non-existent: ok=%v err=%v", ok, err)
	}

	// UpdateQueryState.
	updated, ok, err := store.UpdateQueryState(ctx, saved.QueryID, core.QueryStateCompleted)
	if err != nil || !ok || updated.State != core.QueryStateCompleted {
		t.Fatalf("UpdateQueryState: ok=%v err=%v state=%q", ok, err, updated.State)
	}

	// UpdateQueryState on non-existent ID returns not-found.
	_, ok, err = store.UpdateQueryState(ctx, id.New("nope"), core.QueryStateCompleted)
	if err != nil || ok {
		t.Fatalf("UpdateQueryState non-existent: ok=%v err=%v", ok, err)
	}

	// SaveQueryResponse.
	resp := core.QueryResponse{
		ResponseID:    id.New("resp"),
		QueryID:       saved.QueryID,
		FromAgentID:   agent.AgentID,
		ToAgentID:     agent.AgentID,
		Artifacts:     []core.QueryArtifact{},
		Redactions:    []string{},
		PolicyBasis:   []string{"grant:explicit"},
		ApprovalState: core.ApprovalStateNotRequired,
		Confidence:    0.9,
		CreatedAt:     now,
	}
	savedResp, err := store.SaveQueryResponse(ctx, resp)
	if err != nil {
		t.Fatalf("SaveQueryResponse: %v", err)
	}

	foundResp, ok, err := store.FindQueryResponse(ctx, savedResp.QueryID)
	if err != nil || !ok {
		t.Fatalf("FindQueryResponse: ok=%v err=%v", ok, err)
	}
	if foundResp.Confidence != 0.9 {
		t.Errorf("Confidence: got %v want 0.9", foundResp.Confidence)
	}

	// FindQueryResponse not-found path.
	_, ok, err = store.FindQueryResponse(ctx, id.New("nope"))
	if err != nil || ok {
		t.Fatalf("FindQueryResponse non-existent: ok=%v err=%v", ok, err)
	}

	// UpdateQueryResponseApprovalState.
	updatedResp, ok, err := store.UpdateQueryResponseApprovalState(ctx, savedResp.QueryID, core.ApprovalStateApproved)
	if err != nil || !ok || updatedResp.ApprovalState != core.ApprovalStateApproved {
		t.Fatalf("UpdateQueryResponseApprovalState: ok=%v err=%v state=%q", ok, err, updatedResp.ApprovalState)
	}

	// UpdateQueryResponseApprovalState non-existent.
	_, ok, err = store.UpdateQueryResponseApprovalState(ctx, id.New("nope"), core.ApprovalStateApproved)
	if err != nil || ok {
		t.Fatalf("UpdateQueryResponseApprovalState non-existent: ok=%v err=%v", ok, err)
	}
}

// --- Requests and approvals ---

func TestSaveRequest_Find_List_Update_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, user, agent := mkOrgUserAgent(t, store)

	now := time.Now().UTC()
	req := core.Request{
		RequestID:     id.New("req"),
		OrgID:         org.OrgID,
		FromAgentID:   agent.AgentID,
		FromUserID:    user.UserID,
		ToAgentID:     agent.AgentID,
		ToUserID:      user.UserID,
		RequestType:   "question",
		Title:         "Need help",
		Content:       "Please advise",
		RiskLevel:     core.RiskLevelL0,
		State:         core.RequestStatePending,
		ApprovalState: core.ApprovalStateNotRequired,
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}

	saved, err := store.SaveRequest(ctx, req)
	if err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	found, ok, err := store.FindRequest(ctx, saved.RequestID)
	if err != nil || !ok || found.RequestID != saved.RequestID {
		t.Fatalf("FindRequest: ok=%v err=%v", ok, err)
	}

	// FindRequest not-found.
	_, ok, err = store.FindRequest(ctx, id.New("nope"))
	if err != nil || ok {
		t.Fatalf("FindRequest non-existent: ok=%v err=%v", ok, err)
	}

	// ListIncomingRequests.
	incoming, err := store.ListIncomingRequests(ctx, agent.AgentID, 10, 0)
	if err != nil {
		t.Fatalf("ListIncomingRequests: %v", err)
	}
	found2 := false
	for _, r := range incoming {
		if r.RequestID == saved.RequestID {
			found2 = true
		}
	}
	if !found2 {
		t.Error("saved request not found in incoming list")
	}

	// ListSentRequests.
	sent, err := store.ListSentRequests(ctx, agent.AgentID, 10, 0)
	if err != nil {
		t.Fatalf("ListSentRequests: %v", err)
	}
	found3 := false
	for _, r := range sent {
		if r.RequestID == saved.RequestID {
			found3 = true
		}
	}
	if !found3 {
		t.Error("saved request not found in sent list")
	}

	// UpdateRequestState.
	updated, ok, err := store.UpdateRequestState(ctx, saved.RequestID, core.RequestStateCompleted, core.ApprovalStateNotRequired, "done")
	if err != nil || !ok || updated.State != core.RequestStateCompleted {
		t.Fatalf("UpdateRequestState: ok=%v err=%v state=%q", ok, err, updated.State)
	}
	if updated.ResponseMessage != "done" {
		t.Errorf("ResponseMessage: got %q want 'done'", updated.ResponseMessage)
	}

	// UpdateRequestState non-existent returns not-found.
	_, ok, err = store.UpdateRequestState(ctx, id.New("nope"), core.RequestStateCompleted, core.ApprovalStateNotRequired, "")
	if err != nil || ok {
		t.Fatalf("UpdateRequestState non-existent: ok=%v err=%v", ok, err)
	}
}

func TestSaveApproval_Find_List_Resolve_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, user, agent := mkOrgUserAgent(t, store)

	now := time.Now().UTC()
	appr := core.Approval{
		ApprovalID:  id.New("appr"),
		OrgID:       org.OrgID,
		AgentID:     agent.AgentID,
		OwnerUserID: user.UserID,
		SubjectType: "query",
		SubjectID:   id.New("q"),
		Reason:      "test",
		State:       core.ApprovalStatePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}

	saved, err := store.SaveApproval(ctx, appr)
	if err != nil {
		t.Fatalf("SaveApproval: %v", err)
	}

	found, ok, err := store.FindApproval(ctx, saved.ApprovalID)
	if err != nil || !ok || found.ApprovalID != saved.ApprovalID {
		t.Fatalf("FindApproval: ok=%v err=%v", ok, err)
	}

	// FindApproval not-found.
	_, ok, err = store.FindApproval(ctx, id.New("nope"))
	if err != nil || ok {
		t.Fatalf("FindApproval non-existent: ok=%v err=%v", ok, err)
	}

	// ListPendingApprovals.
	list, err := store.ListPendingApprovals(ctx, agent.AgentID, 10, 0)
	if err != nil || len(list) == 0 {
		t.Fatalf("ListPendingApprovals: len=%d err=%v", len(list), err)
	}

	// ResolveApproval.
	resolved, ok, err := store.ResolveApproval(ctx, saved.ApprovalID, core.ApprovalStateApproved, time.Now().UTC())
	if err != nil || !ok || resolved.State != core.ApprovalStateApproved {
		t.Fatalf("ResolveApproval: ok=%v err=%v state=%q", ok, err, resolved.State)
	}
	if resolved.ResolvedAt == nil {
		t.Error("ResolvedAt should be set after resolution")
	}

	// ResolveApproval on already-resolved returns not-found (state != pending).
	_, ok, err = store.ResolveApproval(ctx, saved.ApprovalID, core.ApprovalStateApproved, time.Now().UTC())
	if err != nil || ok {
		t.Fatalf("ResolveApproval already-resolved: ok=%v err=%v", ok, err)
	}
}

// --- Email verification ---

func TestEmailVerification_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, _, agent := mkOrgUserAgent(t, store)

	now := time.Now().UTC()
	v := core.EmailVerification{
		VerificationID: id.New("ver"),
		AgentID:        agent.AgentID,
		OrgID:          org.OrgID,
		Email:          agent.AgentID + "@example.com",
		CodeHash:       "hash-" + id.New("h"),
		CreatedAt:      now,
		ExpiresAt:      now.Add(10 * time.Minute),
		Attempts:       0,
	}

	saved, err := store.SaveEmailVerification(ctx, v)
	if err != nil {
		t.Fatalf("SaveEmailVerification: %v", err)
	}

	pending, ok, err := store.FindPendingVerification(ctx, saved.AgentID)
	if err != nil || !ok {
		t.Fatalf("FindPendingVerification: ok=%v err=%v", ok, err)
	}
	if pending.VerificationID != saved.VerificationID {
		t.Errorf("VerificationID mismatch: got %q want %q", pending.VerificationID, saved.VerificationID)
	}

	// FindPendingVerification not-found.
	_, ok, err = store.FindPendingVerification(ctx, id.New("nobody"))
	if err != nil || ok {
		t.Fatalf("FindPendingVerification non-existent: ok=%v err=%v", ok, err)
	}

	// IncrementVerificationAttempts.
	if err := store.IncrementVerificationAttempts(ctx, saved.VerificationID); err != nil {
		t.Fatalf("IncrementVerificationAttempts: %v", err)
	}

	// IncrementVerificationAttempts on non-existent ID.
	if err := store.IncrementVerificationAttempts(ctx, id.New("nope")); !errors.Is(err, storage.ErrVerificationNotFound) {
		t.Fatalf("IncrementVerificationAttempts non-existent: got %v want ErrVerificationNotFound", err)
	}

	// MarkEmailVerified.
	if err := store.MarkEmailVerified(ctx, saved.VerificationID, time.Now().UTC()); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}

	// After marking verified, FindPendingVerification should return not-found.
	_, ok, err = store.FindPendingVerification(ctx, saved.AgentID)
	if err != nil || ok {
		t.Fatalf("FindPendingVerification after verification: ok=%v err=%v", ok, err)
	}

	// MarkEmailVerified on already-verified (or non-existent) returns ErrVerificationNotFound.
	if err := store.MarkEmailVerified(ctx, saved.VerificationID, time.Now().UTC()); !errors.Is(err, storage.ErrVerificationNotFound) {
		t.Fatalf("MarkEmailVerified already-verified: got %v want ErrVerificationNotFound", err)
	}

	// SaveEmailVerification invalidates the previous pending record.
	v2 := core.EmailVerification{
		VerificationID: id.New("ver2"),
		AgentID:        agent.AgentID,
		OrgID:          org.OrgID,
		Email:          agent.AgentID + "@example.com",
		CodeHash:       "hash2-" + id.New("h"),
		CreatedAt:      now,
		ExpiresAt:      now.Add(10 * time.Minute),
	}
	if _, err := store.SaveEmailVerification(ctx, v2); err != nil {
		t.Fatalf("SaveEmailVerification second: %v", err)
	}
	// The new one should be the pending one.
	pending2, ok, err := store.FindPendingVerification(ctx, agent.AgentID)
	if err != nil || !ok || pending2.VerificationID != v2.VerificationID {
		t.Fatalf("FindPendingVerification after second save: ok=%v err=%v id=%q", ok, err, pending2.VerificationID)
	}
}

// --- Agent approval ---

func TestAgentApproval_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, _, agent := mkOrgUserAgent(t, store)

	now := time.Now().UTC()
	appr := core.AgentApproval{
		ApprovalID:  id.New("agappr"),
		AgentID:     agent.AgentID,
		OrgID:       org.OrgID,
		RequestedAt: now,
	}

	if err := store.SaveAgentApproval(ctx, appr); err != nil {
		t.Fatalf("SaveAgentApproval: %v", err)
	}

	// FindPendingAgentApprovals.
	pending, err := store.FindPendingAgentApprovals(ctx, org.OrgID, 10, 0)
	if err != nil {
		t.Fatalf("FindPendingAgentApprovals: %v", err)
	}
	found := false
	for _, a := range pending {
		if a.ApprovalID == appr.ApprovalID {
			found = true
		}
	}
	if !found {
		t.Error("agent approval not found in pending list")
	}

	// FindAgentApprovalByAgentID.
	retrieved, err := store.FindAgentApprovalByAgentID(ctx, agent.AgentID)
	if err != nil {
		t.Fatalf("FindAgentApprovalByAgentID: %v", err)
	}
	if retrieved.ApprovalID != appr.ApprovalID {
		t.Errorf("ApprovalID: got %q want %q", retrieved.ApprovalID, appr.ApprovalID)
	}

	// FindAgentApprovalByAgentID not-found.
	_, err = store.FindAgentApprovalByAgentID(ctx, id.New("nobody"))
	if !errors.Is(err, storage.ErrAgentApprovalNotFound) {
		t.Fatalf("FindAgentApprovalByAgentID non-existent: got %v want ErrAgentApprovalNotFound", err)
	}

	// UpdateAgentApproval.
	reviewedAt := time.Now().UTC()
	if err := store.UpdateAgentApproval(ctx, appr.ApprovalID, "approved", "looks good", "reviewer-id", reviewedAt); err != nil {
		t.Fatalf("UpdateAgentApproval: %v", err)
	}

	// After update, FindPendingAgentApprovals should no longer return the approved agent.
	pending2, err := store.FindPendingAgentApprovals(ctx, org.OrgID, 10, 0)
	if err != nil {
		t.Fatalf("FindPendingAgentApprovals after update: %v", err)
	}
	for _, a := range pending2 {
		if a.ApprovalID == appr.ApprovalID {
			t.Error("approved agent approval should not appear in pending list")
		}
	}
}

// --- Risk policy ---

func TestRiskPolicy_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, user, _ := mkOrgUserAgent(t, store)

	// NextPolicyVersionForOrg on empty org returns 1.
	v, err := store.NextPolicyVersionForOrg(ctx, org.OrgID)
	if err != nil || v != 1 {
		t.Fatalf("NextPolicyVersionForOrg empty: got %d err=%v", v, err)
	}

	now := time.Now().UTC()
	policy := core.RiskPolicy{
		PolicyID:        id.New("pol"),
		OrgID:           org.OrgID,
		Name:            "test-policy",
		Version:         1,
		Source:          `{"rules":[]}`,
		CreatedAt:       now,
		CreatedByUserID: user.UserID,
	}

	saved, err := store.SavePolicy(ctx, policy)
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}

	// FindPolicyByID.
	found, ok, err := store.FindPolicyByID(ctx, saved.PolicyID)
	if err != nil || !ok || found.PolicyID != saved.PolicyID {
		t.Fatalf("FindPolicyByID: ok=%v err=%v", ok, err)
	}
	// Postgres JSONB normalizes whitespace (e.g. adds spaces after ':'), so
	// compare after stripping all whitespace from both sides.
	normalize := func(s string) string { return strings.Join(strings.Fields(s), "") }
	if normalize(found.Source) != normalize(`{"rules":[]}`) {
		t.Errorf("Source: got %q want compact {\"rules\":[]}", found.Source)
	}

	// FindPolicyByID not-found.
	_, ok, err = store.FindPolicyByID(ctx, id.New("nope"))
	if err != nil || ok {
		t.Fatalf("FindPolicyByID non-existent: ok=%v err=%v", ok, err)
	}

	// FindActivePolicyForOrg — no active policy yet.
	_, ok, err = store.FindActivePolicyForOrg(ctx, org.OrgID)
	if err != nil || ok {
		t.Fatalf("FindActivePolicyForOrg before activation: ok=%v err=%v", ok, err)
	}

	// ActivatePolicy.
	if err := store.ActivatePolicy(ctx, saved.PolicyID, time.Now().UTC()); err != nil {
		t.Fatalf("ActivatePolicy: %v", err)
	}

	active, ok, err := store.FindActivePolicyForOrg(ctx, org.OrgID)
	if err != nil || !ok || active.PolicyID != saved.PolicyID {
		t.Fatalf("FindActivePolicyForOrg after activation: ok=%v err=%v", ok, err)
	}
	if active.ActiveAt == nil {
		t.Error("ActiveAt should be set after activation")
	}

	// ActivatePolicy on non-existent policy.
	if err := store.ActivatePolicy(ctx, id.New("nope"), time.Now().UTC()); !errors.Is(err, storage.ErrRiskPolicyNotFound) {
		t.Fatalf("ActivatePolicy non-existent: got %v want ErrRiskPolicyNotFound", err)
	}

	// Save a second policy and activate it; first should lose active status.
	v2, err := store.NextPolicyVersionForOrg(ctx, org.OrgID)
	if err != nil || v2 != 2 {
		t.Fatalf("NextPolicyVersionForOrg after first save: got %d err=%v", v2, err)
	}
	policy2 := core.RiskPolicy{
		PolicyID:  id.New("pol2"),
		OrgID:     org.OrgID,
		Name:      "test-policy-2",
		Version:   v2,
		Source:    `{"rules":[{"action":"deny"}]}`,
		CreatedAt: now,
	}
	saved2, err := store.SavePolicy(ctx, policy2)
	if err != nil {
		t.Fatalf("SavePolicy second: %v", err)
	}
	if err := store.ActivatePolicy(ctx, saved2.PolicyID, time.Now().UTC()); err != nil {
		t.Fatalf("ActivatePolicy second: %v", err)
	}

	active2, ok, err := store.FindActivePolicyForOrg(ctx, org.OrgID)
	if err != nil || !ok || active2.PolicyID != saved2.PolicyID {
		t.Fatalf("FindActivePolicyForOrg after second activation: ok=%v err=%v id=%q", ok, err, active2.PolicyID)
	}

	// ListPoliciesForOrg.
	list, err := store.ListPoliciesForOrg(ctx, org.OrgID, 10, 0)
	if err != nil || len(list) < 2 {
		t.Fatalf("ListPoliciesForOrg: len=%d err=%v", len(list), err)
	}
}

// --- Actions ---

func TestActions_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, user, agent := mkOrgUserAgent(t, store)

	now := time.Now().UTC()
	action := core.Action{
		ActionID:     id.New("act"),
		OrgID:        org.OrgID,
		OwnerUserID:  user.UserID,
		OwnerAgentID: agent.AgentID,
		Kind:         core.ActionKindAcknowledgeBlocker,
		Inputs:       map[string]any{"key": "val"},
		RiskLevel:    core.RiskLevelL0,
		State:        core.ActionStatePending,
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
	}

	saved, err := store.SaveAction(ctx, action)
	if err != nil {
		t.Fatalf("SaveAction: %v", err)
	}

	// FindActionByID.
	found, ok, err := store.FindActionByID(ctx, saved.ActionID)
	if err != nil || !ok || found.ActionID != saved.ActionID {
		t.Fatalf("FindActionByID: ok=%v err=%v", ok, err)
	}
	if found.Kind != core.ActionKindAcknowledgeBlocker {
		t.Errorf("Kind: got %q want acknowledge_blocker", found.Kind)
	}

	// FindActionByID not-found.
	_, ok, err = store.FindActionByID(ctx, id.New("nope"))
	if err != nil || ok {
		t.Fatalf("FindActionByID non-existent: ok=%v err=%v", ok, err)
	}

	// ListActions — by owner.
	list, err := store.ListActions(ctx, storage.ActionFilter{OwnerUserID: user.UserID, Limit: 10})
	if err != nil || len(list) == 0 {
		t.Fatalf("ListActions: len=%d err=%v", len(list), err)
	}

	// ListActions — by state.
	listPending, err := store.ListActions(ctx, storage.ActionFilter{State: core.ActionStatePending, Limit: 10})
	if err != nil {
		t.Fatalf("ListActions by state: %v", err)
	}
	foundInList := false
	for _, a := range listPending {
		if a.ActionID == saved.ActionID {
			foundInList = true
		}
	}
	if !foundInList {
		t.Error("saved action not found in state-filtered list")
	}

	// UpdateActionState — pending → approved.
	action.State = core.ActionStateApproved
	updated, err := store.UpdateActionState(ctx, action)
	if err != nil || updated.State != core.ActionStateApproved {
		t.Fatalf("UpdateActionState pending→approved: err=%v state=%q", err, updated.State)
	}

	// UpdateActionState — approved → executed (terminal).
	execAt := time.Now().UTC()
	action.State = core.ActionStateExecuted
	action.ExecutedAt = &execAt
	action.Result = map[string]any{"status": "ok"}
	_, err = store.UpdateActionState(ctx, action)
	if err != nil {
		t.Fatalf("UpdateActionState approved→executed: %v", err)
	}

	// UpdateActionState on terminal state must return ErrActionInTerminalState.
	_, err = store.UpdateActionState(ctx, action)
	if !errors.Is(err, storage.ErrActionInTerminalState) {
		t.Fatalf("UpdateActionState terminal→terminal: got %v want ErrActionInTerminalState", err)
	}

	// UpdateActionState on non-existent action must return ErrActionNotFound.
	action2 := action
	action2.ActionID = id.New("nope")
	action2.State = core.ActionStateApproved
	_, err = store.UpdateActionState(ctx, action2)
	if !errors.Is(err, storage.ErrActionNotFound) {
		t.Fatalf("UpdateActionState non-existent: got %v want ErrActionNotFound", err)
	}

	// SetOperatorEnabled.
	if err := store.SetOperatorEnabled(ctx, user.UserID, true); err != nil {
		t.Fatalf("SetOperatorEnabled: %v", err)
	}
}

// --- Org graph: teams and manager edges ---

func TestOrgGraph_Teams_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, user, _ := mkOrgUserAgent(t, store)

	// Create a second user for team tests.
	user2 := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: id.New("u2") + "@example.com", Role: core.UserRoleMember}
	if _, err := store.UpsertUser(ctx, user2); err != nil {
		t.Fatalf("UpsertUser 2: %v", err)
	}

	now := time.Now().UTC()
	team := core.Team{
		TeamID:    id.New("team"),
		OrgID:     org.OrgID,
		Name:      "Engineering",
		CreatedAt: now,
	}

	savedTeam, err := store.SaveTeam(ctx, team)
	if err != nil {
		t.Fatalf("SaveTeam: %v", err)
	}

	// FindTeamByID.
	found, ok, err := store.FindTeamByID(ctx, savedTeam.TeamID)
	if err != nil || !ok || found.TeamID != savedTeam.TeamID {
		t.Fatalf("FindTeamByID: ok=%v err=%v", ok, err)
	}

	// FindTeamByID not-found.
	_, ok, err = store.FindTeamByID(ctx, id.New("nope"))
	if err != nil || ok {
		t.Fatalf("FindTeamByID non-existent: ok=%v err=%v", ok, err)
	}

	// ListTeamsForOrg.
	teams, err := store.ListTeamsForOrg(ctx, org.OrgID, 10, 0)
	if err != nil || len(teams) == 0 {
		t.Fatalf("ListTeamsForOrg: len=%d err=%v", len(teams), err)
	}

	// SaveTeamMember.
	m1 := core.TeamMember{TeamID: savedTeam.TeamID, UserID: user.UserID, Role: core.TeamMemberRoleMember, JoinedAt: now}
	m2 := core.TeamMember{TeamID: savedTeam.TeamID, UserID: user2.UserID, Role: core.TeamMemberRoleLead, JoinedAt: now}
	if err := store.SaveTeamMember(ctx, m1); err != nil {
		t.Fatalf("SaveTeamMember 1: %v", err)
	}
	if err := store.SaveTeamMember(ctx, m2); err != nil {
		t.Fatalf("SaveTeamMember 2: %v", err)
	}

	// ListTeamMembers.
	members, err := store.ListTeamMembers(ctx, savedTeam.TeamID, 10, 0)
	if err != nil || len(members) != 2 {
		t.Fatalf("ListTeamMembers: len=%d err=%v", len(members), err)
	}

	// ListTeamsForUser.
	userTeams, err := store.ListTeamsForUser(ctx, user.UserID)
	if err != nil || len(userTeams) == 0 {
		t.Fatalf("ListTeamsForUser: len=%d err=%v", len(userTeams), err)
	}

	// UsersShareTeam — should be true.
	share, err := store.UsersShareTeam(ctx, user.UserID, user2.UserID)
	if err != nil || !share {
		t.Fatalf("UsersShareTeam: share=%v err=%v", share, err)
	}

	// UsersShareTeam — user with themselves.
	shareSelf, err := store.UsersShareTeam(ctx, user.UserID, user.UserID)
	if err != nil || !shareSelf {
		t.Fatalf("UsersShareTeam self: share=%v err=%v", shareSelf, err)
	}

	// DeleteTeamMember.
	if err := store.DeleteTeamMember(ctx, savedTeam.TeamID, user.UserID); err != nil {
		t.Fatalf("DeleteTeamMember: %v", err)
	}

	// DeleteTeamMember non-existent.
	if err := store.DeleteTeamMember(ctx, savedTeam.TeamID, id.New("nobody")); !errors.Is(err, storage.ErrTeamMemberNotFound) {
		t.Fatalf("DeleteTeamMember non-existent: got %v want ErrTeamMemberNotFound", err)
	}

	// DeleteTeam.
	if err := store.DeleteTeam(ctx, savedTeam.TeamID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}

	// DeleteTeam non-existent.
	if err := store.DeleteTeam(ctx, id.New("nope")); !errors.Is(err, storage.ErrTeamNotFound) {
		t.Fatalf("DeleteTeam non-existent: got %v want ErrTeamNotFound", err)
	}
}

func TestOrgGraph_ManagerEdges_Postgres(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	org, user, _ := mkOrgUserAgent(t, store)

	// Create manager user.
	mgr := core.User{UserID: id.New("mgr"), OrgID: org.OrgID, Email: id.New("mgr") + "@example.com", Role: core.UserRoleMember}
	if _, err := store.UpsertUser(ctx, mgr); err != nil {
		t.Fatalf("UpsertUser mgr: %v", err)
	}

	// No edge yet.
	_, ok, err := store.FindCurrentManagerEdge(ctx, user.UserID)
	if err != nil || ok {
		t.Fatalf("FindCurrentManagerEdge before save: ok=%v err=%v", ok, err)
	}

	now := time.Now().UTC()
	edge := core.ManagerEdge{
		EdgeID:        id.New("edge"),
		UserID:        user.UserID,
		ManagerUserID: mgr.UserID,
		EffectiveAt:   now,
	}

	savedEdge, err := store.SaveManagerEdge(ctx, edge)
	if err != nil {
		t.Fatalf("SaveManagerEdge: %v", err)
	}

	// FindCurrentManagerEdge.
	found, ok, err := store.FindCurrentManagerEdge(ctx, user.UserID)
	if err != nil || !ok || found.EdgeID != savedEdge.EdgeID {
		t.Fatalf("FindCurrentManagerEdge: ok=%v err=%v", ok, err)
	}
	if found.ManagerUserID != mgr.UserID {
		t.Errorf("ManagerUserID: got %q want %q", found.ManagerUserID, mgr.UserID)
	}

	// WalkManagerChain — one hop.
	chain, err := store.WalkManagerChain(ctx, user.UserID, 5)
	if err != nil || len(chain) != 1 {
		t.Fatalf("WalkManagerChain: len=%d err=%v", len(chain), err)
	}
	if chain[0].ManagerUserID != mgr.UserID {
		t.Errorf("chain[0].ManagerUserID: got %q want %q", chain[0].ManagerUserID, mgr.UserID)
	}

	// SaveManagerEdge again — should atomically replace the prior edge.
	mgr2 := core.User{UserID: id.New("mgr2"), OrgID: org.OrgID, Email: id.New("mgr2") + "@example.com", Role: core.UserRoleMember}
	if _, err := store.UpsertUser(ctx, mgr2); err != nil {
		t.Fatalf("UpsertUser mgr2: %v", err)
	}
	edge2 := core.ManagerEdge{
		EdgeID:        id.New("edge2"),
		UserID:        user.UserID,
		ManagerUserID: mgr2.UserID,
		EffectiveAt:   now.Add(time.Minute),
	}
	if _, err := store.SaveManagerEdge(ctx, edge2); err != nil {
		t.Fatalf("SaveManagerEdge replacement: %v", err)
	}
	found2, ok, err := store.FindCurrentManagerEdge(ctx, user.UserID)
	if err != nil || !ok || found2.ManagerUserID != mgr2.UserID {
		t.Fatalf("FindCurrentManagerEdge after replacement: ok=%v err=%v mgr=%q", ok, err, found2.ManagerUserID)
	}

	// RevokeCurrentManagerEdge.
	if err := store.RevokeCurrentManagerEdge(ctx, user.UserID, time.Now().UTC()); err != nil {
		t.Fatalf("RevokeCurrentManagerEdge: %v", err)
	}

	// After revocation no active edge exists.
	_, ok, err = store.FindCurrentManagerEdge(ctx, user.UserID)
	if err != nil || ok {
		t.Fatalf("FindCurrentManagerEdge after revocation: ok=%v err=%v", ok, err)
	}

	// WalkManagerChain on user with no manager returns empty.
	chain2, err := store.WalkManagerChain(ctx, user.UserID, 5)
	if err != nil || len(chain2) != 0 {
		t.Fatalf("WalkManagerChain after revocation: len=%d err=%v", len(chain2), err)
	}
}
