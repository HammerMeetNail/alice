package mcp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"alice/internal/app"
	"alice/internal/config"
	"alice/internal/httpapi"
)

func TestToolDiscoveryAndQueryFlow(t *testing.T) {
	handler := newTestHandler(t)

	aliceServer := NewServer(handler)
	bobServer := NewServer(handler)

	initialize := aliceServer.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
	})
	if initialize == nil || initialize.Error != nil {
		t.Fatalf("initialize failed: %#v", initialize)
	}

	listResult := callTool(t, aliceServer, "tools/list", nil)
	var toolList []map[string]any
	mustDecodeInto(t, listResult["tools"], &toolList)

	names := make([]string, 0, len(toolList))
	for _, tool := range toolList {
		names = append(names, tool["name"].(string))
	}
	for _, expected := range []string{
		"register_agent",
		"publish_artifact",
		"query_peer_status",
		"get_query_result",
		"grant_permission",
		"list_allowed_peers",
		"send_request_to_peer",
		"list_incoming_requests",
		"respond_to_request",
		"list_pending_approvals",
		"resolve_approval",
	} {
		if !slices.Contains(names, expected) {
			t.Fatalf("missing tool %q in %v", expected, names)
		}
	}

	fixture := newFixture(t)
	aliceKeys := generateKeys(t)
	bobKeys := generateKeys(t)

	aliceRegistration := mustStructuredContent(t, callTool(t, aliceServer, "register_agent", map[string]any{
		"org_slug":     fixture.OrgSlug,
		"owner_email":  fixture.AliceEmail,
		"agent_name":   "alice-agent",
		"client_type":  "codex",
		"public_key":   aliceKeys.PublicKey,
		"private_key":  aliceKeys.PrivateKey,
		"capabilities": []string{"publish_artifact", "respond_query"},
	}))
	if strings.TrimSpace(aliceRegistration["access_token"].(string)) == "" {
		t.Fatalf("alice registration did not return an access token")
	}

	bobRegistration := mustStructuredContent(t, callTool(t, bobServer, "register_agent", map[string]any{
		"org_slug":     fixture.OrgSlug,
		"owner_email":  fixture.BobEmail,
		"agent_name":   "bob-agent",
		"client_type":  "codex",
		"public_key":   bobKeys.PublicKey,
		"private_key":  bobKeys.PrivateKey,
		"capabilities": []string{"publish_artifact", "respond_query"},
	}))
	if strings.TrimSpace(bobRegistration["access_token"].(string)) == "" {
		t.Fatalf("bob registration did not return an access token")
	}

	mustStructuredContent(t, callTool(t, bobServer, "publish_artifact", map[string]any{
		"artifact": map[string]any{
			"type":    "summary",
			"title":   "Working on payments",
			"content": "Focused on payments retry work.",
			"structured_payload": map[string]any{
				"project_refs": []string{fixture.ProjectScope},
			},
			"source_refs": []map[string]any{
				{
					"source_system": "github",
					"source_type":   "pull_request",
					"source_id":     "repo:org/payments:pr:128",
					"observed_at":   time.Now().UTC().Format(time.RFC3339),
					"trust_class":   "structured_system",
					"sensitivity":   "medium",
				},
			},
			"visibility_mode": "explicit_grants_only",
			"sensitivity":     "medium",
			"confidence":      0.9,
		},
	}))

	mustStructuredContent(t, callTool(t, bobServer, "grant_permission", map[string]any{
		"grantee_user_email":     fixture.AliceEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "medium",
		"allowed_purposes":       []string{"status_check"},
	}))

	peers := mustStructuredContent(t, callTool(t, aliceServer, "list_allowed_peers", map[string]any{}))
	var peerList []map[string]any
	mustDecodeInto(t, peers["peers"], &peerList)
	if len(peerList) != 1 {
		t.Fatalf("expected one allowed peer, got %d", len(peerList))
	}

	queryResponse := mustStructuredContent(t, callTool(t, aliceServer, "query_peer_status", map[string]any{
		"to_user_email":   fixture.BobEmail,
		"purpose":         "status_check",
		"question":        "What has Bob been working on today?",
		"requested_types": []string{"summary"},
		"project_scope":   []string{fixture.ProjectScope},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	}))

	queryID := queryResponse["query_id"].(string)
	result := mustStructuredContent(t, callTool(t, aliceServer, "get_query_result", map[string]any{
		"query_id": queryID,
	}))

	responsePayload := result["response"].(map[string]any)
	var artifacts []map[string]any
	mustDecodeInto(t, responsePayload["artifacts"], &artifacts)
	if len(artifacts) != 1 {
		t.Fatalf("expected one artifact in query result, got %d", len(artifacts))
	}

	requestResult := mustStructuredContent(t, callTool(t, aliceServer, "send_request_to_peer", map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "ask_for_review",
		"title":         "Need review today",
		"content":       "Can you review the payments retry PR today?",
		"structured_payload": map[string]any{
			"project_refs": []string{fixture.ProjectScope},
		},
	}))
	requestID := requestResult["request_id"].(string)

	incomingRequests := mustStructuredContent(t, callTool(t, bobServer, "list_incoming_requests", map[string]any{}))
	var requestsList []map[string]any
	mustDecodeInto(t, incomingRequests["requests"], &requestsList)
	if len(requestsList) != 1 {
		t.Fatalf("expected one incoming request, got %d", len(requestsList))
	}

	responseResult := mustStructuredContent(t, callTool(t, bobServer, "respond_to_request", map[string]any{
		"request_id": requestID,
		"response":   "require_approval",
		"message":    "Need explicit approval before accepting this request.",
	}))
	approvalID := responseResult["approval_id"].(string)
	if approvalID == "" {
		t.Fatalf("expected approval_id in request response result")
	}

	pendingApprovals := mustStructuredContent(t, callTool(t, bobServer, "list_pending_approvals", map[string]any{}))
	var approvalsList []map[string]any
	mustDecodeInto(t, pendingApprovals["approvals"], &approvalsList)
	if len(approvalsList) != 1 {
		t.Fatalf("expected one pending approval, got %d", len(approvalsList))
	}

	resolveResult := mustStructuredContent(t, callTool(t, bobServer, "resolve_approval", map[string]any{
		"approval_id": approvalID,
		"decision":    "approved",
	}))
	if resolveResult["state"] != "approved" {
		t.Fatalf("expected approval to resolve as approved, got %#v", resolveResult["state"])
	}
}

