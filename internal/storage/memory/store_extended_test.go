package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
	"alice/internal/storage/memory"
)

// --- Organization ---

func TestUpsertOrganization_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	org := core.Organization{
		OrgID:  id.New("org"),
		Name:   "Acme",
		Slug:   "acme",
		Status: "active",
	}
	saved, err := store.UpsertOrganization(ctx, org)
	if err != nil || saved.OrgID != org.OrgID {
		t.Fatalf("UpsertOrganization: %v", err)
	}

	// Find by slug.
	found, ok, err := store.FindOrganizationBySlug(ctx, "acme")
	if err != nil || !ok || found.OrgID != org.OrgID {
		t.Fatalf("FindOrganizationBySlug: ok=%v err=%v id=%s", ok, err, found.OrgID)
	}

	// Find by ID.
	found, ok, err = store.FindOrganizationByID(ctx, org.OrgID)
	if err != nil || !ok || found.OrgID != org.OrgID {
		t.Fatalf("FindOrganizationByID: ok=%v err=%v", ok, err)
	}
}

func TestFindOrganizationBySlug_NotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	_, ok, err := store.FindOrganizationBySlug(ctx, "nope")
	if err != nil || ok {
		t.Fatalf("expected not-found: ok=%v err=%v", ok, err)
	}
}

func TestUpdateOrgVerificationMode(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	org := core.Organization{OrgID: id.New("org"), Slug: "acme"}
	store.UpsertOrganization(ctx, org)

	if err := store.UpdateOrgVerificationMode(ctx, org.OrgID, "invite_token"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	found, ok, _ := store.FindOrganizationByID(ctx, org.OrgID)
	if !ok || found.VerificationMode != "invite_token" {
		t.Fatalf("VerificationMode not updated: %q", found.VerificationMode)
	}
}

func TestSetOrgInviteTokenHash(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	org := core.Organization{OrgID: id.New("org"), Slug: "acme"}
	store.UpsertOrganization(ctx, org)

	if err := store.SetOrgInviteTokenHash(ctx, org.OrgID, "sha256abc"); err != nil {
		t.Fatalf("SetOrgInviteTokenHash: %v", err)
	}

	found, _, _ := store.FindOrganizationByID(ctx, org.OrgID)
	if found.InviteTokenHash != "sha256abc" {
		t.Fatalf("InviteTokenHash not updated: %q", found.InviteTokenHash)
	}
}

func TestUpdateGatekeeperTuning_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	org := core.Organization{OrgID: id.New("org"), Slug: "tune"}
	store.UpsertOrganization(ctx, org)

	threshold := 0.85
	window := 72 * time.Hour
	if err := store.UpdateGatekeeperTuning(ctx, org.OrgID, &threshold, &window); err != nil {
		t.Fatalf("UpdateGatekeeperTuning: %v", err)
	}

	found, _, _ := store.FindOrganizationByID(ctx, org.OrgID)
	if found.GatekeeperConfidenceThreshold == nil || *found.GatekeeperConfidenceThreshold != threshold {
		t.Fatalf("threshold not persisted: %v", found.GatekeeperConfidenceThreshold)
	}
	if found.GatekeeperLookbackWindow == nil || *found.GatekeeperLookbackWindow != window {
		t.Fatalf("window not persisted: %v", found.GatekeeperLookbackWindow)
	}

	// Mutating the caller's pointer after the call must not change stored state.
	threshold = 0.1
	window = time.Second
	found, _, _ = store.FindOrganizationByID(ctx, org.OrgID)
	if *found.GatekeeperConfidenceThreshold != 0.85 {
		t.Fatalf("store aliased caller's pointer for threshold: got %v", *found.GatekeeperConfidenceThreshold)
	}
	if *found.GatekeeperLookbackWindow != 72*time.Hour {
		t.Fatalf("store aliased caller's pointer for window: got %v", *found.GatekeeperLookbackWindow)
	}

	// Clearing both back to nil.
	if err := store.UpdateGatekeeperTuning(ctx, org.OrgID, nil, nil); err != nil {
		t.Fatalf("clear UpdateGatekeeperTuning: %v", err)
	}
	found, _, _ = store.FindOrganizationByID(ctx, org.OrgID)
	if found.GatekeeperConfidenceThreshold != nil || found.GatekeeperLookbackWindow != nil {
		t.Fatalf("expected both overrides nil, got %+v / %+v", found.GatekeeperConfidenceThreshold, found.GatekeeperLookbackWindow)
	}

	// Missing org returns ErrOrgNotFound.
	if err := store.UpdateGatekeeperTuning(ctx, "org_no_such_org", &threshold, nil); !errors.Is(err, storage.ErrOrgNotFound) {
		t.Fatalf("expected ErrOrgNotFound, got %v", err)
	}
}

