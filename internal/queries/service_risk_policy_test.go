package queries_test

import (
	"context"
	"testing"

	"alice/internal/core"
	"alice/internal/queries"
	"alice/internal/riskpolicy"
)

// TestQueryEvaluate_RiskPolicyOverride proves an admin-applied policy can
// override the grant's RequiresApprovalAboveRisk ladder. The grant says
// "require approval above L1", the query is L2 (would normally need
// approval), but the applied policy says "allow everything" — so the
// query should succeed without creating an approval.
func TestQueryEvaluate_RiskPolicyOverride(t *testing.T) {
	store, fromUserID, toUserID, artifact := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)
	orgID := fromAgent.OrgID
	_ = artifact

	grant := makeGrant(orgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "")
	grant.RequiresApprovalAboveRisk = core.RiskLevelL1
	store.SaveGrant(ctx, grant)

	// Apply an "allow everything" policy at the org scope. The service
	// bypasses the admin check when users=nil.
	policySvc := riskpolicy.NewService(store, nil)
	if _, err := policySvc.Apply(ctx, core.Agent{OrgID: orgID, OwnerUserID: fromUserID}, "permissive",
		[]byte(`{"rules":[{"when":{},"then":"allow"}]}`)); err != nil {
		t.Fatalf("apply policy: %v", err)
	}

	svc := queries.NewService(store, store, store, store, store).
		WithRiskPolicyEvaluator(policySvc.AsQueriesEvaluator())

	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)
	query.OrgID = orgID
	query.RiskLevel = core.RiskLevelL2

	resp, err := svc.Evaluate(ctx, query)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(resp.Artifacts) != 1 {
		t.Fatalf("expected policy override to surface 1 artifact, got %d (redactions=%v)", len(resp.Artifacts), resp.Redactions)
	}
	if resp.ApprovalState != core.ApprovalStateNotRequired {
		t.Fatalf("expected approval not required under allow-everything policy, got %v", resp.ApprovalState)
	}
}

// TestQueryEvaluate_RiskPolicyRequiresApprovalBelowLadder: the policy can
// also tighten the ladder — an L1 query that would normally pass is
// gated by a policy that says "any status_check requires approval".
func TestQueryEvaluate_RiskPolicyRequiresApprovalBelowLadder(t *testing.T) {
	store, fromUserID, toUserID, _ := setupQueryTest(t)
	ctx := context.Background()

	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)
	orgID := fromAgent.OrgID

	grant := makeGrant(orgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck}, "")
	grant.RequiresApprovalAboveRisk = core.RiskLevelL3
	store.SaveGrant(ctx, grant)

	policySvc := riskpolicy.NewService(store, nil)
	if _, err := policySvc.Apply(ctx, core.Agent{OrgID: orgID, OwnerUserID: fromUserID}, "strict",
		[]byte(`{"rules":[
			{"when":{"purpose":"status_check"},"then":"require_approval","reason":"status always needs review"},
			{"when":{},"then":"allow"}
		]}`)); err != nil {
		t.Fatalf("apply policy: %v", err)
	}

	svc := queries.NewService(store, store, store, store, store).
		WithRiskPolicyEvaluator(policySvc.AsQueriesEvaluator())

	query := makeQuery(fromAgent.AgentID, fromUserID, toAgent.AgentID, toUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary}, core.QueryPurposeStatusCheck)
	query.OrgID = orgID
	query.RiskLevel = core.RiskLevelL0 // well below the L3 grant ladder

	resp, err := svc.Evaluate(ctx, query)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if resp.ApprovalState != core.ApprovalStatePending {
		t.Fatalf("expected approval required under strict policy, got %v", resp.ApprovalState)
	}
	if len(resp.Artifacts) != 0 {
		t.Fatalf("expected 0 artifacts when approval pending, got %d", len(resp.Artifacts))
	}
}
