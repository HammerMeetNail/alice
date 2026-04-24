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
	"sync"
	"testing"
	"time"

	"alice/internal/actions"
	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/approvals"
	"alice/internal/artifacts"
	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/email"
	"alice/internal/orggraph"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/requests"
	"alice/internal/riskpolicy"
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

func TestMalformedJSONReturns400(t *testing.T) {
	handler := newTestHandler(t, "")

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/agents/register/challenge"},
		{http.MethodPost, "/v1/agents/register"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, strings.NewReader("{not valid json"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOversizedBodyReturns413(t *testing.T) {
	handler := newTestHandler(t, "")

	// Build a body just over 1 MiB.
	big := strings.NewReader(`{"padding":"` + strings.Repeat("x", 1<<20+1) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/register/challenge", big)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d", rec.Code)
	}
}

func TestExpiredTokenReturns401(t *testing.T) {
	handler := newTestHandler(t, "")
	rec := performJSON(t, handler, http.MethodGet, "/v1/peers", "expired~token-value", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired/invalid token, got %d", rec.Code)
	}
}

func TestCrossAgentArtifactCorrection(t *testing.T) {
	// Alice publishes an artifact; Bob should not be able to correct it.
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice publishes an artifact and captures its ID.
	rec := performJSON(t, handler, http.MethodPost, "/v1/artifacts", alice.AccessToken, map[string]any{
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Alice's status",
			"content":         "All good.",
			"visibility_mode": "explicit_grants_only",
			"sensitivity":     "low",
			"confidence":      0.9,
			"source_refs": []map[string]any{{
				"source_system": "test",
				"source_type":   "manual",
				"source_id":     "alice-art-1",
				"observed_at":   time.Now().UTC().Format(time.RFC3339),
			}},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Alice publish artifact status = %d body=%s", rec.Code, rec.Body.String())
	}
	var publishResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&publishResp); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	artifactID := publishResp["artifact_id"].(string)

	// Bob tries to correct Alice's artifact — should be 403.
	rec = performJSON(t, handler, http.MethodPost, "/v1/artifacts/"+artifactID+"/correct", bob.AccessToken, map[string]any{
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Corrected status",
			"content":         "Actually not so good.",
			"visibility_mode": "explicit_grants_only",
			"sensitivity":     "low",
			"confidence":      0.8,
			"source_refs": []map[string]any{{
				"source_system": "test",
				"source_type":   "manual",
				"source_id":     "alice-art-1",
				"observed_at":   time.Now().UTC().Format(time.RFC3339),
			}},
		},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when Bob corrects Alice's artifact, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCrossOrgIsolation(t *testing.T) {
	// Alice registers in org "alpha"; Bob registers in org "beta".
	// Each org is completely separate: neither can query or grant across org boundaries.
	handler := newTestHandler(t, "")

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	aliceEmail := "alice-" + suffix + "@example.com"
	bobEmail := "bob-" + suffix + "@example.com"

	alice := registerAgent(t, handler, "alpha-"+suffix, aliceEmail)
	bob := registerAgent(t, handler, "beta-"+suffix, bobEmail)

	// Bob publishes an artifact in his own org.
	publishArtifact(t, handler, bob.AccessToken, core.Artifact{
		Type:           core.ArtifactTypeSummary,
		Title:          "Bob's cross-org status",
		Content:        "All good.",
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		SourceRefs: []core.SourceReference{
			{SourceSystem: "test", SourceType: "manual", SourceID: "xorg-1", ObservedAt: time.Now().UTC()},
		},
	})

	// Alice queries Bob's status — Bob's email is not in Alice's org, so 404.
	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   bobEmail,
		"purpose":         "status_check",
		"question":        "What has Bob been working on?",
		"requested_types": []string{"summary"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org query: expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Alice tries to grant Bob permission — Bob's email is not in Alice's org, so 404.
	rec = performJSON(t, handler, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     bobEmail,
		"scope_type":             "project",
		"scope_ref":              "any-project",
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org grant: expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Bob tries to send Alice a request — Alice's email is not in Bob's org, so 404.
	rec = performJSON(t, handler, http.MethodPost, "/v1/requests", bob.AccessToken, map[string]any{
		"to_user_email": aliceEmail,
		"request_type":  "ask_for_review",
		"title":         "Cross-org request",
		"content":       "Can you review this?",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-org request: expected 404, got %d body=%s", rec.Code, rec.Body.String())
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

func TestRequestApprovalFlow(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		runRequestApprovalFlow(t, newTestHandler(t, ""), newFixture(t))
	})

	if databaseURL := strings.TrimSpace(os.Getenv("ALICE_TEST_DATABASE_URL")); databaseURL != "" {
		t.Run("postgres", func(t *testing.T) {
			runRequestApprovalFlow(t, newTestHandler(t, databaseURL), newFixture(t))
		})
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
	storage.RequestRepository
	storage.ApprovalRepository
	storage.AuditRepository
	storage.EmailVerificationRepository
	storage.RiskPolicyRepository
	storage.ActionRepository
	storage.UserPreferencesRepository
	storage.OrgGraphRepository
	storage.Transactor
}

func buildTestHandler(cfg config.Config, repos testRepositories) http.Handler {
	return buildTestHandlerWithSender(cfg, repos, nil)
}

func buildTestHandlerWithSender(cfg config.Config, repos testRepositories, sender email.Sender) http.Handler {
	agentService := agents.NewService(repos, repos, repos, repos, repos, cfg, repos)
	if sender != nil {
		agentService = agentService.WithEmailSender(sender, repos)
	}
	artifactService := artifacts.NewService(repos)
	policyService := policy.NewService(repos)
	riskPolicyService := riskpolicy.NewService(repos, repos)
	orgGraphService := orggraph.NewService(repos, repos)
	queryService := queries.NewService(repos, artifactService, policyService, repos, repos).
		WithRiskPolicyEvaluator(riskPolicyService.AsQueriesEvaluator()).
		WithOrgGraph(orgGraphService.AsEvaluator())
	requestService := requests.NewService(repos, repos, repos)
	approvalService := approvals.NewService(repos, repos, repos, repos)
	auditService := audit.NewService(repos)
	actionService := actions.NewService(repos, repos, repos, repos).
		WithRiskPolicyEvaluator(riskPolicyService).
		WithExecutor(actions.NewAcknowledgeBlockerExecutor(repos))

	return NewRouter(RouterOptions{Services: services.Container{
		Agents:     agentService,
		Artifacts:  artifactService,
		Policy:     policyService,
		Queries:    queryService,
		Requests:   requestService,
		Approvals:  approvalService,
		Audit:      auditService,
		RiskPolicy: riskPolicyService,
		Actions:    actionService,
		OrgGraph:   orgGraphService,
	}})
}

// testCapturingSender implements email.Sender, capturing sent messages.
type testCapturingSender struct {
	mu   sync.Mutex
	sent []struct{ To, Subject, Body string }
}

func (s *testCapturingSender) Send(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, struct{ To, Subject, Body string }{to, subject, body})
	return nil
}

func (s *testCapturingSender) LastCode() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return "", false
	}
	body := s.sent[len(s.sent)-1].Body
	const prefix = "Your Alice verification code is: "
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			code := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if len(code) == 6 {
				return code, true
			}
		}
	}
	return "", false
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

func runRequestApprovalFlow(t *testing.T, handler http.Handler, fixture testFixture) {
	t.Helper()

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	requestID := sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "ask_for_review",
		"title":         "Need review today",
		"content":       "Can you review the payments retry PR today?",
		"structured_payload": map[string]any{
			"project_refs": []string{fixture.ProjectScope},
		},
	})

	incoming := listIncomingRequests(t, handler, bob.AccessToken)
	requestsList := incoming["requests"].([]any)
	if len(requestsList) != 1 {
		t.Fatalf("expected 1 incoming request, got %d", len(requestsList))
	}

	response := respondToRequest(t, handler, bob.AccessToken, requestID, map[string]any{
		"response": "require_approval",
		"message":  "Need to confirm this with the user first.",
	})
	approvalID, ok := response["approval_id"].(string)
	if !ok || approvalID == "" {
		t.Fatalf("expected approval_id in response payload: %#v", response)
	}

	approvals := listPendingApprovals(t, handler, bob.AccessToken)
	approvalList := approvals["approvals"].([]any)
	if len(approvalList) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(approvalList))
	}

	resolution := resolveApproval(t, handler, bob.AccessToken, approvalID, map[string]any{
		"decision": "approved",
	})
	if resolution["state"] != "approved" {
		t.Fatalf("expected approval state approved, got %#v", resolution["state"])
	}

	incoming = listIncomingRequests(t, handler, bob.AccessToken)
	requestsList = incoming["requests"].([]any)
	requestRecord := requestsList[0].(map[string]any)
	if requestRecord["state"] != "accepted" {
		t.Fatalf("expected request state accepted after approval, got %#v", requestRecord["state"])
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

func sendRequestToPeer(t *testing.T, handler http.Handler, accessToken string, body map[string]any) string {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/requests", accessToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("send request status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode send request response: %v", err)
	}
	return payload["request_id"].(string)
}

