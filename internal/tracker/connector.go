package tracker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/edge"
)

// Connector is a single source of publishable artifacts. Each tracker tick
// asks every enabled connector for artifacts; the tracker handles dedup,
// supersedes chains, and publishing centrally, so connectors are free to
// return the same item every tick — the digest map filters out no-ops.
type Connector interface {
	// Name returns a short identifier used in logs and errors. It is not
	// required to be unique across deployments, but must be stable for a
	// single tracker instance so structured-log filtering works.
	Name() string
	// Poll returns the artifacts the connector currently believes describe
	// the user's work. Errors bubble up to the tracker loop, which logs
	// them at WARN and proceeds to the next connector.
	Poll(ctx context.Context) ([]core.Artifact, error)
}

// buildConnectorsFromEnv reads connector configuration from ALICE_TRACK_*
// environment variables and returns the resulting connector list. The list
// mirrors the order the user supplied in ALICE_TRACK_CONNECTORS so logs and
// audit events stay predictable.
//
// Backward compatibility: when ALICE_TRACK_CONNECTORS is unset but
// ALICE_TRACK_REPOS is set, the git connector is enabled alone — this
// preserves the pre-multi-connector behaviour without requiring the user to
// re-declare ALICE_TRACK_CONNECTORS=git.
func buildConnectorsFromEnv(cfg Config) ([]Connector, error) {
	requested := strings.TrimSpace(os.Getenv("ALICE_TRACK_CONNECTORS"))
	if requested == "" {
		if len(cfg.RepoPaths) > 0 {
			requested = "git"
		} else {
			return nil, nil
		}
	}

	connectors := make([]Connector, 0)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(requested, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		connector, err := buildConnectorByName(name, cfg)
		if err != nil {
			return nil, fmt.Errorf("configure %s connector: %w", name, err)
		}
		if connector == nil {
			// Connector disabled (missing required env vars). Log once at
			// startup; subsequent ticks will not log repeatedly.
			slog.Warn("tracker: connector skipped (required configuration missing)", "connector", name)
			continue
		}
		connectors = append(connectors, connector)
	}
	return connectors, nil
}

func buildConnectorByName(name string, cfg Config) (Connector, error) {
	switch name {
	case "git":
		if len(cfg.RepoPaths) == 0 {
			return nil, nil
		}
		return newGitConnector(cfg.RepoPaths), nil
	case "github":
		return newGitHubConnectorFromEnv()
	case "jira":
		return newJiraConnectorFromEnv()
	case "calendar", "gcal":
		return newCalendarConnectorFromEnv()
	default:
		return nil, fmt.Errorf("unknown connector %q (valid: git, github, jira, calendar)", name)
	}
}

// envCommaList parses a comma-separated environment variable into a trimmed
// list, dropping empty entries.
func envCommaList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// envOr returns the value of key when set, else fallback.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// assignObservedAt applies a fresh ObservedAt timestamp to every SourceRef
// whose own timestamp is zero. The edge derivation emits SourceRefs with
// ObservedAt already set to the event time, but we pass artifacts back
// through publish which expects a non-zero ObservedAt.
func assignObservedAt(artifacts []core.Artifact, now time.Time) {
	for i := range artifacts {
		for j := range artifacts[i].SourceRefs {
			if artifacts[i].SourceRefs[j].ObservedAt.IsZero() {
				artifacts[i].SourceRefs[j].ObservedAt = now
			}
		}
	}
}

// Expose the edge NormalizedEvent type under the tracker package so callers
// aren't forced to import both packages just to assemble fixtures in tests.
type normalizedEvent = edge.NormalizedEvent
