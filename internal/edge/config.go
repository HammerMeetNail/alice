package edge

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Agent      AgentConfig      `json:"agent"`
	Server     ServerConfig     `json:"server"`
	Runtime    RuntimeConfig    `json:"runtime"`
	Connectors ConnectorsConfig `json:"connectors"`

	configDir string
}

type AgentConfig struct {
	OrgSlug      string   `json:"org_slug"`
	OwnerEmail   string   `json:"owner_email"`
	AgentName    string   `json:"agent_name"`
	ClientType   string   `json:"client_type"`
	Capabilities []string `json:"capabilities"`
}

type ServerConfig struct {
	BaseURL string `json:"base_url"`
}

type RuntimeConfig struct {
	StateFile            string   `json:"state_file"`
	CredentialsFile      string   `json:"credentials_file"`
	CredentialsKeyEnvVar string   `json:"credentials_key_env_var"`
	CredentialsKeyFile   string   `json:"credentials_key_file"`
	ArtifactFixtureFile  string   `json:"artifact_fixture_file"`
	QueryWatchIDs        []string `json:"query_watch_ids"`
	PollIncomingRequests bool     `json:"poll_incoming_requests"`
}

type ConnectorsConfig struct {
	GitHub GitHubConnectorConfig `json:"github"`
	Jira   JiraConnectorConfig   `json:"jira"`
	GCal   GCalConnectorConfig   `json:"gcal"`
}

type ConnectorOAuthConfig struct {
	Enabled            bool              `json:"enabled"`
	ClientID           string            `json:"client_id"`
	ClientSecretEnvVar string            `json:"client_secret_env_var"`
	ClientSecretFile   string            `json:"client_secret_file"`
	AuthorizationURL   string            `json:"authorization_url"`
	TokenURL           string            `json:"token_url"`
	CallbackURL        string            `json:"callback_url"`
	Scopes             []string          `json:"scopes"`
	ExtraAuthParams    map[string]string `json:"extra_auth_params"`
	ExtraTokenParams   map[string]string `json:"extra_token_params"`
}

type GitHubWebhookConfig struct {
	Enabled      bool                     `json:"enabled"`
	ListenAddr   string                   `json:"listen_addr"`
	SecretEnvVar string                   `json:"secret_env_var"`
	SecretFile   string                   `json:"secret_file"`
	Repositories []GitHubRepositoryConfig `json:"repositories"`
}

type JiraWebhookConfig struct {
	Enabled      bool                `json:"enabled"`
	ListenAddr   string              `json:"listen_addr"`
	SecretEnvVar string              `json:"secret_env_var"`
	SecretFile   string              `json:"secret_file"`
	Projects     []JiraProjectConfig `json:"projects"`
}

type GitHubConnectorConfig struct {
	Enabled      bool                     `json:"enabled"`
	FixtureFile  string                   `json:"fixture_file"`
	APIBaseURL   string                   `json:"api_base_url"`
	TokenEnvVar  string                   `json:"token_env_var"`
	TokenFile    string                   `json:"token_file"`
	OAuth        ConnectorOAuthConfig     `json:"oauth"`
	Webhook      GitHubWebhookConfig      `json:"webhook"`
	ActorLogin   string                   `json:"actor_login"`
	Repositories []GitHubRepositoryConfig `json:"repositories"`
}

type GitHubRepositoryConfig struct {
	Name        string   `json:"name"`
	ProjectRefs []string `json:"project_refs"`
}

type JiraConnectorConfig struct {
	Enabled        bool                 `json:"enabled"`
	FixtureFile    string               `json:"fixture_file"`
	APIBaseURL     string               `json:"api_base_url"`
	TokenEnvVar    string               `json:"token_env_var"`
	TokenFile      string               `json:"token_file"`
	OAuth          ConnectorOAuthConfig `json:"oauth"`
	Webhook        JiraWebhookConfig    `json:"webhook"`
	ActorAccountID string               `json:"actor_account_id"`
	ActorEmail     string               `json:"actor_email"`
	Projects       []JiraProjectConfig  `json:"projects"`
}

type JiraProjectConfig struct {
	Key         string   `json:"key"`
	ProjectRefs []string `json:"project_refs"`
}

type GCalConnectorConfig struct {
	Enabled     bool                 `json:"enabled"`
	FixtureFile string               `json:"fixture_file"`
	APIBaseURL  string               `json:"api_base_url"`
	TokenEnvVar string               `json:"token_env_var"`
	TokenFile   string               `json:"token_file"`
	OAuth       ConnectorOAuthConfig `json:"oauth"`
	Calendars   []GCalCalendarConfig `json:"calendars"`
}

