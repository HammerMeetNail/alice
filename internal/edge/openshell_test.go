package edge

import (
	"strings"
	"testing"
)

func TestGenerateOpenShellPolicyNoConnectors(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			BaseURL: "https://alice.example.com",
		},
	}

	policy := GenerateOpenShellPolicy(cfg)
	if !strings.Contains(policy, "alice_server:") {
		t.Fatalf("expected coordination server block in policy:\n%s", policy)
	}
	if strings.Contains(policy, "github_api:") {
		t.Fatalf("did not expect github live block in policy:\n%s", policy)
	}
	if strings.Contains(policy, "jira_api:") {
		t.Fatalf("did not expect jira block in policy:\n%s", policy)
	}
	if strings.Contains(policy, "google_calendar:") {
		t.Fatalf("did not expect gcal block in policy:\n%s", policy)
	}
}

func TestGenerateOpenShellPolicyWarnsForLoopbackServer(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			BaseURL: "http://127.0.0.1:8080",
		},
	}

	policy := GenerateOpenShellPolicy(cfg)
	if !strings.Contains(policy, "# WARNING: The coordination server URL resolves to a loopback address.") {
		t.Fatalf("expected loopback warning in policy:\n%s", policy)
	}
	if !strings.Contains(policy, "host: 127.0.0.1") {
		t.Fatalf("expected loopback host in policy:\n%s", policy)
	}
	if !strings.Contains(policy, "port: 8080") {
		t.Fatalf("expected loopback port in policy:\n%s", policy)
	}
}

func TestGenerateOpenShellPolicyGitHubLive(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			BaseURL: "https://alice.example.com",
		},
		Connectors: ConnectorsConfig{
			GitHub: GitHubConnectorConfig{
				Repositories: []GitHubRepositoryConfig{{
					Name: "acme/payments",
				}},
			},
		},
	}
	cfg.applyDefaults()

	policy := GenerateOpenShellPolicy(cfg)
	if !strings.Contains(policy, "github_api:") {
		t.Fatalf("expected github live block in policy:\n%s", policy)
	}
	if !strings.Contains(policy, "host: api.github.com") {
		t.Fatalf("expected github api host in policy:\n%s", policy)
	}
	if strings.Contains(policy, "github_oauth:") {
		t.Fatalf("did not expect github oauth block in live-only policy:\n%s", policy)
	}
}

func TestGenerateOpenShellPolicyGitHubOAuth(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			BaseURL: "https://alice.example.com",
		},
		Connectors: ConnectorsConfig{
			GitHub: GitHubConnectorConfig{
				OAuth: ConnectorOAuthConfig{
					Enabled:  true,
					ClientID: "github-client",
				},
			},
		},
	}
	cfg.applyDefaults()

	policy := GenerateOpenShellPolicy(cfg)
	if !strings.Contains(policy, "github_oauth:") {
		t.Fatalf("expected github oauth block in policy:\n%s", policy)
	}
	if count := strings.Count(policy, "host: github.com"); count != 1 {
		t.Fatalf("expected github oauth host once, got %d:\n%s", count, policy)
	}
}

func TestGenerateOpenShellPolicyJiraLiveAndOAuth(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			BaseURL: "https://alice.example.com",
		},
		Connectors: ConnectorsConfig{
			Jira: JiraConnectorConfig{
				Projects: []JiraProjectConfig{{
					Key: "PAY",
				}},
				APIBaseURL: "https://example.atlassian.net",
				OAuth: ConnectorOAuthConfig{
					Enabled:  true,
					ClientID: "jira-client",
				},
			},
		},
	}
	cfg.applyDefaults()

	policy := GenerateOpenShellPolicy(cfg)
	if !strings.Contains(policy, "jira_api:") {
		t.Fatalf("expected jira block in policy:\n%s", policy)
	}
	if !strings.Contains(policy, "host: example.atlassian.net") {
		t.Fatalf("expected jira tenant host in policy:\n%s", policy)
	}
	if count := strings.Count(policy, "host: auth.atlassian.com"); count != 1 {
		t.Fatalf("expected jira oauth host once, got %d:\n%s", count, policy)
	}
}

