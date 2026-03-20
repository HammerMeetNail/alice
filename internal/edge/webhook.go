package edge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const GitHubWebhookPath = "/webhooks/github"
const JiraWebhookPath = "/webhooks/jira"
const GCalWebhookPath = "/webhooks/gcal"
const webhookReplayRetention = 7 * 24 * time.Hour

type WebhookDeliveryResult struct {
	ConnectorType      string              `json:"connector_type"`
	EventType          string              `json:"event_type"`
	Action             string              `json:"action,omitempty"`
	Accepted           bool                `json:"accepted"`
	Message            string              `json:"message,omitempty"`
	PublishedArtifacts []PublishedArtifact `json:"published_artifacts,omitempty"`
	SkippedDigests     []string            `json:"skipped_digests,omitempty"`
}

type webhookUnauthorizedError struct {
	reason string
}

func (e *webhookUnauthorizedError) Error() string {
	return e.reason
}

type webhookBadRequestError struct {
	reason string
}

func (e *webhookBadRequestError) Error() string {
	return e.reason
}

type gitHubWebhookRepository struct {
	FullName string `json:"full_name"`
}

type gitHubWebhookPullRequestPayload struct {
	Action      string                  `json:"action"`
	Repository  gitHubWebhookRepository `json:"repository"`
	PullRequest gitHubPullResponse      `json:"pull_request"`
}

type jiraWebhookIssuePayload struct {
	WebhookEvent string            `json:"webhookEvent"`
	Issue        jiraIssueResponse `json:"issue"`
}

type webhookReplayGuard struct {
	deliveryKey    string
	sequenceKey    string
	sequenceNumber int64
}

func (r *Runtime) GitHubWebhookHandler() (http.Handler, error) {
	if !r.cfg.GitHubWebhookEnabled() {
		return nil, fmt.Errorf("github webhook intake is not configured")
	}

	secret, err := loadOptionalSecret("github webhook", r.cfg.GitHubWebhookSecretEnvVar(), r.cfg.GitHubWebhookSecretFile())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("github webhook secret is required")
	}

	source := newGitHubLiveSource(r.cfg)
	repositories := r.cfg.GitHubWebhookRepositories()
	if len(repositories) == 0 {
		repositories = append([]GitHubRepositoryConfig(nil), r.cfg.Connectors.GitHub.Repositories...)
	}

	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc(GitHubWebhookPath, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		if err != nil {
			http.Error(w, "read webhook body", http.StatusBadRequest)
			return
		}

		mu.Lock()
		result, handleErr := r.handleGitHubWebhook(req.Context(), source, repositories, secret, req.Header, body)
		mu.Unlock()

		statusCode := http.StatusOK
		switch {
		case handleErr == nil && !result.Accepted:
			statusCode = http.StatusAccepted
		case handleErr == nil:
			statusCode = http.StatusOK
		default:
			var unauthorizedErr *webhookUnauthorizedError
			var badRequestErr *webhookBadRequestError
			switch {
			case errors.As(handleErr, &unauthorizedErr):
				statusCode = http.StatusUnauthorized
			case errors.As(handleErr, &badRequestErr):
				statusCode = http.StatusBadRequest
			default:
				statusCode = http.StatusInternalServerError
			}
			if strings.TrimSpace(result.Message) == "" {
				result.Message = handleErr.Error()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(result)
	})
	return mux, nil
}

