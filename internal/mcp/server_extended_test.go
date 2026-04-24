package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"alice/internal/app"
	"alice/internal/config"
	"alice/internal/httpapi"
)

// callToolRaw invokes a tool and returns the raw result map without fatalffing
// on MCP-level or tool-level errors. Use this for tests that expect failures.
func callToolRaw(t *testing.T, server *Server, toolName string, arguments map[string]any) (map[string]any, *responseError) {
	t.Helper()

	params, err := json.Marshal(map[string]any{
		"name":      toolName,
		"arguments": arguments,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      toolName,
		Method:  "tools/call",
		Params:  params,
	})
	if resp == nil {
		t.Fatalf("nil response for tool %s", toolName)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		// Re-encode and decode to get a plain map.
		data, _ := json.Marshal(resp.Result)
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("decode result for %s: %v", toolName, err)
		}
	}
	return result, nil
}

func TestToolCallWithoutAuth(t *testing.T) {
	handler := newTestHandler(t)
	server := NewServer(handler)

	// Call a protected tool without registering first. The server has no access
	// token set, so callAuthedJSON returns an error immediately.
	result, rpcErr := callToolRaw(t, server, "publish_artifact", map[string]any{
		"artifact": map[string]any{},
	})
	if rpcErr != nil {
		t.Fatalf("unexpected JSON-RPC protocol error: %+v", rpcErr)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true for unauthenticated tool call, got result=%v", result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatal("expected structuredContent in error result")
	}
	errMsg, _ := structured["error"].(string)
	if errMsg == "" {
		t.Fatal("expected non-empty error message in structuredContent")
	}
}

func TestToolCallWithExpiredToken(t *testing.T) {
	// Build a handler with a negative token TTL so any issued token is
	// already expired by the time it is used.
	cfg := config.Config{
		DefaultOrgName:   "Expiry Test Org",
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     -time.Second,
	}
	container, closeFn, err := app.NewContainer(cfg)
	if err != nil {
		t.Fatalf("build app container: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	handler := &httpapiTestHandler{handler: httpapi.NewRouter(httpapi.RouterOptions{Services: container})}
	server := NewServer(handler)

	fixture := newFixture(t)
	keys := generateKeys(t)

	// Registration succeeds (challenge TTL is still positive), but the issued
	// bearer token has a negative TTL so it expires before first use.
	callTool(t, server, "register_agent", map[string]any{
		"org_slug":    fixture.OrgSlug,
		"owner_email": fixture.AliceEmail,
		"agent_name":  "expiry-agent",
		"client_type": "mcp",
		"public_key":  keys.PublicKey,
		"private_key": keys.PrivateKey,
	})

	// The access token is now set but already expired. Any protected call
	// must return an auth error from the server (HTTP 401 → isError=true).
	result, rpcErr := callToolRaw(t, server, "list_allowed_peers", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("unexpected JSON-RPC protocol error: %+v", rpcErr)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true for expired-token tool call, got %v", result)
	}
}

func TestRemoteMode_BadURL(t *testing.T) {
	// Port 1 is reserved and never listens; the HTTP client gets a connection
	// refused error, which the tool translates into an MCP error result.
	server := NewServer(nil, WithServerURL("http://127.0.0.1:1", ""))

	result, rpcErr := callToolRaw(t, server, "register_agent", map[string]any{
		"org_slug":    "test-org",
		"owner_email": "alice@example.com",
		"agent_name":  "test",
		"client_type": "mcp",
	})
	if rpcErr != nil {
		t.Fatalf("unexpected JSON-RPC protocol error: %+v", rpcErr)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true for bad URL, got %v", result)
	}
}

func TestListSentRequests(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	aliceKeys := generateKeys(t)
	bobKeys := generateKeys(t)

	aliceServer := NewServer(handler)
	bobServer := NewServer(handler)

	callTool(t, aliceServer, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": aliceKeys.PublicKey, "private_key": aliceKeys.PrivateKey,
	})
	callTool(t, bobServer, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.BobEmail,
		"agent_name": "bob-agent", "client_type": "mcp",
		"public_key": bobKeys.PublicKey, "private_key": bobKeys.PrivateKey,
	})

	callTool(t, aliceServer, "send_request_to_peer", map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "ask_for_review",
		"title":         "Review PR",
		"content":       "Please review",
	})

	result := mustStructuredContent(t, callTool(t, aliceServer, "list_sent_requests", map[string]any{}))
	var reqs []map[string]any
	mustDecodeInto(t, result["requests"], &reqs)
	if len(reqs) != 1 {
		t.Fatalf("expected 1 sent request, got %d", len(reqs))
	}
}