func TestGenerateOpenShellPolicyGCalLiveAndOAuth(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			BaseURL: "https://alice.example.com",
		},
		Connectors: ConnectorsConfig{
			GCal: GCalConnectorConfig{
				Calendars: []GCalCalendarConfig{{
					ID: "primary",
				}},
				OAuth: ConnectorOAuthConfig{
					Enabled:  true,
					ClientID: "gcal-client",
				},
			},
		},
	}
	cfg.applyDefaults()

	policy := GenerateOpenShellPolicy(cfg)
	if !strings.Contains(policy, "google_calendar:") {
		t.Fatalf("expected gcal block in policy:\n%s", policy)
	}
	for _, host := range []string{"www.googleapis.com", "accounts.google.com", "oauth2.googleapis.com"} {
		if !strings.Contains(policy, "host: "+host) {
			t.Fatalf("expected gcal host %q in policy:\n%s", host, policy)
		}
	}
}

func TestGenerateOpenShellPolicyAllConnectors(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{
			BaseURL: "https://alice.example.com",
		},
		Connectors: ConnectorsConfig{
			GitHub: GitHubConnectorConfig{
				Repositories: []GitHubRepositoryConfig{{
					Name: "acme/payments",
				}},
				OAuth: ConnectorOAuthConfig{
					Enabled:  true,
					ClientID: "github-client",
				},
			},
			Jira: JiraConnectorConfig{
				Projects: []JiraProjectConfig{{
					Key: "PAY",
				}},
				APIBaseURL: "https://example.atlassian.net",
				OAuth: ConnectorOAuthConfig{
					Enabled:  true,
					ClientID: "jira-client",
				},
			},
			GCal: GCalConnectorConfig{
				Calendars: []GCalCalendarConfig{{
					ID: "primary",
				}},
				OAuth: ConnectorOAuthConfig{
					Enabled:  true,
					ClientID: "gcal-client",
				},
			},
		},
	}
	cfg.applyDefaults()

	policy := GenerateOpenShellPolicy(cfg)
	for _, block := range []string{"alice_server:", "github_api:", "github_oauth:", "jira_api:", "google_calendar:"} {
		if !strings.Contains(policy, block) {
			t.Fatalf("expected block %q in policy:\n%s", block, policy)
		}
	}
	if !strings.Contains(policy, "ALICE_TRACK_SUMMARISER") {
		t.Fatalf("expected summariser note in policy:\n%s", policy)
	}
}

func TestParseURLForPolicy(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		host   string
		port   int
	}{
		{
			name:   "https default port",
			rawURL: "https://alice.example.com",
			host:   "alice.example.com",
			port:   443,
		},
		{
			name:   "http default port",
			rawURL: "http://alice.example.com",
			host:   "alice.example.com",
			port:   80,
		},
		{
			name:   "explicit port",
			rawURL: "https://alice.example.com:8443/path",
			host:   "alice.example.com",
			port:   8443,
		},
		{
			name:   "host without scheme",
			rawURL: "api.github.com",
			host:   "api.github.com",
			port:   443,
		},
		{
			name:   "host and port without scheme",
			rawURL: "api.github.com:8443",
			host:   "api.github.com",
			port:   8443,
		},
		{
			name:   "invalid url",
			rawURL: "://bad",
			host:   "",
			port:   443,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, port := parseURLForPolicy(tc.rawURL)
			if host != tc.host || port != tc.port {
				t.Fatalf("parseURLForPolicy(%q) = (%q, %d), want (%q, %d)", tc.rawURL, host, port, tc.host, tc.port)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "api.localhost", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "alice.example.com", want: false},
	}

	for _, tc := range tests {
		if got := isLoopbackHost(tc.host); got != tc.want {
			t.Fatalf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