type GCalCalendarConfig struct {
	ID          string   `json:"id"`
	ProjectRefs []string `json:"project_refs"`
	Category    string   `json:"category"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	cfg.configDir = filepath.Dir(path)
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.Agent.ClientType) == "" {
		c.Agent.ClientType = "edge_agent"
	}
	if len(c.Agent.Capabilities) == 0 {
		c.Agent.Capabilities = []string{"publish_artifact", "respond_query", "request_approval"}
	}
	if strings.TrimSpace(c.Runtime.CredentialsKeyEnvVar) == "" {
		c.Runtime.CredentialsKeyEnvVar = "ALICE_EDGE_CREDENTIAL_KEY"
	}
	if c.GitHubLiveEnabled() {
		if strings.TrimSpace(c.Connectors.GitHub.APIBaseURL) == "" {
			c.Connectors.GitHub.APIBaseURL = "https://api.github.com"
		}
		if strings.TrimSpace(c.Connectors.GitHub.TokenEnvVar) == "" {
			c.Connectors.GitHub.TokenEnvVar = "ALICE_GITHUB_TOKEN"
		}
	}
	if c.GitHubWebhookEnabled() {
		if strings.TrimSpace(c.Connectors.GitHub.Webhook.ListenAddr) == "" {
			c.Connectors.GitHub.Webhook.ListenAddr = "127.0.0.1:8788"
		}
		if strings.TrimSpace(c.Connectors.GitHub.Webhook.SecretEnvVar) == "" {
			c.Connectors.GitHub.Webhook.SecretEnvVar = "ALICE_GITHUB_WEBHOOK_SECRET"
		}
	}
	if c.GitHubOAuthEnabled() {
		c.applyOAuthDefaults(
			&c.Connectors.GitHub.OAuth,
			"github",
			"https://github.com/login/oauth/authorize",
			"https://github.com/login/oauth/access_token",
			"ALICE_GITHUB_CLIENT_SECRET",
			[]string{"repo", "read:user"},
			nil,
		)
	}
	if c.JiraLiveEnabled() {
		if strings.TrimSpace(c.Connectors.Jira.TokenEnvVar) == "" {
			c.Connectors.Jira.TokenEnvVar = "ALICE_JIRA_TOKEN"
		}
		if strings.TrimSpace(c.Connectors.Jira.ActorEmail) == "" {
			c.Connectors.Jira.ActorEmail = c.Agent.OwnerEmail
		}
	}
	if c.JiraWebhookEnabled() {
		if strings.TrimSpace(c.Connectors.Jira.Webhook.ListenAddr) == "" {
			c.Connectors.Jira.Webhook.ListenAddr = "127.0.0.1:8789"
		}
		if strings.TrimSpace(c.Connectors.Jira.Webhook.SecretEnvVar) == "" {
			c.Connectors.Jira.Webhook.SecretEnvVar = "ALICE_JIRA_WEBHOOK_SECRET"
		}
		if strings.TrimSpace(c.Connectors.Jira.ActorEmail) == "" {
			c.Connectors.Jira.ActorEmail = c.Agent.OwnerEmail
		}
	}
	if c.JiraOAuthEnabled() {
		c.applyOAuthDefaults(
			&c.Connectors.Jira.OAuth,
			"jira",
			"https://auth.atlassian.com/authorize",
			"https://auth.atlassian.com/oauth/token",
			"ALICE_JIRA_CLIENT_SECRET",
			[]string{"read:jira-user", "read:jira-work", "offline_access"},
			map[string]string{
				"audience": "api.atlassian.com",
				"prompt":   "consent",
			},
		)
	}
	if c.GCalLiveEnabled() {
		if strings.TrimSpace(c.Connectors.GCal.APIBaseURL) == "" {
			c.Connectors.GCal.APIBaseURL = "https://www.googleapis.com/calendar/v3"
		}
		if strings.TrimSpace(c.Connectors.GCal.TokenEnvVar) == "" {
			c.Connectors.GCal.TokenEnvVar = "ALICE_GCAL_TOKEN"
		}
	}
	if c.GCalOAuthEnabled() {
		c.applyOAuthDefaults(
			&c.Connectors.GCal.OAuth,
			"gcal",
			"https://accounts.google.com/o/oauth2/v2/auth",
			"https://oauth2.googleapis.com/token",
			"ALICE_GCAL_CLIENT_SECRET",
			[]string{"https://www.googleapis.com/auth/calendar.readonly"},
			map[string]string{
				"access_type":            "offline",
				"include_granted_scopes": "true",
				"prompt":                 "consent",
			},
		)
	}
}

func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.Agent.OrgSlug) == "":
		return fmt.Errorf("agent.org_slug is required")
	case strings.TrimSpace(c.Agent.OwnerEmail) == "":
		return fmt.Errorf("agent.owner_email is required")
	case strings.TrimSpace(c.Agent.AgentName) == "":
		return fmt.Errorf("agent.agent_name is required")
	case strings.TrimSpace(c.Server.BaseURL) == "":
		return fmt.Errorf("server.base_url is required")
	case strings.TrimSpace(c.Runtime.StateFile) == "":
		return fmt.Errorf("runtime.state_file is required")
	default:
		for i, repo := range c.Connectors.GitHub.Repositories {
			if strings.TrimSpace(repo.Name) == "" {
				return fmt.Errorf("connectors.github.repositories[%d].name is required", i)
			}
		}
		for i, repo := range c.Connectors.GitHub.Webhook.Repositories {
			if strings.TrimSpace(repo.Name) == "" {
				return fmt.Errorf("connectors.github.webhook.repositories[%d].name is required", i)
			}
		}
		if c.GitHubLiveEnabled() && len(c.Connectors.GitHub.Repositories) == 0 {
			return fmt.Errorf("connectors.github.repositories is required when github live polling is enabled")
		}
		if c.GitHubWebhookEnabled() {
			if _, err := net.ResolveTCPAddr("tcp", strings.TrimSpace(c.Connectors.GitHub.Webhook.ListenAddr)); err != nil {
				return fmt.Errorf("connectors.github.webhook.listen_addr is invalid: %w", err)
			}
			if strings.TrimSpace(c.Connectors.GitHub.Webhook.SecretEnvVar) == "" && strings.TrimSpace(c.Connectors.GitHub.Webhook.SecretFile) == "" {
				return fmt.Errorf("connectors.github.webhook.secret_env_var or connectors.github.webhook.secret_file is required when github webhook intake is enabled")
			}
		}
		for i, project := range c.Connectors.Jira.Projects {
			if strings.TrimSpace(project.Key) == "" {
				return fmt.Errorf("connectors.jira.projects[%d].key is required", i)
			}
		}
		for i, project := range c.Connectors.Jira.Webhook.Projects {
			if strings.TrimSpace(project.Key) == "" {
				return fmt.Errorf("connectors.jira.webhook.projects[%d].key is required", i)
			}
		}
		if c.JiraLiveEnabled() {
			if strings.TrimSpace(c.Connectors.Jira.APIBaseURL) == "" {
				return fmt.Errorf("connectors.jira.api_base_url is required when jira live polling is enabled")
			}
			if len(c.Connectors.Jira.Projects) == 0 {
				return fmt.Errorf("connectors.jira.projects is required when jira live polling is enabled")
			}
		}
		if c.JiraWebhookEnabled() {
			if _, err := net.ResolveTCPAddr("tcp", strings.TrimSpace(c.Connectors.Jira.Webhook.ListenAddr)); err != nil {
				return fmt.Errorf("connectors.jira.webhook.listen_addr is invalid: %w", err)
			}
			if strings.TrimSpace(c.Connectors.Jira.Webhook.SecretEnvVar) == "" && strings.TrimSpace(c.Connectors.Jira.Webhook.SecretFile) == "" {
				return fmt.Errorf("connectors.jira.webhook.secret_env_var or connectors.jira.webhook.secret_file is required when jira webhook intake is enabled")
			}
		}
		for i, calendar := range c.Connectors.GCal.Calendars {
			if strings.TrimSpace(calendar.ID) == "" {
				return fmt.Errorf("connectors.gcal.calendars[%d].id is required", i)
			}
		}
		if c.GCalLiveEnabled() && len(c.Connectors.GCal.Calendars) == 0 {
			return fmt.Errorf("connectors.gcal.calendars is required when gcal live polling is enabled")
		}
		if err := validateOAuthConfig("connectors.github.oauth", c.Connectors.GitHub.OAuth, c.GitHubOAuthEnabled()); err != nil {
			return err
		}
		if err := validateOAuthConfig("connectors.jira.oauth", c.Connectors.Jira.OAuth, c.JiraOAuthEnabled()); err != nil {
			return err
		}
		if err := validateOAuthConfig("connectors.gcal.oauth", c.Connectors.GCal.OAuth, c.GCalOAuthEnabled()); err != nil {
			return err
		}
		return nil
	}
}

func (c Config) StatePath() string {
	return c.resolvePath(c.Runtime.StateFile)
}

func (c Config) CredentialsPath() string {
	if strings.TrimSpace(c.Runtime.CredentialsFile) != "" {
		return c.resolvePath(c.Runtime.CredentialsFile)
	}

	statePath := c.StatePath()
	ext := filepath.Ext(statePath)
	if ext == "" {
		return statePath + ".credentials"
	}
	return strings.TrimSuffix(statePath, ext) + ".credentials" + ext
}

func (c Config) CredentialsKeyEnvVar() string {
	return strings.TrimSpace(c.Runtime.CredentialsKeyEnvVar)
}

func (c Config) CredentialsKeyFile() string {
	return c.resolvePath(c.Runtime.CredentialsKeyFile)
}

func (c Config) ArtifactFixturePath() string {
	if strings.TrimSpace(c.Runtime.ArtifactFixtureFile) == "" {
		return ""
	}
	return c.resolvePath(c.Runtime.ArtifactFixtureFile)
}

func (c Config) GitHubFixturePath() string {
	return c.resolveConnectorPath(c.Connectors.GitHub.FixtureFile)
}

func (c Config) JiraFixturePath() string {
	return c.resolveConnectorPath(c.Connectors.Jira.FixtureFile)
}

func (c Config) GCalFixturePath() string {
	return c.resolveConnectorPath(c.Connectors.GCal.FixtureFile)
}

func (c Config) GitHubLiveEnabled() bool {
	return c.Connectors.GitHub.Enabled || len(c.Connectors.GitHub.Repositories) > 0
}

func (c Config) JiraLiveEnabled() bool {
	return c.Connectors.Jira.Enabled || len(c.Connectors.Jira.Projects) > 0
}

func (c Config) GCalLiveEnabled() bool {
	return c.Connectors.GCal.Enabled || len(c.Connectors.GCal.Calendars) > 0
}

func (c Config) GitHubAPIBaseURL() string {
	return strings.TrimSpace(c.Connectors.GitHub.APIBaseURL)
}

func (c Config) GitHubTokenEnvVar() string {
	return strings.TrimSpace(c.Connectors.GitHub.TokenEnvVar)
}

func (c Config) GitHubTokenFile() string {
	return c.resolveConnectorPath(c.Connectors.GitHub.TokenFile)
}

func (c Config) GitHubOAuthEnabled() bool {
	return oauthConfigEnabled(c.Connectors.GitHub.OAuth)
}

func (c Config) GitHubOAuthConfig() ConnectorOAuthConfig {
	return c.resolveOAuthConfig(c.Connectors.GitHub.OAuth)
}

func (c Config) GitHubWebhookEnabled() bool {
	return c.Connectors.GitHub.Webhook.Enabled
}

func (c Config) GitHubWebhookListenAddr() string {
	return strings.TrimSpace(c.Connectors.GitHub.Webhook.ListenAddr)
}

func (c Config) GitHubWebhookSecretEnvVar() string {
	return strings.TrimSpace(c.Connectors.GitHub.Webhook.SecretEnvVar)
}

func (c Config) GitHubWebhookSecretFile() string {
	return c.resolveConnectorPath(c.Connectors.GitHub.Webhook.SecretFile)
}

func (c Config) GitHubWebhookRepositories() []GitHubRepositoryConfig {
	return append([]GitHubRepositoryConfig(nil), c.Connectors.GitHub.Webhook.Repositories...)
}

func (c Config) JiraAPIBaseURL() string {
	return strings.TrimSpace(c.Connectors.Jira.APIBaseURL)
}

func (c Config) JiraTokenEnvVar() string {
	return strings.TrimSpace(c.Connectors.Jira.TokenEnvVar)
}

func (c Config) JiraTokenFile() string {
	return c.resolveConnectorPath(c.Connectors.Jira.TokenFile)
}

func (c Config) JiraOAuthEnabled() bool {
	return oauthConfigEnabled(c.Connectors.Jira.OAuth)
}

func (c Config) JiraOAuthConfig() ConnectorOAuthConfig {
	return c.resolveOAuthConfig(c.Connectors.Jira.OAuth)
}

func (c Config) JiraWebhookEnabled() bool {
	return c.Connectors.Jira.Webhook.Enabled
}

func (c Config) JiraWebhookListenAddr() string {
	return strings.TrimSpace(c.Connectors.Jira.Webhook.ListenAddr)
}

func (c Config) JiraWebhookSecretEnvVar() string {
	return strings.TrimSpace(c.Connectors.Jira.Webhook.SecretEnvVar)
}

func (c Config) JiraWebhookSecretFile() string {
	return c.resolveConnectorPath(c.Connectors.Jira.Webhook.SecretFile)
}

func (c Config) JiraWebhookProjects() []JiraProjectConfig {
	if len(c.Connectors.Jira.Webhook.Projects) > 0 {
		return append([]JiraProjectConfig(nil), c.Connectors.Jira.Webhook.Projects...)
	}
	return append([]JiraProjectConfig(nil), c.Connectors.Jira.Projects...)
}

func (c Config) GCalAPIBaseURL() string {
	return strings.TrimSpace(c.Connectors.GCal.APIBaseURL)
}

func (c Config) GCalTokenEnvVar() string {
	return strings.TrimSpace(c.Connectors.GCal.TokenEnvVar)
}

func (c Config) GCalTokenFile() string {
	return c.resolveConnectorPath(c.Connectors.GCal.TokenFile)
}

func (c Config) GCalOAuthEnabled() bool {
	return oauthConfigEnabled(c.Connectors.GCal.OAuth)
}

func (c Config) GCalOAuthConfig() ConnectorOAuthConfig {
	return c.resolveOAuthConfig(c.Connectors.GCal.OAuth)
}

func (c Config) resolvePath(value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Clean(filepath.Join(c.configDir, value))
}

func (c Config) resolveConnectorPath(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return c.resolvePath(value)
}

func (c *Config) applyOAuthDefaults(target *ConnectorOAuthConfig, connectorType, defaultAuthURL, defaultTokenURL, defaultSecretEnvVar string, defaultScopes []string, defaultAuthParams map[string]string) {
	if strings.TrimSpace(target.AuthorizationURL) == "" {
		target.AuthorizationURL = defaultAuthURL
	}
	if strings.TrimSpace(target.TokenURL) == "" {
		target.TokenURL = defaultTokenURL
	}
	if strings.TrimSpace(target.CallbackURL) == "" {
		target.CallbackURL = fmt.Sprintf("http://127.0.0.1:8787/oauth/%s/callback", connectorType)
	}
	if strings.TrimSpace(target.ClientSecretEnvVar) == "" {
		target.ClientSecretEnvVar = defaultSecretEnvVar
	}
	if len(target.Scopes) == 0 {
		target.Scopes = append([]string(nil), defaultScopes...)
	}
	if len(defaultAuthParams) > 0 {
		if target.ExtraAuthParams == nil {
			target.ExtraAuthParams = map[string]string{}
		}
		for key, value := range defaultAuthParams {
			if strings.TrimSpace(target.ExtraAuthParams[key]) == "" {
				target.ExtraAuthParams[key] = value
			}
		}
	}
}

func (c Config) resolveOAuthConfig(cfg ConnectorOAuthConfig) ConnectorOAuthConfig {
	cfg.ClientSecretFile = c.resolveConnectorPath(cfg.ClientSecretFile)
	return cfg
}

func oauthConfigEnabled(cfg ConnectorOAuthConfig) bool {
	return cfg.Enabled ||
		strings.TrimSpace(cfg.ClientID) != "" ||
		strings.TrimSpace(cfg.AuthorizationURL) != "" ||
		strings.TrimSpace(cfg.TokenURL) != ""
}

func validateOAuthConfig(label string, cfg ConnectorOAuthConfig, enabled bool) error {
	if !enabled {
		return nil
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return fmt.Errorf("%s.client_id is required when connector oauth is enabled", label)
	}
	if err := validateURLField(label+".authorization_url", cfg.AuthorizationURL, false); err != nil {
		return err
	}
	if err := validateURLField(label+".token_url", cfg.TokenURL, false); err != nil {
		return err
	}
	if err := validateURLField(label+".callback_url", cfg.CallbackURL, true); err != nil {
		return err
	}
	return nil
}

func validateURLField(label, value string, requireLoopback bool) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", label, err)
	}
	if requireLoopback && parsed.Scheme != "http" {
		return fmt.Errorf("%s must use http for local loopback callbacks", label)
	}
	if !requireLoopback && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", label)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("%s host is required", label)
	}
	if requireLoopback && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("%s must target localhost or a loopback address", label)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