func listIncomingRequests(t *testing.T, handler http.Handler, accessToken string) map[string]any {
	t.Helper()
	rec := performJSON(t, handler, http.MethodGet, "/v1/requests/incoming", accessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list incoming requests status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list incoming requests response: %v", err)
	}
	return payload
}

func respondToRequest(t *testing.T, handler http.Handler, accessToken, requestID string, body map[string]any) map[string]any {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/requests/"+requestID+"/respond", accessToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("respond to request status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode respond to request response: %v", err)
	}
	return payload
}

func listPendingApprovals(t *testing.T, handler http.Handler, accessToken string) map[string]any {
	t.Helper()
	rec := performJSON(t, handler, http.MethodGet, "/v1/approvals", accessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list approvals status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode approvals response: %v", err)
	}
	return payload
}

func resolveApproval(t *testing.T, handler http.Handler, accessToken, approvalID string, body map[string]any) map[string]any {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/approvals/"+approvalID+"/resolve", accessToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve approval status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode resolve approval response: %v", err)
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

func newTestHandlerWithOTP(t *testing.T) (http.Handler, *testCapturingSender) {
	t.Helper()
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL:    5 * time.Minute,
		AuthTokenTTL:        time.Hour,
		DefaultOrgName:      "Test Org",
		EmailOTPTTL:         10 * time.Minute,
		EmailOTPMaxAttempts: 5,
	}
	sender := &testCapturingSender{}
	return buildTestHandlerWithSender(cfg, store, sender), sender
}

