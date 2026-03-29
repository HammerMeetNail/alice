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
	handler := &httpapiTestHandler{handler: httpapi.NewRouter(container)}
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
