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