func (r *Runtime) JiraWebhookHandler() (http.Handler, error) {
	if !r.cfg.JiraWebhookEnabled() {
		return nil, fmt.Errorf("jira webhook intake is not configured")
	}

	secret, err := loadOptionalSecret("jira webhook", r.cfg.JiraWebhookSecretEnvVar(), r.cfg.JiraWebhookSecretFile())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("jira webhook secret is required")
	}

	source := newJiraLiveSource(r.cfg)
	projects := r.cfg.JiraWebhookProjects()

	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc(JiraWebhookPath, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
		if err != nil {
			http.Error(w, "read webhook body", http.StatusBadRequest)
			return
		}

		mu.Lock()
		result, handleErr := r.handleJiraWebhook(req.Context(), source, projects, secret, req.Header, body)
		mu.Unlock()

		statusCode := http.StatusOK
		switch {
		case handleErr == nil && !result.Accepted:
			statusCode = http.StatusAccepted
		case handleErr == nil:
			statusCode = http.StatusOK
		default:
			var unauthorizedErr *webhookUnauthorizedError
			var badRequestErr *webhookBadRequestError
			switch {
			case errors.As(handleErr, &unauthorizedErr):
				statusCode = http.StatusUnauthorized
			case errors.As(handleErr, &badRequestErr):
				statusCode = http.StatusBadRequest
			default:
				statusCode = http.StatusInternalServerError
			}
			if strings.TrimSpace(result.Message) == "" {
				result.Message = handleErr.Error()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(result)
	})
	return mux, nil
}

func (r *Runtime) GCalWebhookHandler() (http.Handler, error) {
	if !r.cfg.GCalWebhookEnabled() {
		return nil, fmt.Errorf("gcal webhook intake is not configured")
	}

	secret, err := loadOptionalSecret("gcal webhook", r.cfg.GCalWebhookSecretEnvVar(), r.cfg.GCalWebhookSecretFile())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("gcal webhook secret is required")
	}

	source := newGCalLiveSource(r.cfg)
	calendars := append([]GCalCalendarConfig(nil), r.cfg.Connectors.GCal.Calendars...)

	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc(GCalWebhookPath, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if _, err := io.Copy(io.Discard, io.LimitReader(req.Body, 1<<20)); err != nil {
			http.Error(w, "read webhook body", http.StatusBadRequest)
			return
		}

		mu.Lock()
		result, handleErr := r.handleGCalWebhook(req.Context(), source, calendars, secret, req.Header)
		mu.Unlock()

		statusCode := http.StatusOK
		switch {
		case handleErr == nil && !result.Accepted:
			statusCode = http.StatusAccepted
		case handleErr == nil:
			statusCode = http.StatusOK
		default:
			var unauthorizedErr *webhookUnauthorizedError
			var badRequestErr *webhookBadRequestError
			switch {
			case errors.As(handleErr, &unauthorizedErr):
				statusCode = http.StatusUnauthorized
			case errors.As(handleErr, &badRequestErr):
				statusCode = http.StatusBadRequest
			default:
				statusCode = http.StatusInternalServerError
			}
			if strings.TrimSpace(result.Message) == "" {
				result.Message = handleErr.Error()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(result)
	})
	return mux, nil
}

func (r *Runtime) ServeWebhooks(ctx context.Context) error {
	type route struct {
		addr    string
		path    string
		handler http.Handler
	}

	routes := make([]route, 0, 3)
	if r.cfg.GitHubWebhookEnabled() {
		handler, err := r.GitHubWebhookHandler()
		if err != nil {
			return err
		}
		routes = append(routes, route{
			addr:    r.cfg.GitHubWebhookListenAddr(),
			path:    GitHubWebhookPath,
			handler: handler,
		})
	}
	if r.cfg.JiraWebhookEnabled() {
		handler, err := r.JiraWebhookHandler()
		if err != nil {
			return err
		}
		routes = append(routes, route{
			addr:    r.cfg.JiraWebhookListenAddr(),
			path:    JiraWebhookPath,
			handler: handler,
		})
	}
	if r.cfg.GCalWebhookEnabled() {
		handler, err := r.GCalWebhookHandler()
		if err != nil {
			return err
		}
		routes = append(routes, route{
			addr:    r.cfg.GCalWebhookListenAddr(),
			path:    GCalWebhookPath,
			handler: handler,
		})
	}
	if len(routes) == 0 {
		return fmt.Errorf("no webhook endpoints configured")
	}

	muxes := make(map[string]*http.ServeMux)
	for _, route := range routes {
		mux := muxes[route.addr]
		if mux == nil {
			mux = http.NewServeMux()
			muxes[route.addr] = mux
		}
		mux.Handle(route.path, route.handler)
	}

	servers := make([]*http.Server, 0, len(muxes))
	serverErrors := make(chan error, len(muxes))
	for addr, mux := range muxes {
		server := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		servers = append(servers, server)
		go func(server *http.Server) {
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErrors <- err
			}
		}(server)
	}

	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for _, server := range servers {
			_ = server.Shutdown(shutdownCtx)
		}
	}

	select {
	case err := <-serverErrors:
		shutdown()
		return fmt.Errorf("serve webhooks: %w", err)
	case <-ctx.Done():
		shutdown()
		return nil
	}
}

