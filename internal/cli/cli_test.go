package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alice/internal/app"
	"alice/internal/cli"
	"alice/internal/config"
	"alice/internal/httpapi"
)

// TestCLIEndToEnd drives the CLI against an in-process coordination server to
// verify the register → grant → publish → query flow two users would see on a
// real deployment. This is the smoke test that proves the CLI can do what
// the README's two-machine demo claims.
func TestCLIEndToEnd(t *testing.T) {
	container, closeFn, err := app.NewContainer(config.Config{
		DefaultOrgName:   "CLI Dev Org",
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("build app container: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	srv := httptest.NewServer(httpapi.NewRouter(container))
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	aliceState := filepath.Join(tmp, "alice.json")
	bobState := filepath.Join(tmp, "bob.json")
	orgSlug := "cli-demo-" + time.Now().UTC().Format("20060102150405.000000000")

	// --- alice registers ---
	aliceRegisterOut := runOK(t, "alice register",
		"--server", srv.URL, "--state", aliceState, "--json",
		"register",
		"--server", srv.URL,
		"--org", orgSlug,
		"--email", "alice@example.com",
		"--agent", "alice-cli",
	)

	// --- alice's register JSON must carry first_invite_token so downstream
	//     automation can capture it. Every new org gets a fresh token on the
	//     first registration, regardless of verification mode. ---
	inviteToken := extractFirstInviteToken(aliceRegisterOut)
	if inviteToken == "" {
		t.Fatalf("expected first_invite_token in alice register JSON, got: %s", aliceRegisterOut)
	}

	bobArgs := []string{
		"--server", srv.URL, "--state", bobState, "--json",
		"register",
		"--server", srv.URL,
		"--org", orgSlug,
		"--email", "bob@example.com",
		"--agent", "bob-cli",
	}
	if inviteToken != "" {
		bobArgs = append(bobArgs, "--invite-token", inviteToken)
	}
	runOK(t, "bob register", bobArgs...)

	// --- bob grants alice permission to query his status ---
	runOK(t, "bob grants alice",
		"--state", bobState, "--json",
		"grant",
		"--to", "alice@example.com",
		"--types", "summary,status_delta",
		"--sensitivity", "medium",
		"--purposes", "status_check,dependency_check",
	)

	// --- bob publishes a status artifact ---
	runOK(t, "bob publish",
		"--state", bobState, "--json",
		"publish",
		"--type", "status_delta",
		"--title", "Auth refactor",
		"--content", "Extracting JWT validation. Two PRs open.",
		"--sensitivity", "low",
		"--visibility", "explicit_grants_only",
		"--confidence", "0.9",
	)

	// --- alice queries bob ---
	stdout, stderr, code := runCLI(t,
		"--state", aliceState, "--json",
		"query",
		"--to", "bob@example.com",
		"--purpose", "status_check",
		"--question", "What is bob working on?",
		"--types", "summary,status_delta",
	)
	if code != 0 {
		t.Fatalf("alice query failed (code=%d): stdout=%s stderr=%s", code, stdout, stderr)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode query result: %v\noutput=%s", err, stdout)
	}
	state, _ := result["state"].(string)
	if state != "completed" {
		t.Fatalf("expected completed state, got %q, result=%v", state, result)
	}
	response, _ := result["response"].(map[string]any)
	artifacts, _ := response["artifacts"].([]any)
	if len(artifacts) == 0 {
		t.Fatalf("expected at least one artifact, response=%v", response)
	}

	// --- alice sends bob a question-type request; the gatekeeper should
	//     auto-answer from bob's existing status_delta artifact ---
	stdout, stderr, code = runCLI(t,
		"--state", aliceState, "--json",
		"request",
		"--to", "bob@example.com",
		"--type", "question",
		"--title", "What is bob working on?",
		"--content", "Need a quick status read before the planning call.",
	)
	if code != 0 {
		t.Fatalf("alice request failed: stdout=%s stderr=%s", stdout, stderr)
	}
	var reqResult map[string]any
	if err := json.Unmarshal([]byte(stdout), &reqResult); err != nil {
		t.Fatalf("decode request result: %v\noutput=%s", err, stdout)
	}
	if state, _ := reqResult["state"].(string); state != "auto_answered" {
		t.Fatalf("expected auto_answered state, got %q, result=%v", state, reqResult)
	}
	if msg, _ := reqResult["response_message"].(string); !strings.Contains(msg, "Auto-answered") {
		t.Fatalf("expected auto-answer response_message, got %q", msg)
	}

	// --- bob's audit trail should carry the auto-answer event ---
	stdout, stderr, code = runCLI(t, "--state", bobState, "--json", "audit")
	if code != 0 {
		t.Fatalf("bob audit failed: stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "request.auto_answered") {
		t.Fatalf("expected bob's audit log to contain request.auto_answered event, got: %s", stdout)
	}

	// --- whoami must not leak the private key or token in human-readable mode ---
	stdout, _, code = runCLI(t, "--state", aliceState, "whoami")
	if code != 0 {
		t.Fatalf("whoami failed: %s", stdout)
	}
	for _, secret := range []string{"private_key", "access_token"} {
		if strings.Contains(stdout, secret+":") {
			t.Fatalf("whoami leaked %s in text output: %s", secret, stdout)
		}
	}
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func runOK(t *testing.T, label string, args ...string) string {
	t.Helper()
	stdout, stderr, code := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("%s failed (code=%d): stdout=%s stderr=%s", label, code, stdout, stderr)
	}
	return stdout
}

// extractFirstInviteToken parses the JSON stdout of `alice register --json`
// and returns the `first_invite_token` field when the server issued one on
// first registration. Returns empty string for subsequent registrants or for
// orgs whose verification mode does not emit a token.
func extractFirstInviteToken(stdout string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return ""
	}
	if tok, ok := payload["first_invite_token"].(string); ok {
		return tok
	}
	return ""
}

// Compile-time use of fmt and os to keep the import tree stable when
// additional diagnostics are added later.
var _ = fmt.Sprintf
var _ = os.Getenv