// --- User ---

func TestUpsertUser_Update(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	orgID := id.New("org")
	user := core.User{UserID: id.New("user"), OrgID: orgID, Email: "alice@example.com", DisplayName: "Alice"}
	store.UpsertUser(ctx, user)

	user.DisplayName = "Alice Updated"
	store.UpsertUser(ctx, user)

	found, ok, err := store.FindUserByID(ctx, user.UserID)
	if err != nil || !ok || found.DisplayName != "Alice Updated" {
		t.Fatalf("upsert update: ok=%v err=%v name=%s", ok, err, found.DisplayName)
	}
}

func TestFindUserByID_NotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	_, ok, err := store.FindUserByID(ctx, "nonexistent")
	if err != nil || ok {
		t.Fatalf("expected not-found: ok=%v err=%v", ok, err)
	}
}

func TestUpdateUserRole(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	user := core.User{UserID: id.New("user"), OrgID: id.New("org"), Email: "u@x.com", Role: core.UserRoleMember}
	store.UpsertUser(ctx, user)

	if err := store.UpdateUserRole(ctx, user.UserID, core.UserRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	found, _, _ := store.FindUserByID(ctx, user.UserID)
	if found.Role != core.UserRoleAdmin {
		t.Fatalf("expected admin role, got %s", found.Role)
	}
}

// --- Agent ---

func TestUpsertAgent_Update(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user"), Status: core.AgentStatusActive}
	store.UpsertAgent(ctx, agent)

	agent.Status = core.AgentStatusPendingEmailVerification
	store.UpsertAgent(ctx, agent)

	found, ok, err := store.FindAgentByID(ctx, agent.AgentID)
	if err != nil || !ok || found.Status != core.AgentStatusPendingEmailVerification {
		t.Fatalf("agent update: ok=%v err=%v status=%s", ok, err, found.Status)
	}
}

func TestFindAgentByID_NotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	_, ok, err := store.FindAgentByID(ctx, "nonexistent")
	if err != nil || ok {
		t.Fatalf("expected not-found: ok=%v err=%v", ok, err)
	}
}

func TestFindAgentByUserID_NotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	_, ok, err := store.FindAgentByUserID(ctx, "nonexistent")
	if err != nil || ok {
		t.Fatalf("expected not-found: ok=%v err=%v", ok, err)
	}
}

// --- Registration challenge ---

func TestSaveAgentRegistrationChallenge_ConcurrentUse(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	now := time.Now().UTC()
	challenge := core.AgentRegistrationChallenge{
		ChallengeID: id.New("challenge"),
		CreatedAt:   now,
		ExpiresAt:   now.Add(5 * time.Minute),
	}
	// Save the initial (unused) challenge.
	store.SaveAgentRegistrationChallenge(ctx, challenge)

	// First completion marks it used.
	usedAt := now
	challenge.UsedAt = &usedAt
	_, err := store.SaveAgentRegistrationChallenge(ctx, challenge)
	if err != nil {
		t.Fatalf("first use should succeed: %v", err)
	}

	// Second concurrent completion should be rejected.
	_, err = store.SaveAgentRegistrationChallenge(ctx, challenge)
	if !errors.Is(err, storage.ErrChallengeAlreadyUsed) {
		t.Fatalf("expected ErrChallengeAlreadyUsed, got %v", err)
	}
}

// --- Token revocation ---