func (r *Runtime) ServeGitHubWebhooks(ctx context.Context) error {
	return r.ServeWebhooks(ctx)
}

func (r *Runtime) handleGitHubWebhook(ctx context.Context, source *gitHubLiveSource, repositories []GitHubRepositoryConfig, secret string, headers http.Header, body []byte) (WebhookDeliveryResult, error) {
	eventType := strings.TrimSpace(headers.Get("X-GitHub-Event"))
	result := WebhookDeliveryResult{
		ConnectorType: "github",
		EventType:     eventType,
	}

	if err := verifyGitHubWebhookSignature(secret, headers.Get("X-Hub-Signature-256"), body); err != nil {
		result.Message = err.Error()
		return result, err
	}

	switch eventType {
	case "ping":
		result.Accepted = true
		result.Message = "github webhook acknowledged"
		return result, nil
	case "pull_request":
		return r.handleGitHubPullRequestWebhook(ctx, source, repositories, headers, body)
	case "":
		err := &webhookBadRequestError{reason: "github webhook event type is required"}
		result.Message = err.Error()
		return result, err
	default:
		result.Message = fmt.Sprintf("ignored unsupported github event %q", eventType)
		return result, nil
	}
}

func (r *Runtime) handleGitHubPullRequestWebhook(ctx context.Context, source *gitHubLiveSource, repositories []GitHubRepositoryConfig, headers http.Header, body []byte) (WebhookDeliveryResult, error) {
	result := WebhookDeliveryResult{
		ConnectorType: "github",
		EventType:     "pull_request",
	}

	var payload gitHubWebhookPullRequestPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		parseErr := &webhookBadRequestError{reason: fmt.Sprintf("decode github pull_request payload: %v", err)}
		result.Message = parseErr.Error()
		return result, parseErr
	}

	result.Action = strings.TrimSpace(payload.Action)
	repositoryName := strings.TrimSpace(payload.Repository.FullName)
	if repositoryName == "" {
		parseErr := &webhookBadRequestError{reason: "github pull_request payload requires repository.full_name"}
		result.Message = parseErr.Error()
		return result, parseErr
	}
	if payload.PullRequest.Number <= 0 {
		parseErr := &webhookBadRequestError{reason: "github pull_request payload requires pull_request.number"}
		result.Message = parseErr.Error()
		return result, parseErr
	}

	repository, ok := findGitHubRepositoryConfig(repositories, repositoryName)
	if !ok {
		repository = GitHubRepositoryConfig{Name: repositoryName}
	}
	payload.PullRequest.Title = strings.TrimSpace(payload.PullRequest.Title)
	if !source.isRelevantPullRequest(payload.PullRequest) {
		result.Message = "ignored github webhook for unrelated pull request"
		return result, nil
	}

	state, err := LoadState(r.cfg.StatePath())
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	processedAt := time.Now().UTC()
	pruneWebhookReplayState(&state, processedAt)
	replayGuard := newGitHubWebhookReplayGuard(headers, body)
	if isDuplicateWebhookDelivery(state, replayGuard) {
		result.Accepted = true
		result.Message = "ignored duplicate github webhook delivery"
		return result, nil
	}
	if err := r.ensureKeyMaterial(&state); err != nil {
		result.Message = err.Error()
		return result, err
	}

	event := normalizeLiveGitHubPullRequest(repository, payload.PullRequest, source.actorLogin)
	freshEvents, cursorUpdates := filterEventsSinceCursors(state, []NormalizedEvent{event})
	if len(freshEvents) == 0 {
		recordWebhookDelivery(&state, replayGuard, processedAt)
		if err := SaveState(r.cfg.StatePath(), state); err != nil {
			result.Message = err.Error()
			return result, err
		}
		result.Accepted = true
		result.Message = "github webhook did not advance the repository cursor"
		return result, nil
	}

	registrationPerformed := false
	published, skipped, err := r.publishArtifactBatch(ctx, &state, deriveArtifacts(freshEvents, &state), cursorUpdates, &registrationPerformed)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	recordWebhookDelivery(&state, replayGuard, processedAt)
	if err := SaveState(r.cfg.StatePath(), state); err != nil {
		result.Message = err.Error()
		return result, err
	}

	result.Accepted = true
	result.PublishedArtifacts = published
	result.SkippedDigests = skipped
	switch {
	case len(published) > 0:
		result.Message = fmt.Sprintf("published %d artifact(s) from github webhook", len(published))
	case len(skipped) > 0:
		result.Message = "github webhook matched already-published artifacts"
	default:
		result.Message = "github webhook produced no publishable artifacts"
	}
	return result, nil
}

