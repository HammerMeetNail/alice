package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"alice/internal/core"
)

type gitHubLiveSource struct {
	baseURL      string
	tokenEnvVar  string
	actorLogin   string
	repositories []GitHubRepositoryConfig
	httpClient   *http.Client
}

type gitHubPullResponse struct {
	Number             int                  `json:"number"`
	State              string               `json:"state"`
	Draft              bool                 `json:"draft"`
	Title              string               `json:"title"`
	UpdatedAt          time.Time            `json:"updated_at"`
	User               gitHubUserResponse   `json:"user"`
	RequestedReviewers []gitHubUserResponse `json:"requested_reviewers"`
	Assignees          []gitHubUserResponse `json:"assignees"`
}

type gitHubUserResponse struct {
	Login string `json:"login"`
}

func newGitHubLiveSource(cfg Config) EventSource {
	return &gitHubLiveSource{
		baseURL:      cfg.GitHubAPIBaseURL(),
		tokenEnvVar:  cfg.GitHubTokenEnvVar(),
		actorLogin:   resolveGitHubActorLogin(cfg.Agent.OwnerEmail, cfg.Connectors.GitHub.ActorLogin),
		repositories: append([]GitHubRepositoryConfig(nil), cfg.Connectors.GitHub.Repositories...),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (s *gitHubLiveSource) Name() string {
	return "github_live"
}

func (s *gitHubLiveSource) Poll(ctx context.Context) ([]NormalizedEvent, error) {
	token := strings.TrimSpace(os.Getenv(s.tokenEnvVar))
	if token == "" {
		return nil, fmt.Errorf("github connector requires %s", s.tokenEnvVar)
	}

	events := make([]NormalizedEvent, 0)
	for _, repository := range s.repositories {
		pullRequests, err := s.listPullRequests(ctx, token, repository.Name)
		if err != nil {
			return nil, fmt.Errorf("list pull requests for %s: %w", repository.Name, err)
		}
		for _, pullRequest := range pullRequests {
			if !s.isRelevantPullRequest(pullRequest) {
				continue
			}
			events = append(events, normalizeLiveGitHubPullRequest(repository, pullRequest, s.actorLogin))
		}
	}
	return events, nil
}

func (s *gitHubLiveSource) listPullRequests(ctx context.Context, token, repository string) ([]gitHubPullResponse, error) {
	repositoryPath, err := gitHubRepositoryAPIPath(repository)
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse github api base url: %w", err)
	}
	base.Path = path.Join("/", base.Path, repositoryPath)

	query := base.Query()
	query.Set("state", "open")
	query.Set("sort", "updated")
	query.Set("direction", "desc")
	query.Set("per_page", "50")
	base.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var payload []gitHubPullResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode github response: %w", err)
	}
	return payload, nil
}

func (s *gitHubLiveSource) isRelevantPullRequest(pullRequest gitHubPullResponse) bool {
	if strings.TrimSpace(s.actorLogin) == "" {
		return true
	}
	if sameLogin(pullRequest.User.Login, s.actorLogin) {
		return true
	}
	if containsGitHubUser(pullRequest.RequestedReviewers, s.actorLogin) {
		return true
	}
	if containsGitHubUser(pullRequest.Assignees, s.actorLogin) {
		return true
	}
	return false
}

func normalizeLiveGitHubPullRequest(repository GitHubRepositoryConfig, pullRequest gitHubPullResponse, actorLogin string) NormalizedEvent {
	observedAt := pullRequest.UpdatedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return NormalizedEvent{
		SourceSystem: "github",
		EventType:    "pull_request",
		SourceType:   "pull_request",
		SourceID:     fmt.Sprintf("repo:%s:pr:%d", repository.Name, pullRequest.Number),
		ObservedAt:   observedAt,
		ProjectRefs:  projectRefsForRepository(repository),
		TrustClass:   core.TrustClassStructuredSystem,
		Sensitivity:  core.SensitivityMedium,
		Attributes: map[string]any{
			"repository":    repository.Name,
			"number":        pullRequest.Number,
			"state":         normalizeLabel(pullRequest.State, "open"),
			"review_status": deriveLiveGitHubReviewStatus(pullRequest, actorLogin),
		},
	}
}

func deriveLiveGitHubReviewStatus(pullRequest gitHubPullResponse, actorLogin string) string {
	switch {
	case pullRequest.Draft:
		return "draft"
	case containsGitHubUser(pullRequest.RequestedReviewers, actorLogin):
		return "review_requested"
	case sameLogin(pullRequest.User.Login, actorLogin):
		return "in_progress"
	case containsGitHubUser(pullRequest.Assignees, actorLogin):
		return "assigned"
	default:
		return "open"
	}
}

func resolveGitHubActorLogin(ownerEmail, configuredLogin string) string {
	if strings.TrimSpace(configuredLogin) != "" {
		return strings.TrimSpace(configuredLogin)
	}
	localPart, _, found := strings.Cut(strings.TrimSpace(ownerEmail), "@")
	if !found {
		return strings.TrimSpace(ownerEmail)
	}
	return strings.TrimSpace(localPart)
}

func projectRefsForRepository(repository GitHubRepositoryConfig) []string {
	if len(repository.ProjectRefs) > 0 {
		return append([]string(nil), repository.ProjectRefs...)
	}
	_, repoName, found := strings.Cut(strings.TrimSpace(repository.Name), "/")
	if found && strings.TrimSpace(repoName) != "" {
		return []string{strings.TrimSpace(repoName)}
	}
	if strings.TrimSpace(repository.Name) != "" {
		return []string{strings.TrimSpace(repository.Name)}
	}
	return nil
}

func gitHubRepositoryAPIPath(repository string) (string, error) {
	owner, repo, found := strings.Cut(strings.TrimSpace(repository), "/")
	if !found || owner == "" || repo == "" {
		return "", fmt.Errorf("github repository %q must be in owner/repo form", repository)
	}
	return path.Join("repos", owner, repo, "pulls"), nil
}

func containsGitHubUser(users []gitHubUserResponse, actorLogin string) bool {
	for _, user := range users {
		if sameLogin(user.Login, actorLogin) {
			return true
		}
	}
	return false
}

func sameLogin(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}