func TestEmailOTP_RegistrationReturnsPendingStatus(t *testing.T) {
	handler, _ := newTestHandlerWithOTP(t)
	fixture := newFixture(t)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, fixture.OrgSlug, fixture.AliceEmail, base64.StdEncoding.EncodeToString(publicKey))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge.Challenge)))

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": signature,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["status"] != "pending_email_verification" {
		t.Fatalf("expected pending_email_verification, got %v", payload["status"])
	}
}

func TestEmailOTP_UnverifiedAgentGets403OnProtectedEndpoints(t *testing.T) {
	handler, _ := newTestHandlerWithOTP(t)
	fixture := newFixture(t)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, fixture.OrgSlug, fixture.AliceEmail, base64.StdEncoding.EncodeToString(publicKey))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge.Challenge)))

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": signature,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d body=%s", rec.Code, rec.Body.String())
	}
	var regPayload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&regPayload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	accessToken := regPayload["access_token"].(string)

	// Protected endpoints should return 403.
	rec = performJSON(t, handler, http.MethodGet, "/v1/peers", accessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unverified agent on /v1/peers, got %d body=%s", rec.Code, rec.Body.String())
	}

	var errPayload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&errPayload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if errPayload["error"] != "email_verification_required" {
		t.Fatalf("expected email_verification_required error, got %v", errPayload["error"])
	}
}

func TestEmailOTP_CorrectCodePromotesToActive(t *testing.T) {
	handler, sender := newTestHandlerWithOTP(t)
	fixture := newFixture(t)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, fixture.OrgSlug, fixture.AliceEmail, base64.StdEncoding.EncodeToString(publicKey))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge.Challenge)))

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": signature,
	})
	var regPayload map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&regPayload)
	accessToken := regPayload["access_token"].(string)

	code, ok := sender.LastCode()
	if !ok {
		t.Fatal("expected OTP code in email")
	}

	rec = performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", accessToken, map[string]any{
		"code": code,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify-email status = %d body=%s", rec.Code, rec.Body.String())
	}

	// After verification, protected endpoints should work.
	rec = performJSON(t, handler, http.MethodGet, "/v1/peers", accessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after verification on /v1/peers, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEmailOTP_WrongCodeReturns401(t *testing.T) {
	handler, _ := newTestHandlerWithOTP(t)
	fixture := newFixture(t)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, fixture.OrgSlug, fixture.AliceEmail, base64.StdEncoding.EncodeToString(publicKey))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge.Challenge)))

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": signature,
	})
	var regPayload map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&regPayload)
	accessToken := regPayload["access_token"].(string)

	rec = performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", accessToken, map[string]any{
		"code": "000000",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong code, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRotateInviteToken(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	admin := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/rotate-invite-token", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate invite token status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}
	if payload["invite_token"] == nil || payload["invite_token"].(string) == "" {
		t.Fatal("expected non-empty invite_token in response")
	}

	// Rotate again — should get a different token.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/rotate-invite-token", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second rotate status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload2 map[string]any
	json.NewDecoder(rec.Body).Decode(&payload2)
	if payload2["invite_token"].(string) == payload["invite_token"].(string) {
		t.Fatal("expected different token on second rotation")
	}
}

