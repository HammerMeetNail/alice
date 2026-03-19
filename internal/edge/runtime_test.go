package edge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
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
			StateFile:       "sam-state.json",
			CredentialsFile: "sam-credentials.json",
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

func TestRuntimeRunOncePollsLiveJira(t *testing.T) {
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

	issueUpdatedAt := time.Date(2026, 3, 19, 15, 45, 0, 0, time.UTC)
	t.Setenv("ALICE_JIRA_TOKEN", "test-jira-token")

	jiraAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-jira-token" {
			t.Fatalf("unexpected jira auth header: %q", got)
		}
		if r.URL.Path != "/rest/api/3/search" {
			t.Fatalf("unexpected jira path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("jql"); !strings.Contains(got, `project = "PAY"`) {
			t.Fatalf("unexpected jira jql: %q", got)
		}

		payload := map[string]any{
			"issues": []map[string]any{
				{
					"key": "PAY-123",
					"fields": map[string]any{
						"issuetype": map[string]any{
							"name": "Story",
						},
						"status": map[string]any{
							"name": "In Review",
						},
						"updated": issueUpdatedAt.Format(time.RFC3339),
						"assignee": map[string]any{
							"emailAddress": "sam@example.com",
						},
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode jira payload: %v", err)
		}
	}))
	defer jiraAPI.Close()

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
			Jira: JiraConnectorConfig{
				Enabled:     true,
				APIBaseURL:  jiraAPI.URL,
				TokenEnvVar: "ALICE_JIRA_TOKEN",
				Projects: []JiraProjectConfig{
					{
						Key:         "PAY",
						ProjectRefs: []string{"payments-api"},
					},
				},
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load live jira config: %v", err)
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with live jira polling: %v", err)
	}
	if len(report.PublishedArtifacts) != 1 {
		t.Fatalf("expected one live jira artifact to be published, got %d", len(report.PublishedArtifacts))
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after live jira publish: %v", err)
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
			"start": issueUpdatedAt.Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   issueUpdatedAt.Add(1 * time.Hour).Format(time.RFC3339),
		},
	})

	var result map[string]any
	doJSON(t, server.URL, http.MethodGet, "/v1/queries/"+queryID, aliceToken, nil, &result)
	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected one live jira artifact in query result, got %d", len(artifacts))
	}
}

