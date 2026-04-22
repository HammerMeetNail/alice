package gatekeeper_test

import (
	"context"
	"testing"
	"time"

	"alice/internal/artifacts"
	"alice/internal/core"
	"alice/internal/gatekeeper"
	"alice/internal/id"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/storage/memory"
)

// TestAutoAnswerWhenGrantAndArtifactsExist confirms the gatekeeper's
// Reporter-style behavior: when a teammate has published a relevant artifact
// and granted access, a request from an eligible type is answered without
// human intervention.
func TestAutoAnswerWhenGrantAndArtifactsExist(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	orgID := id.New("org")
	recipient := core.User{UserID: id.New("user"), OrgID: orgID, Email: "bob@example.com"}
	sender := core.User{UserID: id.New("user"), OrgID: orgID, Email: "alice@example.com"}
	if _, err := store.UpsertUser(ctx, recipient); err != nil {
		t.Fatalf("save recipient: %v", err)
	}
	if _, err := store.UpsertUser(ctx, sender); err != nil {
		t.Fatalf("save sender: %v", err)
	}

	// Bob publishes a status_delta artifact.
	artSvc := artifacts.NewService(store)
	artifact := core.Artifact{
		ArtifactID:     id.New("artifact"),
		OrgID:          orgID,
		OwnerUserID:    recipient.UserID,
		OwnerAgentID:   id.New("agent"),
		Type:           core.ArtifactTypeStatusDelta,
		Title:          "Auth refactor",
		Content:        "Extracting JWT validation. Two PRs open.",
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{{
			SourceSystem: "test",
			SourceType:   "manual",
			SourceID:     "seed",
			ObservedAt:   time.Now().UTC(),
			TrustClass:   core.TrustClassStructuredSystem,
			Sensitivity:  core.SensitivityLow,
		}},
	}
	if _, err := store.SaveArtifact(ctx, artifact); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	_ = artSvc

	// Bob grants Alice status_check access.
	polSvc := policy.NewService(store)
	if _, err := polSvc.Grant(ctx, orgID, recipient, sender, "project", "*",
		[]core.ArtifactType{core.ArtifactTypeStatusDelta, core.ArtifactTypeSummary},
		core.SensitivityMedium,
		[]core.QueryPurpose{core.QueryPurposeRequestContext, core.QueryPurposeStatusCheck}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	qSvc := queries.NewService(store, artSvc, polSvc, store, store)
	gk := gatekeeper.NewService(qSvc, gatekeeper.Options{})

	req := core.Request{
		RequestID:   id.New("request"),
		OrgID:       orgID,
		FromAgentID: id.New("agent"),
		FromUserID:  sender.UserID,
		ToAgentID:   id.New("agent"),
		ToUserID:    recipient.UserID,
		RequestType: "question",
		Title:       "What is bob working on?",
		Content:     "Context for today's planning call.",
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		State:       core.RequestStatePending,
	}

	verdict := gk.Evaluate(ctx, req)
	if !verdict.Answered {
		t.Fatalf("expected auto-answer, got reason=%q", verdict.Reason)
	}
	if verdict.Confidence < 0.6 {
		t.Fatalf("expected confidence above threshold, got %v", verdict.Confidence)
	}
	if len(verdict.ArtifactIDs) == 0 {
		t.Fatalf("expected at least one supporting artifact id")
	}
}

// TestDeferWhenNoGrant confirms the gatekeeper falls through to the human
// path when no grant exists between the sender and recipient.
func TestDeferWhenNoGrant(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	orgID := id.New("org")
	recipientID := id.New("user")
	senderID := id.New("user")

	qSvc := queries.NewService(store, artifacts.NewService(store), policy.NewService(store), store, store)
	gk := gatekeeper.NewService(qSvc, gatekeeper.Options{})

	verdict := gk.Evaluate(ctx, core.Request{
		OrgID:       orgID,
		FromUserID:  senderID,
		ToUserID:    recipientID,
		RequestType: "question",
		Title:       "Anything?",
		Content:     "status",
		CreatedAt:   time.Now().UTC(),
	})
	if verdict.Answered {
		t.Fatalf("expected deferral when no grant exists")
	}
}

// TestDeferWhenConfidenceBelowConfiguredThreshold confirms operators can
// raise the gatekeeper's confidence bar via Options.ConfidenceThreshold. An
// artifact at 0.9 that would auto-answer under the default 0.6 threshold
// must defer when the threshold is raised to 0.95.
func TestDeferWhenConfidenceBelowConfiguredThreshold(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	orgID := id.New("org")
	recipient := core.User{UserID: id.New("user"), OrgID: orgID, Email: "bob@example.com"}
	sender := core.User{UserID: id.New("user"), OrgID: orgID, Email: "alice@example.com"}
	if _, err := store.UpsertUser(ctx, recipient); err != nil {
		t.Fatalf("save recipient: %v", err)
	}
	if _, err := store.UpsertUser(ctx, sender); err != nil {
		t.Fatalf("save sender: %v", err)
	}
	if _, err := store.SaveArtifact(ctx, core.Artifact{
		ArtifactID:     id.New("artifact"),
		OrgID:          orgID,
		OwnerUserID:    recipient.UserID,
		Type:           core.ArtifactTypeStatusDelta,
		Title:          "Auth refactor",
		Content:        "Extracting JWT validation.",
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{{
			SourceSystem: "test", SourceType: "manual", SourceID: "seed",
			ObservedAt: time.Now().UTC(), TrustClass: core.TrustClassStructuredSystem,
			Sensitivity: core.SensitivityLow,
		}},
	}); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	polSvc := policy.NewService(store)
	if _, err := polSvc.Grant(ctx, orgID, recipient, sender, "project", "*",
		[]core.ArtifactType{core.ArtifactTypeStatusDelta},
		core.SensitivityMedium,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	qSvc := queries.NewService(store, artifacts.NewService(store), polSvc, store, store)
	gk := gatekeeper.NewService(qSvc, gatekeeper.Options{ConfidenceThreshold: 0.95})

	verdict := gk.Evaluate(ctx, core.Request{
		OrgID:       orgID,
		FromUserID:  sender.UserID,
		ToUserID:    recipient.UserID,
		RequestType: "question",
		Title:       "What is bob working on?",
		Content:     "status",
		CreatedAt:   time.Now().UTC(),
	})
	if verdict.Answered {
		t.Fatalf("expected deferral when confidence %.2f is below threshold 0.95", verdict.Confidence)
	}
	if verdict.Confidence == 0 {
		t.Fatalf("expected non-zero confidence recorded in verdict even on deferral")
	}
}

// TestPerOrgOverrideRaisesThreshold confirms that a per-org override beats
// the server-wide (Options) default: a request that would auto-answer at the
// 0.6 server default falls through to the human when the org sets 0.95.
func TestPerOrgOverrideRaisesThreshold(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	orgID := id.New("org")
	recipient := core.User{UserID: id.New("user"), OrgID: orgID, Email: "bob@example.com"}
	sender := core.User{UserID: id.New("user"), OrgID: orgID, Email: "alice@example.com"}
	orgThreshold := 0.95
	if _, err := store.UpsertOrganization(ctx, core.Organization{
		OrgID:                         orgID,
		Slug:                          "tuned-org",
		VerificationMode:              "email_otp",
		GatekeeperConfidenceThreshold: &orgThreshold,
	}); err != nil {
		t.Fatalf("upsert org: %v", err)
	}
	if _, err := store.UpsertUser(ctx, recipient); err != nil {
		t.Fatalf("save recipient: %v", err)
	}
	if _, err := store.UpsertUser(ctx, sender); err != nil {
		t.Fatalf("save sender: %v", err)
	}
	if _, err := store.SaveArtifact(ctx, core.Artifact{
		ArtifactID:     id.New("artifact"),
		OrgID:          orgID,
		OwnerUserID:    recipient.UserID,
		Type:           core.ArtifactTypeStatusDelta,
		Title:          "Auth refactor",
		Content:        "Extracting JWT validation.",
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{{
			SourceSystem: "test", SourceType: "manual", SourceID: "seed",
			ObservedAt: time.Now().UTC(), TrustClass: core.TrustClassStructuredSystem,
			Sensitivity: core.SensitivityLow,
		}},
	}); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	polSvc := policy.NewService(store)
	if _, err := polSvc.Grant(ctx, orgID, recipient, sender, "project", "*",
		[]core.ArtifactType{core.ArtifactTypeStatusDelta},
		core.SensitivityMedium,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	qSvc := queries.NewService(store, artifacts.NewService(store), polSvc, store, store)
	// Options defaults to 0.6, but the per-org override of 0.95 must win.
	gk := gatekeeper.NewService(qSvc, gatekeeper.Options{}).WithOrgLookup(store)

	verdict := gk.Evaluate(ctx, core.Request{
		OrgID:       orgID,
		FromUserID:  sender.UserID,
		ToUserID:    recipient.UserID,
		RequestType: "question",
		Title:       "What is bob working on?",
		Content:     "status",
		CreatedAt:   time.Now().UTC(),
	})
	if verdict.Answered {
		t.Fatalf("expected deferral under per-org 0.95 threshold, got answered with confidence %.2f", verdict.Confidence)
	}
}

// TestPerOrgOverrideLowersThreshold covers the opposite direction: a server
// default high enough to defer (0.95) but a permissive per-org override (0.3)
// lets the gatekeeper answer. Validates the fallback chain, not just clamping.
func TestPerOrgOverrideLowersThreshold(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	orgID := id.New("org")
	recipient := core.User{UserID: id.New("user"), OrgID: orgID, Email: "bob@example.com"}
	sender := core.User{UserID: id.New("user"), OrgID: orgID, Email: "alice@example.com"}
	orgThreshold := 0.3
	if _, err := store.UpsertOrganization(ctx, core.Organization{
		OrgID:                         orgID,
		Slug:                          "permissive-org",
		VerificationMode:              "email_otp",
		GatekeeperConfidenceThreshold: &orgThreshold,
	}); err != nil {
		t.Fatalf("upsert org: %v", err)
	}
	if _, err := store.UpsertUser(ctx, recipient); err != nil {
		t.Fatalf("save recipient: %v", err)
	}
	if _, err := store.UpsertUser(ctx, sender); err != nil {
		t.Fatalf("save sender: %v", err)
	}
	if _, err := store.SaveArtifact(ctx, core.Artifact{
		ArtifactID:     id.New("artifact"),
		OrgID:          orgID,
		OwnerUserID:    recipient.UserID,
		Type:           core.ArtifactTypeStatusDelta,
		Title:          "Auth refactor",
		Content:        "Extracting JWT validation.",
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Confidence:     0.5,
		SourceRefs: []core.SourceReference{{
			SourceSystem: "test", SourceType: "manual", SourceID: "seed",
			ObservedAt: time.Now().UTC(), TrustClass: core.TrustClassStructuredSystem,
			Sensitivity: core.SensitivityLow,
		}},
	}); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	polSvc := policy.NewService(store)
	if _, err := polSvc.Grant(ctx, orgID, recipient, sender, "project", "*",
		[]core.ArtifactType{core.ArtifactTypeStatusDelta},
		core.SensitivityMedium,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	qSvc := queries.NewService(store, artifacts.NewService(store), polSvc, store, store)
	// Options threshold 0.95 would defer this 0.5-confidence artifact, but
	// the per-org override of 0.3 lets it through.
	gk := gatekeeper.NewService(qSvc, gatekeeper.Options{ConfidenceThreshold: 0.95}).WithOrgLookup(store)

	verdict := gk.Evaluate(ctx, core.Request{
		OrgID:       orgID,
		FromUserID:  sender.UserID,
		ToUserID:    recipient.UserID,
		RequestType: "question",
		Title:       "What is bob working on?",
		Content:     "status",
		CreatedAt:   time.Now().UTC(),
	})
	if !verdict.Answered {
		t.Fatalf("expected auto-answer under per-org 0.3 threshold, got reason=%q confidence=%.2f", verdict.Reason, verdict.Confidence)
	}
}

// TestPerOrgOverrideFallsBackWhenAbsent confirms that an org without a per-org
// override still uses the Options threshold.
func TestPerOrgOverrideFallsBackWhenAbsent(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	orgID := id.New("org")
	recipient := core.User{UserID: id.New("user"), OrgID: orgID, Email: "bob@example.com"}
	sender := core.User{UserID: id.New("user"), OrgID: orgID, Email: "alice@example.com"}
	// Org has no overrides set.
	if _, err := store.UpsertOrganization(ctx, core.Organization{
		OrgID:            orgID,
		Slug:             "default-org",
		VerificationMode: "email_otp",
	}); err != nil {
		t.Fatalf("upsert org: %v", err)
	}
	if _, err := store.UpsertUser(ctx, recipient); err != nil {
		t.Fatalf("save recipient: %v", err)
	}
	if _, err := store.UpsertUser(ctx, sender); err != nil {
		t.Fatalf("save sender: %v", err)
	}
	if _, err := store.SaveArtifact(ctx, core.Artifact{
		ArtifactID:     id.New("artifact"),
		OrgID:          orgID,
		OwnerUserID:    recipient.UserID,
		Type:           core.ArtifactTypeStatusDelta,
		Title:          "Auth refactor",
		Content:        "Extracting JWT validation.",
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{{
			SourceSystem: "test", SourceType: "manual", SourceID: "seed",
			ObservedAt: time.Now().UTC(), TrustClass: core.TrustClassStructuredSystem,
			Sensitivity: core.SensitivityLow,
		}},
	}); err != nil {
		t.Fatalf("save artifact: %v", err)
	}
	polSvc := policy.NewService(store)
	if _, err := polSvc.Grant(ctx, orgID, recipient, sender, "project", "*",
		[]core.ArtifactType{core.ArtifactTypeStatusDelta},
		core.SensitivityMedium,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	qSvc := queries.NewService(store, artifacts.NewService(store), polSvc, store, store)
	// Server-wide Options 0.95 must defer this 0.9-confidence artifact when
	// the org has no override.
	gk := gatekeeper.NewService(qSvc, gatekeeper.Options{ConfidenceThreshold: 0.95}).WithOrgLookup(store)

	verdict := gk.Evaluate(ctx, core.Request{
		OrgID:       orgID,
		FromUserID:  sender.UserID,
		ToUserID:    recipient.UserID,
		RequestType: "question",
		Title:       "What is bob working on?",
		Content:     "status",
		CreatedAt:   time.Now().UTC(),
	})
	if verdict.Answered {
		t.Fatalf("expected deferral when server-wide 0.95 applies, got answered")
	}
}

// TestDeferWhenRequestTypeIneligible confirms action-like request types (e.g.
// ask_for_time) are never auto-answered, since the agent cannot act on the
// user's behalf.
func TestDeferWhenRequestTypeIneligible(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	qSvc := queries.NewService(store, artifacts.NewService(store), policy.NewService(store), store, store)
	gk := gatekeeper.NewService(qSvc, gatekeeper.Options{})
	verdict := gk.Evaluate(ctx, core.Request{
		RequestType: "ask_for_time",
		Title:       "Have 15 minutes today?",
	})
	if verdict.Answered {
		t.Fatalf("expected ineligible request type to defer")
	}
}
