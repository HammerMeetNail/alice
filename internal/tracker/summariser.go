package tracker

import (
	"context"
	"fmt"
	"os"
	"strings"

	"alice/internal/core"
)

// Summariser turns a RepoState snapshot into a teammate-visible artifact.
// It exists as a seam so a future LLM-backed implementation (or any other
// derivation strategy) can slot in without touching the tracker loop.
//
// Implementations must be pure: the same input yields the same artifact,
// network or model calls notwithstanding. They must never emit raw commit
// subjects, branch names, or file paths into fields used for routing or
// permission checks — those belong in `Content` / `StructuredPayload`
// where they are treated as untrusted data downstream.
type Summariser interface {
	// Name returns a short identifier used in logs and env-var selection.
	Name() string
	// Summarise produces one artifact for the given repo state. Returning
	// an error tells the tracker to fall back to the heuristic path and
	// record the failure at WARN.
	Summarise(ctx context.Context, state RepoState) (core.Artifact, error)
}

// HeuristicSummariser is the default, deterministic derivation baked into
// the MCP tracker since its first cut. It is kept as a named type so the
// registry can return it and so future summarisers can delegate to it when
// they need to fall back (e.g. on LLM timeout).
type HeuristicSummariser struct{}

// NewHeuristicSummariser constructs the default summariser.
func NewHeuristicSummariser() *HeuristicSummariser {
	return &HeuristicSummariser{}
}

// Name implements Summariser.
func (*HeuristicSummariser) Name() string { return "heuristic" }

// Summarise delegates to the existing deriveGitStatusArtifact helper.
// Wrapping it behind the interface avoids duplicating the heuristic rules
// while giving callers a stable entry point that a future summariser can
// shadow.
func (*HeuristicSummariser) Summarise(_ context.Context, state RepoState) (core.Artifact, error) {
	return deriveGitStatusArtifact(state), nil
}

// SelectSummariserFromEnv returns the summariser named by the
// ALICE_TRACK_SUMMARISER environment variable. An unset value (or the
// explicit "heuristic") returns the default. Unknown names return an error
// at startup so misconfiguration fails loudly instead of silently running
// the heuristic.
//
// The "claude" value is reserved for a future LLM-backed summariser and
// currently returns an error; we register the name now so env-var plumbing
// is stable for deployments that want to adopt the feature as soon as it
// lands.
func SelectSummariserFromEnv() (Summariser, error) {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("ALICE_TRACK_SUMMARISER")))
	switch name {
	case "", "heuristic":
		return NewHeuristicSummariser(), nil
	case "claude":
		return nil, fmt.Errorf("ALICE_TRACK_SUMMARISER=claude is reserved for a future LLM-backed summariser and is not yet implemented")
	default:
		return nil, fmt.Errorf("ALICE_TRACK_SUMMARISER=%q is not a valid summariser (valid: heuristic)", name)
	}
}
