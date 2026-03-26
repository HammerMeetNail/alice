package artifacts_test

import (
	"context"
	"testing"

	"alice/internal/artifacts"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

func validArtifact() core.Artifact {
	return core.Artifact{
		Type:    core.ArtifactTypeSummary,
		Title:   "Weekly status",
		Content: "All tasks on track.",
		SourceRefs: []core.SourceReference{
			{SourceSystem: "github", SourceType: "pr", SourceID: "42"},
		},
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Confidence:     0.9,
	}
}

func TestPublishArtifact(t *testing.T) {
	svc := artifacts.NewService(memory.New())
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	user := core.User{UserID: agent.OwnerUserID, OrgID: agent.OrgID}

	saved, err := svc.PublishArtifact(ctx, agent, user, validArtifact())
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}
	if saved.ArtifactID == "" {
		t.Fatal("expected non-empty ArtifactID")
	}
	if saved.OrgID != agent.OrgID {
		t.Fatalf("OrgID mismatch: got %s want %s", saved.OrgID, agent.OrgID)
	}
	if saved.OwnerAgentID != agent.AgentID {
		t.Fatalf("OwnerAgentID mismatch: got %s want %s", saved.OwnerAgentID, agent.AgentID)
	}
	if saved.OwnerUserID != user.UserID {
		t.Fatalf("OwnerUserID mismatch: got %s want %s", saved.OwnerUserID, user.UserID)
	}
	if saved.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
}

func TestPublishArtifact_InvalidType(t *testing.T) {
	svc := artifacts.NewService(memory.New())
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	user := core.User{UserID: agent.OwnerUserID}

	a := validArtifact()
	a.Type = "bogus"

	_, err := svc.PublishArtifact(ctx, agent, user, a)
	if !core.IsValidationError(err) {
		t.Fatalf("expected ValidationError for invalid type, got %v", err)
	}
}

func TestPublishArtifact_MissingTitle(t *testing.T) {
	svc := artifacts.NewService(memory.New())
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	user := core.User{UserID: agent.OwnerUserID}

	a := validArtifact()
	a.Title = ""

	_, err := svc.PublishArtifact(ctx, agent, user, a)
	if !core.IsValidationError(err) {
		t.Fatalf("expected ValidationError for missing title, got %v", err)
	}
}

func TestCorrectArtifact(t *testing.T) {
	store := memory.New()
	svc := artifacts.NewService(store)
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	user := core.User{UserID: agent.OwnerUserID, OrgID: agent.OrgID}

	original, err := svc.PublishArtifact(ctx, agent, user, validArtifact())
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}

	correction := validArtifact()
	correction.Title = "Corrected summary"

	corrected, err := svc.CorrectArtifact(ctx, agent, user, original.ArtifactID, correction)
	if err != nil {
		t.Fatalf("CorrectArtifact: %v", err)
	}
	if corrected.SupersedesArtifactID == nil || *corrected.SupersedesArtifactID != original.ArtifactID {
		t.Fatalf("SupersedesArtifactID: got %v, want %s", corrected.SupersedesArtifactID, original.ArtifactID)
	}
	if corrected.Title != "Corrected summary" {
		t.Fatalf("Title mismatch: got %s", corrected.Title)
	}
}

func TestCorrectArtifact_NotOwner(t *testing.T) {
	store := memory.New()
	svc := artifacts.NewService(store)
	ctx := context.Background()

	ownerAgent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	ownerUser := core.User{UserID: ownerAgent.OwnerUserID, OrgID: ownerAgent.OrgID}

	original, err := svc.PublishArtifact(ctx, ownerAgent, ownerUser, validArtifact())
	if err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}

	otherAgent := core.Agent{AgentID: id.New("agent"), OrgID: ownerAgent.OrgID, OwnerUserID: id.New("user")}
	otherUser := core.User{UserID: otherAgent.OwnerUserID, OrgID: ownerAgent.OrgID}

	_, err = svc.CorrectArtifact(ctx, otherAgent, otherUser, original.ArtifactID, validArtifact())
	if !core.IsForbiddenError(err) {
		t.Fatalf("expected ForbiddenError, got %v", err)
	}
}

func TestCorrectArtifact_NotFound(t *testing.T) {
	svc := artifacts.NewService(memory.New())
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	user := core.User{UserID: agent.OwnerUserID}

	_, err := svc.CorrectArtifact(ctx, agent, user, "nonexistent", validArtifact())
	if err == nil {
		t.Fatal("expected error for nonexistent artifact")
	}
}

func TestListArtifactsByOwner(t *testing.T) {
	store := memory.New()
	svc := artifacts.NewService(store)
	ctx := context.Background()

	agent := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	user := core.User{UserID: agent.OwnerUserID, OrgID: agent.OrgID}

	for i := 0; i < 3; i++ {
		if _, err := svc.PublishArtifact(ctx, agent, user, validArtifact()); err != nil {
			t.Fatalf("PublishArtifact %d: %v", i, err)
		}
	}

	list, err := svc.ListArtifactsByOwner(ctx, user.UserID)
	if err != nil {
		t.Fatalf("ListArtifactsByOwner: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 artifacts, got %d", len(list))
	}
}

func TestListArtifactsByOwner_OnlyOwnArtifacts(t *testing.T) {
	store := memory.New()
	svc := artifacts.NewService(store)
	ctx := context.Background()

	agentA := core.Agent{AgentID: id.New("agent"), OrgID: id.New("org"), OwnerUserID: id.New("user")}
	userA := core.User{UserID: agentA.OwnerUserID, OrgID: agentA.OrgID}
	agentB := core.Agent{AgentID: id.New("agent"), OrgID: agentA.OrgID, OwnerUserID: id.New("user")}
	userB := core.User{UserID: agentB.OwnerUserID, OrgID: agentB.OrgID}

	if _, err := svc.PublishArtifact(ctx, agentA, userA, validArtifact()); err != nil {
		t.Fatalf("PublishArtifact A: %v", err)
	}
	if _, err := svc.PublishArtifact(ctx, agentB, userB, validArtifact()); err != nil {
		t.Fatalf("PublishArtifact B: %v", err)
	}

	list, err := svc.ListArtifactsByOwner(ctx, userA.UserID)
	if err != nil {
		t.Fatalf("ListArtifactsByOwner: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 artifact for user A, got %d", len(list))
	}
	if list[0].OwnerUserID != userA.UserID {
		t.Fatalf("artifact belongs to wrong user: %s", list[0].OwnerUserID)
	}
}