func (r *Runtime) handleJiraWebhook(ctx context.Context, source *jiraLiveSource, projects []JiraProjectConfig, secret string, headers http.Header, body []byte) (WebhookDeliveryResult, error) {
	result := WebhookDeliveryResult{
		ConnectorType: "jira",
	}

	if err := verifySharedSecretWebhook(secret, headers); err != nil {
		result.Message = err.Error()
		return result, err
	}

	var payload jiraWebhookIssuePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		parseErr := &webhookBadRequestError{reason: fmt.Sprintf("decode jira webhook payload: %v", err)}
		result.Message = parseErr.Error()
		return result, parseErr
	}

	result.EventType = strings.TrimSpace(payload.WebhookEvent)
	switch result.EventType {
	case "", "jira:issue_created", "jira:issue_updated":
		if strings.TrimSpace(result.EventType) == "" {
			result.EventType = "jira:issue_updated"
		}
		return r.handleJiraIssueWebhook(ctx, source, projects, headers, body, payload, result)
	default:
		result.Message = fmt.Sprintf("ignored unsupported jira event %q", result.EventType)
		return result, nil
	}
}

func (r *Runtime) handleJiraIssueWebhook(ctx context.Context, source *jiraLiveSource, projects []JiraProjectConfig, headers http.Header, body []byte, payload jiraWebhookIssuePayload, result WebhookDeliveryResult) (WebhookDeliveryResult, error) {
	issueKey := strings.TrimSpace(payload.Issue.Key)
	if issueKey == "" {
		parseErr := &webhookBadRequestError{reason: "jira webhook payload requires issue.key"}
		result.Message = parseErr.Error()
		return result, parseErr
	}

	project, ok := findJiraProjectForIssue(projects, issueKey)
	if !ok {
		projectKey, _, found := strings.Cut(issueKey, "-")
		if !found || strings.TrimSpace(projectKey) == "" {
			parseErr := &webhookBadRequestError{reason: "jira webhook payload requires an issue key with a project prefix"}
			result.Message = parseErr.Error()
			return result, parseErr
		}
		project = JiraProjectConfig{Key: strings.TrimSpace(projectKey)}
	}

	if !source.isRelevantIssue(payload.Issue) {
		result.Message = "ignored jira webhook for unrelated issue"
		return result, nil
	}

	state, err := LoadState(r.cfg.StatePath())
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	processedAt := time.Now().UTC()
	pruneWebhookReplayState(&state, processedAt)
	replayGuard := newJiraWebhookReplayGuard(headers, body)
	if isDuplicateWebhookDelivery(state, replayGuard) {
		result.Accepted = true
		result.Message = "ignored duplicate jira webhook delivery"
		return result, nil
	}
	if err := r.ensureKeyMaterial(&state); err != nil {
		result.Message = err.Error()
		return result, err
	}

	event := normalizeLiveJiraIssue(project, payload.Issue)
	freshEvents, cursorUpdates := filterEventsSinceCursors(state, []NormalizedEvent{event})
	if len(freshEvents) == 0 {
		recordWebhookDelivery(&state, replayGuard, processedAt)
		if err := SaveState(r.cfg.StatePath(), state); err != nil {
			result.Message = err.Error()
			return result, err
		}
		result.Accepted = true
		result.Message = "jira webhook did not advance the project cursor"
		return result, nil
	}

	registrationPerformed := false
	published, skipped, err := r.publishArtifactBatch(ctx, &state, deriveArtifacts(freshEvents, &state), cursorUpdates, &registrationPerformed)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	recordWebhookDelivery(&state, replayGuard, processedAt)
	if err := SaveState(r.cfg.StatePath(), state); err != nil {
		result.Message = err.Error()
		return result, err
	}

	result.Accepted = true
	result.PublishedArtifacts = published
	result.SkippedDigests = skipped
	switch {
	case len(published) > 0:
		result.Message = fmt.Sprintf("published %d artifact(s) from jira webhook", len(published))
	case len(skipped) > 0:
		result.Message = "jira webhook matched already-published artifacts"
	default:
		result.Message = "jira webhook produced no publishable artifacts"
	}
	return result, nil
}

