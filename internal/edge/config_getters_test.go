package edge

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"alice/internal/core"
)

// minimalConfig returns a Config with only the required fields set so tests
// that exercise optional getter methods don't need to deal with validation.
func minimalConfig() Config {
	return Config{
		Agent: AgentConfig{
			OrgSlug:    "test-org",
			OwnerEmail: "owner@example.com",
			AgentName:  "Test Agent",
			ClientType: "edge_agent",
		},
		Server:  ServerConfig{BaseURL: "http://localhost:8080"},
		Runtime: RuntimeConfig{StateFile: "/tmp/test-state.json", AllowPlaintextState: true},
	}
}

// --- Jira getter methods ---

func TestJiraOAuthConfig_ReturnsOAuthConfig(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.Jira.OAuth = ConnectorOAuthConfig{
		ClientID:  "jira-client-id",
		TokenURL:  "https://auth.atlassian.com/oauth/token",
		CallbackURL: "http://127.0.0.1:8787/oauth/jira/callback",
		AuthorizationURL: "https://auth.atlassian.com/authorize",
	}
	got := cfg.JiraOAuthConfig()
	if got.ClientID != "jira-client-id" {
		t.Fatalf("JiraOAuthConfig().ClientID = %q, want %q", got.ClientID, "jira-client-id")
	}
}

func TestJiraWebhookListenAddr_ReturnsTrimmed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.Jira.Webhook.ListenAddr = "  127.0.0.1:8789  "
	got := cfg.JiraWebhookListenAddr()
	if got != "127.0.0.1:8789" {
		t.Fatalf("JiraWebhookListenAddr() = %q, want %q", got, "127.0.0.1:8789")
	}
}

func TestJiraWebhookSecretEnvVar_ReturnsTrimmed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.Jira.Webhook.SecretEnvVar = "  ALICE_JIRA_WEBHOOK_SECRET  "
	got := cfg.JiraWebhookSecretEnvVar()
	if got != "ALICE_JIRA_WEBHOOK_SECRET" {
		t.Fatalf("JiraWebhookSecretEnvVar() = %q, want %q", got, "ALICE_JIRA_WEBHOOK_SECRET")
	}
}

func TestJiraWebhookSecretFile_Resolves(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.Jira.Webhook.SecretFile = ""
	// An empty path resolves to empty.
	got := cfg.JiraWebhookSecretFile()
	if got != "" {
		t.Fatalf("JiraWebhookSecretFile() = %q, want empty", got)
	}
}

func TestJiraWebhookProjects_FallsBackToLiveProjects(t *testing.T) {
	cfg := minimalConfig()
	live := []JiraProjectConfig{{Key: "PROJ"}}
	cfg.Connectors.Jira.Projects = live
	// No webhook-specific projects set — should fall back to live projects.
	got := cfg.JiraWebhookProjects()
	if len(got) != 1 || got[0].Key != "PROJ" {
		t.Fatalf("JiraWebhookProjects() = %v, want [{PROJ}]", got)
	}
}

func TestJiraWebhookProjects_UsesWebhookOverride(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.Jira.Projects = []JiraProjectConfig{{Key: "LIVE"}}
	cfg.Connectors.Jira.Webhook.Projects = []JiraProjectConfig{{Key: "WEBHOOK"}}
	got := cfg.JiraWebhookProjects()
	if len(got) != 1 || got[0].Key != "WEBHOOK" {
		t.Fatalf("JiraWebhookProjects() = %v, want [{WEBHOOK}]", got)
	}
}

// --- GCal getter methods ---

func TestGCalOAuthConfig_ReturnsOAuthConfig(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GCal.OAuth = ConnectorOAuthConfig{
		ClientID:         "gcal-client-id",
		TokenURL:         "https://oauth2.googleapis.com/token",
		AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth",
		CallbackURL:      "http://127.0.0.1:8787/oauth/gcal/callback",
	}
	got := cfg.GCalOAuthConfig()
	if got.ClientID != "gcal-client-id" {
		t.Fatalf("GCalOAuthConfig().ClientID = %q, want %q", got.ClientID, "gcal-client-id")
	}
}

func TestGCalWebhookListenAddr_ReturnsTrimmed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GCal.Webhook.ListenAddr = "  127.0.0.1:8790  "
	got := cfg.GCalWebhookListenAddr()
	if got != "127.0.0.1:8790" {
		t.Fatalf("GCalWebhookListenAddr() = %q, want %q", got, "127.0.0.1:8790")
	}
}

func TestGCalWebhookCallbackURL_ReturnsTrimmed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GCal.Webhook.CallbackURL = "  https://example.com/gcal/callback  "
	got := cfg.GCalWebhookCallbackURL()
	if got != "https://example.com/gcal/callback" {
		t.Fatalf("GCalWebhookCallbackURL() = %q, want %q", got, "https://example.com/gcal/callback")
	}
}

func TestGCalWebhookChannelIDPrefix_ReturnsTrimmed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GCal.Webhook.ChannelIDPrefix = "  alice-  "
	got := cfg.GCalWebhookChannelIDPrefix()
	if got != "alice-" {
		t.Fatalf("GCalWebhookChannelIDPrefix() = %q, want %q", got, "alice-")
	}
}

