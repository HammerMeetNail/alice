package edge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ConnectorBootstrapPrompt struct {
	ConnectorType    string `json:"connector_type"`
	AuthorizationURL string `json:"authorization_url"`
	CallbackURL      string `json:"callback_url"`
}

type ConnectorBootstrapResult struct {
	ConnectorType    string    `json:"connector_type"`
	AuthorizationURL string    `json:"authorization_url"`
	CallbackURL      string    `json:"callback_url"`
	TokenExpiresAt   time.Time `json:"token_expires_at,omitempty"`
	StoredInState    bool      `json:"stored_in_state"`
}

type connectorOAuthProvider struct {
	connectorType string
	oauth         ConnectorOAuthConfig
}

type connectorCallbackRequest struct {
	code             string
	state            string
	authorizationErr string
	reply            chan error
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

func (r *Runtime) BootstrapConnector(ctx context.Context, connectorType string, publish func(ConnectorBootstrapPrompt) error) (ConnectorBootstrapResult, error) {
	state, err := LoadState(r.cfg.StatePath())
	if err != nil {
		return ConnectorBootstrapResult{}, err
	}

	provider, err := r.oauthProvider(connectorType)
	if err != nil {
		return ConnectorBootstrapResult{}, err
	}

	listener, callbackURL, callbackPath, err := listenLoopbackCallback(provider.oauth.CallbackURL)
	if err != nil {
		return ConnectorBootstrapResult{}, err
	}
	defer listener.Close()

	pending, authorizationURL, err := newPendingConnectorAuth(provider, callbackURL)
	if err != nil {
		return ConnectorBootstrapResult{}, err
	}

	state.normalizePublishedArtifacts()
	state.PendingConnectorAuths[provider.connectorType] = pending
	if err := SaveState(r.cfg.StatePath(), state); err != nil {
		return ConnectorBootstrapResult{}, err
	}

	callbacks := make(chan connectorCallbackRequest, 1)
	serverErrors := make(chan error, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != callbackPath {
				http.NotFound(w, req)
				return
			}
			if req.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}

			callback := connectorCallbackRequest{
				code:             strings.TrimSpace(req.URL.Query().Get("code")),
				state:            strings.TrimSpace(req.URL.Query().Get("state")),
				authorizationErr: strings.TrimSpace(req.URL.Query().Get("error")),
				reply:            make(chan error, 1),
			}

			select {
			case callbacks <- callback:
			case <-ctx.Done():
				http.Error(w, "bootstrap context canceled", http.StatusRequestTimeout)
				return
			}

			if err := <-callback.reply; err != nil {
				http.Error(w, "connector authorization failed: "+err.Error(), http.StatusBadRequest)
				return
			}

			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "Connector authorization stored. You can close this window.\n")
		}),
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	prompt := ConnectorBootstrapPrompt{
		ConnectorType:    provider.connectorType,
		AuthorizationURL: authorizationURL,
		CallbackURL:      callbackURL,
	}
	if publish != nil {
		if err := publish(prompt); err != nil {
			_ = r.clearPendingConnectorAuth(provider.connectorType)
			return ConnectorBootstrapResult{}, err
		}
	}

	for {
		select {
		case callback := <-callbacks:
			result, err := r.completeConnectorBootstrap(ctx, provider, pending, prompt, callback)
			callback.reply <- err
			close(callback.reply)
			return result, err
		case err := <-serverErrors:
			_ = r.clearPendingConnectorAuth(provider.connectorType)
			return ConnectorBootstrapResult{}, fmt.Errorf("serve connector callback: %w", err)
		case <-ctx.Done():
			_ = r.clearPendingConnectorAuth(provider.connectorType)
			return ConnectorBootstrapResult{}, fmt.Errorf("wait for connector callback: %w", ctx.Err())
		}
	}
}

func (r *Runtime) oauthProvider(connectorType string) (connectorOAuthProvider, error) {
	switch normalizeLabel(connectorType, "") {
	case "github":
		if !r.cfg.GitHubOAuthEnabled() {
			return connectorOAuthProvider{}, fmt.Errorf("github oauth is not configured")
		}
		return connectorOAuthProvider{connectorType: "github", oauth: r.cfg.GitHubOAuthConfig()}, nil
	case "jira":
		if !r.cfg.JiraOAuthEnabled() {
			return connectorOAuthProvider{}, fmt.Errorf("jira oauth is not configured")
		}
		return connectorOAuthProvider{connectorType: "jira", oauth: r.cfg.JiraOAuthConfig()}, nil
	case "gcal":
		if !r.cfg.GCalOAuthEnabled() {
			return connectorOAuthProvider{}, fmt.Errorf("gcal oauth is not configured")
		}
		return connectorOAuthProvider{connectorType: "gcal", oauth: r.cfg.GCalOAuthConfig()}, nil
	default:
		return connectorOAuthProvider{}, fmt.Errorf("unsupported connector %q", connectorType)
	}
}

