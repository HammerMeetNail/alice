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
	srv := httptest.NewServer(httpapi.NewRouter(httpapi.RouterOptions{Services: container}))
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

	// --- alice sends a fresh ask_for_time request that won't be auto-answered
	//     (ineligible type) and bob watches his inbox; the watch loop must
	//     surface the new request and return cleanly when the context is
	//     cancelled. ---
	runOK(t, "alice request ask_for_time",
		"--state", aliceState, "--json",
		"request",
		"--to", "bob@example.com",
		"--type", "ask_for_time",
		"--title", "15 minutes tomorrow?",
		"--content", "Planning chat.",
	)

	watchCtx, watchCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer watchCancel()
	var watchOut, watchErr bytes.Buffer
	watchCode := cli.Run(watchCtx,
		[]string{"--state", bobState, "inbox", "--watch", "--interval", "1s"},
		strings.NewReader(""), &watchOut, &watchErr)
	if watchCode != 0 {
		t.Fatalf("inbox --watch failed (code=%d): stdout=%s stderr=%s", watchCode, watchOut.String(), watchErr.String())
	}
	if !strings.Contains(watchOut.String(), "Incoming requests (watching") {
		t.Fatalf("expected watch-mode banner, got: %s", watchOut.String())
	}
	if !strings.Contains(watchOut.String(), "15 minutes tomorrow?") {
		t.Fatalf("expected pending request surfaced by --watch, got: %s", watchOut.String())
	}
}

