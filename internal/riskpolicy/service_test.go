package riskpolicy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"alice/internal/core"
	"alice/internal/riskpolicy"
	"alice/internal/storage"
	"alice/internal/storage/memory"
)

// fakeUsers lets the tests inject a specific role without standing up the
// full agents service.
type fakeUsers struct {
	role string
}

func (f fakeUsers) FindUserByID(_ context.Context, _ string) (core.User, bool, error) {
	return core.User{UserID: "u1", Role: f.role}, true, nil
}

func admin() core.Agent {
	return core.Agent{AgentID: "a1", OrgID: "org1", OwnerUserID: "u1"}
}

func TestServiceApplyActivatesNewPolicy(t *testing.T) {
	store := memory.New()
	if _, err := store.UpsertOrganization(context.Background(), core.Organization{OrgID: "org1", Slug: "org1"}); err != nil {
		t.Fatalf("upsert org: %v", err)
	}
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})

	policy, err := svc.Apply(context.Background(), admin(), "baseline", []byte(`{"rules":[{"when":{},"then":"allow"}]}`))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if policy.ActiveAt == nil {
		t.Fatal("expected applied policy to be active")
	}
	if policy.Version != 1 {
		t.Fatalf("expected version 1, got %d", policy.Version)
	}
}

func TestServiceApplyRejectsInvalidPolicy(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})
	if _, err := svc.Apply(context.Background(), admin(), "", []byte(`{"rules":[]}`)); err == nil {
		t.Fatal("expected invalid policy to be rejected")
	}
}

func TestServiceApplyEnforcesAdminRole(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleMember})
	_, err := svc.Apply(context.Background(), admin(), "", []byte(`{"rules":[{"when":{},"then":"allow"}]}`))
	if !errors.Is(err, riskpolicy.ErrNotOrgAdmin) {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
}

func TestServiceApplyOnlyOneActivePerOrg(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})
	ctx := context.Background()

	first, err := svc.Apply(ctx, admin(), "v1", []byte(`{"rules":[{"when":{},"then":"allow"}]}`))
	if err != nil {
		t.Fatalf("apply first: %v", err)
	}
	second, err := svc.Apply(ctx, admin(), "v2", []byte(`{"rules":[{"when":{},"then":"deny"}]}`))
	if err != nil {
		t.Fatalf("apply second: %v", err)
	}

	active, ok, err := store.FindActivePolicyForOrg(ctx, "org1")
	if err != nil || !ok {
		t.Fatalf("find active: ok=%v err=%v", ok, err)
	}
	if active.PolicyID != second.PolicyID {
		t.Fatalf("expected latest policy %s active, got %s", second.PolicyID, active.PolicyID)
	}

	// The first version should be in history but not active.
	prior, ok, err := store.FindPolicyByID(ctx, first.PolicyID)
	if err != nil || !ok {
		t.Fatalf("find prior: %v", err)
	}
	if prior.ActiveAt != nil {
		t.Fatalf("prior policy should be deactivated, ActiveAt=%v", prior.ActiveAt)
	}
}

func TestServiceActivateRollsBack(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})
	ctx := context.Background()

	first, _ := svc.Apply(ctx, admin(), "v1", []byte(`{"rules":[{"when":{},"then":"allow"}]}`))
	if _, err := svc.Apply(ctx, admin(), "v2", []byte(`{"rules":[{"when":{},"then":"deny"}]}`)); err != nil {
		t.Fatalf("apply v2: %v", err)
	}

	// Roll back to v1.
	rolled, err := svc.Activate(ctx, admin(), first.PolicyID)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if rolled.PolicyID != first.PolicyID {
		t.Fatalf("expected rollback to return first policy, got %s", rolled.PolicyID)
	}

	active, ok, _ := store.FindActivePolicyForOrg(ctx, "org1")
	if !ok || active.PolicyID != first.PolicyID {
		t.Fatalf("expected %s active after rollback, got %+v", first.PolicyID, active)
	}
}

func TestServiceEvaluateFallsBackToDefaultWhenNoPolicy(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})
	d := svc.Evaluate(context.Background(), "orgX", riskpolicy.Inputs{RiskLevel: core.RiskLevelL4})
	if d.Action != core.RiskDecisionAllow {
		t.Fatalf("expected default-policy allow when no policy set, got %v", d.Action)
	}
}

func TestServiceEvaluateUsesActivePolicy(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})
	ctx := context.Background()

	if _, err := svc.Apply(ctx, admin(), "strict", []byte(`{"rules":[
		{"when":{"risk_level_at_least":"L2"},"then":"require_approval","reason":"strict"},
		{"when":{},"then":"allow"}
	]}`)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	d := svc.Evaluate(ctx, "org1", riskpolicy.Inputs{RiskLevel: core.RiskLevelL2})
	if d.Action != core.RiskDecisionRequireApproval {
		t.Fatalf("expected strict policy to require approval, got %v", d.Action)
	}
}

func TestServiceActivateRequiresSameOrg(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})
	ctx := context.Background()

	// Policy belongs to org1; admin belongs to org1 via the admin() helper.
	saved, err := store.SavePolicy(ctx, core.RiskPolicy{
		PolicyID: "rpolicy_other",
		OrgID:    "otherOrg",
		Name:     "other-owned",
		Version:  1,
		Source:   `{"rules":[{"when":{},"then":"allow"}]}`,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if _, err := svc.Activate(ctx, admin(), saved.PolicyID); err == nil {
		t.Fatal("expected cross-org activate to fail")
	}
}

func TestServiceActivateMissingPolicy(t *testing.T) {
	store := memory.New()
	svc := riskpolicy.NewService(store, fakeUsers{role: core.UserRoleAdmin})
	if _, err := svc.Activate(context.Background(), admin(), "rpolicy_missing"); !errors.Is(err, storage.ErrRiskPolicyNotFound) {
		t.Fatalf("expected ErrRiskPolicyNotFound, got %v", err)
	}
}
