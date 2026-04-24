package tracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"alice/internal/core"
)

// PublishFunc publishes an artifact and returns the server response.
type PublishFunc func(ctx context.Context, body map[string]any) (map[string]any, error)

// Config holds tracker configuration.
type Config struct {
	RepoPaths  []string
	Interval   time.Duration
	StatePath  string
	OrgSlug    string
	OwnerEmail string
	AgentName  string
	ClientType string
	DryRun     bool
}

// ConfigFromEnv reads tracker configuration from environment variables.
// Returns the config and whether tracking is enabled.
func ConfigFromEnv() (Config, bool) {
	repos := strings.TrimSpace(os.Getenv("ALICE_TRACK_REPOS"))
	if repos == "" {
		return Config{}, false
	}

	paths := make([]string, 0)
	for _, p := range strings.Split(repos, ",") {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	if len(paths) == 0 {
		return Config{}, false
	}

	interval := 5 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("ALICE_TRACK_INTERVAL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			interval = parsed
		}
	}

	return Config{
		RepoPaths:  paths,
		Interval:   interval,
		StatePath:  strings.TrimSpace(os.Getenv("ALICE_TRACK_STATE_FILE")),
		OrgSlug:    strings.TrimSpace(os.Getenv("ALICE_TRACK_ORG_SLUG")),
		OwnerEmail: strings.TrimSpace(os.Getenv("ALICE_TRACK_OWNER_EMAIL")),
		AgentName:  strings.TrimSpace(os.Getenv("ALICE_TRACK_AGENT_NAME")),
		ClientType: "mcp_tracker",
		DryRun:     strings.TrimSpace(os.Getenv("ALICE_TRACK_DRY_RUN")) == "true",
	}, true
}

// Tracker reads local git state and periodically publishes status artifacts.
type Tracker struct {
	cfg        Config
	publish    PublishFunc
	register   func(ctx context.Context) error
	hasSession func() bool
	connectors []Connector
	published  map[string]string // digest -> artifact ID
	latest     map[string]string // derivation_key -> artifact ID
}

// New creates a Tracker, loading persisted state if a state file is configured.
// When no connectors are explicitly provided, the tracker falls back to the
// configured git repo paths; this preserves the pre-multi-connector default.
func New(cfg Config, publish PublishFunc, register func(ctx context.Context) error, hasSession func() bool) *Tracker {
	connectors, err := buildConnectorsFromEnv(cfg)
	if err != nil {
		slog.Error("tracker: connector configuration rejected, continuing with git-only", "err", err)
	}
	if len(connectors) == 0 && len(cfg.RepoPaths) > 0 {
		connectors = []Connector{newGitConnector(cfg.RepoPaths)}
	}

	state := loadTrackerState(cfg.StatePath)
	return &Tracker{
		cfg:        cfg,
		publish:    publish,
		register:   register,
		hasSession: hasSession,
		connectors: connectors,
		published:  state.Published,
		latest:     state.Latest,
	}
}

// WithConnectors replaces the auto-built connector list with an explicit
// set. Used by tests to inject fakes without standing up real pollers.
func (t *Tracker) WithConnectors(connectors []Connector) *Tracker {
	t.connectors = append([]Connector(nil), connectors...)
	return t
}

// Run starts the tracking loop, blocking until ctx is cancelled.
func (t *Tracker) Run(ctx context.Context) {
	names := make([]string, 0, len(t.connectors))
	for _, c := range t.connectors {
		names = append(names, c.Name())
	}
	if t.cfg.DryRun {
		slog.Info("tracker dry-run: will log artifacts that would be published without sending them to the server",
			"connectors", names, "repos", t.cfg.RepoPaths, "interval", t.cfg.Interval)
	} else {
		slog.Info("tracker started", "connectors", names, "repos", t.cfg.RepoPaths, "interval", t.cfg.Interval)
	}

	t.Tick(ctx)

	ticker := time.NewTicker(t.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("tracker stopped")
			return
		case <-ticker.C:
			t.Tick(ctx)
		}
	}
}

func (t *Tracker) Tick(ctx context.Context) {
	if !t.hasSession() {
		if err := t.register(ctx); err != nil {
			slog.Warn("tracker: registration failed, skipping tick", "err", err)
			return
		}
	}

	dirty := false
	for _, connector := range t.connectors {
		artifacts, err := connector.Poll(ctx)
		if err != nil {
			slog.Warn("tracker: connector poll failed", "connector", connector.Name(), "err", err)
			continue
		}

		for _, artifact := range artifacts {
			digest, err := artifactContentDigest(artifact)
			if err != nil {
				slog.Warn("tracker: digest failed", "connector", connector.Name(), "err", err)
				continue
			}
			if _, seen := t.published[digest]; seen {
				continue
			}

			derivationKey := derivationKeyFromArtifact(artifact)
			if derivationKey != "" {
				if prev, ok := t.latest[derivationKey]; ok && prev != "" {
					artifact.SupersedesArtifactID = &prev
				}
			}

			// In dry-run mode log what would be published and skip the
			// actual publish call so no data leaves the local machine.
			if t.cfg.DryRun {
				slog.Info("tracker dry-run: would publish",
					"connector", connector.Name(),
					"type", artifact.Type,
					"title", artifact.Title,
					"sensitivity", artifact.Sensitivity,
					"digest", digest)
				continue
			}

			body := map[string]any{"artifact": artifactToMap(artifact)}
			resp, err := t.publish(ctx, body)
			if err != nil {
				slog.Warn("tracker: publish failed", "connector", connector.Name(), "err", err)
				continue
			}

			if artifactID, ok := resp["artifact_id"].(string); ok {
				t.published[digest] = artifactID
				if derivationKey != "" {
					t.latest[derivationKey] = artifactID
				}
				dirty = true
				slog.Info("tracker: published", "connector", connector.Name(), "artifact_id", artifactID)
			}
		}
	}

	if dirty {
		saveTrackerState(t.cfg.StatePath, trackerState{
			Published: t.published,
			Latest:    t.latest,
		})
	}
}

func artifactContentDigest(artifact core.Artifact) (string, error) {
	a := artifact
	a.SupersedesArtifactID = nil
	a.SourceRefs = nil // exclude time-varying ObservedAt from digest
	data, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("marshal artifact for digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func derivationKeyFromArtifact(artifact core.Artifact) string {
	if artifact.StructuredPayload == nil {
		return ""
	}
	raw, ok := artifact.StructuredPayload["derivation_key"]
	if !ok {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func artifactToMap(artifact core.Artifact) map[string]any {
	data, err := json.Marshal(artifact)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	return m
}
