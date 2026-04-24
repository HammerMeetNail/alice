//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"alice/internal/app"
	"alice/internal/config"
	"alice/internal/httpapi"
)

// newE2EServer creates a fresh in-process HTTP server for one test. It
// registers a cleanup that stops the server when the test ends.
func newE2EServer(t *testing.T) string {
	t.Helper()

	cfg := config.Config{
		DefaultOrgName:   "E2E Test Org",
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     15 * time.Minute,
	}

	if dbURL := strings.TrimSpace(os.Getenv("ALICE_TEST_DATABASE_URL")); dbURL != "" {
		cfg.DatabaseURL = dbURL
	}

	container, closeFn, err := app.NewContainer(cfg)
	if err != nil {
		t.Fatalf("build app container: %v", err)
	}

	ts := httptest.NewServer(httpapi.NewRouter(httpapi.RouterOptions{Services: container}))
	t.Cleanup(func() {
		ts.Close()
		if closeFn != nil {
			_ = closeFn()
		}
	})
	return ts.URL
}

// --- HTTP helpers ---

type registeredAgent struct {
	AgentID     string
	UserID      string
	OrgID       string
	AccessToken string
}

func registerAgent(t *testing.T, baseURL, orgSlug, email string) registeredAgent {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keys: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	var chalResp map[string]any
	doJSON(t, baseURL, http.MethodPost, "/v1/agents/register/challenge", "", map[string]any{
		"org_slug":    orgSlug,
		"owner_email": email,
		"agent_name":  email + "-agent",
		"client_type": "mcp",
		"public_key":  pubB64,
	}, http.StatusOK, &chalResp)

	challengeID := chalResp["challenge_id"].(string)
	challenge := chalResp["challenge"].(string)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(challenge)))

	var regResp map[string]any
	doJSON(t, baseURL, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challengeID,
		"challenge_signature": sig,
	}, http.StatusOK, &regResp)

	return registeredAgent{
		AgentID:     str(regResp, "agent_id"),
		UserID:      str(regResp, "user_id"),
		OrgID:       str(regResp, "org_id"),
		AccessToken: str(regResp, "access_token"),
	}
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// doJSON makes a request against baseURL+path, asserts the status code, and
// optionally decodes the response body into out.
func doJSON(t *testing.T, baseURL, method, path, token string, body any, wantStatus int, out any) {
	t.Helper()

	status, respBytes := httpRaw(t, baseURL, method, path, token, body)
	if status != wantStatus {
		t.Fatalf("%s %s: expected %d, got %d body=%s", method, path, wantStatus, status, respBytes)
	}
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			t.Fatalf("decode response for %s %s: %v", method, path, err)
		}
	}
}

// doJSONRaw makes a request and returns the status code + body without
// fatalffing on unexpected status codes.
func doJSONRaw(t *testing.T, baseURL, method, path, token string, body any) (int, []byte) {
	t.Helper()
	return httpRaw(t, baseURL, method, path, token, body)
}

func httpRaw(t *testing.T, baseURL, method, path, token string, body any) (int, []byte) {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		switch v := body.(type) {
		case string:
			bodyReader = strings.NewReader(v)
		default:
			data, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			bodyReader = bytes.NewReader(data)
		}
	}

	req, err := http.NewRequestWithContext(context.Background(), method, baseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, respBytes
}

// orgSlug generates a unique org slug for each test.
func orgSlug(t *testing.T) string {
	t.Helper()
	suffix := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, strings.ToLower(t.Name()))
	if len(suffix) > 30 {
		suffix = suffix[:30]
	}
	return "e2e-" + suffix + "-" + fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
}

// capturingSender captures email OTP codes for use in tests.
type capturingSender struct {
	mu   sync.Mutex
	sent []struct{ To, Body string }
}

func (s *capturingSender) Send(_ context.Context, to, _, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, struct{ To, Body string }{to, body})
	return nil
}

func (s *capturingSender) LastCode() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return "", false
	}
	body := s.sent[len(s.sent)-1].Body
	const prefix = "Your Alice verification code is: "
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			code := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if len(code) == 6 {
				return code, true
			}
		}
	}
	return "", false
}
