package tracker

import (
	"context"
	"log/slog"

	"alice/internal/core"
)

// gitConnector wraps the local git tracker logic behind the Connector
// interface. Each Poll reads every configured repo's state and derives one
// status_delta artifact per repo.
type gitConnector struct {
	repoPaths []string
}

func newGitConnector(repoPaths []string) *gitConnector {
	return &gitConnector{repoPaths: append([]string(nil), repoPaths...)}
}

func (c *gitConnector) Name() string { return "git" }

func (c *gitConnector) Poll(ctx context.Context) ([]core.Artifact, error) {
	artifacts := make([]core.Artifact, 0, len(c.repoPaths))
	for _, repoPath := range c.repoPaths {
		state, err := ReadRepoState(ctx, repoPath)
		if err != nil {
			// One bad repo must not stop the rest; the tracker loop also
			// logs at WARN, so we keep the message tight here.
			slog.Warn("tracker: git connector failed to read repo", "repo", repoPath, "err", err)
			continue
		}
		artifacts = append(artifacts, DeriveArtifacts(state)...)
	}
	return artifacts, nil
}
