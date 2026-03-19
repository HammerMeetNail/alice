package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"alice/internal/core"
)

type gitHubFixtureFile struct {
	PullRequests []gitHubPullRequest `json:"pull_requests"`
}

type gitHubPullRequest struct {
	Repository   string    `json:"repository"`
	Number       int       `json:"number"`
	State        string    `json:"state"`
	ReviewStatus string    `json:"review_status"`
	UpdatedAt    time.Time `json:"updated_at"`
	ProjectRefs  []string  `json:"project_refs"`
}

type jiraFixtureFile struct {
	Issues []jiraIssue `json:"issues"`
}

type jiraIssue struct {
	Key         string    `json:"key"`
	IssueType   string    `json:"issue_type"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
	ProjectRefs []string  `json:"project_refs"`
}

type gcalFixtureFile struct {
	Events []gcalEvent `json:"events"`
}

type gcalEvent struct {
	EventID       string    `json:"event_id"`
	Category      string    `json:"category"`
	StartAt       time.Time `json:"start_at"`
	EndAt         time.Time `json:"end_at"`
	ProjectRefs   []string  `json:"project_refs"`
	AttendeeCount int       `json:"attendee_count"`
}

type fixtureEventSource struct {
	name string
	load func(context.Context, State) ([]NormalizedEvent, error)
}

func (s fixtureEventSource) Name() string {
	return s.name
}

func (s fixtureEventSource) Poll(ctx context.Context, state State) ([]NormalizedEvent, error) {
	return s.load(ctx, state)
}

func loadConnectorArtifacts(ctx context.Context, cfg Config, state State) ([]core.Artifact, map[string]time.Time, error) {
	sources := configuredEventSources(cfg)
	artifacts := make([]core.Artifact, 0)
	cursorUpdates := make(map[string]time.Time)
	for _, source := range sources {
		events, err := source.Poll(ctx, state)
		if err != nil {
			return nil, nil, fmt.Errorf("poll %s: %w", source.Name(), err)
		}
		freshEvents, sourceUpdates := filterEventsSinceCursors(state, events)
		mergeCursorUpdates(cursorUpdates, sourceUpdates)
		artifacts = append(artifacts, deriveArtifacts(freshEvents)...)
	}
	return artifacts, cursorUpdates, nil
}

func configuredEventSources(cfg Config) []EventSource {
	sources := make([]EventSource, 0)

	if path := cfg.GitHubFixturePath(); path != "" {
		sources = append(sources, fixtureEventSource{
			name: "github_fixture",
			load: func(context.Context, State) ([]NormalizedEvent, error) {
				return loadGitHubFixtureEvents(path)
			},
		})
	}
	if cfg.GitHubLiveEnabled() {
		sources = append(sources, newGitHubLiveSource(cfg))
	}
	if path := cfg.JiraFixturePath(); path != "" {
		sources = append(sources, fixtureEventSource{
			name: "jira_fixture",
			load: func(context.Context, State) ([]NormalizedEvent, error) {
				return loadJiraFixtureEvents(path)
			},
		})
	}
	if cfg.JiraLiveEnabled() {
		sources = append(sources, newJiraLiveSource(cfg))
	}
	if path := cfg.GCalFixturePath(); path != "" {
		sources = append(sources, fixtureEventSource{
			name: "gcal_fixture",
			load: func(context.Context, State) ([]NormalizedEvent, error) {
				return loadGCalFixtureEvents(path)
			},
		})
	}
	if cfg.GCalLiveEnabled() {
		sources = append(sources, newGCalLiveSource(cfg))
	}
	return sources
}

func loadGitHubFixtureEvents(path string) ([]NormalizedEvent, error) {
	fixtures, err := loadJSONFixture[gitHubFixtureFile](path)
	if err != nil {
		return nil, err
	}

	events := make([]NormalizedEvent, 0, len(fixtures.PullRequests))
	for _, pullRequest := range fixtures.PullRequests {
		events = append(events, normalizeFixtureGitHubPullRequest(pullRequest))
	}
	return events, nil
}

func loadJiraFixtureEvents(path string) ([]NormalizedEvent, error) {
	fixtures, err := loadJSONFixture[jiraFixtureFile](path)
	if err != nil {
		return nil, err
	}

	events := make([]NormalizedEvent, 0, len(fixtures.Issues))
	for _, issue := range fixtures.Issues {
		events = append(events, normalizeFixtureJiraIssue(issue))
	}
	return events, nil
}

func loadGCalFixtureEvents(path string) ([]NormalizedEvent, error) {
	fixtures, err := loadJSONFixture[gcalFixtureFile](path)
	if err != nil {
		return nil, err
	}

	events := make([]NormalizedEvent, 0, len(fixtures.Events))
	for _, calendarEvent := range fixtures.Events {
		events = append(events, normalizeFixtureGCalEvent(calendarEvent))
	}
	return events, nil
}

func normalizeFixtureGitHubPullRequest(pullRequest gitHubPullRequest) NormalizedEvent {
	observedAt := pullRequest.UpdatedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return NormalizedEvent{
		SourceSystem: "github",
		EventType:    "pull_request",
		SourceType:   "pull_request",
		SourceID:     fmt.Sprintf("repo:%s:pr:%d", pullRequest.Repository, pullRequest.Number),
		ObservedAt:   observedAt,
		ProjectRefs:  append([]string(nil), pullRequest.ProjectRefs...),
		TrustClass:   core.TrustClassStructuredSystem,
		Sensitivity:  core.SensitivityMedium,
		Attributes: map[string]any{
			"repository":    pullRequest.Repository,
			"number":        pullRequest.Number,
			"state":         normalizeLabel(pullRequest.State, "open"),
			"review_status": normalizeLabel(pullRequest.ReviewStatus, "in_progress"),
		},
	}
}

func normalizeFixtureJiraIssue(issue jiraIssue) NormalizedEvent {
	observedAt := issue.UpdatedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return NormalizedEvent{
		SourceSystem: "jira",
		EventType:    "issue",
		SourceType:   "issue",
		SourceID:     issue.Key,
		ObservedAt:   observedAt,
		ProjectRefs:  append([]string(nil), issue.ProjectRefs...),
		TrustClass:   core.TrustClassStructuredSystem,
		Sensitivity:  core.SensitivityMedium,
		Attributes: map[string]any{
			"issue_key":  issue.Key,
			"issue_type": normalizeLabel(issue.IssueType, "work item"),
			"status":     normalizeLabel(issue.Status, "in_progress"),
		},
	}
}

func normalizeFixtureGCalEvent(event gcalEvent) NormalizedEvent {
	observedAt := event.StartAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return NormalizedEvent{
		SourceSystem: "gcal",
		EventType:    "event",
		SourceType:   "event",
		SourceID:     event.EventID,
		ObservedAt:   observedAt,
		ProjectRefs:  append([]string(nil), event.ProjectRefs...),
		TrustClass:   core.TrustClassStructuredSystem,
		Sensitivity:  core.SensitivityLow,
		Attributes: map[string]any{
			"category":       normalizeLabel(event.Category, "work"),
			"attendee_count": event.AttendeeCount,
			"start_at":       event.StartAt,
			"end_at":         event.EndAt,
		},
	}
}

func deriveArtifacts(events []NormalizedEvent) []core.Artifact {
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

func deriveGitHubArtifact(event NormalizedEvent) core.Artifact {
	repository := eventStringAttribute(event, "repository", "unknown")
	reviewStatus := normalizeLabel(eventStringAttribute(event, "review_status", "in_progress"), "in_progress")
	state := normalizeLabel(eventStringAttribute(event, "state", "open"), "open")
	pullRequestNumber := eventIntAttribute(event, "number", 0)

	content := fmt.Sprintf(
		"GitHub activity indicates an active pull request in %s with state %s and review status %s.",
		repository,
		state,
		reviewStatus,
	)

	return core.Artifact{
		Type:    core.ArtifactTypeSummary,
		Title:   fmt.Sprintf("GitHub update in %s", repository),
		Content: content,
		StructuredPayload: map[string]any{
			"project_refs":        append([]string(nil), event.ProjectRefs...),
			"source_system":       event.SourceSystem,
			"repository":          repository,
			"pull_request_number": pullRequestNumber,
			"review_status":       reviewStatus,
		},
		SourceRefs:     []core.SourceReference{sourceReferenceForEvent(event)},
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    event.Sensitivity,
		Confidence:     0.82,
	}
}

func deriveJiraArtifact(event NormalizedEvent) core.Artifact {
	issueKey := eventStringAttribute(event, "issue_key", event.SourceID)
	issueType := normalizeLabel(eventStringAttribute(event, "issue_type", "work item"), "work item")
	status := normalizeLabel(eventStringAttribute(event, "status", "in_progress"), "in_progress")

	content := fmt.Sprintf(
		"Jira activity indicates %s %s is currently in %s.",
		issueType,
		issueKey,
		status,
	)

	return core.Artifact{
		Type:    core.ArtifactTypeSummary,
		Title:   fmt.Sprintf("Jira update for %s", issueKey),
		Content: content,
		StructuredPayload: map[string]any{
			"project_refs":  append([]string(nil), event.ProjectRefs...),
			"source_system": event.SourceSystem,
			"issue_key":     issueKey,
			"issue_type":    issueType,
			"status":        status,
		},
		SourceRefs:     []core.SourceReference{sourceReferenceForEvent(event)},
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    event.Sensitivity,
		Confidence:     0.8,
	}
}

func deriveGCalArtifact(event NormalizedEvent) core.Artifact {
	category := normalizeLabel(eventStringAttribute(event, "category", "work"), "work")
	attendeeCount := eventIntAttribute(event, "attendee_count", 0)
	startAt := eventTimeAttribute(event, "start_at", event.ObservedAt)
	endAt := eventTimeAttribute(event, "end_at", time.Time{})

	content := fmt.Sprintf(
		"Calendar activity indicates a %s block affecting current work, with %d participants scheduled.",
		category,
		attendeeCount,
	)

	return core.Artifact{
		Type:    core.ArtifactTypeStatusDelta,
		Title:   fmt.Sprintf("Calendar %s block", category),
		Content: content,
		StructuredPayload: map[string]any{
			"project_refs":   append([]string(nil), event.ProjectRefs...),
			"source_system":  event.SourceSystem,
			"category":       category,
			"attendee_count": attendeeCount,
			"start_at":       startAt,
			"end_at":         endAt,
		},
		SourceRefs:     []core.SourceReference{sourceReferenceForEvent(event)},
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    event.Sensitivity,
		Confidence:     0.72,
	}
}

func sourceReferenceForEvent(event NormalizedEvent) core.SourceReference {
	return core.SourceReference{
		SourceSystem: event.SourceSystem,
		SourceType:   event.SourceType,
		SourceID:     event.SourceID,
		ObservedAt:   event.ObservedAt,
		TrustClass:   event.TrustClass,
		Sensitivity:  event.Sensitivity,
	}
}

func filterEventsSinceCursors(state State, events []NormalizedEvent) ([]NormalizedEvent, map[string]time.Time) {
	filtered := make([]NormalizedEvent, 0, len(events))
	cursorUpdates := make(map[string]time.Time)
	for _, event := range events {
		if strings.TrimSpace(event.CursorKey) != "" {
			existing := state.CursorTime(event.CursorKey)
			if !existing.IsZero() && !event.ObservedAt.After(existing) {
				continue
			}
			if current := cursorUpdates[event.CursorKey]; event.ObservedAt.After(current) {
				cursorUpdates[event.CursorKey] = event.ObservedAt
			}
		}
		filtered = append(filtered, event)
	}
	return filtered, cursorUpdates
}

func mergeCursorUpdates(target map[string]time.Time, updates map[string]time.Time) {
	for key, value := range updates {
		if value.After(target[key]) {
			target[key] = value
		}
	}
}

func eventStringAttribute(event NormalizedEvent, key, fallback string) string {
	raw, ok := event.Attributes[key]
	if !ok {
		return fallback
	}

	value, ok := raw.(string)
	if !ok {
		return fallback
	}
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func eventIntAttribute(event NormalizedEvent, key string, fallback int) int {
	raw, ok := event.Attributes[key]
	if !ok {
		return fallback
	}

	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func eventTimeAttribute(event NormalizedEvent, key string, fallback time.Time) time.Time {
	raw, ok := event.Attributes[key]
	if !ok {
		return fallback
	}

	switch value := raw.(type) {
	case time.Time:
		return value
	case string:
		parsed, err := time.Parse(time.RFC3339, value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func loadJSONFixture[T any](path string) (T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("read fixture file: %w", err)
	}

	var payload T
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		var zero T
		return zero, fmt.Errorf("decode fixture file: %w", err)
	}
	return payload, nil
}

func normalizeLabel(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return strings.ToLower(trimmed)
}
