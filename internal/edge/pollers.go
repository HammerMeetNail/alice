package edge

import (
	"context"
	"net/http"
	"strings"
	"time"

	"alice/internal/core"
)

// LivePollerOptions carries the shared knobs for the exported live pollers.
// Callers that want different HTTP client behaviour (timeout, transport) can
// pass their own; the default is a 10s client.
type LivePollerOptions struct {
	HTTPClient *http.Client
}

// GitHubLivePollerConfig fully parameterises a GitHub poller without pulling
// in the edge Config struct. Mirror the fields of newGitHubLiveSource so the
// tracker can drive the same polling code as the edge runtime.
type GitHubLivePollerConfig struct {
	APIBaseURL   string
	Token        string
	ActorLogin   string
	Repositories []GitHubRepositoryConfig
}

// GitHubLivePoller is the reusable shape of the internal gitHubLiveSource.
// It exists so the tracker (and any future consumer) can poll GitHub without
// having to synthesise an edge Config / CredentialStore pair.
type GitHubLivePoller struct {
	cfg     GitHubLivePollerConfig
	options LivePollerOptions
}

// NewGitHubLivePoller builds a poller from explicit parameters. The token is
// read from the config field — callers that keep the token in an env var or
// credential file should resolve it themselves before calling.
func NewGitHubLivePoller(cfg GitHubLivePollerConfig, opts LivePollerOptions) *GitHubLivePoller {
	return &GitHubLivePoller{cfg: cfg, options: opts}
}

// Poll returns normalized events for every configured repository. Transient
// HTTP errors bubble up; the caller decides whether to retry or fall through.
func (p *GitHubLivePoller) Poll(ctx context.Context) ([]NormalizedEvent, error) {
	source := &gitHubLiveSource{
		baseURL:      p.cfg.APIBaseURL,
		actorLogin:   strings.TrimSpace(p.cfg.ActorLogin),
		repositories: append([]GitHubRepositoryConfig(nil), p.cfg.Repositories...),
		httpClient:   p.options.httpClient(),
	}
	return source.pollWithToken(ctx, strings.TrimSpace(p.cfg.Token))
}

// JiraLivePollerConfig fully parameterises a Jira poller.
type JiraLivePollerConfig struct {
	APIBaseURL     string
	Token          string
	ActorAccountID string
	ActorEmail     string
	Projects       []JiraProjectConfig
	// Since, if non-zero, narrows the JQL "updated >" cursor for every project.
	Since time.Time
}

// JiraLivePoller mirrors the private jiraLiveSource for external callers.
type JiraLivePoller struct {
	cfg     JiraLivePollerConfig
	options LivePollerOptions
}

// NewJiraLivePoller constructs a Jira poller from explicit parameters.
func NewJiraLivePoller(cfg JiraLivePollerConfig, opts LivePollerOptions) *JiraLivePoller {
	return &JiraLivePoller{cfg: cfg, options: opts}
}

// Poll returns normalized events for every configured Jira project.
func (p *JiraLivePoller) Poll(ctx context.Context) ([]NormalizedEvent, error) {
	source := &jiraLiveSource{
		baseURL:        p.cfg.APIBaseURL,
		actorAccountID: strings.TrimSpace(p.cfg.ActorAccountID),
		actorEmail:     strings.TrimSpace(p.cfg.ActorEmail),
		projects:       append([]JiraProjectConfig(nil), p.cfg.Projects...),
		httpClient:     p.options.httpClient(),
	}
	return source.pollWithToken(ctx, strings.TrimSpace(p.cfg.Token), p.cfg.Since)
}

// GCalLivePollerConfig fully parameterises a Google Calendar poller.
type GCalLivePollerConfig struct {
	APIBaseURL string
	Token      string
	Calendars  []GCalCalendarConfig
	// Since, if non-zero, narrows the events query to events updated after
	// the given time. Callers typically leave this zero and let the
	// downstream dedup layer filter repeats.
	Since time.Time
}

// GCalLivePoller mirrors the private gcalLiveSource for external callers.
type GCalLivePoller struct {
	cfg     GCalLivePollerConfig
	options LivePollerOptions
}

// NewGCalLivePoller constructs a Google Calendar poller from explicit
// parameters.
func NewGCalLivePoller(cfg GCalLivePollerConfig, opts LivePollerOptions) *GCalLivePoller {
	return &GCalLivePoller{cfg: cfg, options: opts}
}

// Poll returns normalized events for every configured calendar.
func (p *GCalLivePoller) Poll(ctx context.Context) ([]NormalizedEvent, error) {
	source := &gcalLiveSource{
		baseURL:    p.cfg.APIBaseURL,
		calendars:  append([]GCalCalendarConfig(nil), p.cfg.Calendars...),
		httpClient: p.options.httpClient(),
	}
	return source.pollWithToken(ctx, strings.TrimSpace(p.cfg.Token), p.cfg.Since)
}

// DeriveArtifactsLive turns normalized events into core artifacts using the
// same derivation rules the edge runtime uses for its `active` state. No
// cross-system aggregate logic runs here — the caller (e.g. the tracker)
// owns dedup and supersedes chains, so stateful aggregate signals would
// double-fire.
func DeriveArtifactsLive(events []NormalizedEvent) []core.Artifact {
	artifacts := make([]core.Artifact, 0, len(events))
	for _, event := range events {
		switch {
		case event.SourceSystem == "github" && event.EventType == "pull_request":
			artifacts = append(artifacts, deriveGitHubArtifact(event))
		case event.SourceSystem == "jira" && event.EventType == "issue":
			artifacts = append(artifacts, deriveJiraArtifact(event))
		case event.SourceSystem == "gcal" && event.EventType == "event":
			artifacts = append(artifacts, deriveGCalArtifact(event))
		}
	}
	return artifacts
}

func (o LivePollerOptions) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}