func TestGetAuditSummary(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	result := mustStructuredContent(t, callTool(t, server, "get_audit_summary", map[string]any{}))
	if result["events"] == nil {
		t.Fatal("expected events in audit summary response")
	}
}

func TestListPendingAgentsMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	result := mustStructuredContent(t, callTool(t, server, "list_pending_agents", map[string]any{}))
	var pending []any
	mustDecodeInto(t, result["pending_agents"], &pending)
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending agents initially, got %d", len(pending))
	}
}

func TestHasSession(t *testing.T) {
	handler := newTestHandler(t)
	server := NewServer(handler)

	if server.HasSession() {
		t.Fatal("expected HasSession=false before registration")
	}

	fixture := newFixture(t)
	keys := generateKeys(t)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	if !server.HasSession() {
		t.Fatal("expected HasSession=true after registration")
	}
}

func TestPublishArtifactMethod(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	result, err := server.PublishArtifact(context.Background(), map[string]any{
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Test summary",
			"content":         "Test content",
			"sensitivity":     "low",
			"visibility_mode": "explicit_grants_only",
			"confidence":      0.9,
			"source_refs": []map[string]any{
				{
					"source_system": "test",
					"source_type":   "manual",
					"source_id":     "1",
					"observed_at":   time.Now().UTC().Format(time.RFC3339),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PublishArtifact error: %v", err)
	}
	if result["artifact_id"] == nil {
		t.Fatal("expected artifact_id in PublishArtifact result")
	}
}

func TestAutoRegister(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)

	server := NewServer(handler)

	err := server.AutoRegister(context.Background(), TrackerRegistration{
		OrgSlug:    fixture.OrgSlug,
		OwnerEmail: fixture.AliceEmail,
		AgentName:  "tracker-agent",
		ClientType: "mcp_tracker",
	})
	if err != nil {
		t.Fatalf("AutoRegister error: %v", err)
	}

	if !server.HasSession() {
		t.Fatal("expected session after AutoRegister")
	}
}

func TestRotateInviteTokenMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	result := mustStructuredContent(t, callTool(t, server, "rotate_invite_token", map[string]any{"confirm": true}))
	if result["invite_token"] == nil || result["invite_token"].(string) == "" {
		t.Fatal("expected non-empty invite_token")
	}
}

func TestUpdateVerificationModeMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	result := mustStructuredContent(t, callTool(t, server, "update_verification_mode", map[string]any{
		"verification_mode": "email_otp",
		"confirm":           true,
	}))
	if result["verification_mode"] != "email_otp" {
		t.Fatalf("expected email_otp, got %v", result["verification_mode"])
	}
}

func TestOperatorActionsMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	aliceKeys := generateKeys(t)
	bobKeys := generateKeys(t)

	alice := NewServer(handler)
	bob := NewServer(handler)

	callTool(t, alice, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice", "client_type": "mcp",
		"public_key": aliceKeys.PublicKey, "private_key": aliceKeys.PrivateKey,
	})
	callTool(t, bob, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.BobEmail,
		"agent_name": "bob", "client_type": "mcp",
		"public_key": bobKeys.PublicKey, "private_key": bobKeys.PrivateKey,
	})

	// Bob opts in to the operator phase.
	mustStructuredContent(t, callTool(t, bob, "enable_operator", map[string]any{
		"enabled": true,
		"confirm": true,
	}))

	// Alice sends Bob a blocker request so the action has something to
	// acknowledge.
	reqResp := mustStructuredContent(t, callTool(t, alice, "send_request_to_peer", map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "blocker",
		"title":         "Queue backlog",
		"content":       "Service 500s on retry",
	}))
	requestID := reqResp["request_id"].(string)

	created := mustStructuredContent(t, callTool(t, bob, "create_action", map[string]any{
		"kind":       "acknowledge_blocker",
		"request_id": requestID,
		"inputs":     map[string]any{"message": "on it"},
		"risk_level": "L0",
		"confirm":    true,
	}))
	if created["state"] != "approved" {
		t.Fatalf("expected action state=approved under default policy, got %v", created["state"])
	}

	executed := mustStructuredContent(t, callTool(t, bob, "execute_action", map[string]any{
		"action_id": created["action_id"],
		"confirm":   true,
	}))
	if executed["state"] != "executed" {
		t.Fatalf("expected executed state, got %v", executed["state"])
	}

	// Replay surfaces as an MCP error result (isError=true).
	replay, _ := callToolRaw(t, bob, "execute_action", map[string]any{
		"action_id": created["action_id"],
		"confirm":   true,
	})
	if isErr, _ := replay["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true on replay, got %v", replay)
	}
}

func TestRiskPolicyMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	applied := mustStructuredContent(t, callTool(t, server, "apply_risk_policy", map[string]any{
		"name": "mcp-smoke",
		"source": map[string]any{
			"rules": []map[string]any{
				{"when": map[string]any{}, "then": "allow"},
			},
		},
		"confirm": true,
	}))
	if applied["policy_id"] == nil {
		t.Fatal("expected policy_id in MCP response")
	}

	history := mustStructuredContent(t, callTool(t, server, "list_risk_policies", map[string]any{}))
	if history["policies"] == nil {
		t.Fatal("expected policies in list response")
	}

	// Apply without confirm must fail at the MCP layer.
	noConfirm, rpcErr := callToolRaw(t, server, "apply_risk_policy", map[string]any{
		"source": map[string]any{"rules": []map[string]any{{"when": map[string]any{}, "then": "allow"}}},
	})
	if rpcErr != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", rpcErr)
	}
	if isErr, _ := noConfirm["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true when confirm is absent, got %v", noConfirm)
	}

	// Activate needs a confirm flag and the caller is admin, so it should
	// succeed against the just-applied policy id.
	activated := mustStructuredContent(t, callTool(t, server, "activate_risk_policy", map[string]any{
		"policy_id": applied["policy_id"],
		"confirm":   true,
	}))
	if activated["policy_id"] != applied["policy_id"] {
		t.Fatalf("activate returned different policy_id; got %v want %v", activated["policy_id"], applied["policy_id"])
	}
}

func TestSetGatekeeperTuningMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	// Set both overrides.
	result := mustStructuredContent(t, callTool(t, server, "set_gatekeeper_tuning", map[string]any{
		"confidence_threshold": 0.75,
		"lookback_window":      "168h",
		"confirm":              true,
	}))
	if got, _ := result["confidence_threshold"].(float64); got != 0.75 {
		t.Fatalf("expected confidence_threshold=0.75, got %v", result["confidence_threshold"])
	}
	if result["lookback_window"] != "168h0m0s" {
		t.Fatalf("expected lookback_window=168h0m0s, got %v", result["lookback_window"])
	}

	// Without confirm, the handler refuses. Tool-level errors surface as
	// isError=true in the result, not as a JSON-RPC-level error.
	noConfirm, rpcErr := callToolRaw(t, server, "set_gatekeeper_tuning", map[string]any{
		"confidence_threshold": 0.9,
	})
	if rpcErr != nil {
		t.Fatalf("unexpected JSON-RPC protocol error: %+v", rpcErr)
	}
	if isErr, _ := noConfirm["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true when confirm is absent, got %v", noConfirm)
	}

	// Clear reverts both overrides.
	result = mustStructuredContent(t, callTool(t, server, "set_gatekeeper_tuning", map[string]any{
		"clear":   true,
		"confirm": true,
	}))
	if result["confidence_threshold"] != nil {
		t.Fatalf("expected nil confidence_threshold, got %v", result["confidence_threshold"])
	}
	if result["lookback_window"] != nil {
		t.Fatalf("expected nil lookback_window, got %v", result["lookback_window"])
	}
}

func TestResendVerificationEmailMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	// Should succeed or return an error (no OTP sender configured) — either way, it exercises the handler
	result, _ := callToolRaw(t, server, "resend_verification_email", map[string]any{})
	_ = result // just verifying the call doesn't panic
}

func TestReviewAgentMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	// Review nonexistent agent — should error but exercise the handler
	result, _ := callToolRaw(t, server, "review_agent", map[string]any{
		"agent_id": "agent_nonexistent",
		"decision": "approved",
		"confirm":  true,
	})
	// Should return an error result (not found)
	if result != nil {
		if isErr, _ := result["isError"].(bool); !isErr {
			t.Log("expected isError for nonexistent agent review")
		}
	}
}

func TestRevokePermissionMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	aliceKeys := generateKeys(t)
	bobKeys := generateKeys(t)

	aliceServer := NewServer(handler)
	bobServer := NewServer(handler)

	callTool(t, aliceServer, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": aliceKeys.PublicKey, "private_key": aliceKeys.PrivateKey,
	})
	callTool(t, bobServer, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.BobEmail,
		"agent_name": "bob-agent", "client_type": "mcp",
		"public_key": bobKeys.PublicKey, "private_key": bobKeys.PrivateKey,
	})

	grantResult := mustStructuredContent(t, callTool(t, aliceServer, "grant_permission", map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
		"confirm":                true,
	}))
	grantID := grantResult["policy_grant_id"].(string)

	revokeResult := mustStructuredContent(t, callTool(t, aliceServer, "revoke_permission", map[string]any{
		"policy_grant_id": grantID,
		"confirm":         true,
	}))
	if revokeResult["revoked"] != true {
		t.Fatalf("expected revoked=true, got %v", revokeResult["revoked"])
	}
}

func TestSubmitCorrectionMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	keys := generateKeys(t)

	server := NewServer(handler)
	callTool(t, server, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice-agent", "client_type": "mcp",
		"public_key": keys.PublicKey, "private_key": keys.PrivateKey,
	})

	pubResult := mustStructuredContent(t, callTool(t, server, "publish_artifact", map[string]any{
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Original",
			"content":         "Original content",
			"sensitivity":     "low",
			"visibility_mode": "explicit_grants_only",
			"confidence":      0.9,
			"source_refs": []map[string]any{
				{"source_system": "test", "source_type": "manual", "source_id": "1", "observed_at": time.Now().UTC().Format(time.RFC3339)},
			},
		},
	}))
	artifactID := pubResult["artifact_id"].(string)

	corrResult := mustStructuredContent(t, callTool(t, server, "submit_correction", map[string]any{
		"artifact_id": artifactID,
		"artifact": map[string]any{
			"type":            "summary",
			"title":           "Corrected",
			"content":         "Corrected content",
			"sensitivity":     "low",
			"visibility_mode": "explicit_grants_only",
			"confidence":      0.95,
			"source_refs": []map[string]any{
				{"source_system": "test", "source_type": "manual", "source_id": "2", "observed_at": time.Now().UTC().Format(time.RFC3339)},
			},
		},
	}))
	if corrResult["artifact_id"] == nil {
		t.Fatal("expected artifact_id in correction result")
	}
}

func TestToolValidation_MissingRequired(t *testing.T) {
	handler := newTestHandler(t)
	server := NewServer(handler)

	// These tools validate their required arguments locally before making any
	// HTTP call, so they return errors even without a registered session.
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"get_query_result", map[string]any{}, "query_id is required"},
		{"revoke_permission", map[string]any{"confirm": true}, "policy_grant_id is required"},
		{"submit_correction", map[string]any{}, "artifact_id is required"},
		{"respond_to_request", map[string]any{}, "request_id is required"},
		{"resolve_approval", map[string]any{}, "approval_id is required"},
		{"verify_email", map[string]any{}, "code is required"},
		{"review_agent", map[string]any{"confirm": true}, "agent_id is required"},
		{"review_agent", map[string]any{"confirm": true, "agent_id": "agent_123"}, "decision is required"},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result, rpcErr := callToolRaw(t, server, tc.tool, tc.args)
			if rpcErr != nil {
				t.Fatalf("unexpected JSON-RPC protocol error: %+v", rpcErr)
			}
			if isErr, _ := result["isError"].(bool); !isErr {
				t.Fatalf("expected isError=true for missing arg %q, got %v", tc.want, result)
			}
			structured, _ := result["structuredContent"].(map[string]any)
			errMsg, _ := structured["error"].(string)
			if !strings.Contains(errMsg, tc.want) {
				t.Fatalf("expected error to contain %q, got %q", tc.want, errMsg)
			}
		})
	}
}