type testFixture struct {
	OrgSlug      string
	AliceEmail   string
	BobEmail     string
	ProjectScope string
}

type keyPair struct {
	PublicKey  string
	PrivateKey string
}

func newTestHandler(t *testing.T) *httpapiTestHandler {
	t.Helper()

	cfg := config.Config{
		DefaultOrgName:   "Alice Development Org",
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     15 * time.Minute,
	}
	container, closeFn, err := app.NewContainer(cfg)
	if err != nil {
		t.Fatalf("build app container: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() {
			if err := closeFn(); err != nil {
				t.Fatalf("close container: %v", err)
			}
		})
	}

	return &httpapiTestHandler{handler: httpapi.NewRouter(container)}
}

type httpapiTestHandler struct {
	handler http.Handler
}

func (h *httpapiTestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

func generateKeys(t *testing.T) keyPair {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key pair: %v", err)
	}

	return keyPair{
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		PrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	}
}

func newFixture(t *testing.T) testFixture {
	t.Helper()

	suffix := strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(t.Name()))
	suffix = suffix + "-" + time.Now().UTC().Format("20060102150405.000000000")

	return testFixture{
		OrgSlug:      "example-corp-" + suffix,
		AliceEmail:   "alice-" + suffix + "@example.com",
		BobEmail:     "bob-" + suffix + "@example.com",
		ProjectScope: "payments-api",
	}
}

func callTool(t *testing.T, server *Server, method string, arguments map[string]any) map[string]any {
	t.Helper()

	var params json.RawMessage
	var err error

	switch method {
	case "tools/list":
		params, err = json.Marshal(map[string]any{})
	default:
		params, err = json.Marshal(map[string]any{
			"name":      method,
			"arguments": arguments,
		})
	}
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	requestMethod := "tools/call"
	if method == "tools/list" {
		requestMethod = "tools/list"
	}

	resp := server.handleRequest(context.Background(), request{
		JSONRPC: "2.0",
		ID:      method,
		Method:  requestMethod,
		Params:  params,
	})
	if resp == nil {
		t.Fatalf("nil response for method %s", method)
	}
	if resp.Error != nil {
		t.Fatalf("tool %s returned error: %+v", method, resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		data, _ := json.Marshal(resp.Result)
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("decode response result for %s: %v", method, err)
		}
	}
	return result
}

func mustStructuredContent(t *testing.T, result map[string]any) map[string]any {
	t.Helper()

	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("tool returned MCP error result: %v", result["structuredContent"])
	}

	structured, ok := result["structuredContent"].(map[string]any)
	if ok {
		return structured
	}

	data, err := json.Marshal(result["structuredContent"])
	if err != nil {
		t.Fatalf("marshal structuredContent: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode structuredContent: %v", err)
	}
	return payload
}

func mustDecodeInto(t *testing.T, value any, target any) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode value: %v", err)
	}
}
