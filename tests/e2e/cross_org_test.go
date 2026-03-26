//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestCrossOrgIsolation verifies that agents in different orgs cannot query,
// grant to, or request each other.
func TestCrossOrgIsolation(t *testing.T) {
	base := newE2EServer(t)
	slugA := orgSlug(t) + "a"
	slugB := orgSlug(t) + "b"

	alice := registerAgent(t, base, slugA, "alice@example.com")
	_ = registerAgent(t, base, slugB, "carol@example.com")

	// Alice tries to grant permission to Carol (different org) → 404.
	status, _ := doJSONRaw(t, base, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     "carol@example.com",
		"scope_type":             "project",
		"scope_ref":              "proj",
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	})
	if status != http.StatusNotFound {
		t.Fatalf("cross-org grant: expected 404, got %d", status)
	}

	// Alice tries to query Carol → 404.
	status, _ = doJSONRaw(t, base, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   "carol@example.com",
		"purpose":         "status_check",
		"question":        "What is Carol doing?",
		"requested_types": []string{"summary"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
	})
	if status != http.StatusNotFound {
		t.Fatalf("cross-org query: expected 404, got %d", status)
	}

	// Alice tries to send a request to Carol → 404.
	status, _ = doJSONRaw(t, base, http.MethodPost, "/v1/requests", alice.AccessToken, map[string]any{
		"to_user_email": "carol@example.com",
		"request_type":  "ask_for_review",
		"title":         "Review this",
		"content":       "Please review.",
	})
	if status != http.StatusNotFound {
		t.Fatalf("cross-org request: expected 404, got %d", status)
	}
}
