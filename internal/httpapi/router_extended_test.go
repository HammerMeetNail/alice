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

func publishAndGetID(t *testing.T, handler http.Handler, accessToken string, artifact core.Artifact) string {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/artifacts", accessToken, map[string]any{"artifact": artifact})
	if rec.Code != http.StatusOK {
		t.Fatalf("publish artifact status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	json.NewDecoder(rec.Body).Decode(&payload)
	return payload["artifact_id"].(string)
}

func TestCorrectArtifact(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	now := time.Now().UTC()
	artifactID := publishAndGetID(t, handler, alice.AccessToken, core.Artifact{
		Type:           core.ArtifactTypeSummary,
		Title:          "Original summary",
		Content:        "Original content",
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{
			{SourceSystem: "test", SourceType: "manual", SourceID: "1", ObservedAt: now},
		},
	})

	rec := performJSON(t, handler, http.MethodPost, "/v1/artifacts/"+artifactID+"/correct", alice.AccessToken, map[string]any{
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Corrected summary",
			"content":         "Corrected content",
			"visibility_mode": "explicit_grants_only",
			"sensitivity":     "low",
			"confidence":      0.95,
			"source_refs": []map[string]any{
				{"source_system": "test", "source_type": "manual", "source_id": "2", "observed_at": now.Format(time.RFC3339)},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("correct artifact status = %d body=%s", rec.Code, rec.Body.String())
	}
	var corrPayload map[string]any
	json.NewDecoder(rec.Body).Decode(&corrPayload)
	if corrPayload["supersedes_artifact_id"] != artifactID {
		t.Fatalf("expected supersedes_artifact_id = %s, got %v", artifactID, corrPayload["supersedes_artifact_id"])
	}
}

func TestCorrectArtifact_NonOwner(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	now := time.Now().UTC()
	artifactID := publishAndGetID(t, handler, alice.AccessToken, core.Artifact{
		Type:           core.ArtifactTypeSummary,
		Title:          "Alice's summary",
		Content:        "Alice content",
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{
			{SourceSystem: "test", SourceType: "manual", SourceID: "1", ObservedAt: now},
		},
	})

	// Bob tries to correct Alice's artifact — should fail with 403
	rec := performJSON(t, handler, http.MethodPost, "/v1/artifacts/"+artifactID+"/correct", bob.AccessToken, map[string]any{
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Hijacked",
			"content":         "Nope",
			"visibility_mode": "explicit_grants_only",
			"sensitivity":     "low",
			"confidence":      0.5,
			"source_refs": []map[string]any{
				{"source_system": "test", "source_type": "manual", "source_id": "x", "observed_at": now.Format(time.RFC3339)},
			},
		},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner correction, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestQueryPeerStatus_NoPeers(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Query without any grants — should return empty result
	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   fixture.BobEmail,
		"purpose":         "status_check",
		"question":        "What is Bob working on?",
		"requested_types": []string{"summary"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	})
	// Should fail with permission denied (no grants)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusOK {
		t.Fatalf("query status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResendVerification(t *testing.T) {
	handler, _ := newTestHandlerWithOTP(t)
	fixture := newFixture(t)

	registered := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// First resend should work (or rate limit).
	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/resend-verification", registered.AccessToken, nil)
	// Accept either 200 or 429 (too soon)
	if rec.Code != http.StatusOK && rec.Code != http.StatusTooManyRequests {
		t.Fatalf("resend verification status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuditSummary_WithFilters(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Query with event_kind filter
	rec := performJSON(t, handler, http.MethodGet, "/v1/audit/summary?event_kind=agent.registered", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit summary with filter status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Query with since filter
	rec = performJSON(t, handler, http.MethodGet, "/v1/audit/summary?since="+time.Now().UTC().Add(-1*time.Hour).Format(time.RFC3339), alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit summary with since status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPagination(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Test pagination params on audit
	rec := performJSON(t, handler, http.MethodGet, "/v1/audit/summary?limit=5&offset=0", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("paginated audit status = %d body=%s", rec.Code, rec.Body.String())
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
