package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
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

func (s fixtureEventSource) Poll(ctx context.Context, state State, _ CredentialStore) ([]NormalizedEvent, error) {
	return s.load(ctx, state)
}

func loadConnectorArtifacts(ctx context.Context, cfg Config, state State, credentials CredentialStore) ([]core.Artifact, map[string]time.Time, error) {
	sources := configuredEventSources(cfg)
	events := make([]NormalizedEvent, 0)
	cursorUpdates := make(map[string]time.Time)
	for _, source := range sources {
		sourceEvents, err := source.Poll(ctx, state, credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("poll %s: %w", source.Name(), err)
		}
		freshEvents, sourceUpdates := filterEventsSinceCursors(state, sourceEvents)
		mergeCursorUpdates(cursorUpdates, sourceUpdates)
		events = append(events, freshEvents...)
	}
	return deriveArtifacts(events), cursorUpdates, nil
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
	artifacts = append(artifacts, deriveAggregateArtifacts(events)...)
	return artifacts
}

type projectEventGroup struct {
	GitHub []NormalizedEvent
	Jira   []NormalizedEvent
	GCal   []NormalizedEvent
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

func deriveAggregateArtifacts(events []NormalizedEvent) []core.Artifact {
	groups := groupEventsByProject(events)
	if len(groups) == 0 {
		return nil
	}

	projectRefs := make([]string, 0, len(groups))
	for projectRef := range groups {
		projectRefs = append(projectRefs, projectRef)
	}
	sort.Strings(projectRefs)

	artifacts := make([]core.Artifact, 0)
	for _, projectRef := range projectRefs {
		group := groups[projectRef]
		if len(group.GitHub) > 0 && len(group.Jira) > 0 {
			artifacts = append(artifacts, deriveProjectStatusDelta(projectRef, group))
		}
		if hasProjectBlocker(group) {
			artifacts = append(artifacts, deriveProjectBlocker(projectRef, group))
		}
		if len(group.GCal) > 0 && (len(group.GitHub) > 0 || len(group.Jira) > 0) {
			artifacts = append(artifacts, deriveProjectCommitment(projectRef, group))
		}
	}
	return artifacts
}

func groupEventsByProject(events []NormalizedEvent) map[string]projectEventGroup {
	groups := make(map[string]projectEventGroup)
	for _, event := range events {
		refs := dedupeStrings(event.ProjectRefs)
		for _, projectRef := range refs {
			if strings.TrimSpace(projectRef) == "" {
				continue
			}
			group := groups[projectRef]
			switch event.SourceSystem {
			case "github":
				group.GitHub = append(group.GitHub, event)
			case "jira":
				group.Jira = append(group.Jira, event)
			case "gcal":
				group.GCal = append(group.GCal, event)
			}
			groups[projectRef] = group
		}
	}
	return groups
}

func deriveProjectStatusDelta(projectRef string, group projectEventGroup) core.Artifact {
	gitHubEvent := latestEvent(group.GitHub)
	jiraEvent := latestEvent(group.Jira)

	issueKey := eventStringAttribute(jiraEvent, "issue_key", jiraEvent.SourceID)
	issueStatus := normalizeLabel(eventStringAttribute(jiraEvent, "status", "in_progress"), "in_progress")
	repository := eventStringAttribute(gitHubEvent, "repository", "unknown")
	pullRequestNumber := eventIntAttribute(gitHubEvent, "number", 0)
	reviewStatus := normalizeLabel(eventStringAttribute(gitHubEvent, "review_status", "in_progress"), "in_progress")

	content := fmt.Sprintf(
		"Project activity indicates %s is in %s while %s PR #%d remains active with review status %s.",
		issueKey,
		issueStatus,
		repository,
		pullRequestNumber,
		reviewStatus,
	)

	return core.Artifact{
		Type:    core.ArtifactTypeStatusDelta,
		Title:   fmt.Sprintf("Cross-system update for %s", projectRef),
		Content: content,
		StructuredPayload: map[string]any{
			"project_refs":           []string{projectRef},
			"source_systems":         []string{"github", "jira"},
			"issue_key":              issueKey,
			"repository":             repository,
			"pull_request_number":    pullRequestNumber,
			"review_status":          reviewStatus,
			"aggregated_event_count": len(group.GitHub) + len(group.Jira),
		},
		SourceRefs:     sourceReferencesForEvents(append(append([]NormalizedEvent{}, group.GitHub...), group.Jira...)),
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    maxSensitivityForEvents(group.GitHub, group.Jira),
		Confidence:     0.83,
	}
}

func deriveProjectBlocker(projectRef string, group projectEventGroup) core.Artifact {
	repository := eventStringAttribute(latestEvent(group.GitHub), "repository", "unknown")
	pullRequestNumber := eventIntAttribute(latestEvent(group.GitHub), "number", 0)
	issueKey := eventStringAttribute(latestEvent(group.Jira), "issue_key", "")
	content := fmt.Sprintf(
		"Project activity indicates a potential blocker on %s: review changes or blocked Jira status need follow-up before work can move forward.",
		projectRef,
	)

	return core.Artifact{
		Type:    core.ArtifactTypeBlocker,
		Title:   fmt.Sprintf("Potential blocker on %s", projectRef),
		Content: content,
		StructuredPayload: map[string]any{
			"project_refs":        []string{projectRef},
			"source_systems":      []string{"github", "jira"},
			"issue_key":           issueKey,
			"repository":          repository,
			"pull_request_number": pullRequestNumber,
		},
		SourceRefs:     sourceReferencesForEvents(append(append([]NormalizedEvent{}, group.GitHub...), group.Jira...)),
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    maxSensitivityForEvents(group.GitHub, group.Jira),
		Confidence:     0.72,
	}
}

func deriveProjectCommitment(projectRef string, group projectEventGroup) core.Artifact {
	calendarEvent := latestEvent(group.GCal)
	category := normalizeLabel(eventStringAttribute(calendarEvent, "category", "work"), "work")
	startAt := eventTimeAttribute(calendarEvent, "start_at", calendarEvent.ObservedAt)

	content := fmt.Sprintf(
		"Calendar activity indicates planned %s time for %s while related work signals remain active, suggesting near-term follow-up on this project.",
		category,
		projectRef,
	)

	return core.Artifact{
		Type:    core.ArtifactTypeCommitment,
		Title:   fmt.Sprintf("Planned follow-up for %s", projectRef),
		Content: content,
		StructuredPayload: map[string]any{
			"project_refs":           []string{projectRef},
			"source_systems":         []string{"gcal", "github", "jira"},
			"category":               category,
			"start_at":               startAt,
			"aggregated_event_count": len(group.GCal) + len(group.GitHub) + len(group.Jira),
		},
		SourceRefs:     sourceReferencesForEvents(append(append(append([]NormalizedEvent{}, group.GCal...), group.GitHub...), group.Jira...)),
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    maxSensitivityForEvents(group.GCal, group.GitHub, group.Jira),
		Confidence:     0.68,
	}
}

func hasProjectBlocker(group projectEventGroup) bool {
	if len(group.GitHub) == 0 || len(group.Jira) == 0 {
		return false
	}
	for _, event := range group.Jira {
		status := normalizeLabel(eventStringAttribute(event, "status", ""), "")
		if strings.Contains(status, "block") {
			return true
		}
	}
	for _, event := range group.GitHub {
		reviewStatus := normalizeLabel(eventStringAttribute(event, "review_status", ""), "")
		if reviewStatus == "changes_requested" {
			return true
		}
	}
	return false
}

func latestEvent(events []NormalizedEvent) NormalizedEvent {
	var latest NormalizedEvent
	for _, event := range events {
		if event.ObservedAt.After(latest.ObservedAt) {
			latest = event
		}
	}
	return latest
}

func sourceReferencesForEvents(eventGroups ...[]NormalizedEvent) []core.SourceReference {
	seen := make(map[string]struct{})
	refs := make([]core.SourceReference, 0)
	for _, events := range eventGroups {
		for _, event := range events {
			key := event.SourceSystem + "|" + event.SourceType + "|" + event.SourceID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, sourceReferenceForEvent(event))
		}
	}
	return refs
}

func maxSensitivityForEvents(eventGroups ...[]NormalizedEvent) core.Sensitivity {
	maxValue := core.SensitivityLow
	for _, events := range eventGroups {
		for _, event := range events {
			if sensitivityRank(event.Sensitivity) > sensitivityRank(maxValue) {
				maxValue = event.Sensitivity
			}
		}
	}
	return maxValue
}

func sensitivityRank(value core.Sensitivity) int {
	switch value {
	case core.SensitivityRestricted:
		return 4
	case core.SensitivityHigh:
		return 3
	case core.SensitivityMedium:
		return 2
	default:
		return 1
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

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
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
