//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentRegistration verifies that completing the same registration
// challenge from multiple goroutines results in exactly one success.
func TestConcurrentRegistration(t *testing.T) {
	base := newE2EServer(t)
	slug := orgSlug(t)
	email := slug + "-race@example.com"

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keys: %v", err)
	}

	// Issue one challenge.
	chalBody, _ := json.Marshal(map[string]any{
		"org_slug":    slug,
		"owner_email": email,
		"agent_name":  "race-agent",
		"client_type": "mcp",
		"public_key":  base64.StdEncoding.EncodeToString(pub),
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		base+"/v1/agents/register/challenge", bytes.NewReader(chalBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("challenge status %d body=%s", resp.StatusCode, body)
	}

	var chalResp map[string]any
	if err := json.Unmarshal(body, &chalResp); err != nil {
		t.Fatalf("decode challenge response: %v", err)
	}
	challengeID := chalResp["challenge_id"].(string)
	challenge := chalResp["challenge"].(string)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(challenge)))

	// Launch N goroutines all trying to complete the same challenge.
	const n = 5
	var (
		successCount atomic.Int32
		errorCount   atomic.Int32
		wg           sync.WaitGroup
	)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			regBody, _ := json.Marshal(map[string]any{
				"challenge_id":        challengeID,
				"challenge_signature": sig,
			})
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
				base+"/v1/agents/register", bytes.NewReader(regBody))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errorCount.Add(1)
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			if resp.StatusCode == http.StatusOK {
				successCount.Add(1)
			} else {
				errorCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if successCount.Load() != 1 {
		t.Fatalf("expected exactly 1 successful registration, got %d (errors=%d)", successCount.Load(), errorCount.Load())
	}
	if errorCount.Load() != n-1 {
		t.Fatalf("expected %d errors, got %d", n-1, errorCount.Load())
	}
}