func (r *Runtime) handleGCalWebhook(ctx context.Context, source *gcalLiveSource, calendars []GCalCalendarConfig, secret string, headers http.Header) (WebhookDeliveryResult, error) {
	resourceState := strings.TrimSpace(headers.Get("X-Goog-Resource-State"))
	result := WebhookDeliveryResult{
		ConnectorType: "gcal",
		EventType:     "calendar_notification",
		Action:        resourceState,
	}

	if err := verifyGCalWebhookSecret(secret, headers); err != nil {
		result.Message = err.Error()
		return result, err
	}
	if resourceState == "" {
		err := &webhookBadRequestError{reason: "gcal webhook resource state is required"}
		result.Message = err.Error()
		return result, err
	}

	switch resourceState {
	case "sync":
		result.Accepted = true
		result.Message = "gcal webhook acknowledged initial sync"
		return result, nil
	case "exists":
		return r.handleGCalExistsWebhook(ctx, source, calendars, headers, result)
	case "not_exists":
		result.Accepted = true
		result.Message = "gcal webhook reported a removed resource"
		return result, nil
	default:
		result.Message = fmt.Sprintf("ignored unsupported gcal resource state %q", resourceState)
		return result, nil
	}
}

func (r *Runtime) handleGCalExistsWebhook(ctx context.Context, source *gcalLiveSource, calendars []GCalCalendarConfig, headers http.Header, result WebhookDeliveryResult) (WebhookDeliveryResult, error) {
	calendarID, err := parseGCalWebhookCalendarID(headers.Get("X-Goog-Resource-URI"))
	if err != nil {
		result.Message = err.Error()
		return result, err
	}

	calendar, ok := findGCalCalendarConfig(calendars, calendarID)
	if !ok {
		result.Message = fmt.Sprintf("ignored gcal webhook for unconfigured calendar %q", calendarID)
		return result, nil
	}

	state, err := LoadState(r.cfg.StatePath())
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	processedAt := time.Now().UTC()
	pruneWebhookReplayState(&state, processedAt)
	replayGuard, err := newGCalWebhookReplayGuard(headers)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	if isDuplicateWebhookDelivery(state, replayGuard) {
		result.Accepted = true
		result.Message = "ignored duplicate or out-of-order gcal webhook delivery"
		return result, nil
	}
	if err := r.ensureKeyMaterial(&state); err != nil {
		result.Message = err.Error()
		return result, err
	}

	credentials, err := r.prepareCredentialStore(ctx, &state)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}

	events, err := source.PollCalendar(ctx, state, credentials, calendar)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	freshEvents, cursorUpdates := filterEventsSinceCursors(state, events)
	if len(freshEvents) == 0 {
		recordWebhookDelivery(&state, replayGuard, processedAt)
		if err := SaveState(r.cfg.StatePath(), state); err != nil {
			result.Message = err.Error()
			return result, err
		}
		result.Accepted = true
		result.Message = "gcal webhook did not advance the calendar cursor"
		return result, nil
	}

	registrationPerformed := false
	published, skipped, err := r.publishArtifactBatch(ctx, &state, deriveArtifacts(freshEvents, &state), cursorUpdates, &registrationPerformed)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	recordWebhookDelivery(&state, replayGuard, processedAt)
	if err := SaveState(r.cfg.StatePath(), state); err != nil {
		result.Message = err.Error()
		return result, err
	}

	result.Accepted = true
	result.PublishedArtifacts = published
	result.SkippedDigests = skipped
	switch {
	case len(published) > 0:
		result.Message = fmt.Sprintf("published %d artifact(s) from gcal webhook", len(published))
	case len(skipped) > 0:
		result.Message = "gcal webhook matched already-published artifacts"
	default:
		result.Message = "gcal webhook produced no publishable artifacts"
	}
	return result, nil
}