func TestGCalWebhookRequestedTTLSeconds_ReturnsValue(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GCal.Webhook.RequestedTTLSeconds = 3600
	got := cfg.GCalWebhookRequestedTTLSeconds()
	if got != 3600 {
		t.Fatalf("GCalWebhookRequestedTTLSeconds() = %d, want 3600", got)
	}
}

func TestGCalWebhookSecretEnvVar_ReturnsTrimmed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GCal.Webhook.SecretEnvVar = "  ALICE_GCAL_WEBHOOK_SECRET  "
	got := cfg.GCalWebhookSecretEnvVar()
	if got != "ALICE_GCAL_WEBHOOK_SECRET" {
		t.Fatalf("GCalWebhookSecretEnvVar() = %q, want %q", got, "ALICE_GCAL_WEBHOOK_SECRET")
	}
}

func TestGCalWebhookSecretFile_ReturnsEmpty(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GCal.Webhook.SecretFile = ""
	got := cfg.GCalWebhookSecretFile()
	if got != "" {
		t.Fatalf("GCalWebhookSecretFile() = %q, want empty", got)
	}
}

// --- GitHub webhook getter ---

func TestGitHubWebhookListenAddr_ReturnsTrimmed(t *testing.T) {
	cfg := minimalConfig()
	cfg.Connectors.GitHub.Webhook.ListenAddr = "  127.0.0.1:8788  "
	got := cfg.GitHubWebhookListenAddr()
	if got != "127.0.0.1:8788" {
		t.Fatalf("GitHubWebhookListenAddr() = %q, want %q", got, "127.0.0.1:8788")
	}
}

// --- shouldRetryConnectorError ---

// mockNetError satisfies net.Error for testing shouldRetryConnectorError.
type mockNetError struct{ timeout, temporary bool }

func (m *mockNetError) Error() string   { return "mock net error" }
func (m *mockNetError) Timeout() bool   { return m.timeout }
func (m *mockNetError) Temporary() bool { return m.temporary }

// Ensure mockNetError satisfies net.Error at compile time.
var _ net.Error = (*mockNetError)(nil)

func TestShouldRetryConnectorError_ContextCanceled(t *testing.T) {
	if shouldRetryConnectorError(context.Canceled) {
		t.Fatal("expected false for context.Canceled")
	}
}

func TestShouldRetryConnectorError_ContextDeadlineExceeded(t *testing.T) {
	if shouldRetryConnectorError(context.DeadlineExceeded) {
		t.Fatal("expected false for context.DeadlineExceeded")
	}
}

func TestShouldRetryConnectorError_PlainError(t *testing.T) {
	if shouldRetryConnectorError(errors.New("some plain error")) {
		t.Fatal("expected false for plain non-net error")
	}
}

func TestShouldRetryConnectorError_NetError(t *testing.T) {
	netErr := &mockNetError{timeout: true}
	if !shouldRetryConnectorError(netErr) {
		t.Fatal("expected true for net.Error")
	}
}

// --- connectorBackoff ---

func TestConnectorBackoff_ZeroAttempt(t *testing.T) {
	if d := connectorBackoff(0); d != 0 {
		t.Fatalf("expected 0 for attempt=0, got %v", d)
	}
	if d := connectorBackoff(-1); d != 0 {
		t.Fatalf("expected 0 for attempt=-1, got %v", d)
	}
}

func TestConnectorBackoff_HighAttempt(t *testing.T) {
	// With attempt=5 the loop doubles past connectorMaxBackoff and returns it.
	d := connectorBackoff(5)
	if d != connectorMaxBackoff {
		t.Fatalf("expected connectorMaxBackoff for attempt=5, got %v", d)
	}
}

// --- isCompletedGitHubState ---

func TestIsCompletedGitHubState(t *testing.T) {
	if !isCompletedGitHubState("closed") {
		t.Fatal("expected true for 'closed'")
	}
	if !isCompletedGitHubState("merged") {
		t.Fatal("expected true for 'merged'")
	}
	if isCompletedGitHubState("open") {
		t.Fatal("expected false for 'open'")
	}
}

// --- sensitivityRank ---

func TestSensitivityRank_AllValues(t *testing.T) {
	cases := []struct {
		in   core.Sensitivity
		want int
	}{
		{core.SensitivityRestricted, 4},
		{core.SensitivityHigh, 3},
		{core.SensitivityMedium, 2},
		{core.SensitivityLow, 1},
	}
	for _, tc := range cases {
		if got := sensitivityRank(tc.in); got != tc.want {
			t.Errorf("sensitivityRank(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// --- sourceReferencesForEvents deduplication ---

func TestSourceReferencesForEvents_DeduplicatesIdenticalEvents(t *testing.T) {
	now := time.Now()
	e := NormalizedEvent{
		SourceSystem: "github",
		SourceType:   "pull_request",
		SourceID:     "pr-1",
		ObservedAt:   now,
	}
	refs := sourceReferencesForEvents([]NormalizedEvent{e, e})
	if len(refs) != 1 {
		t.Fatalf("expected 1 deduplicated ref, got %d", len(refs))
	}
}
