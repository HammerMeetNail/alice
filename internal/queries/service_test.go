package queries_test

import (
	"context"
	"testing"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/queries"
	"alice/internal/storage/memory"
)

// setupQueryTest creates a memory store populated with two agents in the same
// org plus one artifact owned by the "target" agent. It returns the store,
// the two user IDs, and the artifact.
func setupQueryTest(t *testing.T) (store *memory.Store, fromUserID, toUserID string, artifact core.Artifact) {
	t.Helper()
	ctx := context.Background()
	store = memory.New()

	orgID := id.New("org")

	fromUser := core.User{
		UserID: id.New("user"),
		OrgID:  orgID,
		Email:  "alice@example.com",
		Status: "active",
	}
	toUser := core.User{
		UserID: id.New("user"),
		OrgID:  orgID,
		Email:  "bob@example.com",
		Status: "active",
	}
	fromUser, _ = store.UpsertUser(ctx, fromUser)
	toUser, _ = store.UpsertUser(ctx, toUser)

	fromAgent := core.Agent{AgentID: id.New("agent"), OrgID: orgID, OwnerUserID: fromUser.UserID, Status: "active"}
	toAgent := core.Agent{AgentID: id.New("agent"), OrgID: orgID, OwnerUserID: toUser.UserID, Status: "active"}
	store.UpsertAgent(ctx, fromAgent)
	store.UpsertAgent(ctx, toAgent)

	now := time.Now().UTC()
	artifact = core.Artifact{
		ArtifactID:     id.New("artifact"),
		OwnerUserID:    toUser.UserID,
		OrgID:          orgID,
		Type:           core.ArtifactTypeSummary,
		Title:          "Bob's summary",
		Content:        "All green.",
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		CreatedAt:      now,
		SourceRefs: []core.SourceReference{
			{SourceSystem: "test", SourceType: "manual", SourceID: "1", ObservedAt: now},
		},
	}
	store.SaveArtifact(ctx, artifact)

	return store, fromUser.UserID, toUser.UserID, artifact
}

func makeGrant(orgID, grantorUserID, granteeUserID string, artifactTypes []core.ArtifactType, maxSensitivity core.Sensitivity, purposes []core.QueryPurpose, scopeRef string) core.PolicyGrant {
	return core.PolicyGrant{
		PolicyGrantID:        id.New("grant"),
		OrgID:                orgID,
		GrantorUserID:        grantorUserID,
		GranteeUserID:        granteeUserID,
		ScopeType:            "project",
		ScopeRef:             scopeRef,
		AllowedArtifactTypes: artifactTypes,
		MaxSensitivity:       maxSensitivity,
		AllowedPurposes:      purposes,
		VisibilityMode:       core.VisibilityModeExplicitGrantsOnly,
		CreatedAt:            time.Now().UTC(),
	}
}

func makeQuery(fromAgentID, fromUserID, toAgentID, toUserID string, artifactTypes []core.ArtifactType, purpose core.QueryPurpose) core.Query {
	now := time.Now().UTC()
	return core.Query{
		QueryID:        id.New("query"),
		FromAgentID:    fromAgentID,
		FromUserID:     fromUserID,
		ToAgentID:      toAgentID,
		ToUserID:       toUserID,
		Purpose:        purpose,
		RequestedTypes: artifactTypes,
		TimeWindow:     core.TimeWindow{Start: now.Add(-24 * time.Hour), End: now.Add(time.Hour)},
		CreatedAt:      now,
	}
}

func TestQueryEvaluate_NoGrant(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)

	_, err := svc.Evaluate(ctx, query)
	if err == nil || err != queries.ErrPermissionDenied {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
}

func TestQueryEvaluate_WithMatchingGrant(t *testing.T) {
	store, fromUserID, toUserID, artifact := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	grant := makeGrant(fromAgent.OrgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "")
	store.SaveGrant(ctx, grant)

	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)

	resp, err := svc.Evaluate(ctx, query)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(resp.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(resp.Artifacts))
	}
	if resp.Artifacts[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("unexpected artifact ID %s", resp.Artifacts[0].ArtifactID)
	}
}

func TestQueryEvaluate_WrongPurpose(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	grant := makeGrant(fromAgent.OrgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeDependencyCheck}, "") // grant allows DependencyCheck only
	store.SaveGrant(ctx, grant)

	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck) // query uses StatusCheck

	resp, err := svc.Evaluate(ctx, query)
	if err != nil {
		t.Fatalf("expected empty result, got error %v", err)
	}
	if len(resp.Artifacts) != 0 {
		t.Fatalf("expected 0 artifacts (wrong purpose), got %d", len(resp.Artifacts))
	}
}