func TestRuntimeRunOncePollsLiveGCal(t *testing.T) {
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

	eventUpdatedAt := time.Date(2026, 3, 19, 16, 15, 0, 0, time.UTC)
	eventStartAt := time.Date(2026, 3, 19, 17, 0, 0, 0, time.UTC)
	eventEndAt := time.Date(2026, 3, 19, 17, 30, 0, 0, time.UTC)
	t.Setenv("ALICE_GCAL_TOKEN", "test-gcal-token")

	gcalAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-gcal-token" {
			t.Fatalf("unexpected gcal auth header: %q", got)
		}
		if r.URL.Path != "/calendar/v3/calendars/primary/events" {
			t.Fatalf("unexpected gcal path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("singleEvents"); got != "true" {
			t.Fatalf("unexpected gcal singleEvents value: %q", got)
		}

		payload := map[string]any{
			"items": []map[string]any{
				{
					"id":        "evt-1",
					"status":    "confirmed",
					"updated":   eventUpdatedAt.Format(time.RFC3339),
					"eventType": "focusTime",
					"start": map[string]any{
						"dateTime": eventStartAt.Format(time.RFC3339),
					},
					"end": map[string]any{
						"dateTime": eventEndAt.Format(time.RFC3339),
					},
					"attendees": []map[string]any{
						{"email": "sam@example.com"},
						{"email": "alice@example.com"},
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode gcal payload: %v", err)
		}
	}))
	defer gcalAPI.Close()

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
			GCal: GCalConnectorConfig{
				Enabled:     true,
				APIBaseURL:  gcalAPI.URL + "/calendar/v3",
				TokenEnvVar: "ALICE_GCAL_TOKEN",
				Calendars: []GCalCalendarConfig{
					{
						ID:          "primary",
						ProjectRefs: []string{"payments-api"},
						Category:    "focus",
					},
				},
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load live gcal config: %v", err)
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with live gcal polling: %v", err)
	}
	if len(report.PublishedArtifacts) != 1 {
		t.Fatalf("expected one live gcal artifact to be published, got %d", len(report.PublishedArtifacts))
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after live gcal publish: %v", err)
	}
	aliceToken := registerAgent(t, server.URL, "example-corp", "alice@example.com", "alice-agent")
	grantPermission(t, server.URL, state.AccessToken, map[string]any{
		"grantee_user_email":     "alice@example.com",
		"scope_type":             "project",
		"scope_ref":              "payments-api",
		"allowed_artifact_types": []string{"status_delta"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, server.URL, aliceToken, map[string]any{
		"to_user_email":   "sam@example.com",
		"purpose":         "status_check",
		"question":        "Is Sam available this afternoon?",
		"requested_types": []string{"status_delta"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": eventUpdatedAt.Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   eventUpdatedAt.Add(2 * time.Hour).Format(time.RFC3339),
		},
	})

	var result map[string]any
	doJSON(t, server.URL, http.MethodGet, "/v1/queries/"+queryID, aliceToken, nil, &result)
	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("expected one live gcal artifact in query result, got %d", len(artifacts))
	}
}

func TestRuntimeRunOncePersistsJiraCursorState(t *testing.T) {
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

	issueUpdatedAt := time.Date(2026, 3, 19, 18, 0, 0, 0, time.UTC)
	t.Setenv("ALICE_JIRA_TOKEN", "test-jira-token")

	requestCount := 0
	jiraAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		jql := r.URL.Query().Get("jql")
		switch requestCount {
		case 1:
			if strings.Contains(jql, "updated >") {
				t.Fatalf("did not expect cursor filter on first jira poll: %q", jql)
			}
		case 2:
			if !strings.Contains(jql, `updated > "2026-03-19 18:00"`) {
				t.Fatalf("expected cursor filter on second jira poll, got %q", jql)
			}
		}

		payload := map[string]any{
			"issues": []map[string]any{
				{
					"key": "PAY-123",
					"fields": map[string]any{
						"issuetype": map[string]any{
							"name": "Story",
						},
						"status": map[string]any{
							"name": "In Review",
						},
						"updated": issueUpdatedAt.Format(time.RFC3339),
						"assignee": map[string]any{
							"emailAddress": "sam@example.com",
						},
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode jira payload: %v", err)
		}
	}))
	defer jiraAPI.Close()

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
			Jira: JiraConnectorConfig{
				Enabled:     true,
				APIBaseURL:  jiraAPI.URL,
				TokenEnvVar: "ALICE_JIRA_TOKEN",
				Projects: []JiraProjectConfig{
					{
						Key:         "PAY",
						ProjectRefs: []string{"payments-api"},
					},
				},
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load jira cursor config: %v", err)
	}

	firstRun, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("first runtime run: %v", err)
	}
	if len(firstRun.PublishedArtifacts) != 1 {
		t.Fatalf("expected first jira cursor run to publish one artifact, got %d", len(firstRun.PublishedArtifacts))
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after first jira cursor run: %v", err)
	}
	if got := state.CursorTime(jiraCursorKey("PAY")); !got.Equal(issueUpdatedAt) {
		t.Fatalf("expected jira cursor %s, got %s", issueUpdatedAt.Format(time.RFC3339), got.Format(time.RFC3339))
	}

	state.PublishedArtifacts = map[string]string{}
	if err := SaveState(cfg.StatePath(), state); err != nil {
		t.Fatalf("save state with cleared artifacts: %v", err)
	}

	secondRun, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second jira cursor runtime run: %v", err)
	}
	if len(secondRun.PublishedArtifacts) != 0 {
		t.Fatalf("expected second jira cursor run to publish zero artifacts, got %d", len(secondRun.PublishedArtifacts))
	}
}