func TestRevokeAllTokensForAgent(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agentID := id.New("agent")
	now := time.Now().UTC()

	tok1 := core.AgentToken{TokenID: id.New("tok"), AgentID: agentID, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	tok2 := core.AgentToken{TokenID: id.New("tok"), AgentID: agentID, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	otherTok := core.AgentToken{TokenID: id.New("tok"), AgentID: id.New("agent"), IssuedAt: now, ExpiresAt: now.Add(time.Hour)}

	store.SaveAgentToken(ctx, tok1)
	store.SaveAgentToken(ctx, tok2)
	store.SaveAgentToken(ctx, otherTok)

	if err := store.RevokeAllTokensForAgent(ctx, agentID, time.Now().UTC()); err != nil {
		t.Fatalf("RevokeAllTokensForAgent: %v", err)
	}

	found1, _, _ := store.FindAgentTokenByID(ctx, tok1.TokenID)
	if found1.RevokedAt == nil {
		t.Fatal("tok1 should be revoked")
	}
	found2, _, _ := store.FindAgentTokenByID(ctx, tok2.TokenID)
	if found2.RevokedAt == nil {
		t.Fatal("tok2 should be revoked")
	}

	// Other agent's token must not be revoked.
	foundOther, _, _ := store.FindAgentTokenByID(ctx, otherTok.TokenID)
	if foundOther.RevokedAt != nil {
		t.Fatal("other agent's token should not be revoked")
	}
}

// --- Query & response lifecycle ---

func TestSaveQuery_FindQuery_UpdateState(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	q := core.Query{
		QueryID: id.New("query"),
		OrgID:   id.New("org"),
		State:   core.QueryStateQueued,
	}
	store.SaveQuery(ctx, q)

	found, ok, err := store.FindQuery(ctx, q.QueryID)
	if err != nil || !ok || found.QueryID != q.QueryID {
		t.Fatalf("FindQuery: ok=%v err=%v", ok, err)
	}

	updated, ok, err := store.UpdateQueryState(ctx, q.QueryID, core.QueryStateCompleted)
	if err != nil || !ok || updated.State != core.QueryStateCompleted {
		t.Fatalf("UpdateQueryState: ok=%v err=%v state=%s", ok, err, updated.State)
	}
}

func TestSaveQueryResponse_FindAndUpdateApprovalState(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	qID := id.New("query")
	resp := core.QueryResponse{
		ResponseID:    id.New("resp"),
		QueryID:       qID,
		ApprovalState: core.ApprovalStatePending,
	}
	store.SaveQueryResponse(ctx, resp)

	found, ok, err := store.FindQueryResponse(ctx, qID)
	if err != nil || !ok || found.ApprovalState != core.ApprovalStatePending {
		t.Fatalf("FindQueryResponse: ok=%v err=%v", ok, err)
	}

	updated, ok, err := store.UpdateQueryResponseApprovalState(ctx, qID, core.ApprovalStateApproved)
	if err != nil || !ok || updated.ApprovalState != core.ApprovalStateApproved {
		t.Fatalf("UpdateQueryResponseApprovalState: ok=%v err=%v", ok, err)
	}
}

// --- Email verification ---

func TestEmailVerification_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agentID := id.New("agent")
	v := core.EmailVerification{
		VerificationID: id.New("verif"),
		AgentID:        agentID,
		OrgID:          id.New("org"),
		Email:          "alice@example.com",
		CodeHash:       "hash123",
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
	store.SaveEmailVerification(ctx, v)

	found, ok, err := store.FindPendingVerification(ctx, agentID)
	if err != nil || !ok || found.VerificationID != v.VerificationID {
		t.Fatalf("FindPendingVerification: ok=%v err=%v", ok, err)
	}
}

func TestMarkEmailVerified(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agentID := id.New("agent")
	v := core.EmailVerification{
		VerificationID: id.New("verif"),
		AgentID:        agentID,
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
	store.SaveEmailVerification(ctx, v)

	if err := store.MarkEmailVerified(ctx, v.VerificationID, time.Now().UTC()); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}

	// No longer pending.
	_, ok, err := store.FindPendingVerification(ctx, agentID)
	if err != nil || ok {
		t.Fatalf("after verification, FindPendingVerification should return not-found: ok=%v err=%v", ok, err)
	}
}

func TestIncrementVerificationAttempts(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	v := core.EmailVerification{
		VerificationID: id.New("verif"),
		AgentID:        id.New("agent"),
		Attempts:       0,
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
	}
	store.SaveEmailVerification(ctx, v)

	for i := 0; i < 3; i++ {
		if err := store.IncrementVerificationAttempts(ctx, v.VerificationID); err != nil {
			t.Fatalf("IncrementVerificationAttempts %d: %v", i, err)
		}
	}

	found, _, _ := store.FindPendingVerification(ctx, v.AgentID)
	if found.Attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", found.Attempts)
	}
}

// --- Agent approval ---

func TestAgentApproval_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	orgID := id.New("org")
	agentID := id.New("agent")
	ap := core.AgentApproval{
		ApprovalID:  id.New("agentap"),
		AgentID:     agentID,
		OrgID:       orgID,
		RequestedAt: time.Now().UTC(),
	}

	if err := store.SaveAgentApproval(ctx, ap); err != nil {
		t.Fatalf("SaveAgentApproval: %v", err)
	}

	// Appears in pending list for this org.
	list, err := store.FindPendingAgentApprovals(ctx, orgID, 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("FindPendingAgentApprovals: len=%d err=%v", len(list), err)
	}

	// Can be found by agent ID.
	found, err := store.FindAgentApprovalByAgentID(ctx, agentID)
	if err != nil || found.ApprovalID != ap.ApprovalID {
		t.Fatalf("FindAgentApprovalByAgentID: %v", err)
	}

	// Update (approve).
	reviewedAt := time.Now().UTC()
	if err := store.UpdateAgentApproval(ctx, ap.ApprovalID, "approved", "", id.New("user"), reviewedAt); err != nil {
		t.Fatalf("UpdateAgentApproval: %v", err)
	}

	// No longer in pending list.
	list, _ = store.FindPendingAgentApprovals(ctx, orgID, 50, 0)
	if len(list) != 0 {
		t.Fatalf("expected empty pending list after approval, got %d", len(list))
	}
}

