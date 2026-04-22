package tracker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/edge"
)

// gitHubConnector adapts the edge package's live GitHub poller so the MCP
// tracker can run it inside its own process. All configuration is sourced
// from ALICE_TRACK_GITHUB_* environment variables; we intentionally do not
// share the edge runtime's JSON config format so the two sides can evolve
// independently.
type gitHubConnector struct {
	poller *edge.GitHubLivePoller
}

func newGitHubConnectorFromEnv() (Connector, error) {
	token := strings.TrimSpace(envOr("ALICE_TRACK_GITHUB_TOKEN", ""))
	if token == "" {
		return nil, nil
	}
	repos := envCommaList("ALICE_TRACK_GITHUB_REPOS")
	if len(repos) == 0 {
		return nil, fmt.Errorf("ALICE_TRACK_GITHUB_REPOS must name at least one owner/repo when the github connector is enabled")
	}

	repoCfgs := make([]edge.GitHubRepositoryConfig, 0, len(repos))
	for _, r := range repos {
		if !strings.Contains(r, "/") {
			return nil, fmt.Errorf("ALICE_TRACK_GITHUB_REPOS entry %q must be in owner/repo form", r)
		}
		repoCfgs = append(repoCfgs, edge.GitHubRepositoryConfig{Name: r})
	}

	apiURL := envOr("ALICE_TRACK_GITHUB_API_URL", "https://api.github.com")
	actor := envOr("ALICE_TRACK_GITHUB_USER", "")

	poller := edge.NewGitHubLivePoller(edge.GitHubLivePollerConfig{
		APIBaseURL:   apiURL,
		Token:        token,
		ActorLogin:   actor,
		Repositories: repoCfgs,
	}, edge.LivePollerOptions{})

	return &gitHubConnector{poller: poller}, nil
}

func (c *gitHubConnector) Name() string { return "github" }

func (c *gitHubConnector) Poll(ctx context.Context) ([]core.Artifact, error) {
	events, err := c.poller.Poll(ctx)
	if err != nil {
		return nil, err
	}
	artifacts := edge.DeriveArtifactsLive(events)
	assignObservedAt(artifacts, time.Now().UTC())
	return artifacts, nil
}