func TestRotateInviteToken_NonAdminForbidden(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	// First agent is admin.
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	// Second agent is a member.
	member := registerAgent(t, handler, fixture.OrgSlug, "member@example.com")

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/rotate-invite-token", member.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin rotate, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateGatekeeperTuning(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	admin := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Set both overrides.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/gatekeeper-tuning", admin.AccessToken, map[string]any{
		"confidence_threshold": 0.8,
		"lookback_window":      "48h",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set tuning status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := payload["confidence_threshold"].(float64); got != 0.8 {
		t.Fatalf("expected confidence_threshold=0.8, got %v", payload["confidence_threshold"])
	}
	if payload["lookback_window"] != "48h0m0s" {
		t.Fatalf("expected lookback_window=48h0m0s, got %v", payload["lookback_window"])
	}

	// Invalid lookback: 400.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/gatekeeper-tuning", admin.AccessToken, map[string]any{
		"lookback_window": "not-a-duration",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad duration, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Out-of-range threshold: 400 via service ValidationError.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/gatekeeper-tuning", admin.AccessToken, map[string]any{
		"confidence_threshold": 1.5,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for out-of-range threshold, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Clear reverts both overrides to nil.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/gatekeeper-tuning", admin.AccessToken, map[string]any{
		"clear": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear tuning status = %d body=%s", rec.Code, rec.Body.String())
	}
	payload = map[string]any{}
	json.NewDecoder(rec.Body).Decode(&payload)
	if payload["confidence_threshold"] != nil {
		t.Fatalf("expected nil confidence_threshold after clear, got %v", payload["confidence_threshold"])
	}
	if payload["lookback_window"] != nil {
		t.Fatalf("expected nil lookback_window after clear, got %v", payload["lookback_window"])
	}

	// Non-admin must receive 403.
	member := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/gatekeeper-tuning", member.AccessToken, map[string]any{
		"confidence_threshold": 0.7,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestActionsLifecycleHTTP(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Bob opts into operator phase.
	rec := performJSON(t, handler, http.MethodPost, "/v1/users/me/operator-enabled", bob.AccessToken, map[string]any{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable operator status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Alice sends Bob a blocker request the action will acknowledge.
	rec = performJSON(t, handler, http.MethodPost, "/v1/requests", alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "blocker",
		"title":         "Queue backlog",
		"content":       "Service 500s on retry",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("send request status = %d body=%s", rec.Code, rec.Body.String())
	}
	var requestPayload map[string]any
	json.NewDecoder(rec.Body).Decode(&requestPayload)
	requestID := requestPayload["request_id"].(string)

	// Bob creates an acknowledge_blocker action targeting the request.
	rec = performJSON(t, handler, http.MethodPost, "/v1/actions", bob.AccessToken, map[string]any{
		"kind":       "acknowledge_blocker",
		"request_id": requestID,
		"inputs":     map[string]any{"message": "on it, ETA 30 min"},
		"risk_level": "L0",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create action status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	actionID := created["action_id"].(string)
	if created["state"] != "approved" {
		t.Fatalf("expected default policy to approve the action, got %v", created["state"])
	}

	// Execute it — this writes to the request and closes it.
	rec = performJSON(t, handler, http.MethodPost, "/v1/actions/"+actionID+"/execute", bob.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute action status = %d body=%s", rec.Code, rec.Body.String())
	}
	var executed map[string]any
	json.NewDecoder(rec.Body).Decode(&executed)
	if executed["state"] != "executed" {
		t.Fatalf("expected executed state, got %v (failure_reason=%v)", executed["state"], executed["failure_reason"])
	}

	// Listing shows the executed action.
	rec = performJSON(t, handler, http.MethodGet, "/v1/actions", bob.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list actions status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listed map[string]any
	json.NewDecoder(rec.Body).Decode(&listed)
	if len(listed["actions"].([]any)) != 1 {
		t.Fatalf("expected 1 action in list, got %d", len(listed["actions"].([]any)))
	}

	// Replay is rejected (409).
	rec = performJSON(t, handler, http.MethodPost, "/v1/actions/"+actionID+"/execute", bob.AccessToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on replay, got %d body=%s", rec.Code, rec.Body.String())
	}

	// A user who has not opted in cannot create an action.
	rec = performJSON(t, handler, http.MethodPost, "/v1/actions", alice.AccessToken, map[string]any{
		"kind":       "acknowledge_blocker",
		"request_id": requestID,
		"inputs":     map[string]any{"message": "hi"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for operator-disabled user, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Unknown action kind must be a 400.
	rec = performJSON(t, handler, http.MethodPost, "/v1/actions", bob.AccessToken, map[string]any{
		"kind":   "send_carrier_pigeon",
		"inputs": map[string]any{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown kind, got %d", rec.Code)
	}

	// The originating request is now completed with Bob's acknowledgement.
	rec = performJSON(t, handler, http.MethodGet, "/v1/requests/sent?limit=10", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sent requests status = %d", rec.Code)
	}
	var sent map[string]any
	json.NewDecoder(rec.Body).Decode(&sent)
	sentItems := sent["requests"].([]any)
	if len(sentItems) == 0 {
		t.Fatal("expected at least one sent request")
	}
	first := sentItems[0].(map[string]any)
	if first["state"] != "completed" {
		t.Fatalf("expected request state=completed, got %v", first["state"])
	}
	if first["response_message"] != "on it, ETA 30 min" {
		t.Fatalf("expected response_message to match action input, got %v", first["response_message"])
	}
}

func TestRiskPolicyApplyHistoryActivate(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	admin := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Admin applies a first policy.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", admin.AccessToken, map[string]any{
		"name": "baseline",
		"source": map[string]any{
			"rules": []map[string]any{
				{"when": map[string]any{}, "then": "allow"},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s", rec.Code, rec.Body.String())
	}
	var first map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&first); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if first["policy_id"] == nil || first["policy_id"].(string) == "" {
		t.Fatal("expected policy_id in response")
	}

	// Apply a second policy — becomes the new active version.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", admin.AccessToken, map[string]any{
		"name": "strict",
		"source": map[string]any{
			"rules": []map[string]any{
				{"when": map[string]any{"risk_level_at_least": "L2"}, "then": "require_approval"},
				{"when": map[string]any{}, "then": "allow"},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("second apply status = %d", rec.Code)
	}
	var second map[string]any
	json.NewDecoder(rec.Body).Decode(&second)

	// History should return both, newest first.
	rec = performJSON(t, handler, http.MethodGet, "/v1/orgs/risk-policies", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d", rec.Code)
	}
	var hist map[string]any
	json.NewDecoder(rec.Body).Decode(&hist)
	policies := hist["policies"].([]any)
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies in history, got %d", len(policies))
	}

	// Rollback: activate the first policy again.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policies/"+first["policy_id"].(string)+"/activate", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Invalid policy → 500 (service error wrapping validation).
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", admin.AccessToken, map[string]any{
		"source": map[string]any{"rules": []any{}},
	})
	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-OK for empty rules, got %d", rec.Code)
	}

	// Non-admin: member calls must be rejected.
	member := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", member.AccessToken, map[string]any{
		"name": "member-attempt",
		"source": map[string]any{
			"rules": []map[string]any{{"when": map[string]any{}, "then": "allow"}},
		},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin apply, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateVerificationMode(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	admin := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/verification-mode", admin.AccessToken, map[string]any{
		"verification_mode": "email_otp",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update verification mode status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["verification_mode"] != "email_otp" {
		t.Fatalf("expected verification_mode = email_otp, got %v", payload["verification_mode"])
	}

	// Invalid mode should fail.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/verification-mode", admin.AccessToken, map[string]any{
		"verification_mode": "invalid_mode",
	})
	if rec.Code == http.StatusOK {
		t.Fatal("expected error for invalid verification mode")
	}
}

func TestListPendingAgents(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	admin := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/orgs/pending-agents", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pending agents status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	pendingAgents := payload["pending_agents"].([]any)
	if len(pendingAgents) != 0 {
		t.Fatalf("expected 0 pending agents, got %d", len(pendingAgents))
	}
}

func TestReviewAgent(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	admin := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Set org to admin_approval mode.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/verification-mode", admin.AccessToken, map[string]any{
		"verification_mode": "admin_approval",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set verification mode status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Register Bob — should succeed but agent will be pending_admin_approval.
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Bob should be blocked from protected routes.
	rec = performJSON(t, handler, http.MethodGet, "/v1/peers", bob.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for pending agent, got %d body=%s", rec.Code, rec.Body.String())
	}

	// List pending — Bob should appear.
	rec = performJSON(t, handler, http.MethodGet, "/v1/orgs/pending-agents", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pending status = %d body=%s", rec.Code, rec.Body.String())
	}
	var listPayload map[string]any
	json.NewDecoder(rec.Body).Decode(&listPayload)
	pending := listPayload["pending_agents"].([]any)
	if len(pending) == 0 {
		t.Fatal("expected at least one pending agent")
	}
	pendingAgent := pending[0].(map[string]any)
	targetAgentID := pendingAgent["agent_id"].(string)

	// Approve Bob.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/agents/"+targetAgentID+"/review", admin.AccessToken, map[string]any{
		"decision": "approved",
		"reason":   "looks good",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("review agent status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Bob should now be able to access protected routes.
	rec = performJSON(t, handler, http.MethodGet, "/v1/peers", bob.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after approval, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEmailOTP_VerificationExemptFromEmailCheck(t *testing.T) {
	// The verify-email and resend-verification endpoints must be accessible
	// even when the agent status is pending_email_verification.
	handler, _ := newTestHandlerWithOTP(t)
	fixture := newFixture(t)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, fixture.OrgSlug, fixture.AliceEmail, base64.StdEncoding.EncodeToString(publicKey))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge.Challenge)))

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": signature,
	})
	var regPayload map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&regPayload)
	accessToken := regPayload["access_token"].(string)

	// verify-email with wrong code → 401 (not 403 email_verification_required).
	rec = performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", accessToken, map[string]any{
		"code": "000000",
	})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("verify-email should not be blocked by email verification check, got 403 body=%s", rec.Body.String())
	}

	// resend-verification → 429 (too soon) but NOT 403.
	rec = performJSON(t, handler, http.MethodPost, "/v1/agents/resend-verification", accessToken, nil)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("resend-verification should not be blocked by email verification check, got 403 body=%s", rec.Body.String())
	}
}

