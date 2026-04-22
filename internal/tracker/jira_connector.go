package tracker

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/edge"
)

// jiraProjectKeyRe matches valid Jira project keys — one uppercase letter
// followed by one or more uppercase letters, digits, or underscores.
// Mirrors the regex in internal/edge/config.go so tracker-sourced project
// keys are validated identically.
var jiraProjectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

// jiraConnector adapts the edge package's live Jira poller.
type jiraConnector struct {
	poller *edge.JiraLivePoller
}

func newJiraConnectorFromEnv() (Connector, error) {
	token := strings.TrimSpace(envOr("ALICE_TRACK_JIRA_TOKEN", ""))
	if token == "" {
		return nil, nil
	}
	baseURL := strings.TrimSpace(envOr("ALICE_TRACK_JIRA_BASE_URL", ""))
	if baseURL == "" {
		return nil, fmt.Errorf("ALICE_TRACK_JIRA_BASE_URL must be set when the jira connector is enabled")
	}
	projects := envCommaList("ALICE_TRACK_JIRA_PROJECTS")
	if len(projects) == 0 {
		return nil, fmt.Errorf("ALICE_TRACK_JIRA_PROJECTS must name at least one project key when the jira connector is enabled")
	}

	projectCfgs := make([]edge.JiraProjectConfig, 0, len(projects))
	for _, key := range projects {
		upper := strings.ToUpper(key)
		if !jiraProjectKeyRe.MatchString(upper) {
			return nil, fmt.Errorf("ALICE_TRACK_JIRA_PROJECTS entry %q is not a valid project key", key)
		}
		projectCfgs = append(projectCfgs, edge.JiraProjectConfig{Key: upper})
	}

	poller := edge.NewJiraLivePoller(edge.JiraLivePollerConfig{
		APIBaseURL:     baseURL,
		Token:          token,
		ActorAccountID: envOr("ALICE_TRACK_JIRA_ACCOUNT_ID", ""),
		ActorEmail:     envOr("ALICE_TRACK_JIRA_EMAIL", ""),
		Projects:       projectCfgs,
	}, edge.LivePollerOptions{})

	return &jiraConnector{poller: poller}, nil
}

func (c *jiraConnector) Name() string { return "jira" }

func (c *jiraConnector) Poll(ctx context.Context) ([]core.Artifact, error) {
	events, err := c.poller.Poll(ctx)
	if err != nil {
		return nil, err
	}
	artifacts := edge.DeriveArtifactsLive(events)
	assignObservedAt(artifacts, time.Now().UTC())
	return artifacts, nil
}
