package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	"alice/internal/storage"
	"alice/internal/storage/memory"
	"alice/internal/storage/postgres"
)

func TestPermissionedQueryFlow(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		runPermissionedQueryFlow(t, newTestHandler(t, ""), newFixture(t))
	})

	if databaseURL := strings.TrimSpace(os.Getenv("ALICE_TEST_DATABASE_URL")); databaseURL != "" {
		t.Run("postgres", func(t *testing.T) {
			runPermissionedQueryFlow(t, newTestHandler(t, databaseURL), newFixture(t))
		})
	}
}

type testFixture struct {
	OrgSlug      string
	AliceEmail   string
	BobEmail     string
	ProjectScope string
}

func newFixture(t *testing.T) testFixture {
	t.Helper()

	suffix := strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	suffix = suffix + "-" + time.Now().UTC().Format("20060102150405.000000000")

	return testFixture{
		OrgSlug:      "example-corp-" + suffix,
		AliceEmail:   "alice-" + suffix + "@example.com",
		BobEmail:     "bob-" + suffix + "@example.com",
		ProjectScope: "payments-api",
	}
}

func newTestHandler(t *testing.T, databaseURL string) http.Handler {
	t.Helper()

	cfg := config.FromEnv()

	if databaseURL == "" {
		store := memory.New()
		return buildTestHandler(cfg, store)
	}

	store, err := postgres.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate postgres store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close postgres store: %v", err)
		}
	})

	return buildTestHandler(cfg, store)
}

type testRepositories interface {
	storage.OrganizationRepository
	storage.UserRepository
	storage.AgentRepository
	storage.ArtifactRepository
	storage.PolicyGrantRepository
	storage.QueryRepository
	storage.AuditRepository
}

func buildTestHandler(cfg config.Config, repos testRepositories) http.Handler {
	agentService := agents.NewService(repos, repos, repos, cfg)
	artifactService := artifacts.NewService(repos)
	policyService := policy.NewService(repos)
	queryService := queries.NewService(repos, artifactService, policyService)
	auditService := audit.NewService(repos)

	return NewRouter(services.Container{
		Agents:    agentService,
		Artifacts: artifactService,
		Policy:    policyService,
		Queries:   queryService,
		Audit:     auditService,
	})
}

func runPermissionedQueryFlow(t *testing.T, handler http.Handler, fixture testFixture) {
	t.Helper()

	aliceAgentID := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bobAgentID := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	publishArtifact(t, handler, bobAgentID, core.Artifact{
		Type:              core.ArtifactTypeSummary,
		Title:             "Working on payments",
		Content:           "Focused on payments retry work.",
		StructuredPayload: map[string]any{"project_refs": []string{fixture.ProjectScope}},
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
		"grantee_user_email":     fixture.AliceEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, handler, aliceAgentID, fixture)
	result := getQueryResult(t, handler, aliceAgentID, queryID)

	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
}

func registerAgent(t *testing.T, handler http.Handler, orgSlug, email string) string {
	t.Helper()

	body := map[string]any{
		"org_slug":     orgSlug,
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

func queryPeerStatus(t *testing.T, handler http.Handler, agentID string, fixture testFixture) string {
	t.Helper()
	body := map[string]any{
		"to_user_email":   fixture.BobEmail,
		"purpose":         "status_check",
		"question":        "What has Bob been working on today?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{fixture.ProjectScope},
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