// --- Audit events ---

func TestAppendAuditEvent_ListAuditEvents(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agentID := id.New("agent")
	now := time.Now().UTC()

	old := core.AuditEvent{AuditEventID: id.New("audit"), ActorAgentID: agentID, CreatedAt: now.Add(-2 * time.Hour)}
	recent := core.AuditEvent{AuditEventID: id.New("audit"), ActorAgentID: agentID, CreatedAt: now}
	store.AppendAuditEvent(ctx, old)
	store.AppendAuditEvent(ctx, recent)

	// Without filter: both returned.
	all, err := store.ListAuditEvents(ctx, storage.AuditFilter{AgentID: agentID, Limit: 50})
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 events, got %d err=%v", len(all), err)
	}

	// With since filter: only recent.
	since := now.Add(-time.Hour)
	filtered, err := store.ListAuditEvents(ctx, storage.AuditFilter{AgentID: agentID, Since: since, Limit: 50})
	if err != nil || len(filtered) != 1 {
		t.Fatalf("expected 1 event after since filter, got %d err=%v", len(filtered), err)
	}
	if filtered[0].AuditEventID != recent.AuditEventID {
		t.Fatalf("wrong event returned by since filter")
	}
}

// --- Sent requests list ---

func TestListSentRequests(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	fromAgentID := id.New("agent")
	now := time.Now().UTC()

	req1 := core.Request{
		RequestID:   id.New("request"),
		OrgID:       id.New("org"),
		FromAgentID: fromAgentID,
		ToAgentID:   id.New("agent"),
		State:       core.RequestStatePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
	req2 := core.Request{
		RequestID:   id.New("request"),
		OrgID:       id.New("org"),
		FromAgentID: fromAgentID,
		ToAgentID:   id.New("agent"),
		State:       core.RequestStatePending,
		CreatedAt:   now.Add(time.Second),
		ExpiresAt:   now.Add(time.Hour),
	}
	// Different sender — should not appear.
	other := core.Request{
		RequestID:   id.New("request"),
		OrgID:       id.New("org"),
		FromAgentID: id.New("agent"),
		ToAgentID:   id.New("agent"),
		State:       core.RequestStatePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
	store.SaveRequest(ctx, req1)
	store.SaveRequest(ctx, req2)
	store.SaveRequest(ctx, other)

	list, err := store.ListSentRequests(ctx, fromAgentID, 50, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 sent requests, got %d err=%v", len(list), err)
	}
}

// --- Pagination ---

func TestPageSlice_ListAuditEvents_Pagination(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	agentID := id.New("agent")
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		store.AppendAuditEvent(ctx, core.AuditEvent{
			AuditEventID: id.New("audit"),
			ActorAgentID: agentID,
			CreatedAt:    now.Add(time.Duration(i) * time.Second),
		})
	}

	page1, _ := store.ListAuditEvents(ctx, storage.AuditFilter{AgentID: agentID, Limit: 2, Offset: 0})
	if len(page1) != 2 {
		t.Fatalf("page1: expected 2, got %d", len(page1))
	}
	page2, _ := store.ListAuditEvents(ctx, storage.AuditFilter{AgentID: agentID, Limit: 2, Offset: 2})
	if len(page2) != 2 {
		t.Fatalf("page2: expected 2, got %d", len(page2))
	}
	page3, _ := store.ListAuditEvents(ctx, storage.AuditFilter{AgentID: agentID, Limit: 2, Offset: 4})
	if len(page3) != 1 {
		t.Fatalf("page3: expected 1, got %d", len(page3))
	}
}

func TestFindOrgBySlug(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	org := core.Organization{OrgID: id.New("org"), Slug: "test-org", Name: "Test Org"}
	store.UpsertOrganization(ctx, org)

	found, err := store.FindOrgBySlug(ctx, "test-org")
	if err != nil {
		t.Fatalf("FindOrgBySlug: err=%v", err)
	}
	if found.OrgID != org.OrgID {
		t.Fatalf("OrgID mismatch: %s vs %s", found.OrgID, org.OrgID)
	}

	_, err = store.FindOrgBySlug(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent slug")
	}
}

func TestSaveAndFindArtifact(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	now := time.Now().UTC()
	artifact := core.Artifact{
		ArtifactID:     id.New("artifact"),
		OwnerUserID:    id.New("user"),
		OrgID:          id.New("org"),
		Type:           core.ArtifactTypeSummary,
		Title:          "Test artifact",
		Content:        "Content here",
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		CreatedAt:      now,
		SourceRefs: []core.SourceReference{
			{SourceSystem: "test", SourceType: "manual", SourceID: "1", ObservedAt: now},
		},
	}

	saved, err := store.SaveArtifact(ctx, artifact)
	if err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}
	if saved.ArtifactID != artifact.ArtifactID {
		t.Fatalf("ArtifactID mismatch")
	}

	found, ok, err := store.FindArtifactByID(ctx, artifact.ArtifactID)
	if err != nil || !ok {
		t.Fatalf("FindArtifactByID: ok=%v err=%v", ok, err)
	}
	if found.Title != "Test artifact" {
		t.Fatalf("Title mismatch: %q", found.Title)
	}

	_, ok, _ = store.FindArtifactByID(ctx, "nonexistent")
	if ok {
		t.Fatal("expected not found for nonexistent ID")
	}

	artifacts, err := store.ListArtifactsByOwner(ctx, artifact.OwnerUserID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("ListArtifactsByOwner: len=%d err=%v", len(artifacts), err)
	}
}

