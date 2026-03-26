package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"alice/internal/agents"
	"alice/internal/approvals"
	"alice/internal/app/services"
	"alice/internal/artifacts"
	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/requests"
	"alice/internal/storage/memory"
)

// newTestHandlerWithApprovals builds a handler where the agent service has the
// AgentApprovalRepository wired up, enabling admin-approval flows.
func newTestHandlerWithApprovals(t *testing.T) http.Handler {
	t.Helper()
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     time.Hour,
		DefaultOrgName:   "Test Org",
	}
	artifactSvc := artifacts.NewService(store)
	policySvc := policy.NewService(store)
	agentService := agents.NewService(store, store, store, store, store, cfg, store).
		WithApprovalRepository(store)
	return NewRouter(services.Container{
		Agents:    agentService,
		Artifacts: artifactSvc,
		Policy:    policySvc,
		Queries:   queries.NewService(store, artifactSvc, policySvc, store, store),
		Requests:  requests.NewService(store, store, store),
		Approvals: approvals.NewService(store, store, store, store),
		Audit:     audit.NewService(store),
	})
}

func TestHealthz(t *testing.T) {
	handler := newTestHandler(t, "")
	rec := performJSON(t, handler, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode healthz response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected status=ok, got %q", payload["status"])
	}
}

func TestRevokePermission(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice grants Bob permission.
	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("grant permission status = %d body=%s", rec.Code, rec.Body.String())
	}
	var grantResp map[string]any
	json.NewDecoder(rec.Body).Decode(&grantResp)
	grantID := grantResp["policy_grant_id"].(string)

	// Alice revokes the grant.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/policy-grants/"+grantID, alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke permission status = %d body=%s", rec.Code, rec.Body.String())
	}
	var revokeResp map[string]any
	json.NewDecoder(rec.Body).Decode(&revokeResp)
	if revokeResp["revoked"] != true {
		t.Fatalf("expected revoked=true, got %v", revokeResp["revoked"])
	}

	// Bob should not appear in Alice's peers list.
	_ = bob
	rec = performJSON(t, handler, http.MethodGet, "/v1/peers", bob.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list peers status = %d", rec.Code)
	}
	var peersResp map[string]any
	json.NewDecoder(rec.Body).Decode(&peersResp)
	if len(peersResp["peers"].([]any)) != 0 {
		t.Fatalf("expected empty peers list after revocation, got %d", len(peersResp["peers"].([]any)))
	}
}

func TestRevokePermission_NonOwner(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice creates a grant.
	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	})
	var grantResp map[string]any
	json.NewDecoder(rec.Body).Decode(&grantResp)
	grantID := grantResp["policy_grant_id"].(string)

	// Bob tries to revoke Alice's grant — should fail.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/policy-grants/"+grantID, bob.AccessToken, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("non-owner revocation should fail, got %d", rec.Code)
	}
}

func TestListSentRequests(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "ask_for_review",
		"title":         "Need review",
		"content":       "Can you review this PR?",
	})

	rec := performJSON(t, handler, http.MethodGet, "/v1/requests/sent", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sent requests status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	json.NewDecoder(rec.Body).Decode(&payload)
	list := payload["requests"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 sent request, got %d", len(list))
	}
	item := list[0].(map[string]any)
	if item["to_user_email"] != fixture.BobEmail {
		t.Fatalf("unexpected to_user_email: %v", item["to_user_email"])
	}
}

func TestAuditSummary(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Publish an artifact and grant a permission to generate audit events.
	publishArtifact(t, handler, alice.AccessToken, core.Artifact{
		Type:           core.ArtifactTypeSummary,
		Title:          "Alice status",
		Content:        "All good",
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{
			{SourceSystem: "test", SourceType: "manual", SourceID: "1", ObservedAt: time.Now().UTC()},
		},
	})
	grantPermission(t, handler, alice.AccessToken, map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	})

	_ = bob

	rec := performJSON(t, handler, http.MethodGet, "/v1/audit/summary", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit summary status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	json.NewDecoder(rec.Body).Decode(&payload)
	events, ok := payload["events"].([]any)
	if !ok {
		t.Fatalf("expected events array in audit summary response")
	}
	if len(events) == 0 {
		t.Fatal("expected at least one audit event after publishing artifact and granting permission")
	}
}

func TestAuditSummary_InvalidSinceParam(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/audit/summary?since=not-a-date", alice.AccessToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid since param, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListPendingAgents_NonAdmin(t *testing.T) {
	// Use a handler with the approval repository wired so the admin check runs.
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	// First registration makes alice admin; bob is member.
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/orgs/pending-agents", bob.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list-pending-agents: expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListAllowedPeers_EmptyInitially(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/peers", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list peers status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	json.NewDecoder(rec.Body).Decode(&payload)
	peers := payload["peers"].([]any)
	if len(peers) != 0 {
		t.Fatalf("expected empty peers initially, got %d", len(peers))
	}
}

func TestListAllowedPeers_AfterGrant(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice grants Bob permission — Bob should now see Alice as an allowed peer.
	grantPermission(t, handler, alice.AccessToken, map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	})

	rec := performJSON(t, handler, http.MethodGet, "/v1/peers", bob.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list peers status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	json.NewDecoder(rec.Body).Decode(&payload)
	peers := payload["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer after grant, got %d", len(peers))
	}
}
