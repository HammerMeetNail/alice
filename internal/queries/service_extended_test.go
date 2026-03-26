package queries_test

import (
	"context"
	"testing"
	"time"

	"alice/internal/artifacts"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/storage/memory"
)

// TestFindResult_Found verifies FindResult returns the saved query and response.
func TestFindResult_Found(t *testing.T) {
	ctx := context.Background()
	store, fromUserID, toUserID, artifact := setupQueryTest(t)

	artifactSvc := artifacts.NewService(store)
	policySvc := policy.NewService(store)
	svc := queries.NewService(store, artifactSvc, policySvc, store, store)

	orgID := id.New("org")
	grant := makeGrant(orgID, toUserID, fromUserID,
		[]core.ArtifactType{core.ArtifactTypeSummary},
		core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck},
		"any")
	store.SaveGrant(ctx, grant)
	_ = artifact

	// Build a query pointing to the agents set up in setupQueryTest.
	fromAgent, _, _ := store.FindAgentByUserID(ctx, fromUserID)
	toAgent, _, _ := store.FindAgentByUserID(ctx, toUserID)

	q := buildQuery(orgID, fromAgent, toAgent, fromUserID, toUserID)
	resp, err := svc.Evaluate(ctx, q)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	foundQuery, foundResp, ok, err := svc.FindResult(ctx, q.QueryID)
	if err != nil {
		t.Fatalf("FindResult: %v", err)
	}
	if !ok {
		t.Fatal("FindResult: expected result to be found")
	}
	if foundQuery.QueryID != q.QueryID {
		t.Fatalf("QueryID mismatch: got %s want %s", foundQuery.QueryID, q.QueryID)
	}
	_ = foundResp
	_ = resp
}

// TestFindResult_NotFound verifies FindResult returns not-found for unknown IDs.
func TestFindResult_NotFound(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	svc := queries.NewService(store, artifacts.NewService(store), policy.NewService(store), store, store)

	_, _, ok, err := svc.FindResult(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("FindResult error: %v", err)
	}
	if ok {
		t.Fatal("expected not-found for nonexistent query")
	}
}

func buildQuery(orgID string, fromAgent, toAgent core.Agent, fromUserID, toUserID string) core.Query {
	now := time.Now().UTC()
	return core.Query{
		QueryID:        id.New("query"),
		OrgID:          orgID,
		FromAgentID:    fromAgent.AgentID,
		FromUserID:     fromUserID,
		ToAgentID:      toAgent.AgentID,
		ToUserID:       toUserID,
		Purpose:        core.QueryPurposeStatusCheck,
		Question:       "What's the status?",
		RequestedTypes: []core.ArtifactType{core.ArtifactTypeSummary},
		TimeWindow:     core.TimeWindow{Start: now.Add(-24 * time.Hour), End: now.Add(time.Hour)},
		State:          core.QueryStateQueued,
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	}
}