func TestFindGrant(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	grant := core.PolicyGrant{
		PolicyGrantID:        id.New("grant"),
		OrgID:                id.New("org"),
		GrantorUserID:        id.New("user"),
		GranteeUserID:        id.New("user"),
		ScopeType:            "project",
		ScopeRef:             "proj1",
		AllowedArtifactTypes: []core.ArtifactType{core.ArtifactTypeSummary},
		MaxSensitivity:       core.SensitivityLow,
		AllowedPurposes:      []core.QueryPurpose{core.QueryPurposeStatusCheck},
		CreatedAt:            time.Now().UTC(),
	}
	store.SaveGrant(ctx, grant)

	found, ok, err := store.FindGrant(ctx, grant.PolicyGrantID)
	if err != nil || !ok {
		t.Fatalf("FindGrant: ok=%v err=%v", ok, err)
	}
	if found.PolicyGrantID != grant.PolicyGrantID {
		t.Fatalf("GrantID mismatch")
	}

	_, ok, _ = store.FindGrant(ctx, "nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestListIncomingGrantsForUser(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	granteeUserID := id.New("user")
	grant := core.PolicyGrant{
		PolicyGrantID:        id.New("grant"),
		OrgID:                id.New("org"),
		GrantorUserID:        id.New("user"),
		GranteeUserID:        granteeUserID,
		ScopeType:            "project",
		ScopeRef:             "proj1",
		AllowedArtifactTypes: []core.ArtifactType{core.ArtifactTypeSummary},
		MaxSensitivity:       core.SensitivityLow,
		AllowedPurposes:      []core.QueryPurpose{core.QueryPurposeStatusCheck},
		CreatedAt:            time.Now().UTC(),
	}
	store.SaveGrant(ctx, grant)

	grants, err := store.ListIncomingGrantsForUser(ctx, granteeUserID, 50, 0)
	if err != nil || len(grants) != 1 {
		t.Fatalf("ListIncomingGrantsForUser: len=%d err=%v", len(grants), err)
	}
}

func TestFindAndUpdateRequest(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	now := time.Now().UTC()
	req := core.Request{
		RequestID:      id.New("request"),
		OrgID:          id.New("org"),
		FromAgentID:    id.New("agent"),
		FromUserID:     id.New("user"),
		ToAgentID:      id.New("agent"),
		ToUserID:       id.New("user"),
		RequestType:    "ask_for_review",
		Title:          "Please review",
		Content:        "Review my PR",
		State:          "pending",
		CreatedAt:      now,
	}
	store.SaveRequest(ctx, req)

	found, ok, err := store.FindRequest(ctx, req.RequestID)
	if err != nil || !ok {
		t.Fatalf("FindRequest: ok=%v err=%v", ok, err)
	}
	if found.Title != "Please review" {
		t.Fatalf("Title mismatch")
	}

	_, ok, _ = store.FindRequest(ctx, "nonexistent")
	if ok {
		t.Fatal("expected not found")
	}

	updated, ok, err := store.UpdateRequestState(ctx, req.RequestID, core.RequestStateAccepted, core.ApprovalStateNotRequired, "lgtm")
	if err != nil || !ok {
		t.Fatalf("UpdateRequestState: ok=%v err=%v", ok, err)
	}
	if updated.State != core.RequestStateAccepted {
		t.Fatalf("expected accepted state, got %q", updated.State)
	}
}

func TestFindApproval(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	approval := core.Approval{
		ApprovalID:  id.New("approval"),
		OrgID:       id.New("org"),
		AgentID:     id.New("agent"),
		OwnerUserID: id.New("user"),
		SubjectType: "request",
		SubjectID:   id.New("request"),
		Reason:      "needs approval",
		State:       core.ApprovalStatePending,
		CreatedAt:   time.Now().UTC(),
	}
	store.SaveApproval(ctx, approval)

	found, ok, err := store.FindApproval(ctx, approval.ApprovalID)
	if err != nil || !ok {
		t.Fatalf("FindApproval: ok=%v err=%v", ok, err)
	}
	if found.Reason != "needs approval" {
		t.Fatalf("Reason mismatch")
	}

	_, ok, _ = store.FindApproval(ctx, "nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestWithTx(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	err := store.WithTx(ctx, func(tx storage.StoreTx) error {
		org := core.Organization{OrgID: id.New("org"), Slug: "tx-org", Name: "TX Org"}
		tx.UpsertOrganization(ctx, org)
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	// Verify the org was created
	found, err := store.FindOrgBySlug(ctx, "tx-org")
	if err != nil {
		t.Fatalf("FindOrgBySlug: %v", err)
	}
	if found.Name != "TX Org" {
		t.Fatalf("Name mismatch: %q", found.Name)
	}
}

func TestFindAgentRegistrationChallenge(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	now := time.Now().UTC()
	challenge := core.AgentRegistrationChallenge{
		ChallengeID: id.New("challenge"),
		OrgSlug:     "test-org",
		OwnerEmail:  "test@example.com",
		AgentName:   "test-agent",
		ClientType:  "mcp",
		PublicKey:    "dGVzdA==",
		Nonce:        "test-nonce",
		ExpiresAt:   now.Add(5 * time.Minute),
	}
	store.SaveAgentRegistrationChallenge(ctx, challenge)

	found, ok, err := store.FindAgentRegistrationChallenge(ctx, challenge.ChallengeID)
	if err != nil || !ok {
		t.Fatalf("FindAgentRegistrationChallenge: ok=%v err=%v", ok, err)
	}
	if found.Nonce != "test-nonce" {
		t.Fatalf("Nonce mismatch: %q", found.Nonce)
	}

	_, ok, _ = store.FindAgentRegistrationChallenge(ctx, "nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}
