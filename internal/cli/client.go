package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is a thin HTTP client that talks to the coordination server. It is
// intentionally remote-only: each CLI invocation is its own short-lived
// process, so an in-process memory store would lose state between commands.
// Users who want to run everything on one machine should start the local
// stack (`make local`) and point ALICE_SERVER_URL at it.
type Client struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
}

// ClientOptions configures a Client.
type ClientOptions struct {
	BaseURL     string
	AccessToken string
	TLSCAFile   string
	Timeout     time.Duration
}

// NewClient constructs a Client pointed at the coordination server.
func NewClient(opts ClientOptions) (*Client, error) {
	baseURL := strings.TrimSuffix(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("server URL is required (set ALICE_SERVER_URL or pass --server)")
	}

	tlsConfig := &tls.Config{}
	if opts.TLSCAFile != "" {
		caCert, err := os.ReadFile(opts.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("no valid certificates in TLS CA file %q", opts.TLSCAFile)
		}
		tlsConfig.RootCAs = pool
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	var transport http.RoundTripper
	if opts.TLSCAFile != "" {
		transport = &http.Transport{TLSClientConfig: tlsConfig}
	}

	return &Client{
		baseURL:     baseURL,
		accessToken: strings.TrimSpace(opts.AccessToken),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}, nil
}

// SetAccessToken updates the bearer token used for authenticated calls.
func (c *Client) SetAccessToken(token string) {
	c.accessToken = strings.TrimSpace(token)
}

// BaseURL returns the configured coordination server URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Do performs an HTTP request against the coordination server, marshalling
// body to JSON and decoding the response into a map. Authentication is
// attached automatically when a bearer token is configured; pass
// skipAuth=true for the unauthenticated registration endpoints.
func (c *Client) Do(ctx context.Context, method, path string, body any, skipAuth bool) (map[string]any, error) {
	var bodyBytes []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyBytes = encoded
	}

	var reader io.Reader
	if bodyBytes != nil {
		reader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if bodyBytes != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !skipAuth && c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coordination server request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var payload map[string]any
	if len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, &payload); err != nil {
			return nil, fmt.Errorf("decode response: %w (raw=%s)", err, truncate(string(respBytes), 200))
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return payload, httpErrorFromPayload(resp.StatusCode, payload)
	}

	return payload, nil
}

// HTTPError wraps a non-2xx server response with the decoded error/message
// fields when they're present. Retaining the structured payload lets the
// caller render a better message.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
	Payload    map[string]any
}

func (e *HTTPError) Error() string {
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func httpErrorFromPayload(statusCode int, payload map[string]any) error {
	he := &HTTPError{StatusCode: statusCode, Payload: payload}
	if code, ok := payload["error"].(string); ok {
		he.Code = code
	}
	if msg, ok := payload["message"].(string); ok {
		he.Message = msg
	}
	return he
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
