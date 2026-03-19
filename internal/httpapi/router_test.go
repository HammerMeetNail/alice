package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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

func TestProtectedRoutesRequireBearerToken(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	registered := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/peers", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = performJSONWithHeaders(t, handler, http.MethodGet, "/v1/peers", map[string]string{
		"X-Agent-ID": registered.AgentID,
	}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("legacy X-Agent-ID status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRegisterAgentRejectsInvalidSignature(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate registration key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, fixture.OrgSlug, fixture.AliceEmail, base64.StdEncoding.EncodeToString(publicKey))

	_, wrongPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate wrong signing key: %v", err)
	}

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": base64.StdEncoding.EncodeToString(ed25519.Sign(wrongPrivateKey, []byte(challenge.Challenge))),
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status = %d body=%s", rec.Code, rec.Body.String())
	}
}

type testFixture struct {
	OrgSlug      string
	AliceEmail   string
	BobEmail     string
	ProjectScope string
}

type registeredAgent struct {
	AgentID     string
	AccessToken string
}

type registrationChallenge struct {
	ChallengeID string
	Challenge   string
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
	storage.AgentRegistrationChallengeRepository
	storage.AgentTokenRepository
	storage.ArtifactRepository
	storage.PolicyGrantRepository
	storage.QueryRepository
	storage.AuditRepository
}

func buildTestHandler(cfg config.Config, repos testRepositories) http.Handler {
	agentService := agents.NewService(repos, repos, repos, repos, repos, cfg)
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

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	publishArtifact(t, handler, bob.AccessToken, core.Artifact{
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

	grantPermission(t, handler, bob.AccessToken, map[string]any{
		"grantee_user_email":     fixture.AliceEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, handler, alice.AccessToken, fixture)
	result := getQueryResult(t, handler, alice.AccessToken, queryID)

	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
}

func registerAgent(t *testing.T, handler http.Handler, orgSlug, email string) registeredAgent {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate registration key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, orgSlug, email, base64.StdEncoding.EncodeToString(publicKey))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge.Challenge)))

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": signature,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("register agent status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode register response: %v", err)
	}

	return registeredAgent{
		AgentID:     payload["agent_id"].(string),
		AccessToken: payload["access_token"].(string),
	}
}

func issueRegistrationChallenge(t *testing.T, handler http.Handler, orgSlug, email, publicKey string) registrationChallenge {
	t.Helper()

	body := map[string]any{
		"org_slug":     orgSlug,
		"owner_email":  email,
		"agent_name":   email + "-agent",
		"client_type":  "codex",
		"public_key":   publicKey,
		"capabilities": []string{"publish_artifact", "respond_query"},
	}

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register/challenge", "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("registration challenge status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode registration challenge response: %v", err)
	}

	return registrationChallenge{
		ChallengeID: payload["challenge_id"].(string),
		Challenge:   payload["challenge"].(string),
	}
}

func publishArtifact(t *testing.T, handler http.Handler, accessToken string, artifact core.Artifact) {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/artifacts", accessToken, map[string]any{"artifact": artifact})
	if rec.Code != http.StatusOK {
		t.Fatalf("publish artifact status = %d", rec.Code)
	}
}

func grantPermission(t *testing.T, handler http.Handler, accessToken string, body map[string]any) {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", accessToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("grant permission status = %d", rec.Code)
	}
}

func queryPeerStatus(t *testing.T, handler http.Handler, accessToken string, fixture testFixture) string {
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
	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", accessToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("query status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode query response: %v", err)
	}
	return payload["query_id"].(string)
}

func getQueryResult(t *testing.T, handler http.Handler, accessToken, queryID string) map[string]any {
	t.Helper()
	rec := performJSON(t, handler, http.MethodGet, "/v1/queries/"+queryID, accessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get query result status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode query result: %v", err)
	}
	return payload
}

func performJSON(t *testing.T, handler http.Handler, method, path, accessToken string, body any) *httptest.ResponseRecorder {
	t.Helper()

	headers := map[string]string{}
	if accessToken != "" {
		headers["Authorization"] = "Bearer " + accessToken
	}

	return performJSONWithHeaders(t, handler, method, path, headers, body)
}

func performJSONWithHeaders(t *testing.T, handler http.Handler, method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
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
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