func TestQueryEvaluate_WrongArtifactType(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	grant := makeGrant(fromAgent.OrgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeBlocker}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "") // grant allows Blocker only
	store.SaveGrant(ctx, grant)

	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck) // query requests Summary

	resp, err := svc.Evaluate(ctx, query)
	if err != nil {
		t.Fatalf("expected empty result, got error %v", err)
	}
	if len(resp.Artifacts) != 0 {
		t.Fatalf("expected 0 artifacts (wrong artifact type in grant), got %d", len(resp.Artifacts))
	}
}

func TestQueryEvaluate_SensitivityCeilingExceeded(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)

	// Publish a high-sensitivity artifact
	now := time.Now().UTC()
	highArtifact := core.Artifact{
		ArtifactID:     id.New("artifact"),
		OwnerUserID:    toUserID,
		Type:           core.ArtifactTypeSummary,
		Title:          "Confidential summary",
		Content:        "Sensitive content.",
		Sensitivity:    core.SensitivityHigh,
		Confidence:     0.9,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		CreatedAt:      now,
		SourceRefs:     []core.SourceReference{{SourceSystem: "test", SourceType: "manual", SourceID: "2", ObservedAt: now}},
	}
	store.SaveArtifact(context.Background(), highArtifact)

	fromAgent, _, _ := store.FindAgentByUserID(context.Background(), fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(context.Background(), toUserID)

	// Grant allows only Low sensitivity
	grant := makeGrant(fromAgent.OrgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "")
	store.SaveGrant(context.Background(), grant)

	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)

	resp, err := svc.Evaluate(context.Background(), query)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	// The low-sensitivity artifact (from setup) should appear; the high-sensitivity should not
	for _, a := range resp.Artifacts {
		if a.ArtifactID == highArtifact.ArtifactID {
			t.Fatal("high-sensitivity artifact should not appear under low-sensitivity grant ceiling")
		}
	}
}

func TestQueryEvaluate_RevokedGrant(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	grant := makeGrant(fromAgent.OrgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "")
	saved, _ := store.SaveGrant(ctx, grant)

	// Verify grant works before revocation
	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)

	resp, err := svc.Evaluate(ctx, query)
	if err != nil || len(resp.Artifacts) == 0 {
		t.Fatalf("pre-revocation: expected artifacts, err=%v", err)
	}

	// Revoke the grant
	store.RevokeGrant(ctx, saved.PolicyGrantID, toUserID)

	// Query should now find no grants and deny
	query.QueryID = id.New("query") // fresh query ID
	_, err = svc.Evaluate(ctx, query)
	if err != queries.ErrPermissionDenied {
		t.Fatalf("post-revocation: expected ErrPermissionDenied, got %v", err)
	}
}

func TestQueryEvaluate_ExpiredGrant(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	past := time.Now().UTC().Add(-time.Hour)
	grant := makeGrant(fromAgent.OrgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "")
	grant.ExpiresAt = &past
	store.SaveGrant(ctx, grant)

	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)

	// Expired grants are now filtered at the storage layer, so the queries
	// service sees no valid grants and denies the query.
	_, err := svc.Evaluate(ctx, query)
	if err != queries.ErrPermissionDenied {
		t.Fatalf("expected ErrPermissionDenied for expired grant, got %v", err)
	}
}

func TestQueryEvaluate_ProjectScope_Match(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)
	ctx := context.Background()

	// Publish an artifact with a project_ref
	now := time.Now().UTC()
	projArtifact := core.Artifact{
		ArtifactID:     id.New("artifact"),
		OwnerUserID:    toUserID,
		Type:           core.ArtifactTypeSummary,
		Title:          "Project summary",
		Content:        "On track.",
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.8,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		CreatedAt:      now,
		SourceRefs:     []core.SourceReference{{SourceSystem: "github", SourceType: "repo", SourceID: "myrepo", ObservedAt: now}},
		StructuredPayload: map[string]any{
			"project_refs": []any{"myproject"},
		},
	}
	store.SaveArtifact(ctx, projArtifact)

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	grant := makeGrant(fromAgent.OrgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "myproject")
	store.SaveGrant(ctx, grant)

	svc := queries.NewService(store, store, store, store, store)
	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)
	query.ProjectScope = []string{"myproject"}

	resp, err := svc.Evaluate(ctx, query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, a := range resp.Artifacts {
		if a.ArtifactID == projArtifact.ArtifactID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected project-scoped artifact in response")
	}
}