func TestRuntimeRunOnceLoadsGitHubTokenFromFile(t *testing.T) {
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

	pullUpdatedAt := time.Date(2026, 3, 19, 19, 15, 0, 0, time.UTC)
	githubAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-from-file" {
			t.Fatalf("unexpected github auth header: %q", got)
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
				"requested_reviewers": []map[string]any{},
				"assignees":           []map[string]any{},
			},
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Fatalf("encode github payload: %v", err)
		}
	}))
	defer githubAPI.Close()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "github-token.txt"), []byte("token-from-file\n"), 0o600); err != nil {
		t.Fatalf("write github token file: %v", err)
	}

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
				Enabled:    true,
				APIBaseURL: githubAPI.URL,
				TokenFile:  "github-token.txt",
				ActorLogin: "sam",
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
		t.Fatalf("load github token-file config: %v", err)
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with github token file: %v", err)
	}
	if len(report.PublishedArtifacts) != 1 {
		t.Fatalf("expected one github artifact with token file auth, got %d", len(report.PublishedArtifacts))
	}
}

func TestRuntimeRunOncePublishesAggregateProjectArtifacts(t *testing.T) {
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
	writeJiraFixtureFile(t, filepath.Join(tempDir, "jira-fixtures.json"))
	writeGCalFixtureFile(t, filepath.Join(tempDir, "gcal-fixtures.json"))

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
			Jira: JiraConnectorConfig{
				FixtureFile: "jira-fixtures.json",
			},
			GCal: GCalConnectorConfig{
				FixtureFile: "gcal-fixtures.json",
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load aggregate fixture config: %v", err)
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with aggregate fixtures: %v", err)
	}
	if len(report.PublishedArtifacts) < 4 {
		t.Fatalf("expected aggregate runtime to publish at least four artifacts, got %d", len(report.PublishedArtifacts))
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after aggregate publish: %v", err)
	}
	aliceToken := registerAgent(t, server.URL, "example-corp", "alice@example.com", "alice-agent")
	grantPermission(t, server.URL, state.AccessToken, map[string]any{
		"grantee_user_email":     "alice@example.com",
		"scope_type":             "project",
		"scope_ref":              "payments-api",
		"allowed_artifact_types": []string{"status_delta", "blocker", "commitment"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	})

	queryID := queryPeerStatus(t, server.URL, aliceToken, map[string]any{
		"to_user_email":   "sam@example.com",
		"purpose":         "status_check",
		"question":        "What changed on payments-api today?",
		"requested_types": []string{"status_delta", "blocker", "commitment"},
		"project_scope":   []string{"payments-api"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
		},
	})

	var result map[string]any
	doJSON(t, server.URL, http.MethodGet, "/v1/queries/"+queryID, aliceToken, nil, &result)
	response := result["response"].(map[string]any)
	artifacts := response["artifacts"].([]any)
	if len(artifacts) < 4 {
		t.Fatalf("expected at least four derived project artifacts, got %d", len(artifacts))
	}

	foundTypes := map[string]bool{}
	for _, artifact := range artifacts {
		payload := artifact.(map[string]any)
		foundTypes[payload["type"].(string)] = true
	}
	if !foundTypes["status_delta"] {
		t.Fatalf("expected aggregate query result to include status_delta")
	}
	if !foundTypes["blocker"] {
		t.Fatalf("expected aggregate query result to include blocker")
	}
	if !foundTypes["commitment"] {
		t.Fatalf("expected aggregate query result to include commitment")
	}
}

func TestRuntimeBootstrapGitHubConnectorPersistsCredentialAndUsesIt(t *testing.T) {
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

	pullUpdatedAt := time.Date(2026, 3, 19, 20, 30, 0, 0, time.UTC)
	oauthProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			if got := r.URL.Query().Get("client_id"); got != "github-client" {
				t.Fatalf("unexpected oauth client_id: %q", got)
			}
			if got := r.URL.Query().Get("response_type"); got != "code" {
				t.Fatalf("unexpected oauth response_type: %q", got)
			}
			if got := r.URL.Query().Get("code_challenge_method"); got != "S256" {
				t.Fatalf("unexpected oauth code challenge method: %q", got)
			}
			if got := r.URL.Query().Get("scope"); got != "repo read:user" {
				t.Fatalf("unexpected oauth scope: %q", got)
			}
			state := r.URL.Query().Get("state")
			if state == "" {
				t.Fatalf("expected oauth state")
			}
			redirectURL := r.URL.Query().Get("redirect_uri")
			if !strings.Contains(redirectURL, "/oauth/github/callback") {
				t.Fatalf("unexpected redirect uri: %q", redirectURL)
			}
			http.Redirect(w, r, redirectURL+"?code=test-oauth-code&state="+state, http.StatusFound)
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse oauth token form: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "authorization_code" {
				t.Fatalf("unexpected oauth grant type: %q", got)
			}
			if got := r.Form.Get("code"); got != "test-oauth-code" {
				t.Fatalf("unexpected oauth code: %q", got)
			}
			if got := r.Form.Get("client_id"); got != "github-client" {
				t.Fatalf("unexpected oauth token client_id: %q", got)
			}
			if got := r.Form.Get("client_secret"); got != "github-secret" {
				t.Fatalf("unexpected oauth client secret: %q", got)
			}
			if got := r.Form.Get("code_verifier"); got == "" {
				t.Fatalf("expected oauth code_verifier")
			}
			payload := map[string]any{
				"access_token": "bootstrapped-github-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			if err := json.NewEncoder(w).Encode(payload); err != nil {
				t.Fatalf("encode oauth token response: %v", err)
			}
		case "/repos/acme/payments-api/pulls":
			if got := r.Header.Get("Authorization"); got != "Bearer bootstrapped-github-token" {
				t.Fatalf("unexpected github auth header after bootstrap: %q", got)
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
					"requested_reviewers": []map[string]any{},
					"assignees":           []map[string]any{},
				},
			}
			if err := json.NewEncoder(w).Encode(payload); err != nil {
				t.Fatalf("encode github payload: %v", err)
			}
		default:
			t.Fatalf("unexpected oauth/github path: %s", r.URL.Path)
		}
	}))
	defer oauthProvider.Close()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "github-client-secret.txt"), []byte("github-secret\n"), 0o600); err != nil {
		t.Fatalf("write github client secret file: %v", err)
	}

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
				Enabled:    true,
				APIBaseURL: oauthProvider.URL,
				ActorLogin: "sam",
				Repositories: []GitHubRepositoryConfig{
					{
						Name:        "acme/payments-api",
						ProjectRefs: []string{"payments-api"},
					},
				},
				OAuth: ConnectorOAuthConfig{
					Enabled:          true,
					ClientID:         "github-client",
					ClientSecretFile: "github-client-secret.txt",
					AuthorizationURL: oauthProvider.URL + "/authorize",
					TokenURL:         oauthProvider.URL + "/token",
					CallbackURL:      "http://127.0.0.1:0/oauth/github/callback",
					Scopes:           []string{"repo", "read:user"},
				},
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load bootstrap github config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := NewRuntime(cfg).BootstrapConnector(ctx, "github", func(prompt ConnectorBootstrapPrompt) error {
		go func() {
			resp, err := http.Get(prompt.AuthorizationURL)
			if err != nil {
				t.Errorf("perform oauth authorization request: %v", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("unexpected oauth callback status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("bootstrap github connector: %v", err)
	}
	if !result.StoredInState {
		t.Fatalf("expected bootstrap result to report persisted state storage")
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after bootstrap: %v", err)
	}
	if len(state.ConnectorCredentials) != 0 {
		t.Fatalf("expected connector credentials to be removed from the general state file")
	}
	credentials, err := LoadCredentialStore(cfg.CredentialsPath())
	if err != nil {
		t.Fatalf("load credential store after bootstrap: %v", err)
	}
	credential := credentials.ConnectorCredential("github")
	if credential.AccessToken != "bootstrapped-github-token" {
		t.Fatalf("expected bootstrapped github access token to be stored, got %q", credential.AccessToken)
	}
	if _, ok := state.PendingConnectorAuths["github"]; ok {
		t.Fatalf("expected pending github oauth state to be cleared")
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with bootstrapped github token: %v", err)
	}
	if len(report.PublishedArtifacts) != 1 {
		t.Fatalf("expected one artifact after bootstrapped github polling, got %d", len(report.PublishedArtifacts))
	}
}

func TestRuntimeBootstrapConnectorRejectsStateMismatch(t *testing.T) {
	tempDir := t.TempDir()
	configPath := writeEdgeConfig(t, tempDir, edgeConfigFile{
		Agent: AgentConfig{
			OrgSlug:    "example-corp",
			OwnerEmail: "sam@example.com",
			AgentName:  "sam-agent",
			ClientType: "edge_agent",
		},
		Server: ServerConfig{
			BaseURL: "http://127.0.0.1:8080",
		},
		Runtime: RuntimeConfig{
			StateFile:       "sam-state.json",
			CredentialsFile: "sam-credentials.json",
		},
		Connectors: ConnectorsConfig{
			GitHub: GitHubConnectorConfig{
				OAuth: ConnectorOAuthConfig{
					Enabled:          true,
					ClientID:         "github-client",
					AuthorizationURL: "https://example.com/authorize",
					TokenURL:         "https://example.com/token",
					CallbackURL:      "http://127.0.0.1:0/oauth/github/callback",
				},
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load github oauth mismatch config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = NewRuntime(cfg).BootstrapConnector(ctx, "github", func(prompt ConnectorBootstrapPrompt) error {
		go func() {
			resp, reqErr := http.Get(prompt.CallbackURL + "?code=test-code&state=wrong-state")
			if reqErr != nil {
				t.Errorf("perform mismatched oauth callback request: %v", reqErr)
				return
			}
			defer resp.Body.Close()
		}()
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("expected oauth state mismatch error, got %v", err)
	}

	state, err := LoadState(cfg.StatePath())
	if err != nil {
		t.Fatalf("load state after mismatch bootstrap: %v", err)
	}
	credentials, err := LoadCredentialStore(cfg.CredentialsPath())
	if err != nil {
		t.Fatalf("load credential store after mismatch bootstrap: %v", err)
	}
	if got := credentials.ConnectorCredential("github").AccessToken; got != "" {
		t.Fatalf("expected no stored github credential after state mismatch, got %q", got)
	}
	if _, ok := state.PendingConnectorAuths["github"]; ok {
		t.Fatalf("expected pending github oauth state to be cleared after mismatch")
	}
}

func TestRuntimeRunOnceRefreshesExpiredGitHubConnectorToken(t *testing.T) {
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

	pullUpdatedAt := time.Date(2026, 3, 19, 21, 0, 0, 0, time.UTC)
	oauthProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse refresh-token form: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "refresh_token" {
				t.Fatalf("unexpected refresh grant type: %q", got)
			}
			if got := r.Form.Get("refresh_token"); got != "github-refresh-token" {
				t.Fatalf("unexpected refresh token: %q", got)
			}
			if got := r.Form.Get("client_id"); got != "github-client" {
				t.Fatalf("unexpected refresh client_id: %q", got)
			}
			if got := r.Form.Get("client_secret"); got != "github-secret" {
				t.Fatalf("unexpected refresh client secret: %q", got)
			}
			payload := map[string]any{
				"access_token": "refreshed-github-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			if err := json.NewEncoder(w).Encode(payload); err != nil {
				t.Fatalf("encode refresh-token response: %v", err)
			}
		case "/repos/acme/payments-api/pulls":
			if got := r.Header.Get("Authorization"); got != "Bearer refreshed-github-token" {
				t.Fatalf("unexpected github auth header after refresh: %q", got)
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
					"requested_reviewers": []map[string]any{},
					"assignees":           []map[string]any{},
				},
			}
			if err := json.NewEncoder(w).Encode(payload); err != nil {
				t.Fatalf("encode github payload: %v", err)
			}
		default:
			t.Fatalf("unexpected refresh/github path: %s", r.URL.Path)
		}
	}))
	defer oauthProvider.Close()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "github-client-secret.txt"), []byte("github-secret\n"), 0o600); err != nil {
		t.Fatalf("write github client secret file: %v", err)
	}

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
			StateFile:       "sam-state.json",
			CredentialsFile: "sam-credentials.json",
		},
		Connectors: ConnectorsConfig{
			GitHub: GitHubConnectorConfig{
				Enabled:    true,
				APIBaseURL: oauthProvider.URL,
				ActorLogin: "sam",
				Repositories: []GitHubRepositoryConfig{
					{
						Name:        "acme/payments-api",
						ProjectRefs: []string{"payments-api"},
					},
				},
				OAuth: ConnectorOAuthConfig{
					Enabled:          true,
					ClientID:         "github-client",
					ClientSecretFile: "github-client-secret.txt",
					AuthorizationURL: oauthProvider.URL + "/authorize",
					TokenURL:         oauthProvider.URL + "/token",
					CallbackURL:      "http://127.0.0.1:8787/oauth/github/callback",
					Scopes:           []string{"repo", "read:user"},
				},
			},
		},
	})

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load refresh github config: %v", err)
	}
	if err := SaveCredentialStore(cfg.CredentialsPath(), CredentialStore{
		ConnectorCredentials: map[string]ConnectorCredential{
			"github": {
				AccessToken:  "expired-github-token",
				RefreshToken: "github-refresh-token",
				TokenType:    "Bearer",
				ExpiresAt:    time.Now().UTC().Add(-2 * time.Minute),
				ObtainedAt:   time.Now().UTC().Add(-1 * time.Hour),
			},
		},
	}); err != nil {
		t.Fatalf("write credential store with expired github token: %v", err)
	}

	report, err := NewRuntime(cfg).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run edge runtime with refreshed github token: %v", err)
	}
	if len(report.PublishedArtifacts) != 1 {
		t.Fatalf("expected one artifact after github token refresh, got %d", len(report.PublishedArtifacts))
	}

	credentials, err := LoadCredentialStore(cfg.CredentialsPath())
	if err != nil {
		t.Fatalf("load credential store after refresh: %v", err)
	}
	credential := credentials.ConnectorCredential("github")
	if credential.AccessToken != "refreshed-github-token" {
		t.Fatalf("expected refreshed github token to be stored, got %q", credential.AccessToken)
	}
	if credential.RefreshToken != "github-refresh-token" {
		t.Fatalf("expected github refresh token to be preserved, got %q", credential.RefreshToken)
	}
	if credential.ExpiresAt.IsZero() || !credential.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expected refreshed github credential to have a future expiry, got %s", credential.ExpiresAt.Format(time.RFC3339))
	}
}

func TestLoadCredentialStoreRejectsOpenPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"connector_credentials":{}}`), 0o644); err != nil {
		t.Fatalf("write open-permission credential store: %v", err)
	}

	_, err := LoadCredentialStore(path)
	if err == nil || !strings.Contains(err.Error(), "must not be group or world accessible") {
		t.Fatalf("expected permission error for open credential store, got %v", err)
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

func writeJiraFixtureFile(t *testing.T, path string) {
	t.Helper()

	payload := map[string]any{
		"issues": []map[string]any{
			{
				"key":          "PAY-123",
				"issue_type":   "Story",
				"status":       "In Review",
				"updated_at":   time.Now().UTC().Format(time.RFC3339),
				"project_refs": []string{"payments-api"},
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal jira fixtures: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write jira fixtures: %v", err)
	}
}

func writeGCalFixtureFile(t *testing.T, path string) {
	t.Helper()

	startAt := time.Now().UTC().Add(30 * time.Minute)
	endAt := startAt.Add(30 * time.Minute)
	payload := map[string]any{
		"events": []map[string]any{
			{
				"event_id":       "evt-1",
				"category":       "focus",
				"start_at":       startAt.Format(time.RFC3339),
				"end_at":         endAt.Format(time.RFC3339),
				"project_refs":   []string{"payments-api"},
				"attendee_count": 2,
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal gcal fixtures: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write gcal fixtures: %v", err)
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