// scopeQueryResult is the decoded slice of the GET /v1/queries/:id response
// the org-graph tests care about.
type scopeQueryResult struct {
	Artifacts   []any
	PolicyBasis []string
}

func runScopeQuery(t *testing.T, handler http.Handler, accessToken, toEmail, purpose string) scopeQueryResult {
	t.Helper()
	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", accessToken, map[string]any{
		"to_user_email":   toEmail,
		"purpose":         purpose,
		"requested_types": []string{"summary"},
		"time_window": map[string]any{
			"start": time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
			"end":   time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("scope query status = %d body=%s", rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	json.NewDecoder(rec.Body).Decode(&envelope)
	queryID := envelope["query_id"].(string)

	rec = performJSON(t, handler, http.MethodGet, "/v1/queries/"+queryID, accessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get query result status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	json.NewDecoder(rec.Body).Decode(&payload)
	response, _ := payload["response"].(map[string]any)
	artifacts, _ := response["artifacts"].([]any)
	rawBasis, _ := response["policy_basis"].([]any)
	basis := make([]string, 0, len(rawBasis))
	for _, b := range rawBasis {
		if s, ok := b.(string); ok {
			basis = append(basis, s)
		}
	}
	return scopeQueryResult{Artifacts: artifacts, PolicyBasis: basis}
}

func containsBasisStr(basis []string, want string) bool {
	for _, b := range basis {
		if b == want {
			return true
		}
	}
	return false
}

// TestOrgGraphLifecycle exercises team + manager graph management via the
// HTTP surface end-to-end: admin-only writes, cycle detection, cross-org
// 404s, and — critically — that team_scope / manager_scope visibility
// modes on published artifacts are answered by scope-based access
// without requiring an explicit grant.
func TestOrgGraphLifecycle(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	carolEmail := "carol-" + strings.TrimPrefix(fixture.AliceEmail, "alice-")

	admin := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)
	carol := registerAgent(t, handler, fixture.OrgSlug, carolEmail)

	// Non-admin cannot create teams.
	rec := performJSON(t, handler, http.MethodPost, "/v1/org/teams", bob.AccessToken, map[string]any{"name": "eng"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin create team, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Admin creates a team.
	rec = performJSON(t, handler, http.MethodPost, "/v1/org/teams", admin.AccessToken, map[string]any{"name": "eng"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create team status = %d body=%s", rec.Code, rec.Body.String())
	}
	var team map[string]any
	json.NewDecoder(rec.Body).Decode(&team)
	teamID := team["team_id"].(string)

	// GET /v1/org/teams lists teams in the org.
	rec = performJSON(t, handler, http.MethodGet, "/v1/org/teams", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list teams status = %d", rec.Code)
	}
	var teamsList map[string]any
	json.NewDecoder(rec.Body).Decode(&teamsList)
	if len(teamsList["teams"].([]any)) != 1 {
		t.Fatalf("expected 1 team in list, got %d", len(teamsList["teams"].([]any)))
	}

	// Admin adds Bob and Carol to the team.
	for _, email := range []string{fixture.BobEmail, carolEmail} {
		rec = performJSON(t, handler, http.MethodPost, "/v1/org/teams/"+teamID+"/members", admin.AccessToken, map[string]any{
			"user_email": email,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("add member %s status = %d body=%s", email, rec.Code, rec.Body.String())
		}
	}

	// Members list contains 2 rows.
	rec = performJSON(t, handler, http.MethodGet, "/v1/org/teams/"+teamID+"/members", admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list members status = %d", rec.Code)
	}
	var members map[string]any
	json.NewDecoder(rec.Body).Decode(&members)
	if n := len(members["members"].([]any)); n != 2 {
		t.Fatalf("expected 2 members, got %d", n)
	}

	// Bob publishes a team_scope summary, no explicit grant to Carol.
	publishArtifact(t, handler, bob.AccessToken, core.Artifact{
		Type:           core.ArtifactTypeSummary,
		Title:          "Shipping queue",
		Content:        "Queued work through Friday.",
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		VisibilityMode: core.VisibilityModeTeamScope,
		SourceRefs: []core.SourceReference{{
			SourceSystem: "test", SourceType: "manual", SourceID: "1",
			ObservedAt: time.Now().UTC(), TrustClass: core.TrustClassTrustedPolicy, Sensitivity: core.SensitivityLow,
		}},
	})

	// Carol queries Bob with no grant — should see the team-scoped artifact via the graph.
	teamResp := runScopeQuery(t, handler, carol.AccessToken, fixture.BobEmail, "status_check")
	if n := len(teamResp.Artifacts); n != 1 {
		t.Fatalf("expected 1 team-scope artifact, got %d basis=%v", n, teamResp.PolicyBasis)
	}
	if !containsBasisStr(teamResp.PolicyBasis, "visibility:team_scope") {
		t.Fatalf("expected visibility:team_scope in policy_basis, got %v", teamResp.PolicyBasis)
	}

	// Remove Carol from the team → follow-up query must return zero.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/org/teams/"+teamID+"/members/"+carolEmail, admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member status = %d", rec.Code)
	}
	// After removal Carol has no grants and no shared team. The query
	// succeeds (with the graph attached we don't early-deny on "no
	// grants"; scope evaluation still runs) but returns zero artifacts
	// because the artifact's team_scope no longer matches.
	removedResp := runScopeQuery(t, handler, carol.AccessToken, fixture.BobEmail, "status_check")
	if len(removedResp.Artifacts) != 0 {
		t.Fatalf("expected 0 artifacts after team removal, got %d basis=%v", len(removedResp.Artifacts), removedResp.PolicyBasis)
	}

	// Assign Admin as Bob's manager, then Bob publishes a manager_scope summary.
	rec = performJSON(t, handler, http.MethodPost, "/v1/org/manager-edges", admin.AccessToken, map[string]any{
		"user_email":    fixture.BobEmail,
		"manager_email": fixture.AliceEmail,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("assign manager status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Self-manager edges are rejected at the service layer → 400.
	rec = performJSON(t, handler, http.MethodPost, "/v1/org/manager-edges", admin.AccessToken, map[string]any{
		"user_email":    fixture.BobEmail,
		"manager_email": fixture.BobEmail,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for self-manager, got %d", rec.Code)
	}

	// Cycle detection: can't set Bob as Alice's manager when Alice is already upstream of Bob.
	rec = performJSON(t, handler, http.MethodPost, "/v1/org/manager-edges", admin.AccessToken, map[string]any{
		"user_email":    fixture.AliceEmail,
		"manager_email": fixture.BobEmail,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for manager cycle, got %d body=%s", rec.Code, rec.Body.String())
	}

	publishArtifact(t, handler, bob.AccessToken, core.Artifact{
		Type:           core.ArtifactTypeSummary,
		Title:          "Status for manager",
		Content:        "On-track; minor risk on payments retry.",
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
		VisibilityMode: core.VisibilityModeManagerScope,
		SourceRefs: []core.SourceReference{{
			SourceSystem: "test", SourceType: "manual", SourceID: "2",
			ObservedAt: time.Now().UTC(), TrustClass: core.TrustClassTrustedPolicy, Sensitivity: core.SensitivityLow,
		}},
	})

	// Alice queries Bob — the manager-scope artifact should be visible.
	mgrResp := runScopeQuery(t, handler, admin.AccessToken, fixture.BobEmail, "manager_update")
	if len(mgrResp.Artifacts) < 1 {
		t.Fatalf("expected ≥1 artifact from manager-scope visibility, got %d basis=%v", len(mgrResp.Artifacts), mgrResp.PolicyBasis)
	}
	if !containsBasisStr(mgrResp.PolicyBasis, "visibility:manager_scope") {
		t.Fatalf("expected visibility:manager_scope in policy_basis, got %v", mgrResp.PolicyBasis)
	}

	// GET /v1/org/manager-edges/:email returns the chain.
	rec = performJSON(t, handler, http.MethodGet, "/v1/org/manager-edges/"+fixture.BobEmail, admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get manager chain status = %d body=%s", rec.Code, rec.Body.String())
	}
	var chainResp map[string]any
	json.NewDecoder(rec.Body).Decode(&chainResp)
	if n := len(chainResp["chain"].([]any)); n != 1 {
		t.Fatalf("expected 1-hop manager chain, got %d", n)
	}

	// Unknown user email → 404.
	rec = performJSON(t, handler, http.MethodGet, "/v1/org/manager-edges/ghost@nowhere.test", admin.AccessToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown user chain, got %d", rec.Code)
	}

	// Bob (non-admin) cannot revoke Bob's own manager edge.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/org/manager-edges/"+fixture.BobEmail, bob.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin revoke, got %d", rec.Code)
	}
	// Admin can.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/org/manager-edges/"+fixture.BobEmail, admin.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke manager status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Cross-org isolation: a user in a different org cannot be addressed as a manager.
	otherFixture := newFixture(t)
	otherAdmin := registerAgent(t, handler, otherFixture.OrgSlug, otherFixture.AliceEmail)
	rec = performJSON(t, handler, http.MethodPost, "/v1/org/teams", otherAdmin.AccessToken, map[string]any{"name": "ops"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create other-org team: %d", rec.Code)
	}
	rec = performJSON(t, handler, http.MethodPost, "/v1/org/manager-edges", admin.AccessToken, map[string]any{
		"user_email":    fixture.BobEmail,
		"manager_email": otherFixture.AliceEmail,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-org manager, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestQueryApprovalPendingStatus(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	// Alice is the first registrant (admin). Bob is the second.
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Bob publishes a summary.
	publishArtifact(t, handler, bob.AccessToken, core.Artifact{
		Type:              core.ArtifactTypeSummary,
		Title:             "Working on infra",
		Content:           "Fixing deployment pipeline.",
		StructuredPayload: map[string]any{"project_refs": []string{fixture.ProjectScope}},
		SourceRefs: []core.SourceReference{{
			SourceSystem: "github",
			SourceType:   "pull_request",
			SourceID:     "repo:org/infra:pr:1",
			ObservedAt:   time.Now().UTC(),
			TrustClass:   core.TrustClassStructuredSystem,
			Sensitivity:  core.SensitivityLow,
		}},
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.8,
	})

	// Bob grants Alice access.
	grantPermission(t, handler, bob.AccessToken, map[string]any{
		"grantee_user_email":     fixture.AliceEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	// Alice (admin) applies a policy that requires approval for every query.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", alice.AccessToken, map[string]any{
		"name": "require-all",
		"source": map[string]any{
			"rules": []map[string]any{
				{"when": map[string]any{}, "then": "require_approval"},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply policy status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Alice queries Bob. Under the active policy every query needs approval.
	qrec := performJSON(t, handler, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   fixture.BobEmail,
		"purpose":         "status_check",
		"question":        "What is Bob working on?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{fixture.ProjectScope},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	})
	if qrec.Code != http.StatusOK {
		t.Fatalf("query status = %d body=%s", qrec.Code, qrec.Body.String())
	}
	var qpayload map[string]any
	if err := json.NewDecoder(qrec.Body).Decode(&qpayload); err != nil {
		t.Fatalf("decode query response: %v", err)
	}

	if qpayload["status"] != string(core.QueryStatePendingApproval) {
		t.Fatalf("expected status=%q, got %v", core.QueryStatePendingApproval, qpayload["status"])
	}
	if qpayload["approval_state"] != string(core.ApprovalStatePending) {
		t.Fatalf("expected approval_state=%q, got %v", core.ApprovalStatePending, qpayload["approval_state"])
	}

	// Follow-up GET /v1/queries/:id must also reflect pending_approval, not the
	// stale "queued" state that existed before the state-persistence fix.
	queryID, _ := qpayload["query_id"].(string)
	if queryID == "" {
		t.Fatal("expected non-empty query_id in POST response")
	}
	grec := performJSON(t, handler, http.MethodGet, "/v1/queries/"+queryID, alice.AccessToken, nil)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET query status = %d body=%s", grec.Code, grec.Body.String())
	}
	var gpayload map[string]any
	if err := json.NewDecoder(grec.Body).Decode(&gpayload); err != nil {
		t.Fatalf("decode GET query response: %v", err)
	}
	if gpayload["state"] != string(core.QueryStatePendingApproval) {
		t.Fatalf("GET: expected state=%q (persisted), got %v", core.QueryStatePendingApproval, gpayload["state"])
	}
}