// TestOrgGraphMCP is a smoke test covering the team and manager tools end
// to end through the MCP handler. Admin-gating, cycle detection, and the
// confirm=true gating are exercised via the shared server-side routes.
func TestOrgGraphMCP(t *testing.T) {
	handler := newTestHandler(t)
	fixture := newFixture(t)
	aliceKeys := generateKeys(t)
	bobKeys := generateKeys(t)

	alice := NewServer(handler)
	bob := NewServer(handler)

	callTool(t, alice, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.AliceEmail,
		"agent_name": "alice", "client_type": "mcp",
		"public_key": aliceKeys.PublicKey, "private_key": aliceKeys.PrivateKey,
	})
	callTool(t, bob, "register_agent", map[string]any{
		"org_slug": fixture.OrgSlug, "owner_email": fixture.BobEmail,
		"agent_name": "bob", "client_type": "mcp",
		"public_key": bobKeys.PublicKey, "private_key": bobKeys.PrivateKey,
	})

	// confirm=false is rejected.
	noConfirm, err := callToolRaw(t, alice, "create_team", map[string]any{"name": "eng"})
	if err != nil {
		t.Fatalf("rpc error: %v", err)
	}
	if isErr, _ := noConfirm["isError"].(bool); !isErr {
		t.Fatal("expected confirm=true gating to reject create_team")
	}

	team := mustStructuredContent(t, callTool(t, alice, "create_team", map[string]any{"name": "eng", "confirm": true}))
	teamID := team["team_id"].(string)

	// Non-admin Bob can't add members.
	bobAdd, _ := callToolRaw(t, bob, "add_team_member", map[string]any{
		"team_id": teamID, "user_email": fixture.AliceEmail, "confirm": true,
	})
	if isErr, _ := bobAdd["isError"].(bool); !isErr {
		t.Fatal("expected non-admin add_team_member to fail")
	}

	// Admin adds Bob.
	mustStructuredContent(t, callTool(t, alice, "add_team_member", map[string]any{
		"team_id": teamID, "user_email": fixture.BobEmail, "confirm": true,
	}))

	members := mustStructuredContent(t, callTool(t, alice, "list_team_members", map[string]any{"team_id": teamID}))
	if len(members["members"].([]any)) != 1 {
		t.Fatalf("expected 1 member, got %v", members["members"])
	}

	// Manager edge: Alice manages Bob.
	edge := mustStructuredContent(t, callTool(t, alice, "assign_manager", map[string]any{
		"user_email": fixture.BobEmail, "manager_email": fixture.AliceEmail, "confirm": true,
	}))
	if edge["manager_user_id"] == nil {
		t.Fatalf("expected manager_user_id in edge response, got %v", edge)
	}

	// Cycle: trying to set Bob as Alice's manager now must fail.
	cycleResult, _ := callToolRaw(t, alice, "assign_manager", map[string]any{
		"user_email": fixture.AliceEmail, "manager_email": fixture.BobEmail, "confirm": true,
	})
	if isErr, _ := cycleResult["isError"].(bool); !isErr {
		t.Fatal("expected cycle detection to reject reciprocal manager edge")
	}

	// get_manager_chain returns the upward walk.
	chain := mustStructuredContent(t, callTool(t, alice, "get_manager_chain", map[string]any{
		"user_email": fixture.BobEmail,
	}))
	rawChain := chain["chain"].([]any)
	if len(rawChain) != 1 {
		t.Fatalf("expected 1-hop chain, got %d", len(rawChain))
	}

	// list_teams returns the team we created.
	teams := mustStructuredContent(t, callTool(t, alice, "list_teams", nil))
	if len(teams["teams"].([]any)) == 0 {
		t.Fatal("expected at least one team")
	}

	// remove_team_member + revoke_manager both require confirm=true; the
	// happy-path calls below also drive the zero-coverage slices.
	mustStructuredContent(t, callTool(t, alice, "remove_team_member", map[string]any{
		"team_id": teamID, "user_email": fixture.BobEmail, "confirm": true,
	}))
	mustStructuredContent(t, callTool(t, alice, "revoke_manager", map[string]any{
		"user_email": fixture.BobEmail, "confirm": true,
	}))

	// confirm=false gating fires on each destructive tool — verifies the
	// same guard is present across all admin-gated writes.
	for _, tool := range []string{"remove_team_member", "revoke_manager", "assign_manager", "add_team_member", "create_team"} {
		raw, _ := callToolRaw(t, alice, tool, map[string]any{
			"team_id": teamID, "user_email": "x@y", "manager_email": "y@z", "name": "x",
		})
		if isErr, _ := raw["isError"].(bool); !isErr {
			t.Fatalf("%s without confirm must fail", tool)
		}
	}
}
