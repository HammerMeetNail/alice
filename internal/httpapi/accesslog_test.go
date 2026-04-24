package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// TestAccessLogPopulatesAgentID verifies that the access-log middleware writes
// the authenticated agent's ID into the "agent_id" field of the structured log
// line.  Unauthenticated requests must emit an empty agent_id field; successful
// bearer-token auth must emit the real agent ID.
func TestAccessLogPopulatesAgentID(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	registered := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Redirect slog output to a buffer for the duration of this test.
	var buf bytes.Buffer
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// --- Unauthenticated request: agent_id should be empty. ---
	recUnauth := performJSON(t, handler, http.MethodGet, "/v1/peers", "", nil)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recUnauth.Code)
	}
	unauthLine := lastLogLine(t, buf.String(), "GET", "/v1/peers")
	if id, _ := unauthLine["agent_id"].(string); id != "" {
		t.Errorf("unauthenticated request: expected empty agent_id, got %q", id)
	}
	buf.Reset()

	// --- Authenticated request: agent_id must match the registered agent. ---
	recAuth := performJSON(t, handler, http.MethodGet, "/v1/peers", registered.AccessToken, nil)
	if recAuth.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recAuth.Code, recAuth.Body.String())
	}
	authLine := lastLogLine(t, buf.String(), "GET", "/v1/peers")
	if id, _ := authLine["agent_id"].(string); id != registered.AgentID {
		t.Errorf("authenticated request: expected agent_id=%q, got %q", registered.AgentID, id)
	}
}

// lastLogLine parses the JSON log lines in s and returns the first one whose
// "method" and "path" fields match, or fails the test if none is found.
func lastLogLine(t *testing.T, s, method, path string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["method"] == method && entry["path"] == path {
			return entry
		}
	}
	t.Fatalf("no log line found for %s %s in:\n%s", method, path, s)
	return nil
}
