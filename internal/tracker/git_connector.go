package tracker

import (
	"context"
	"log/slog"

	"alice/internal/core"
)

// gitConnector wraps the local git tracker logic behind the Connector
// interface. Each Poll reads every configured repo's state and asks the
// configured summariser to produce one artifact per repo. On summariser
// failure we log at WARN and fall back to the heuristic so an experimental
// summariser bug never blocks the silent-publish contract.
type gitConnector struct {
	repoPaths  []string
	summariser Summariser
	fallback   Summariser
}

func newGitConnector(repoPaths []string) *gitConnector {
	return newGitConnectorWithSummariser(repoPaths, NewHeuristicSummariser())
}

func newGitConnectorWithSummariser(repoPaths []string, summariser Summariser) *gitConnector {
	if summariser == nil {
		summariser = NewHeuristicSummariser()
	}
	return &gitConnector{
		repoPaths:  append([]string(nil), repoPaths...),
		summariser: summariser,
		fallback:   NewHeuristicSummariser(),
	}
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

		artifact, err := c.summariser.Summarise(ctx, state)
		if err != nil {
			slog.Warn("tracker: summariser failed, falling back to heuristic",
				"summariser", c.summariser.Name(), "repo", repoPath, "err", err)
			if c.summariser.Name() == c.fallback.Name() {
				// Nothing better to try; skip this repo for this tick.
				continue
			}
			artifact, err = c.fallback.Summarise(ctx, state)
			if err != nil {
				slog.Warn("tracker: heuristic fallback also failed", "repo", repoPath, "err", err)
				continue
			}
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}
