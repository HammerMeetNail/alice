package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/artifacts"
	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/storage/memory"
)

func TestPermissionedQueryFlow(t *testing.T) {
	store := memory.New()
	agentService := agents.NewService(store, config.FromEnv())
	artifactService := artifacts.NewService(store)
	policyService := policy.NewService(store)
	queryService := queries.NewService(store, artifactService, policyService)
	auditService := audit.NewService(store)

	handler := NewRouter(services.Container{
		Agents:    agentService,
		Artifacts: artifactService,
		Policy:    policyService,
		Queries:   queryService,
		Audit:     auditService,
	})

	aliceAgentID := registerAgent(t, handler, "alice@example.com")
	bobAgentID := registerAgent(t, handler, "bob@example.com")

	publishArtifact(t, handler, bobAgentID, core.Artifact{
		Type:              core.ArtifactTypeSummary,
		Title:             "Working on payments",
		Content:           "Focused on payments retry work.",
		StructuredPayload: map[string]any{"project_refs": []string{"payments-api"}},
		SourceRefs: []core.SourceReference{
			{
				SourceSystem: "github",
				SourceType:   "pull_request",
				SourceID:     "repo:org/payments:pr:128",
				ObservedAt:   time.Now().UTC(),
				TrustClass:   core.TrustClassStructuredSystem,
				Sensitivity:  core.SensitivityMedium,
			},
		},
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    core.SensitivityMedium,
		Confidence:     0.9,
	})

	grantPermission(t, handler, bobAgentID, map[string]any{
		"grantee_user_email":     "alice@example.com",
		"scope_type":             "project",
		"scope_ref":              "payments-api",
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, handler, aliceAgentID)
	result := getQueryResult(t, handler, aliceAgentID, queryID)

	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
}

func registerAgent(t *testing.T, handler http.Handler, email string) string {
	t.Helper()

	body := map[string]any{
		"org_slug":     "example-corp",
		"owner_email":  email,
		"agent_name":   email + "-agent",
		"client_type":  "codex",
		"public_key":   "dev-key",
		"capabilities": []string{"publish_artifact", "respond_query"},
	}
	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", body)

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return payload["agent_id"].(string)
}

func publishArtifact(t *testing.T, handler http.Handler, agentID string, artifact core.Artifact) {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/artifacts", agentID, map[string]any{"artifact": artifact})
	if rec.Code != http.StatusOK {
		t.Fatalf("publish artifact status = %d", rec.Code)
	}
}

func grantPermission(t *testing.T, handler http.Handler, agentID string, body map[string]any) {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", agentID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant permission status = %d", rec.Code)
	}
}

func queryPeerStatus(t *testing.T, handler http.Handler, agentID string) string {
	t.Helper()
	body := map[string]any{
		"to_user_email":   "bob@example.com",
		"purpose":         "status_check",
		"question":        "What has Bob been working on today?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", agentID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("query status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode query response: %v", err)
	}
	return payload["query_id"].(string)
}

func getQueryResult(t *testing.T, handler http.Handler, agentID, queryID string) map[string]any {
	t.Helper()
	rec := performJSON(t, handler, http.MethodGet, "/v1/queries/"+queryID, agentID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get query result status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode query result: %v", err)
	}
	return payload
}

func performJSON(t *testing.T, handler http.Handler, method, path, agentID string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
