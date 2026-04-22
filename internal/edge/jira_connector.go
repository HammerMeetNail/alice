package edge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"alice/internal/core"
)

type jiraLiveSource struct {
	baseURL        string
	tokenEnvVar    string
	tokenFile      string
	actorAccountID string
	actorEmail     string
	projects       []JiraProjectConfig
	httpClient     *http.Client
}

type jiraSearchResponse struct {
	StartAt    int                 `json:"startAt"`
	MaxResults int                 `json:"maxResults"`
	Total      int                 `json:"total"`
	Issues     []jiraIssueResponse `json:"issues"`
}

type jiraIssueResponse struct {
	Key    string                  `json:"key"`
	Fields jiraIssueFieldsResponse `json:"fields"`
}

type jiraIssueFieldsResponse struct {
	IssueType jiraNamedField       `json:"issuetype"`
	Status    jiraNamedField       `json:"status"`
	Updated   time.Time            `json:"updated"`
	Assignee  jiraAssigneeResponse `json:"assignee"`
}

type jiraNamedField struct {
	Name string `json:"name"`
}

type jiraAssigneeResponse struct {
	AccountID    string `json:"accountId"`
	EmailAddress string `json:"emailAddress"`
	DisplayName  string `json:"displayName"`
}

func newJiraLiveSource(cfg Config) *jiraLiveSource {
	return &jiraLiveSource{
		baseURL:        cfg.JiraAPIBaseURL(),
		tokenEnvVar:    cfg.JiraTokenEnvVar(),
		tokenFile:      cfg.JiraTokenFile(),
		actorAccountID: strings.TrimSpace(cfg.Connectors.Jira.ActorAccountID),
		actorEmail:     strings.TrimSpace(cfg.Connectors.Jira.ActorEmail),
		projects:       append([]JiraProjectConfig(nil), cfg.Connectors.Jira.Projects...),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (s *jiraLiveSource) Name() string {
	return "jira_live"
}

func (s *jiraLiveSource) Poll(ctx context.Context, state State, credentials CredentialStore) ([]NormalizedEvent, error) {
	token, err := loadConnectorSecret("jira", s.tokenEnvVar, s.tokenFile, credentials.ConnectorCredential("jira"))
	if err != nil {
		return nil, err
	}

	events := make([]NormalizedEvent, 0)
	for _, project := range s.projects {
		issues, err := s.listIssues(ctx, token, project, state.CursorTime(jiraCursorKey(project.Key)))
		if err != nil {
			return nil, fmt.Errorf("list issues for %s: %w", project.Key, err)
		}
		for _, issue := range issues {
			if !s.isRelevantIssue(issue) {
				continue
			}
			events = append(events, normalizeLiveJiraIssue(project, issue))
		}
	}
	return events, nil
}

// pollWithToken accepts a caller-resolved token and an explicit `since`
// cursor, letting reusers drive the poller without synthesising a State /
// CredentialStore pair.
func (s *jiraLiveSource) pollWithToken(ctx context.Context, token string, since time.Time) ([]NormalizedEvent, error) {
	events := make([]NormalizedEvent, 0)
	for _, project := range s.projects {
		issues, err := s.listIssues(ctx, token, project, since)
		if err != nil {
			return nil, fmt.Errorf("list issues for %s: %w", project.Key, err)
		}
		for _, issue := range issues {
			if !s.isRelevantIssue(issue) {
				continue
			}
			events = append(events, normalizeLiveJiraIssue(project, issue))
		}
	}
	return events, nil
}

func (s *jiraLiveSource) listIssues(ctx context.Context, token string, project JiraProjectConfig, cursor time.Time) ([]jiraIssueResponse, error) {
	const pageSize = 50

	base, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse jira api base url: %w", err)
	}
	base.Path = path.Join("/", base.Path, "rest/api/3/search")

	issues := make([]jiraIssueResponse, 0)
	for startAt := 0; ; {
		payload, _, err := doConnectorJSON[jiraSearchResponse](ctx, s.httpClient, "jira", func() (*http.Request, error) {
			requestURL := *base
			query := requestURL.Query()
			query.Set("fields", "issuetype,status,assignee,updated")
			query.Set("maxResults", strconv.Itoa(pageSize))
			query.Set("startAt", strconv.Itoa(startAt))
			query.Set("jql", buildJiraJQL(project.Key, cursor))
			requestURL.RawQuery = query.Encode()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			return req, nil
		})
		if err != nil {
			return nil, err
		}

		issues = append(issues, payload.Issues...)
		if len(payload.Issues) == 0 {
			break
		}

		resolvedPageSize := payload.MaxResults
		if resolvedPageSize <= 0 {
			resolvedPageSize = pageSize
		}
		nextStartAt := payload.StartAt + len(payload.Issues)
		if payload.Total > 0 && nextStartAt >= payload.Total {
			break
		}
		if len(payload.Issues) < resolvedPageSize {
			break
		}
		startAt = nextStartAt
	}

	return issues, nil
}

func (s *jiraLiveSource) isRelevantIssue(issue jiraIssueResponse) bool {
	if strings.TrimSpace(s.actorAccountID) != "" && strings.TrimSpace(issue.Fields.Assignee.AccountID) == strings.TrimSpace(s.actorAccountID) {
		return true
	}
	if sameEmail(issue.Fields.Assignee.EmailAddress, s.actorEmail) {
		return true
	}
	if strings.TrimSpace(s.actorAccountID) == "" && strings.TrimSpace(s.actorEmail) == "" {
		return true
	}
	return false
}

func normalizeLiveJiraIssue(project JiraProjectConfig, issue jiraIssueResponse) NormalizedEvent {
	observedAt := issue.Fields.Updated
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return NormalizedEvent{
		SourceSystem: "jira",
		EventType:    "issue",
		SourceType:   "issue",
		SourceID:     issue.Key,
		ObservedAt:   observedAt,
		CursorKey:    jiraCursorKey(project.Key),
		ProjectRefs:  projectRefsForJiraProject(project),
		TrustClass:   core.TrustClassStructuredSystem,
		Sensitivity:  core.SensitivityMedium,
		Attributes: map[string]any{
			"issue_key":  issue.Key,
			"issue_type": normalizeLabel(issue.Fields.IssueType.Name, "work item"),
			"status":     normalizeLabel(issue.Fields.Status.Name, "in_progress"),
		},
	}
}

func buildJiraJQL(projectKey string, cursor time.Time) string {
	jql := fmt.Sprintf(`project = "%s"`, strings.TrimSpace(projectKey))
	if !cursor.IsZero() {
		jql += fmt.Sprintf(` AND updated > "%s"`, cursor.UTC().Format("2006-01-02 15:04"))
	}
	jql += " ORDER BY updated DESC"
	return jql
}

func projectRefsForJiraProject(project JiraProjectConfig) []string {
	if len(project.ProjectRefs) > 0 {
		return append([]string(nil), project.ProjectRefs...)
	}
	if strings.TrimSpace(project.Key) == "" {
		return nil
	}
	return []string{strings.ToLower(strings.TrimSpace(project.Key))}
}

func findJiraProjectForIssue(projects []JiraProjectConfig, issueKey string) (JiraProjectConfig, bool) {
	trimmedIssueKey := strings.TrimSpace(issueKey)
	for _, project := range projects {
		prefix := strings.TrimSpace(project.Key)
		if prefix == "" {
			continue
		}
		if strings.EqualFold(trimmedIssueKey, prefix) || strings.HasPrefix(strings.ToUpper(trimmedIssueKey), strings.ToUpper(prefix)+"-") {
			return project, true
		}
	}
	return JiraProjectConfig{}, false
}

func jiraCursorKey(projectKey string) string {
	return "jira:project:" + strings.TrimSpace(projectKey)
}

func sameEmail(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}