// TestCLITuning exercises `alice tuning` end-to-end, confirming the per-org
// override plumbing survives the full CLI → HTTP → service → storage round
// trip. Alice is the first registrant so she is admin; Bob is a member whose
// call must fail with an authorization error.
func TestCLITuning(t *testing.T) {
	container, closeFn, err := app.NewContainer(config.Config{
		DefaultOrgName:   "CLI Tuning Org",
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("build app container: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	srv := httptest.NewServer(httpapi.NewRouter(httpapi.RouterOptions{Services: container}))
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	aliceState := filepath.Join(tmp, "alice.json")
	bobState := filepath.Join(tmp, "bob.json")
	orgSlug := "cli-tuning-" + time.Now().UTC().Format("20060102150405.000000000")

	aliceRegisterOut := runOK(t, "alice register",
		"--server", srv.URL, "--state", aliceState, "--json",
		"register",
		"--server", srv.URL,
		"--org", orgSlug,
		"--email", "alice@example.com",
		"--agent", "alice-cli",
	)
	inviteToken := extractFirstInviteToken(aliceRegisterOut)

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

	// Admin path: alice sets both overrides.
	out := runOK(t, "alice tuning set",
		"--state", aliceState, "--json",
		"tuning",
		"--confidence", "0.8",
		"--lookback", "168h",
	)
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode tuning response: %v, raw=%s", err, out)
	}
	if got, _ := payload["confidence_threshold"].(float64); got != 0.8 {
		t.Fatalf("expected confidence_threshold=0.8 in CLI output, got %v", payload["confidence_threshold"])
	}
	if payload["lookback_window"] != "168h0m0s" {
		t.Fatalf("expected lookback_window=168h0m0s, got %v", payload["lookback_window"])
	}

	// Clear reverts to server defaults.
	out = runOK(t, "alice tuning clear",
		"--state", aliceState, "--json",
		"tuning", "--clear",
	)
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode clear response: %v, raw=%s", err, out)
	}
	if v, ok := payload["confidence_threshold"].(string); !ok || v != "(server default)" {
		t.Fatalf("expected confidence_threshold=(server default), got %v", payload["confidence_threshold"])
	}

	// Non-admin: bob's call must fail.
	stdout, stderr, code := runCLI(t,
		"--state", bobState, "--json",
		"tuning", "--confidence", "0.7",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit for non-admin tuning call: stdout=%s stderr=%s", stdout, stderr)
	}

	// No flags → require at least one of --confidence / --lookback / --clear.
	stdout, stderr, code = runCLI(t,
		"--state", aliceState, "--json",
		"tuning",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit for empty tuning call: stdout=%s stderr=%s", stdout, stderr)
	}
}

// TestCLIOrgGraph exercises `alice team` and `alice manager` end-to-end.
// Alice registers first in a fresh org so she auto-acquires the `admin`
// role; Bob registers second as a plain member. The test verifies:
//   - non-admin can list teams but can't create/add-member
//   - admin can create a team, add a member, and list members
//   - admin can assign and revoke a manager edge and read the chain back
func TestCLIOrgGraph(t *testing.T) {
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
	srv := httptest.NewServer(httpapi.NewRouter(httpapi.RouterOptions{Services: container}))
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	aliceState := filepath.Join(tmp, "alice.json")
	bobState := filepath.Join(tmp, "bob.json")
	carolState := filepath.Join(tmp, "carol.json")
	orgSlug := "cli-orggraph-" + time.Now().UTC().Format("20060102150405.000000000")

	aliceOut := runOK(t, "alice register",
		"--server", srv.URL, "--state", aliceState, "--json",
		"register", "--server", srv.URL, "--org", orgSlug,
		"--email", "alice@example.com", "--agent", "alice-cli",
	)
	inviteToken := extractFirstInviteToken(aliceOut)

	for state, email := range map[string]string{bobState: "bob@example.com", carolState: "carol@example.com"} {
		args := []string{"--server", srv.URL, "--state", state, "--json",
			"register", "--server", srv.URL, "--org", orgSlug,
			"--email", email, "--agent", email + "-cli"}
		if inviteToken != "" {
			args = append(args, "--invite-token", inviteToken)
		}
		runOK(t, "register "+email, args...)
	}

	// Admin creates a team and captures the id.
	createOut := runOK(t, "team create", "--state", aliceState, "--json",
		"team", "create", "--name", "eng")
	var created map[string]any
	if err := json.Unmarshal([]byte(createOut), &created); err != nil {
		t.Fatalf("decode create: %v body=%s", err, createOut)
	}
	teamID, _ := created["team_id"].(string)
	if teamID == "" {
		t.Fatalf("expected team_id in create output, got %s", createOut)
	}

	// Non-admin Bob can't create a team.
	_, _, code := runCLI(t, "--state", bobState, "--json",
		"team", "create", "--name", "bob-team")
	if code == 0 {
		t.Fatal("expected non-zero exit for non-admin team create")
	}

	// Non-admin can list teams.
	listOut := runOK(t, "team list", "--state", bobState, "--json",
		"team", "list")
	if !strings.Contains(listOut, teamID) {
		t.Fatalf("expected team list to include %s, got %s", teamID, listOut)
	}

	// Admin adds Bob and Carol to the team.
	runOK(t, "team add-member bob", "--state", aliceState, "--json",
		"team", "add-member", "--team", teamID, "--email", "bob@example.com")
	runOK(t, "team add-member carol", "--state", aliceState, "--json",
		"team", "add-member", "--team", teamID, "--email", "carol@example.com", "--role", "lead")

	// Non-admin can't add.
	_, _, code = runCLI(t, "--state", bobState, "--json",
		"team", "add-member", "--team", teamID, "--email", "alice@example.com")
	if code == 0 {
		t.Fatal("expected non-zero exit for non-admin add-member")
	}

	// Members list reflects the adds.
	membersOut := runOK(t, "team members", "--state", aliceState, "--json",
		"team", "members", teamID)
	var membersPayload map[string]any
	if err := json.Unmarshal([]byte(membersOut), &membersPayload); err != nil {
		t.Fatalf("decode members: %v body=%s", err, membersOut)
	}
	items, _ := membersPayload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 members, got %d in %s", len(items), membersOut)
	}

	// Admin can remove a member.
	runOK(t, "team remove-member carol", "--state", aliceState, "--json",
		"team", "remove-member", "--team", teamID, "--email", "carol@example.com")

	// Manager edges: Alice manages Bob.
	runOK(t, "manager set", "--state", aliceState, "--json",
		"manager", "set", "--user", "bob@example.com", "--manager", "alice@example.com")

	// Reading the chain returns a non-empty walk.
	chainOut := runOK(t, "manager chain", "--state", aliceState, "--json",
		"manager", "chain", "--user", "bob@example.com")
	if !strings.Contains(chainOut, "manager_user_id") {
		t.Fatalf("expected chain to include an edge, got %s", chainOut)
	}

	// Non-admin can't revoke.
	_, _, code = runCLI(t, "--state", bobState, "--json",
		"manager", "revoke", "--user", "bob@example.com")
	if code == 0 {
		t.Fatal("expected non-zero exit for non-admin manager revoke")
	}

	// Admin revokes cleanly.
	runOK(t, "manager revoke", "--state", aliceState, "--json",
		"manager", "revoke", "--user", "bob@example.com")

	// Missing required flags surface as non-zero exits.
	for _, tc := range [][]string{
		{"team", "create"},
		{"team", "add-member", "--team", teamID},
		{"manager", "set", "--user", "bob@example.com"},
	} {
		_, _, code := runCLI(t, append([]string{"--state", aliceState, "--json"}, tc...)...)
		if code == 0 {
			t.Fatalf("expected missing-flag exit for %v", tc)
		}
	}

	// Admin can delete the team.
	runOK(t, "team delete", "--state", aliceState, "--json",
		"team", "delete", teamID)
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
