package edge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alice/internal/app"
	"alice/internal/config"
	"alice/internal/httpapi"
)

func TestRuntimeRunOncePublishesFixturesAndPollsState(t *testing.T) {
	handler, closeFn := newTestHandler(t)
	if closeFn != nil {
		t.Cleanup(func() {
			if err := closeFn(); err != nil {
				t.Fatalf("close test container: %v", err)
			}
		})
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	tempDir := t.TempDir()
	bobConfigPath := writeEdgeConfig(t, tempDir, edgeConfigFile{
		Agent: AgentConfig{
			OrgSlug:    "example-corp",
			OwnerEmail: "bob@example.com",
			AgentName:  "bob-agent",
			ClientType: "edge_agent",
		},
		Server: ServerConfig{
			BaseURL: server.URL,
		},
		Runtime: RuntimeConfig{
			StateFile:           "bob-state.json",
			ArtifactFixtureFile: "bob-fixtures.json",
		},
	})
	writeFixtureFile(t, filepath.Join(tempDir, "bob-fixtures.json"))

	cfg, err := LoadConfig(bobConfigPath)
	if err != nil {
		t.Fatalf("load bob config: %v", err)
	}

	firstRun, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("first runtime run: %v", err)
	}
	if !firstRun.RegistrationPerformed {
		t.Fatalf("expected first run to perform registration")
	}
	if len(firstRun.PublishedArtifacts) != 1 {
		t.Fatalf("expected one published artifact, got %d", len(firstRun.PublishedArtifacts))
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after first run: %v", err)
	}
	if strings.TrimSpace(state.AccessToken) == "" {
		t.Fatalf("expected persisted access token in state")
	}

	aliceToken := registerAgent(t, server.URL, "example-corp", "alice@example.com", "alice-agent")
	grantPermission(t, server.URL, state.AccessToken, map[string]any{
		"grantee_user_email":     "alice@example.com",
		"scope_type":             "project",
		"scope_ref":              "payments-api",
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, server.URL, aliceToken, map[string]any{
		"to_user_email":   "bob@example.com",
		"purpose":         "status_check",
		"question":        "What has Bob been working on today?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	})
	sendRequestToPeer(t, server.URL, aliceToken, map[string]any{
		"to_user_email": "bob@example.com",
		"request_type":  "ask_for_review",
		"title":         "Need review today",
		"content":       "Can you review the payments retry PR today?",
		"structured_payload": map[string]any{
			"project_refs": []string{"payments-api"},
		},
	})

	secondConfigPath := writeEdgeConfig(t, tempDir, edgeConfigFile{
		Agent: AgentConfig{
			OrgSlug:    "example-corp",
			OwnerEmail: "bob@example.com",
			AgentName:  "bob-agent",
			ClientType: "edge_agent",
		},
		Server: ServerConfig{
			BaseURL: server.URL,
		},
		Runtime: RuntimeConfig{
			StateFile:            "bob-state.json",
			QueryWatchIDs:        []string{queryID},
			PollIncomingRequests: true,
		},
	})

	secondCfg, err := LoadConfig(secondConfigPath)
	if err != nil {
		t.Fatalf("load second bob config: %v", err)
	}
	secondRun, err := NewRuntime(secondCfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second runtime run: %v", err)
	}
	if secondRun.RegistrationPerformed {
		t.Fatalf("expected second run to reuse persisted token")
	}
	if len(secondRun.QueryResults) != 1 {
		t.Fatalf("expected one query result, got %d", len(secondRun.QueryResults))
	}
	if len(secondRun.IncomingRequests) != 1 {
		t.Fatalf("expected one incoming request, got %d", len(secondRun.IncomingRequests))
	}
}

func TestRuntimeRunOnceDerivesArtifactsFromConnectorFixtures(t *testing.T) {
	handler, closeFn := newTestHandler(t)
	if closeFn != nil {
		t.Cleanup(func() {
			if err := closeFn(); err != nil {
				t.Fatalf("close test container: %v", err)
			}
		})
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	tempDir := t.TempDir()
	writeGitHubFixtureFile(t, filepath.Join(tempDir, "github-fixtures.json"))
	configPath := writeEdgeConfig(t, tempDir, edgeConfigFile{
		Agent: AgentConfig{
			OrgSlug:    "example-corp",
			OwnerEmail: "sam@example.com",
			AgentName:  "sam-agent",
			ClientType: "edge_agent",
		},
		Server: ServerConfig{
			BaseURL: server.URL,
		},
		Runtime: RuntimeConfig{
			StateFile: "sam-state.json",
		},
		Connectors: ConnectorsConfig{
			GitHub: GitHubConnectorConfig{
				FixtureFile: "github-fixtures.json",
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load connector config: %v", err)
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with connector fixtures: %v", err)
	}
	if len(report.PublishedArtifacts) != 1 {
		t.Fatalf("expected one derived artifact to be published, got %d", len(report.PublishedArtifacts))
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after derived publish: %v", err)
	}
	aliceToken := registerAgent(t, server.URL, "example-corp", "alice@example.com", "alice-agent")
	grantPermission(t, server.URL, state.AccessToken, map[string]any{
		"grantee_user_email":     "alice@example.com",
		"scope_type":             "project",
		"scope_ref":              "payments-api",
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, server.URL, aliceToken, map[string]any{
		"to_user_email":   "sam@example.com",
		"purpose":         "status_check",
		"question":        "What has Sam been working on today?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	})

	var result map[string]any
	doJSON(t, server.URL, http.MethodGet, "/v1/queries/"+queryID, aliceToken, nil, &result)
	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected one derived artifact in query result, got %d", len(artifacts))
	}
}

func TestRuntimeRunOncePollsLiveGitHub(t *testing.T) {
	handler, closeFn := newTestHandler(t)
	if closeFn != nil {
		t.Cleanup(func() {
			if err := closeFn(); err != nil {
				t.Fatalf("close test container: %v", err)
			}
		})
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	pullUpdatedAt := time.Date(2026, 3, 19, 14, 30, 0, 0, time.UTC)
	t.Setenv("ALICE_GITHUB_TOKEN", "test-github-token")

	githubAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-github-token" {
			t.Fatalf("unexpected github auth header: %q", got)
		}
		if r.URL.Path != "/repos/acme/payments-api/pulls" {
			t.Fatalf("unexpected github path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Fatalf("unexpected github state filter: %q", got)
		}

		payload := []map[string]any{
			{
				"number":     128,
				"state":      "open",
				"draft":      false,
				"title":      "Retry payments handler",
				"updated_at": pullUpdatedAt.Format(time.RFC3339),
				"user": map[string]any{
					"login": "sam",
				},
				"requested_reviewers": []map[string]any{
					{
						"login": "alice",
					},
				},
				"assignees": []map[string]any{},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode github payload: %v", err)
		}
	}))
	defer githubAPI.Close()

	tempDir := t.TempDir()
	configPath := writeEdgeConfig(t, tempDir, edgeConfigFile{
		Agent: AgentConfig{
			OrgSlug:    "example-corp",
			OwnerEmail: "sam@example.com",
			AgentName:  "sam-agent",
			ClientType: "edge_agent",
		},
		Server: ServerConfig{
			BaseURL: server.URL,
		},
		Runtime: RuntimeConfig{
			StateFile: "sam-state.json",
		},
		Connectors: ConnectorsConfig{
			GitHub: GitHubConnectorConfig{
				Enabled:     true,
				APIBaseURL:  githubAPI.URL,
				TokenEnvVar: "ALICE_GITHUB_TOKEN",
				ActorLogin:  "sam",
				Repositories: []GitHubRepositoryConfig{
					{
						Name:        "acme/payments-api",
						ProjectRefs: []string{"payments-api"},
					},
				},
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load live github config: %v", err)
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with live github polling: %v", err)
	}
	if len(report.PublishedArtifacts) != 1 {
		t.Fatalf("expected one live github artifact to be published, got %d", len(report.PublishedArtifacts))
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after live github publish: %v", err)
	}
	aliceToken := registerAgent(t, server.URL, "example-corp", "alice@example.com", "alice-agent")
	grantPermission(t, server.URL, state.AccessToken, map[string]any{
		"grantee_user_email":     "alice@example.com",
		"scope_type":             "project",
		"scope_ref":              "payments-api",
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, server.URL, aliceToken, map[string]any{
		"to_user_email":   "sam@example.com",
		"purpose":         "status_check",
		"question":        "What has Sam been working on today?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": pullUpdatedAt.Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   pullUpdatedAt.Add(1 * time.Hour).Format(time.RFC3339),
		},
	})

	var result map[string]any
	doJSON(t, server.URL, http.MethodGet, "/v1/queries/"+queryID, aliceToken, nil, &result)
	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected one live github artifact in query result, got %d", len(artifacts))
	}
}

type edgeConfigFile struct {
	Agent      AgentConfig      `json:"agent"`
	Server     ServerConfig     `json:"server"`
	Runtime    RuntimeConfig    `json:"runtime"`
	Connectors ConnectorsConfig `json:"connectors,omitempty"`
}

func newTestHandler(t *testing.T) (http.Handler, func() error) {
	t.Helper()

	cfg := config.Config{
		DefaultOrgName:   "Alice Development Org",
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     15 * time.Minute,
	}
	container, closeFn, err := app.NewContainer(cfg)
	if err != nil {
		t.Fatalf("build app container: %v", err)
	}
	return httpapi.NewRouter(container), closeFn
}

func writeEdgeConfig(t *testing.T, dir string, cfg edgeConfigFile) string {
	t.Helper()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal edge config: %v", err)
	}
	path := filepath.Join(dir, "edge-config-"+base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write edge config: %v", err)
	}
	return path
}

func writeFixtureFile(t *testing.T, path string) {
	t.Helper()

	payload := map[string]any{
		"artifacts": []map[string]any{
			{
				"type":    "summary",
				"title":   "Working on payments",
				"content": "Focused on payments retry work.",
				"structured_payload": map[string]any{
					"project_refs": []string{"payments-api"},
				},
				"source_refs": []map[string]any{
					{
						"source_system": "github",
						"source_type":   "pull_request",
						"source_id":     "repo:org/payments:pr:128",
						"observed_at":   time.Now().UTC().Format(time.RFC3339),
						"trust_class":   "structured_system",
						"sensitivity":   "medium",
					},
				},
				"visibility_mode": "explicit_grants_only",
				"sensitivity":     "medium",
				"confidence":      0.9,
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixtures: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixtures: %v", err)
	}
}

func writeGitHubFixtureFile(t *testing.T, path string) {
	t.Helper()

	payload := map[string]any{
		"pull_requests": []map[string]any{
			{
				"repository":    "org/payments",
				"number":        128,
				"state":         "open",
				"review_status": "changes_requested",
				"updated_at":    time.Now().UTC().Format(time.RFC3339),
				"project_refs":  []string{"payments-api"},
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal github fixtures: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write github fixtures: %v", err)
	}
}

func registerAgent(t *testing.T, baseURL, orgSlug, email, agentName string) string {
	t.Helper()

	client := NewClient(baseURL)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 keypair: %v", err)
	}

	challenge, err := client.BeginRegistration(context.Background(), map[string]any{
		"org_slug":     orgSlug,
		"owner_email":  email,
		"agent_name":   agentName,
		"client_type":  "test_client",
		"public_key":   base64.StdEncoding.EncodeToString(publicKey),
		"capabilities": []string{"publish_artifact", "respond_query", "request_approval"},
	})
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}

	signature := ed25519.Sign(privateKey, []byte(challenge.Challenge))
	response, err := client.CompleteRegistration(context.Background(), challenge.ChallengeID, base64.StdEncoding.EncodeToString(signature))
	if err != nil {
		t.Fatalf("complete registration: %v", err)
	}
	return response.AccessToken
}

func grantPermission(t *testing.T, baseURL, accessToken string, body map[string]any) {
	t.Helper()
	doJSON(t, baseURL, http.MethodPost, "/v1/policy-grants", accessToken, body, nil)
}

func queryPeerStatus(t *testing.T, baseURL, accessToken string, body map[string]any) string {
	t.Helper()
	var payload map[string]any
	doJSON(t, baseURL, http.MethodPost, "/v1/queries", accessToken, body, &payload)
	return payload["query_id"].(string)
}

func sendRequestToPeer(t *testing.T, baseURL, accessToken string, body map[string]any) string {
	t.Helper()
	var payload map[string]any
	doJSON(t, baseURL, http.MethodPost, "/v1/requests", accessToken, body, &payload)
	return payload["request_id"].(string)
}

func doJSON(t *testing.T, baseURL, method, path, accessToken string, body any, out any) {
	t.Helper()

	var requestBody *strings.Reader
	if body == nil {
		requestBody = strings.NewReader("")
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		requestBody = strings.NewReader(string(data))
	}

	req, err := http.NewRequestWithContext(context.Background(), method, baseURL+path, requestBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		t.Fatalf("request %s %s failed: status=%d body=%v", method, path, resp.StatusCode, payload)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
}
