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
	"strings"
	"sync"
	"time"
)

const GitHubWebhookPath = "/webhooks/github"

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

func (r *Runtime) ServeGitHubWebhooks(ctx context.Context) error {
	handler, err := r.GitHubWebhookHandler()
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              r.cfg.GitHubWebhookListenAddr(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("serve github webhooks: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	}
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
		return r.handleGitHubPullRequestWebhook(ctx, source, repositories, body)
	case "":
		err := &webhookBadRequestError{reason: "github webhook event type is required"}
		result.Message = err.Error()
		return result, err
	default:
		result.Message = fmt.Sprintf("ignored unsupported github event %q", eventType)
		return result, nil
	}
}

func (r *Runtime) handleGitHubPullRequestWebhook(ctx context.Context, source *gitHubLiveSource, repositories []GitHubRepositoryConfig, body []byte) (WebhookDeliveryResult, error) {
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
	if err := r.ensureKeyMaterial(&state); err != nil {
		result.Message = err.Error()
		return result, err
	}

	event := normalizeLiveGitHubPullRequest(repository, payload.PullRequest, source.actorLogin)
	freshEvents, cursorUpdates := filterEventsSinceCursors(state, []NormalizedEvent{event})
	if len(freshEvents) == 0 {
		result.Accepted = true
		result.Message = "github webhook did not advance the repository cursor"
		return result, nil
	}

	registrationPerformed := false
	published, skipped, err := r.publishArtifactBatch(ctx, &state, deriveArtifacts(freshEvents), cursorUpdates, &registrationPerformed)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
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
