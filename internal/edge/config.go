package edge

import (
	"encoding/json"
	"fmt"
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
	ArtifactFixtureFile  string   `json:"artifact_fixture_file"`
	QueryWatchIDs        []string `json:"query_watch_ids"`
	PollIncomingRequests bool     `json:"poll_incoming_requests"`
}

type ConnectorsConfig struct {
	GitHub GitHubConnectorConfig `json:"github"`
	Jira   JiraConnectorConfig   `json:"jira"`
	GCal   GCalConnectorConfig   `json:"gcal"`
}

type GitHubConnectorConfig struct {
	Enabled      bool                     `json:"enabled"`
	FixtureFile  string                   `json:"fixture_file"`
	APIBaseURL   string                   `json:"api_base_url"`
	TokenEnvVar  string                   `json:"token_env_var"`
	ActorLogin   string                   `json:"actor_login"`
	Repositories []GitHubRepositoryConfig `json:"repositories"`
}

type GitHubRepositoryConfig struct {
	Name        string   `json:"name"`
	ProjectRefs []string `json:"project_refs"`
}

type JiraConnectorConfig struct {
	Enabled        bool                `json:"enabled"`
	FixtureFile    string              `json:"fixture_file"`
	APIBaseURL     string              `json:"api_base_url"`
	TokenEnvVar    string              `json:"token_env_var"`
	ActorAccountID string              `json:"actor_account_id"`
	ActorEmail     string              `json:"actor_email"`
	Projects       []JiraProjectConfig `json:"projects"`
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
	if c.GitHubLiveEnabled() {
		if strings.TrimSpace(c.Connectors.GitHub.APIBaseURL) == "" {
			c.Connectors.GitHub.APIBaseURL = "https://api.github.com"
		}
		if strings.TrimSpace(c.Connectors.GitHub.TokenEnvVar) == "" {
			c.Connectors.GitHub.TokenEnvVar = "ALICE_GITHUB_TOKEN"
		}
	}
	if c.JiraLiveEnabled() {
		if strings.TrimSpace(c.Connectors.Jira.TokenEnvVar) == "" {
			c.Connectors.Jira.TokenEnvVar = "ALICE_JIRA_TOKEN"
		}
		if strings.TrimSpace(c.Connectors.Jira.ActorEmail) == "" {
			c.Connectors.Jira.ActorEmail = c.Agent.OwnerEmail
		}
	}
	if c.GCalLiveEnabled() {
		if strings.TrimSpace(c.Connectors.GCal.APIBaseURL) == "" {
			c.Connectors.GCal.APIBaseURL = "https://www.googleapis.com/calendar/v3"
		}
		if strings.TrimSpace(c.Connectors.GCal.TokenEnvVar) == "" {
			c.Connectors.GCal.TokenEnvVar = "ALICE_GCAL_TOKEN"
		}
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
		if c.GitHubLiveEnabled() && len(c.Connectors.GitHub.Repositories) == 0 {
			return fmt.Errorf("connectors.github.repositories is required when github live polling is enabled")
		}
		for i, project := range c.Connectors.Jira.Projects {
			if strings.TrimSpace(project.Key) == "" {
				return fmt.Errorf("connectors.jira.projects[%d].key is required", i)
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
		for i, calendar := range c.Connectors.GCal.Calendars {
			if strings.TrimSpace(calendar.ID) == "" {
				return fmt.Errorf("connectors.gcal.calendars[%d].id is required", i)
			}
		}
		if c.GCalLiveEnabled() && len(c.Connectors.GCal.Calendars) == 0 {
			return fmt.Errorf("connectors.gcal.calendars is required when gcal live polling is enabled")
		}
		return nil
	}
}

func (c Config) StatePath() string {
	return c.resolvePath(c.Runtime.StateFile)
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

func (c Config) JiraAPIBaseURL() string {
	return strings.TrimSpace(c.Connectors.Jira.APIBaseURL)
}

func (c Config) JiraTokenEnvVar() string {
	return strings.TrimSpace(c.Connectors.Jira.TokenEnvVar)
}

func (c Config) GCalAPIBaseURL() string {
	return strings.TrimSpace(c.Connectors.GCal.APIBaseURL)
}

func (c Config) GCalTokenEnvVar() string {
	return strings.TrimSpace(c.Connectors.GCal.TokenEnvVar)
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