func verifyGitHubWebhookSignature(secret, headerValue string, body []byte) error {
	trimmedSecret := strings.TrimSpace(secret)
	if trimmedSecret == "" {
		return &webhookUnauthorizedError{reason: "github webhook secret is not configured"}
	}
	signature := strings.TrimSpace(headerValue)
	if signature == "" {
		return &webhookUnauthorizedError{reason: "github webhook signature is missing"}
	}

	mac := hmac.New(sha256.New, []byte(trimmedSecret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return &webhookUnauthorizedError{reason: "github webhook signature is invalid"}
	}
	return nil
}

func verifySharedSecretWebhook(secret string, headers http.Header) error {
	expectedSecret := strings.TrimSpace(secret)
	if expectedSecret == "" {
		return &webhookUnauthorizedError{reason: "webhook secret is not configured"}
	}

	if providedSecret := strings.TrimSpace(headers.Get("X-Alice-Webhook-Secret")); providedSecret != "" {
		if subtleConstantTimeEqual(expectedSecret, providedSecret) {
			return nil
		}
		return &webhookUnauthorizedError{reason: "webhook shared secret is invalid"}
	}

	authorization := strings.TrimSpace(headers.Get("Authorization"))
	if token, ok := strings.CutPrefix(authorization, "Bearer "); ok {
		if subtleConstantTimeEqual(expectedSecret, strings.TrimSpace(token)) {
			return nil
		}
		return &webhookUnauthorizedError{reason: "webhook bearer secret is invalid"}
	}
	if authorization != "" {
		return &webhookUnauthorizedError{reason: "webhook authorization must use Bearer auth"}
	}
	return &webhookUnauthorizedError{reason: "webhook shared secret is missing"}
}

func verifyGCalWebhookSecret(secret string, headers http.Header) error {
	expectedSecret := strings.TrimSpace(secret)
	if expectedSecret == "" {
		return &webhookUnauthorizedError{reason: "gcal webhook secret is not configured"}
	}

	if providedToken := strings.TrimSpace(headers.Get("X-Goog-Channel-Token")); providedToken != "" {
		if subtleConstantTimeEqual(expectedSecret, providedToken) {
			return nil
		}
		return &webhookUnauthorizedError{reason: "gcal webhook channel token is invalid"}
	}

	return verifySharedSecretWebhook(secret, headers)
}

func parseGCalWebhookCalendarID(resourceURI string) (string, error) {
	trimmedURI := strings.TrimSpace(resourceURI)
	if trimmedURI == "" {
		return "", &webhookBadRequestError{reason: "gcal webhook resource URI is required"}
	}

	parsed, err := url.Parse(trimmedURI)
	if err != nil {
		return "", &webhookBadRequestError{reason: fmt.Sprintf("parse gcal webhook resource URI: %v", err)}
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i+2 < len(segments); i++ {
		if segments[i] != "calendars" || segments[i+2] != "events" {
			continue
		}
		calendarID, unescapeErr := url.PathUnescape(segments[i+1])
		if unescapeErr != nil {
			return "", &webhookBadRequestError{reason: fmt.Sprintf("decode gcal webhook calendar id: %v", unescapeErr)}
		}
		calendarID = strings.TrimSpace(calendarID)
		if calendarID == "" {
			break
		}
		return calendarID, nil
	}
	return "", &webhookBadRequestError{reason: "gcal webhook resource URI must identify a calendar events feed"}
}

func pruneWebhookReplayState(state *State, now time.Time) {
	state.PruneProcessedWebhooks(now.Add(-webhookReplayRetention))
}

func isDuplicateWebhookDelivery(state State, guard webhookReplayGuard) bool {
	if guard.deliveryKey != "" && !state.WebhookProcessedAt(guard.deliveryKey).IsZero() {
		return true
	}
	if guard.sequenceKey != "" && guard.sequenceNumber > 0 && state.WebhookSequenceNumber(guard.sequenceKey) >= guard.sequenceNumber {
		return true
	}
	return false
}

func recordWebhookDelivery(state *State, guard webhookReplayGuard, processedAt time.Time) {
	if guard.deliveryKey != "" {
		state.MarkWebhookProcessed(guard.deliveryKey, processedAt)
	}
	if guard.sequenceKey != "" && guard.sequenceNumber > 0 {
		state.SetWebhookSequenceNumber(guard.sequenceKey, guard.sequenceNumber)
	}
}

func newGitHubWebhookReplayGuard(headers http.Header, body []byte) webhookReplayGuard {
	if deliveryID := strings.TrimSpace(headers.Get("X-GitHub-Delivery")); deliveryID != "" {
		return webhookReplayGuard{deliveryKey: "github:delivery:" + deliveryID}
	}
	return webhookReplayGuard{deliveryKey: "github:payload:" + digestWebhookPayload(body)}
}

func newJiraWebhookReplayGuard(headers http.Header, body []byte) webhookReplayGuard {
	for _, headerName := range []string{"X-Atlassian-Webhook-Identifier", "X-Request-Id"} {
		if deliveryID := strings.TrimSpace(headers.Get(headerName)); deliveryID != "" {
			return webhookReplayGuard{deliveryKey: "jira:delivery:" + deliveryID}
		}
	}
	return webhookReplayGuard{deliveryKey: "jira:payload:" + digestWebhookPayload(body)}
}

func newGCalWebhookReplayGuard(headers http.Header) (webhookReplayGuard, error) {
	channelID := strings.TrimSpace(headers.Get("X-Goog-Channel-ID"))
	messageNumber := strings.TrimSpace(headers.Get("X-Goog-Message-Number"))
	if channelID != "" && messageNumber != "" {
		parsed, err := strconv.ParseInt(messageNumber, 10, 64)
		if err != nil || parsed <= 0 {
			return webhookReplayGuard{}, &webhookBadRequestError{reason: "gcal webhook message number is invalid"}
		}
		return webhookReplayGuard{
			deliveryKey:    fmt.Sprintf("gcal:channel:%s:message:%d", channelID, parsed),
			sequenceKey:    "gcal:channel:" + channelID,
			sequenceNumber: parsed,
		}, nil
	}

	return webhookReplayGuard{
		deliveryKey: "gcal:fallback:" + digestWebhookStrings(
			headers.Get("X-Goog-Resource-State"),
			headers.Get("X-Goog-Resource-URI"),
			headers.Get("X-Goog-Resource-ID"),
			headers.Get("X-Goog-Channel-ID"),
		),
	}, nil
}

func digestWebhookPayload(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func digestWebhookStrings(values ...string) string {
	mac := sha256.New()
	for _, value := range values {
		_, _ = mac.Write([]byte(strings.TrimSpace(value)))
		_, _ = mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func subtleConstantTimeEqual(expected, provided string) bool {
	return hmac.Equal([]byte(expected), []byte(provided))
}