func (r *Runtime) completeConnectorBootstrap(ctx context.Context, provider connectorOAuthProvider, pending PendingConnectorAuth, prompt ConnectorBootstrapPrompt, callback connectorCallbackRequest) (ConnectorBootstrapResult, error) {
	if callback.authorizationErr != "" {
		_ = r.clearPendingConnectorAuth(provider.connectorType)
		return ConnectorBootstrapResult{}, fmt.Errorf("provider returned oauth error %q", callback.authorizationErr)
	}
	if callback.state != pending.State {
		_ = r.clearPendingConnectorAuth(provider.connectorType)
		return ConnectorBootstrapResult{}, fmt.Errorf("oauth state mismatch")
	}
	if callback.code == "" {
		_ = r.clearPendingConnectorAuth(provider.connectorType)
		return ConnectorBootstrapResult{}, fmt.Errorf("oauth callback did not include an authorization code")
	}

	credential, err := exchangeConnectorAuthorizationCode(ctx, provider, pending, callback.code)
	if err != nil {
		_ = r.clearPendingConnectorAuth(provider.connectorType)
		return ConnectorBootstrapResult{}, err
	}

	state, err := LoadState(r.cfg.StatePath())
	if err != nil {
		return ConnectorBootstrapResult{}, err
	}
	state.normalizePublishedArtifacts()
	state.ConnectorCredentials[provider.connectorType] = credential
	delete(state.PendingConnectorAuths, provider.connectorType)
	if err := SaveState(r.cfg.StatePath(), state); err != nil {
		return ConnectorBootstrapResult{}, err
	}

	return ConnectorBootstrapResult{
		ConnectorType:    provider.connectorType,
		AuthorizationURL: prompt.AuthorizationURL,
		CallbackURL:      prompt.CallbackURL,
		TokenExpiresAt:   credential.ExpiresAt,
		StoredInState:    true,
	}, nil
}

func (r *Runtime) clearPendingConnectorAuth(connectorType string) error {
	state, err := LoadState(r.cfg.StatePath())
	if err != nil {
		return err
	}
	state.normalizePublishedArtifacts()
	delete(state.PendingConnectorAuths, connectorType)
	return SaveState(r.cfg.StatePath(), state)
}

func newPendingConnectorAuth(provider connectorOAuthProvider, callbackURL string) (PendingConnectorAuth, string, error) {
	oauthState, err := randomOAuthValue(24)
	if err != nil {
		return PendingConnectorAuth{}, "", fmt.Errorf("generate oauth state: %w", err)
	}
	codeVerifier, err := randomOAuthValue(48)
	if err != nil {
		return PendingConnectorAuth{}, "", fmt.Errorf("generate pkce verifier: %w", err)
	}

	pending := PendingConnectorAuth{
		State:        oauthState,
		CodeVerifier: codeVerifier,
		RedirectURL:  callbackURL,
		CreatedAt:    time.Now().UTC(),
	}

	authorizationURL, err := buildAuthorizationURL(provider, pending)
	if err != nil {
		return PendingConnectorAuth{}, "", err
	}
	return pending, authorizationURL, nil
}

func buildAuthorizationURL(provider connectorOAuthProvider, pending PendingConnectorAuth) (string, error) {
	parsed, err := url.Parse(provider.oauth.AuthorizationURL)
	if err != nil {
		return "", fmt.Errorf("parse authorization url: %w", err)
	}

	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", provider.oauth.ClientID)
	query.Set("redirect_uri", pending.RedirectURL)
	query.Set("state", pending.State)
	query.Set("code_challenge", pkceChallenge(pending.CodeVerifier))
	query.Set("code_challenge_method", "S256")
	if len(provider.oauth.Scopes) > 0 {
		query.Set("scope", strings.Join(provider.oauth.Scopes, " "))
	}
	for key, value := range provider.oauth.ExtraAuthParams {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func exchangeConnectorAuthorizationCode(ctx context.Context, provider connectorOAuthProvider, pending PendingConnectorAuth, code string) (ConnectorCredential, error) {
	clientSecret, err := loadOptionalSecret(provider.connectorType+" client secret", provider.oauth.ClientSecretEnvVar, provider.oauth.ClientSecretFile)
	if err != nil {
		return ConnectorCredential{}, err
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", pending.RedirectURL)
	form.Set("client_id", provider.oauth.ClientID)
	form.Set("code_verifier", pending.CodeVerifier)
	if strings.TrimSpace(clientSecret) != "" {
		form.Set("client_secret", clientSecret)
	}
	for key, value := range provider.oauth.ExtraTokenParams {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		form.Set(key, value)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.oauth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return ConnectorCredential{}, fmt.Errorf("build token exchange request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return ConnectorCredential{}, fmt.Errorf("perform token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ConnectorCredential{}, fmt.Errorf("token exchange returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload oauthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ConnectorCredential{}, fmt.Errorf("decode token exchange response: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return ConnectorCredential{}, fmt.Errorf("token exchange response did not include access_token")
	}

	credential := ConnectorCredential{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		TokenType:    payload.TokenType,
		Scope:        payload.Scope,
		ObtainedAt:   time.Now().UTC(),
	}
	if credential.Scope == "" && len(provider.oauth.Scopes) > 0 {
		credential.Scope = strings.Join(provider.oauth.Scopes, " ")
	}
	if payload.ExpiresIn > 0 {
		credential.ExpiresAt = credential.ObtainedAt.Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	return credential, nil
}

func listenLoopbackCallback(rawCallbackURL string) (net.Listener, string, string, error) {
	parsed, err := url.Parse(rawCallbackURL)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse callback url: %w", err)
	}
	if parsed.Scheme != "http" {
		return nil, "", "", fmt.Errorf("callback url must use http for local bootstrap")
	}
	hostname := parsed.Hostname()
	if !isLoopbackHost(hostname) {
		return nil, "", "", fmt.Errorf("callback url must target localhost or a loopback address")
	}
	port := parsed.Port()
	if port == "" {
		return nil, "", "", fmt.Errorf("callback url port is required")
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(hostname, port))
	if err != nil {
		return nil, "", "", fmt.Errorf("listen for callback: %w", err)
	}

	actualCallbackURL := *parsed
	actualCallbackURL.Host = net.JoinHostPort(hostname, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	callbackPath := actualCallbackURL.Path
	if callbackPath == "" {
		callbackPath = "/"
		actualCallbackURL.Path = callbackPath
	}
	return listener, actualCallbackURL.String(), callbackPath, nil
}

func randomOAuthValue(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func pkceChallenge(codeVerifier string) string {
	sum := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
