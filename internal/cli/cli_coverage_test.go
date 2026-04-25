package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alice/internal/app"
	"alice/internal/cli"
	"alice/internal/config"
	"alice/internal/httpapi"
	httptest "alice/internal/testhttptest"
)

// TestCLICoverageFlow exercises the CLI subcommands that the main
// TestCLIEndToEnd doesn't touch. The goal is breadth: each subcommand gets a
// happy-path call so the top-level dispatch + flag parsing + HTTP plumbing
// is exercised, pushing the commands.go coverage up. This catches
// flag-parsing regressions and argument-validation drift without requiring a
// bespoke test per subcommand.
func TestCLICoverageFlow(t *testing.T) {
	container, closeFn, err := app.NewContainer(config.Config{
		DefaultOrgName:   "CLI Coverage Org",
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
	orgSlug := "cli-cov-" + time.Now().UTC().Format("20060102150405.000000000")

	// Two admins? No — first registrant is admin, second is member. We want
	// alice as admin (policy, operator toggles) and bob as a peer.
	aliceOut := runOK(t, "alice register",
		"--server", srv.URL, "--state", aliceState, "--json",
		"register", "--server", srv.URL, "--org", orgSlug,
		"--email", "alice@example.com", "--agent", "alice-cli",
	)
	inviteToken := extractFirstInviteToken(aliceOut)

	bobArgs := []string{"--server", srv.URL, "--state", bobState, "--json",
		"register", "--server", srv.URL, "--org", orgSlug,
		"--email", "bob@example.com", "--agent", "bob-cli"}
	if inviteToken != "" {
		bobArgs = append(bobArgs, "--invite-token", inviteToken)
	}
	runOK(t, "bob register", bobArgs...)

	t.Run("whoami_text_does_not_leak_token", func(t *testing.T) {
		stdout, _, code := runCLI(t, "--state", aliceState, "whoami")
		if code != 0 {
			t.Fatalf("whoami exit %d", code)
		}
		for _, leak := range []string{"access_token", "private_key"} {
			if strings.Contains(stdout, leak+":") {
				t.Fatalf("whoami text leaked %s: %s", leak, stdout)
			}
		}
	})

	t.Run("result_fetches_completed_query", func(t *testing.T) {
		// Bob grants Alice, then publishes; Alice queries; result by ID.
		runOK(t, "bob grant", "--state", bobState, "--json",
			"grant", "--to", "alice@example.com",
			"--types", "summary,status_delta",
			"--sensitivity", "medium",
			"--purposes", "status_check",
		)
		runOK(t, "bob publish", "--state", bobState, "--json",
			"publish", "--type", "status_delta", "--title", "Focus",
			"--content", "Working on retries.", "--confidence", "0.9",
			"--sensitivity", "low", "--visibility", "explicit_grants_only",
		)
		queryOut := runOK(t, "alice query", "--state", aliceState, "--json",
			"query", "--to", "bob@example.com",
			"--purpose", "status_check",
			"--question", "What is bob up to?",
			"--types", "summary,status_delta",
			"--wait=false",
		)
		var qr map[string]any
		if err := json.Unmarshal([]byte(queryOut), &qr); err != nil {
			t.Fatalf("decode query: %v", err)
		}
		queryID, _ := qr["query_id"].(string)
		if queryID == "" {
			t.Fatalf("no query_id in %s", queryOut)
		}
		// Let the query complete (Evaluate is synchronous but poll once).
		resultOut := runOK(t, "result", "--state", aliceState, "--json", "result", queryID)
		if !strings.Contains(resultOut, "artifacts") {
			t.Fatalf("result missing artifacts: %s", resultOut)
		}
	})

	t.Run("peers_lists_after_grant", func(t *testing.T) {
		peersOut := runOK(t, "peers", "--state", aliceState, "--json", "peers")
		if !strings.Contains(peersOut, "bob@example.com") {
			t.Fatalf("peers should list bob: %s", peersOut)
		}
	})

	t.Run("outbox_lists_sent_requests", func(t *testing.T) {
		runOK(t, "send request", "--state", aliceState, "--json",
			"request", "--to", "bob@example.com",
			"--type", "ask_for_time", "--title", "Sync?",
			"--content", "30 min sync tomorrow.",
		)
		out := runOK(t, "outbox", "--state", aliceState, "--json",
			"outbox", "--limit", "10",
		)
		if !strings.Contains(out, "Sync?") {
			t.Fatalf("outbox missing request: %s", out)
		}
	})

	t.Run("respond_accepts_request", func(t *testing.T) {
		inboxOut := runOK(t, "inbox", "--state", bobState, "--json",
			"inbox", "--limit", "5",
		)
		var ib map[string]any
		if err := json.Unmarshal([]byte(inboxOut), &ib); err != nil {
			t.Fatalf("decode inbox: %v", err)
		}
		items, _ := ib["items"].([]any)
		if len(items) == 0 {
			t.Fatalf("inbox empty: %s", inboxOut)
		}
		firstID, _ := items[0].(map[string]any)["request_id"].(string)
		if firstID == "" {
			t.Fatalf("first item has no request_id: %v", items[0])
		}
		runOK(t, "respond",
			"--state", bobState, "--json",
			"respond", firstID, "--response", "accepted",
			"--message", "Will do.",
		)
	})

	t.Run("approvals_list_and_resolve", func(t *testing.T) {
		// Drive a require-approval path: Alice sends an ask_for_review,
		// Bob responds with require_approval, then lists and approves.
		runOK(t, "alice ask_for_review",
			"--state", aliceState, "--json",
			"request", "--to", "bob@example.com",
			"--type", "ask_for_review", "--title", "PR review",
			"--content", "Please review PR #42.",
		)
		inboxOut := runOK(t, "bob inbox", "--state", bobState, "--json", "inbox", "--limit", "20")
		var ib map[string]any
		_ = json.Unmarshal([]byte(inboxOut), &ib)
		items, _ := ib["items"].([]any)
		// Find the ask_for_review request.
		var reviewID string
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m["request_type"] == "ask_for_review" && m["state"] == "pending" {
				reviewID, _ = m["request_id"].(string)
				break
			}
		}
		if reviewID == "" {
			t.Fatalf("no pending ask_for_review in bob inbox: %s", inboxOut)
		}
		respOut := runOK(t, "bob require approval",
			"--state", bobState, "--json",
			"respond", reviewID, "--response", "require_approval",
			"--message", "Need to check",
		)
		var respMap map[string]any
		_ = json.Unmarshal([]byte(respOut), &respMap)
		approvalID, _ := respMap["approval_id"].(string)
		if approvalID == "" {
			t.Fatalf("no approval_id in respond output: %s", respOut)
		}

		// List approvals.
		listOut := runOK(t, "bob approvals",
			"--state", bobState, "--json",
			"approvals",
		)
		if !strings.Contains(listOut, approvalID) {
			t.Fatalf("approval list missing id %s: %s", approvalID, listOut)
		}

		// Approve it.
		runOK(t, "bob approve",
			"--state", bobState, "--json",
			"approve", approvalID,
		)

		// A second approval we deny — exercise the deny path too.
		runOK(t, "alice ask_for_review 2",
			"--state", aliceState, "--json",
			"request", "--to", "bob@example.com",
			"--type", "ask_for_review", "--title", "PR review 2",
			"--content", "Please review PR #43.",
		)
		inbox2 := runOK(t, "bob inbox 2", "--state", bobState, "--json", "inbox", "--limit", "20")
		_ = json.Unmarshal([]byte(inbox2), &ib)
		items2, _ := ib["items"].([]any)
		var review2 string
		for _, it := range items2 {
			m, _ := it.(map[string]any)
			if m["request_type"] == "ask_for_review" && m["title"] == "PR review 2" && m["state"] == "pending" {
				review2, _ = m["request_id"].(string)
				break
			}
		}
		if review2 == "" {
			t.Fatalf("no review 2 pending: %s", inbox2)
		}
		resp2 := runOK(t, "bob require approval 2",
			"--state", bobState, "--json",
			"respond", review2, "--response", "require_approval",
			"--message", "Blocking",
		)
		var resp2Map map[string]any
		_ = json.Unmarshal([]byte(resp2), &resp2Map)
		id2, _ := resp2Map["approval_id"].(string)
		runOK(t, "bob deny",
			"--state", bobState, "--json",
			"deny", id2,
		)
	})

	t.Run("policy_apply_history_activate", func(t *testing.T) {
		policyPath := filepath.Join(tmp, "policy.json")
		policy := `{
			"rules": [
				{"when": {"risk_level_at_least": "L3"}, "then": "require_approval"},
				{"when": {"risk_level_at_least": "L4"}, "then": "deny"}
			]
		}`
		if err := os.WriteFile(policyPath, []byte(policy), 0600); err != nil {
			t.Fatalf("write policy: %v", err)
		}
		applyOut := runOK(t, "alice policy apply",
			"--state", aliceState, "--json",
			"policy", "apply", "--file", policyPath, "--name", "baseline",
		)
		var applyMap map[string]any
		_ = json.Unmarshal([]byte(applyOut), &applyMap)
		p1, _ := applyMap["policy_id"].(string)
		if p1 == "" {
			t.Fatalf("no policy_id in apply output: %s", applyOut)
		}

		// Apply a second version, then roll back to v1.
		policy2Path := filepath.Join(tmp, "policy2.json")
		if err := os.WriteFile(policy2Path, []byte(`{"rules": [{"when": {}, "then": "allow"}]}`), 0600); err != nil {
			t.Fatalf("write policy2: %v", err)
		}
		runOK(t, "alice policy apply v2",
			"--state", aliceState, "--json",
			"policy", "apply", "--file", policy2Path, "--name", "empty",
		)

		histOut := runOK(t, "alice policy history",
			"--state", aliceState, "--json",
			"policy", "history", "--limit", "10",
		)
		if !strings.Contains(histOut, p1) {
			t.Fatalf("history missing original policy: %s", histOut)
		}

		runOK(t, "alice policy activate",
			"--state", aliceState, "--json",
			"policy", "activate", p1,
		)
	})

	t.Run("operator_toggle", func(t *testing.T) {
		enabledOut := runOK(t, "operator enable",
			"--state", aliceState, "--json",
			"operator", "enable",
		)
		if !strings.Contains(enabledOut, "operator_enabled") {
			t.Fatalf("enable output: %s", enabledOut)
		}
		runOK(t, "operator disable",
			"--state", aliceState, "--json",
			"operator", "disable",
		)
	})

	t.Run("audit_with_filters", func(t *testing.T) {
		// Both the --since (RFC3339) and --since (duration) branches, plus
		// --event-kind and --limit, should all parse.
		runOK(t, "audit since duration", "--state", aliceState, "--json",
			"audit", "--since", "24h",
		)
		runOK(t, "audit since rfc3339", "--state", aliceState, "--json",
			"audit", "--since", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		)
		runOK(t, "audit event-kind filter", "--state", aliceState, "--json",
			"audit", "--event-kind", "artifact.published", "--limit", "5",
		)
	})

	t.Run("revoke_grant", func(t *testing.T) {
		// Bob created a grant earlier; list sent grants… actually, revoke
		// needs the grant_id, which isn't exposed to Alice via `peers`. We
		// issue a fresh grant + capture + revoke.
		grantOut := runOK(t, "alice grant carol", "--state", aliceState, "--json",
			"grant", "--to", "bob@example.com",
			"--types", "summary",
			"--sensitivity", "low",
			"--purposes", "status_check",
		)
		var gr map[string]any
		_ = json.Unmarshal([]byte(grantOut), &gr)
		grantID, _ := gr["policy_grant_id"].(string)
		if grantID == "" {
			t.Fatalf("no policy_grant_id in grant: %s", grantOut)
		}
		runOK(t, "alice revoke grant",
			"--state", aliceState, "--json",
			"revoke", grantID,
		)
	})

	t.Run("actions_lifecycle", func(t *testing.T) {
		// Exercise operator actions — list is enough to hit the list branch,
		// which is the biggest uncovered piece of cmdActions.
		runOK(t, "alice actions list",
			"--state", aliceState, "--json",
			"actions", "list",
		)
	})

	t.Run("logout_clears_state_file", func(t *testing.T) {
		clone := filepath.Join(tmp, "clone.json")
		raw, err := os.ReadFile(aliceState)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if err := os.WriteFile(clone, raw, 0600); err != nil {
			t.Fatalf("write clone: %v", err)
		}
		runOK(t, "logout",
			"--state", clone, "--json", "logout",
		)
		if _, err := os.Stat(clone); !os.IsNotExist(err) {
			t.Fatalf("logout should remove state file; stat err = %v", err)
		}
	})
}

// TestCLIDispatchErrors covers the top-level error branches in Run: unknown
// subcommand, missing subcommand, help flag, and commands that require flags.
func TestCLIDispatchErrors(t *testing.T) {
	tmp := t.TempDir()
	stateFile := filepath.Join(tmp, "missing.json")

	// No args → usage on stderr with exit 2.
	stdout, stderr, code := runCLI(t)
	if code != 2 {
		t.Fatalf("no args exit = %d (stdout=%s stderr=%s)", code, stdout, stderr)
	}

	// --help → usage on stdout with exit 0.
	stdout, stderr, code = runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("--help exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "alice") {
		t.Fatalf("--help stdout should mention alice: %s", stdout)
	}

	// Unknown subcommand → exit 2.
	_, _, code = runCLI(t, "--state", stateFile, "bogus-subcommand")
	if code != 2 {
		t.Fatalf("unknown subcmd exit = %d", code)
	}

	// Publish with no flags → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "publish")
	if code == 0 {
		t.Fatalf("publish without flags should fail")
	}

	// Policy with no args → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "policy")
	if code == 0 {
		t.Fatalf("policy without args should fail")
	}

	// Policy with bogus sub → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "policy", "unknown")
	if code == 0 {
		t.Fatalf("policy unknown subcommand should fail")
	}

	// Operator with no args → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "operator")
	if code == 0 {
		t.Fatalf("operator without args should fail")
	}

	// Operator with bogus → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "operator", "toggle")
	if code == 0 {
		t.Fatalf("operator unknown subcommand should fail")
	}

	// Result without id → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "result")
	if code == 0 {
		t.Fatalf("result without id should fail")
	}

	// Revoke without id → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "revoke")
	if code == 0 {
		t.Fatalf("revoke without id should fail")
	}

	// Respond without id → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "respond")
	if code == 0 {
		t.Fatalf("respond without id should fail")
	}

	// Approve without id → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "approve")
	if code == 0 {
		t.Fatalf("approve without id should fail")
	}

	// Policy activate without id → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "policy", "activate")
	if code == 0 {
		t.Fatalf("policy activate without id should fail")
	}

	// Actions without subcommand → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "actions")
	if code == 0 {
		t.Fatalf("actions without subcommand should fail")
	}

	// Tuning with no flags → exit 1 (must pass at least one).
	_, _, code = runCLI(t, "--state", stateFile, "tuning")
	if code == 0 {
		t.Fatalf("tuning without flags should fail")
	}

	// Init in JSON mode without --server → exit 1.
	_, _, code = runCLI(t, "--state", stateFile, "--json", "init")
	if code == 0 {
		t.Fatalf("init in JSON mode without flags should fail")
	}

	// Global flags work when placed after the subcommand.
	// `whoami --state <path>` must not be treated as an unknown flag.
	stdout, _, code = runCLI(t, "whoami", "--state", stateFile)
	if code != 0 {
		// whoami exits 0 with "no active session" even without a session.
		t.Fatalf("whoami with --state after subcommand exit = %d, want 0 (stdout=%q)", code, stdout)
	}
}

// TestCLIAuthErrorPaths covers the "no session" branches each command enters
// before hitting the server. They all share the mustHaveSession guard and
// return a non-zero exit with a clear error.
func TestCLIAuthErrorPaths(t *testing.T) {
	tmp := t.TempDir()
	stateFile := filepath.Join(tmp, "missing.json")

	// whoami is intentionally session-tolerant — it prints "No active session"
	// and exits 0 so users without a session can still see what would be used.
	// All other commands hit the "not authenticated" guard.
	for _, argv := range [][]string{
		{"publish", "--title", "x", "--content", "y"},
		{"query", "--to", "a@b", "--question", "hi"},
		{"grant", "--to", "a@b", "--types", "summary", "--sensitivity", "low", "--purposes", "status_check"},
		{"revoke", "some-id"},
		{"peers"},
		{"request", "--to", "a@b", "--type", "question", "--title", "x", "--content", "y"},
		{"inbox"},
		{"outbox"},
		{"respond", "some-id"},
		{"approvals"},
		{"approve", "some-id"},
		{"audit"},
		{"tuning", "--confidence", "0.5"},
		{"policy", "history"},
		{"operator", "enable"},
		{"actions", "list"},
	} {
		t.Run(fmt.Sprintf("%v", argv), func(t *testing.T) {
			args := append([]string{"--state", stateFile}, argv...)
			_, _, code := runCLI(t, args...)
			if code == 0 {
				t.Fatalf("expected non-zero exit for %v with no session", argv)
			}
		})
	}
}

// TestCLIInitFullFlow drives cmdInit with all required flags so it takes the
// no-prompt branch. It also covers the "session already exists" refusal and
// the --force override.
func TestCLIInitFullFlow(t *testing.T) {
	container, closeFn, err := app.NewContainer(config.Config{
		DefaultOrgName:   "CLI Init Org",
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
	stateFile := filepath.Join(tmp, "alice.json")
	orgSlug := "cli-init-" + time.Now().UTC().Format("20060102150405.000000000")

	runOK(t, "init alice",
		"--server", srv.URL, "--state", stateFile, "--json",
		"init",
		"--server", srv.URL,
		"--org", orgSlug,
		"--email", "alice@example.com",
		"--agent", "alice-cli",
	)

	// Second init without --force must refuse.
	_, stderr, code := runCLI(t,
		"--server", srv.URL, "--state", stateFile, "--json",
		"init",
		"--server", srv.URL,
		"--org", orgSlug,
		"--email", "alice@example.com",
		"--agent", "alice-cli",
	)
	if code == 0 {
		t.Fatalf("init without --force should refuse existing session; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "session already exists") {
		t.Fatalf("expected session-exists error; stderr=%s", stderr)
	}

	// With --force it overwrites.
	_ = context.Background() // silence unused import guard when tests are cut
	runOK(t, "init force",
		"--server", srv.URL, "--state", stateFile, "--json",
		"init",
		"--server", srv.URL,
		"--org", orgSlug,
		"--email", "alice@example.com",
		"--agent", "alice-cli",
		"--force",
	)
}

// TestCLIStateEncryption verifies the opt-in AES-256-GCM encryption of the
// CLI state file via ALICE_ENCRYPT_STATE_KEY.
func TestCLIStateEncryption(t *testing.T) {
	tmp := t.TempDir()
	stateFile := filepath.Join(tmp, "state.json")

	original := cli.State{
		ServerURL:      "https://example.com",
		OrgSlug:        "testorg",
		OrgID:          "org-1",
		OwnerEmail:     "alice@example.com",
		AgentName:      "alice-cli",
		AgentID:        "agent-1",
		PublicKey:      "pubkey-base64",
		PrivateKey:     "super-secret-private-key",
		AccessToken:    "bearer-token-value",
		TokenExpiresAt: time.Now().UTC().Add(time.Hour).Round(time.Second),
	}

	t.Run("plaintext_default", func(t *testing.T) {
		t.Setenv(cli.EnvEncryptStateKey, "")
		if err := cli.SaveState(stateFile, original); err != nil {
			t.Fatalf("SaveState plaintext: %v", err)
		}
		raw, _ := os.ReadFile(stateFile)
		if !strings.Contains(string(raw), "super-secret-private-key") {
			t.Fatalf("expected plaintext private key in unencrypted file")
		}
		loaded, err := cli.LoadState(stateFile)
		if err != nil {
			t.Fatalf("LoadState plaintext: %v", err)
		}
		if loaded.PrivateKey != original.PrivateKey {
			t.Fatalf("private key mismatch: got %q want %q", loaded.PrivateKey, original.PrivateKey)
		}
		if loaded.AccessToken != original.AccessToken {
			t.Fatalf("access token mismatch: got %q want %q", loaded.AccessToken, original.AccessToken)
		}
	})

	t.Run("encrypted_hides_secrets", func(t *testing.T) {
		t.Setenv(cli.EnvEncryptStateKey, "my-secret-passphrase-for-testing")
		if err := cli.SaveState(stateFile, original); err != nil {
			t.Fatalf("SaveState encrypted: %v", err)
		}
		raw, _ := os.ReadFile(stateFile)
		rawStr := string(raw)
		if strings.Contains(rawStr, "super-secret-private-key") {
			t.Fatalf("private key must not appear in plaintext in encrypted file")
		}
		if strings.Contains(rawStr, "bearer-token-value") {
			t.Fatalf("access token must not appear in plaintext in encrypted file")
		}
		if !strings.Contains(rawStr, "encrypted_secrets") {
			t.Fatalf("encrypted file must contain encrypted_secrets block")
		}
		// JSON must have empty private_key / access_token fields.
		var raw2 map[string]any
		if err := json.Unmarshal([]byte(rawStr), &raw2); err != nil {
			t.Fatalf("json decode: %v", err)
		}
		if v, _ := raw2["private_key"].(string); v != "" {
			t.Fatalf("private_key field must be empty in encrypted file, got %q", v)
		}
		if v, _ := raw2["access_token"].(string); v != "" {
			t.Fatalf("access_token field must be empty in encrypted file, got %q", v)
		}
	})

	t.Run("decrypt_with_correct_key", func(t *testing.T) {
		t.Setenv(cli.EnvEncryptStateKey, "my-secret-passphrase-for-testing")
		if err := cli.SaveState(stateFile, original); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		loaded, err := cli.LoadState(stateFile)
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if loaded.PrivateKey != original.PrivateKey {
			t.Fatalf("private key: got %q want %q", loaded.PrivateKey, original.PrivateKey)
		}
		if loaded.AccessToken != original.AccessToken {
			t.Fatalf("access token: got %q want %q", loaded.AccessToken, original.AccessToken)
		}
		if loaded.OwnerEmail != original.OwnerEmail {
			t.Fatalf("owner email: got %q want %q", loaded.OwnerEmail, original.OwnerEmail)
		}
	})

	t.Run("load_encrypted_without_key_fails", func(t *testing.T) {
		t.Setenv(cli.EnvEncryptStateKey, "my-secret-passphrase-for-testing")
		if err := cli.SaveState(stateFile, original); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		t.Setenv(cli.EnvEncryptStateKey, "")
		_, err := cli.LoadState(stateFile)
		if err == nil {
			t.Fatalf("expected error loading encrypted file without key")
		}
		if !strings.Contains(err.Error(), cli.EnvEncryptStateKey) {
			t.Fatalf("error should mention env var name: %v", err)
		}
	})

	t.Run("load_encrypted_with_wrong_key_fails", func(t *testing.T) {
		t.Setenv(cli.EnvEncryptStateKey, "correct-key")
		if err := cli.SaveState(stateFile, original); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		t.Setenv(cli.EnvEncryptStateKey, "wrong-key")
		_, err := cli.LoadState(stateFile)
		if err == nil {
			t.Fatalf("expected error loading encrypted file with wrong key")
		}
	})

	t.Run("missing_file_returns_empty_state", func(t *testing.T) {
		t.Setenv(cli.EnvEncryptStateKey, "")
		missing := filepath.Join(tmp, "does-not-exist.json")
		state, err := cli.LoadState(missing)
		if err != nil {
			t.Fatalf("expected no error for missing file: %v", err)
		}
		if state.HasSession() {
			t.Fatalf("missing file should return empty state")
		}
	})
}
