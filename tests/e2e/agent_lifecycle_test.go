//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestAgentLifecycle exercises the full publish→grant→query→revoke cycle over
// a real TCP connection.
func TestAgentLifecycle(t *testing.T) {
	base := newE2EServer(t)
	slug := orgSlug(t)

	alice := registerAgent(t, base, slug, "alice@example.com")
	bob := registerAgent(t, base, slug, "bob@example.com")

	// Bob publishes a status artifact.
	doJSON(t, base, http.MethodPost, "/v1/artifacts", bob.AccessToken, map[string]any{
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Working on payments",
			"content":         "PR #42 in review.",
			"visibility_mode": "explicit_grants_only",
			"sensitivity":     "low",
			"confidence":      0.9,
			"structured_payload": map[string]any{
				"project_refs": []string{"payments-api"},
			},
			"source_refs": []map[string]any{{
				"source_system": "github",
				"source_type":   "pull_request",
				"source_id":     "org/repo:pr:42",
				"observed_at":   time.Now().UTC().Format(time.RFC3339),
			}},
		},
	}, http.StatusOK, nil)

	// Bob grants Alice permission.
	var grantResp map[string]any
	doJSON(t, base, http.MethodPost, "/v1/policy-grants", bob.AccessToken, map[string]any{
		"grantee_user_email":     "alice@example.com",
		"scope_type":             "project",
		"scope_ref":              "payments-api",
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	}, http.StatusOK, &grantResp)
	grantID := str(grantResp, "policy_grant_id")

	// Alice queries Bob's status → should succeed with one artifact.
	var queryResp map[string]any
	doJSON(t, base, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   "bob@example.com",
		"purpose":         "status_check",
		"question":        "What is Bob working on?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
	}, http.StatusOK, &queryResp)

	queryID := str(queryResp, "query_id")

	var resultResp map[string]any
	doJSON(t, base, http.MethodGet, "/v1/queries/"+queryID, alice.AccessToken, nil, http.StatusOK, &resultResp)

	responsePayload := resultResp["response"].(map[string]any)
	artifacts := responsePayload["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact in query result, got %d", len(artifacts))
	}

	// Alice queries Bob with a purpose not covered by the grant → succeeds but
	// returns 0 artifacts (no matching grant for this purpose).
	var mismatchResp map[string]any
	doJSON(t, base, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   "bob@example.com",
		"purpose":         "dependency_check",
		"question":        "Any blockers?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
	}, http.StatusOK, &mismatchResp)

	mismatchQueryID := str(mismatchResp, "query_id")
	var mismatchResult map[string]any
	doJSON(t, base, http.MethodGet, "/v1/queries/"+mismatchQueryID, alice.AccessToken, nil, http.StatusOK, &mismatchResult)
	mismatchResponse := mismatchResult["response"].(map[string]any)
	mismatchArtifacts := mismatchResponse["artifacts"].([]any)
	if len(mismatchArtifacts) != 0 {
		t.Fatalf("expected 0 artifacts for wrong-purpose query, got %d", len(mismatchArtifacts))
	}

	// Bob revokes the grant → Alice's peers list becomes empty.
	doJSON(t, base, http.MethodDelete, "/v1/policy-grants/"+grantID, bob.AccessToken, nil, http.StatusOK, nil)

	var peersResp map[string]any
	doJSON(t, base, http.MethodGet, "/v1/peers", alice.AccessToken, nil, http.StatusOK, &peersResp)
	peers := peersResp["peers"].([]any)
	if len(peers) != 0 {
		t.Fatalf("expected empty peers after revocation, got %d", len(peers))
	}
}
