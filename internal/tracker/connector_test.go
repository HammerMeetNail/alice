package tracker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"alice/internal/core"
)

// TestBuildConnectorsFromEnv covers the env-var driven connector registry:
// defaults, backward compat, error paths, and multi-connector selection.
func TestBuildConnectorsFromEnv(t *testing.T) {
	t.Run("no env returns no connectors", func(t *testing.T) {
		t.Setenv("ALICE_TRACK_CONNECTORS", "")
		connectors, err := buildConnectorsFromEnv(Config{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(connectors) != 0 {
			t.Fatalf("expected 0 connectors, got %d", len(connectors))
		}
	})

	t.Run("backward compat: repos set, connectors unset → git only", func(t *testing.T) {
		t.Setenv("ALICE_TRACK_CONNECTORS", "")
		connectors, err := buildConnectorsFromEnv(Config{RepoPaths: []string{"/tmp/r"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(connectors) != 1 || connectors[0].Name() != "git" {
			t.Fatalf("expected git-only default, got %v", connectorNames(connectors))
		}
	})

	t.Run("unknown connector returns a configuration error", func(t *testing.T) {
		t.Setenv("ALICE_TRACK_CONNECTORS", "gopher")
		_, err := buildConnectorsFromEnv(Config{})
		if err == nil {
			t.Fatal("expected error for unknown connector")
		}
	})

	t.Run("duplicate entries are deduplicated", func(t *testing.T) {
		t.Setenv("ALICE_TRACK_CONNECTORS", "git,git,git")
		connectors, err := buildConnectorsFromEnv(Config{RepoPaths: []string{"/tmp/r"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(connectors) != 1 {
			t.Fatalf("expected dedup to produce 1 connector, got %d", len(connectors))
		}
	})

	t.Run("explicitly requested connector without required env is skipped", func(t *testing.T) {
		// GitHub requested but no ALICE_TRACK_GITHUB_TOKEN — should silently skip.
		t.Setenv("ALICE_TRACK_CONNECTORS", "github")
		t.Setenv("ALICE_TRACK_GITHUB_TOKEN", "")
		connectors, err := buildConnectorsFromEnv(Config{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(connectors) != 0 {
			t.Fatalf("expected github to be skipped when token missing, got %v", connectorNames(connectors))
		}
	})

	t.Run("github requires repo list when token is set", func(t *testing.T) {
		t.Setenv("ALICE_TRACK_CONNECTORS", "github")
		t.Setenv("ALICE_TRACK_GITHUB_TOKEN", "ghs_xxx")
		t.Setenv("ALICE_TRACK_GITHUB_REPOS", "")
		_, err := buildConnectorsFromEnv(Config{})
		if err == nil {
			t.Fatal("expected error when github connector has token but no repos")
		}
	})
}

// TestGitHubConnector_Poll exercises the live GitHub connector against a
// mock GitHub API. It is deliberately small — the edge package already has
// its own fixture coverage; this test exists to prove the tracker can drive
// the poller end-to-end and that artifact derivation runs on the results.
func TestGitHubConnector_Poll(t *testing.T) {
	pullUpdatedAt := time.Date(2026, 3, 19, 14, 30, 0, 0, time.UTC)

	var requests int
	githubAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if r.URL.Path != "/repos/acme/payments-api/pulls" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Fatalf("unexpected state filter: %q", got)
		}

		payload := []map[string]any{{
			"number":     128,
			"state":      "open",
			"draft":      false,
			"title":      "Retry payments handler",
			"updated_at": pullUpdatedAt.Format(time.RFC3339),
			"user":       map[string]any{"login": "alice"},
			"requested_reviewers": []map[string]any{},
			"assignees":           []map[string]any{},
		}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer githubAPI.Close()

	t.Setenv("ALICE_TRACK_GITHUB_TOKEN", "test-token")
	t.Setenv("ALICE_TRACK_GITHUB_REPOS", "acme/payments-api")
	t.Setenv("ALICE_TRACK_GITHUB_USER", "alice")
	t.Setenv("ALICE_TRACK_GITHUB_API_URL", githubAPI.URL)

	connector, err := newGitHubConnectorFromEnv()
	if err != nil {
		t.Fatalf("newGitHubConnectorFromEnv: %v", err)
	}
	if connector == nil {
		t.Fatal("expected a connector when token and repos are set")
	}

	artifacts, err := connector.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if artifacts[0].StructuredPayload["source_system"] != "github" {
		t.Fatalf("expected source_system=github, got %v", artifacts[0].StructuredPayload["source_system"])
	}
	if artifacts[0].StructuredPayload["repository"] != "acme/payments-api" {
		t.Fatalf("expected repository=acme/payments-api, got %v", artifacts[0].StructuredPayload["repository"])
	}
	if requests == 0 {
		t.Fatal("mock GitHub API was not called")
	}
}

// TestJiraConnector_Poll drives the tracker's Jira connector against a mock
// Jira REST endpoint. The JQL shape is driven by the edge package; this
// test covers the tracker's env-var plumbing into that shape.
func TestJiraConnector_Poll(t *testing.T) {
	issueUpdatedAt := time.Date(2026, 3, 19, 12, 0, 0, 0, time.UTC)

	jiraAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer jira-token" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if r.URL.Path != "/rest/api/3/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		payload := map[string]any{
			"startAt":    0,
			"maxResults": 50,
			"total":      1,
			"issues": []map[string]any{{
				"key": "PROJ-1",
				"fields": map[string]any{
					"issuetype": map[string]any{"name": "Task"},
					"status":    map[string]any{"name": "In Progress"},
					"updated":   issueUpdatedAt.Format(time.RFC3339),
					"assignee": map[string]any{
						"accountId":    "acct-1",
						"emailAddress": "alice@example.com",
						"displayName":  "Alice",
					},
				},
			}},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer jiraAPI.Close()

	t.Setenv("ALICE_TRACK_JIRA_TOKEN", "jira-token")
	t.Setenv("ALICE_TRACK_JIRA_BASE_URL", jiraAPI.URL)
	t.Setenv("ALICE_TRACK_JIRA_PROJECTS", "PROJ")
	t.Setenv("ALICE_TRACK_JIRA_ACCOUNT_ID", "acct-1")

	connector, err := newJiraConnectorFromEnv()
	if err != nil {
		t.Fatalf("newJiraConnectorFromEnv: %v", err)
	}
	if connector == nil {
		t.Fatal("expected a connector when required env is set")
	}

	artifacts, err := connector.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if artifacts[0].StructuredPayload["issue_key"] != "PROJ-1" {
		t.Fatalf("expected issue_key=PROJ-1, got %v", artifacts[0].StructuredPayload["issue_key"])
	}
}

// TestCalendarConnector_Poll drives the tracker's Calendar connector against
// a mock Google Calendar API.
func TestCalendarConnector_Poll(t *testing.T) {
	eventUpdatedAt := time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)

	gcalAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer gcal-token" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if r.URL.Path != "/calendars/primary/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		payload := map[string]any{
			"items": []map[string]any{{
				"id":        "evt-1",
				"status":    "confirmed",
				"updated":   eventUpdatedAt.Format(time.RFC3339),
				"eventType": "default",
				"start":     map[string]any{"dateTime": eventUpdatedAt.Format(time.RFC3339)},
				"end":       map[string]any{"dateTime": eventUpdatedAt.Add(time.Hour).Format(time.RFC3339)},
				"attendees": []map[string]any{{"email": "a@example.com"}, {"email": "b@example.com"}},
			}},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer gcalAPI.Close()

	t.Setenv("ALICE_TRACK_CALENDAR_TOKEN", "gcal-token")
	t.Setenv("ALICE_TRACK_CALENDAR_API_URL", gcalAPI.URL)
	// Default ID "primary" applies when ALICE_TRACK_CALENDAR_IDS is unset.
	t.Setenv("ALICE_TRACK_CALENDAR_IDS", "")

	connector, err := newCalendarConnectorFromEnv()
	if err != nil {
		t.Fatalf("newCalendarConnectorFromEnv: %v", err)
	}
	if connector == nil {
		t.Fatal("expected a connector when token is set")
	}

	artifacts, err := connector.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if artifacts[0].StructuredPayload["source_system"] != "gcal" {
		t.Fatalf("expected source_system=gcal, got %v", artifacts[0].StructuredPayload["source_system"])
	}
}

// TestTracker_MultipleConnectors asserts the tracker loop iterates multiple
// connectors, deduplicates across them, and publishes each unique artifact.
// A failing connector must not stop the others from publishing.
func TestTracker_MultipleConnectors(t *testing.T) {
	ghAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := []map[string]any{{
			"number":              1,
			"state":               "open",
			"draft":               false,
			"title":               "Add retries",
			"updated_at":          time.Now().UTC().Format(time.RFC3339),
			"user":                map[string]any{"login": "alice"},
			"requested_reviewers": []map[string]any{},
			"assignees":           []map[string]any{},
		}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer ghAPI.Close()

	t.Setenv("ALICE_TRACK_CONNECTORS", "github")
	t.Setenv("ALICE_TRACK_GITHUB_TOKEN", "tok")
	t.Setenv("ALICE_TRACK_GITHUB_REPOS", "acme/one,acme/two")
	t.Setenv("ALICE_TRACK_GITHUB_USER", "alice")
	t.Setenv("ALICE_TRACK_GITHUB_API_URL", ghAPI.URL)

	var mu sync.Mutex
	var published []map[string]any
	publishFn := func(_ context.Context, body map[string]any) (map[string]any, error) {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, body)
		return map[string]any{"artifact_id": "art_001"}, nil
	}

	// Also inject a deliberately failing connector alongside the real GitHub
	// one to prove a single failure does not block the rest.
	tr := New(Config{Interval: time.Minute}, publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)
	tr = tr.WithConnectors(append(tr.connectors, failingConnector{}))

	tr.Tick(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// Two repos, each returning one PR → two artifacts. The failing
	// connector contributes nothing but also does not abort the tick.
	if len(published) != 2 {
		t.Fatalf("expected 2 publishes, got %d", len(published))
	}
}

type failingConnector struct{}

func (failingConnector) Name() string { return "failing" }
func (failingConnector) Poll(context.Context) ([]core.Artifact, error) {
	return nil, errSimulated
}

var errSimulated = simulatedError("simulated connector failure")

type simulatedError string

func (e simulatedError) Error() string { return string(e) }

func connectorNames(connectors []Connector) []string {
	names := make([]string, 0, len(connectors))
	for _, c := range connectors {
		names = append(names, c.Name())
	}
	return names
}
