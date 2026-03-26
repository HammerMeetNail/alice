//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestInputValidation exercises body-size limits and malformed-JSON detection
// over a real TCP connection.
func TestInputValidation(t *testing.T) {
	base := newE2EServer(t)

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		endpoints := []struct{ method, path string }{
			{http.MethodPost, "/v1/agents/register/challenge"},
			{http.MethodPost, "/v1/agents/register"},
		}
		for _, ep := range endpoints {
			t.Run(ep.method+" "+ep.path, func(t *testing.T) {
				status, _ := doJSONRaw(t, base, ep.method, ep.path, "", "{not valid json}")
				if status != http.StatusBadRequest {
					t.Fatalf("expected 400 for malformed JSON, got %d", status)
				}
			})
		}
	})

	t.Run("oversized body returns 413", func(t *testing.T) {
		bigBody := `{"padding":"` + strings.Repeat("x", 1<<20+1) + `"}`
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			base+"/v1/agents/register/challenge", strings.NewReader(bigBody))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("send oversized request: %v", err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 for oversized body, got %d", resp.StatusCode)
		}
	})

	t.Run("missing auth header returns 401", func(t *testing.T) {
		status, _ := doJSONRaw(t, base, http.MethodGet, "/v1/peers", "", nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("expected 401 for missing auth, got %d", status)
		}
	})

	t.Run("malformed bearer token returns 401", func(t *testing.T) {
		status, _ := doJSONRaw(t, base, http.MethodGet, "/v1/peers", "not-a-real-token", nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("expected 401 for malformed token, got %d", status)
		}
	})

	t.Run("nonexistent query returns 404", func(t *testing.T) {
		slug := orgSlug(t)
		agent := registerAgent(t, base, slug, "user@example.com")
		status, _ := doJSONRaw(t, base, http.MethodGet, "/v1/queries/nonexistent-query-id", agent.AccessToken, nil)
		if status != http.StatusNotFound {
			t.Fatalf("expected 404 for nonexistent query, got %d", status)
		}
	})
}
