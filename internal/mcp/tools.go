package mcp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

func (s *Server) registerTools() map[string]toolDefinition {
	return map[string]toolDefinition{
		"register_agent": {
			Name:        "register_agent",
			Description: "Register the current edge agent and establish an authenticated MCP session.",
			InputSchema: objectSchema(map[string]any{
				"org_slug":            stringSchema("Organization slug."),
				"owner_email":         stringSchema("Agent owner email address."),
				"agent_name":          stringSchema("Human-readable agent name."),
				"client_type":         stringSchema("Client type identifier."),
				"public_key":          stringSchema("Base64-encoded Ed25519 public key."),
				"private_key":         stringSchema("Optional base64-encoded Ed25519 private key for one-shot local bootstrap."),
				"challenge_id":        stringSchema("Optional challenge id for explicit registration completion."),
				"challenge_signature": stringSchema("Optional base64-encoded challenge signature for explicit registration completion."),
				"capabilities": arraySchema(
					map[string]any{"type": "string"},
					"Agent capabilities.",
				),
			}),
			Handler: s.handleRegisterAgent,
		},
		"publish_artifact": {
			Name:        "publish_artifact",
			Description: "Publish a shareable artifact for the authenticated agent.",
			InputSchema: objectSchema(map[string]any{
				"artifact": map[string]any{
					"type":        "object",
					"description": "Artifact payload matching the current HTTP publish contract.",
				},
			}),
			Handler: s.handlePublishArtifact,
		},
		"query_peer_status": {
			Name:        "query_peer_status",
			Description: "Submit a permission-checked status query to another agent.",
			InputSchema: objectSchema(map[string]any{
				"to_user_email":   stringSchema("Recipient user email."),
				"purpose":         stringSchema("Query purpose."),
				"question":        stringSchema("Natural-language question."),
				"requested_types": arraySchema(map[string]any{"type": "string"}, "Requested artifact types."),
				"project_scope":   arraySchema(map[string]any{"type": "string"}, "Optional project scope."),
				"time_window": map[string]any{
					"type":        "object",
					"description": "RFC3339 time window with start and end.",
				},
			}),
			Handler: s.handleQueryPeerStatus,
		},
		"get_query_result": {
			Name:        "get_query_result",
			Description: "Retrieve the current result for a previously submitted query.",
			InputSchema: objectSchema(map[string]any{
				"query_id": stringSchema("Query identifier."),
			}),
			Handler: s.handleGetQueryResult,
		},
		"grant_permission": {
			Name:        "grant_permission",
			Description: "Create a permission grant from the authenticated agent to another user.",
			InputSchema: objectSchema(map[string]any{
				"grantee_user_email":     stringSchema("Recipient user email."),
				"scope_type":             stringSchema("Grant scope type."),
				"scope_ref":              stringSchema("Grant scope reference."),
				"allowed_artifact_types": arraySchema(map[string]any{"type": "string"}, "Allowed artifact types."),
				"max_sensitivity":        stringSchema("Maximum allowed sensitivity."),
				"allowed_purposes":       arraySchema(map[string]any{"type": "string"}, "Allowed query purposes."),
			}),
			Handler: s.handleGrantPermission,
		},
		"list_allowed_peers": {
			Name:        "list_allowed_peers",
			Description: "List peers visible to the authenticated agent under current grants.",
			InputSchema: objectSchema(map[string]any{}),
			Handler:     s.handleListAllowedPeers,
		},
		"send_request_to_peer": {
			Name:        "send_request_to_peer",
			Description: "Send a Gatekeeper request to another agent.",
			InputSchema: objectSchema(map[string]any{
				"to_user_email": stringSchema("Recipient user email."),
				"request_type":  stringSchema("Request type."),
				"title":         stringSchema("Request title."),
				"content":       stringSchema("Request content."),
				"structured_payload": map[string]any{
					"type":        "object",
					"description": "Optional structured request payload.",
				},
			}),
			Handler: s.handleSendRequestToPeer,
		},
		"list_incoming_requests": {
			Name:        "list_incoming_requests",
			Description: "List incoming requests for the authenticated agent.",
			InputSchema: objectSchema(map[string]any{}),
			Handler:     s.handleListIncomingRequests,
		},
		"respond_to_request": {
			Name:        "respond_to_request",
			Description: "Respond to an incoming request or require approval.",
			InputSchema: objectSchema(map[string]any{
				"request_id": stringSchema("Request identifier."),
				"response":   stringSchema("accepted, deferred, denied, completed, or require_approval."),
				"message":    stringSchema("Optional response message."),
			}),
			Handler: s.handleRespondToRequest,
		},
		"list_pending_approvals": {
			Name:        "list_pending_approvals",
			Description: "List approvals pending for the authenticated agent.",
			InputSchema: objectSchema(map[string]any{}),
			Handler:     s.handleListPendingApprovals,
		},
		"resolve_approval": {
			Name:        "resolve_approval",
			Description: "Resolve a pending approval for the authenticated agent.",
			InputSchema: objectSchema(map[string]any{
				"approval_id": stringSchema("Approval identifier."),
				"decision":    stringSchema("approved or denied."),
			}),
			Handler: s.handleResolveApproval,
		},
	}
}

func (s *Server) handleRegisterAgent(ctx context.Context, args map[string]any) (any, error) {
	if args == nil {
		args = map[string]any{}
	}

	challengeID := stringArg(args, "challenge_id")
	challengeSignature := stringArg(args, "challenge_signature")
	if challengeID != "" || challengeSignature != "" {
		if challengeID == "" || challengeSignature == "" {
			return nil, fmt.Errorf("challenge_id and challenge_signature must be provided together")
		}

		response, err := s.callJSON(ctx, http.MethodPost, "/v1/agents/register", map[string]any{
			"challenge_id":        challengeID,
			"challenge_signature": challengeSignature,
		}, "")
		if err != nil {
			return nil, err
		}

		if accessToken := stringArg(response, "access_token"); accessToken != "" {
			s.setAccessToken(accessToken)
		}
		return response, nil
	}

	body := map[string]any{
		"org_slug":     args["org_slug"],
		"owner_email":  args["owner_email"],
		"agent_name":   args["agent_name"],
		"client_type":  args["client_type"],
		"public_key":   args["public_key"],
		"capabilities": args["capabilities"],
	}
	challenge, err := s.callJSON(ctx, http.MethodPost, "/v1/agents/register/challenge", body, "")
	if err != nil {
		return nil, err
	}

	privateKey := stringArg(args, "private_key")
	if privateKey == "" {
		challenge["next_step"] = "sign the challenge string and call register_agent again with challenge_id and challenge_signature, or provide private_key for one-shot local bootstrap"
		return challenge, nil
	}

	signature, err := signRegistrationChallenge(stringArg(challenge, "challenge"), privateKey)
	if err != nil {
		return nil, err
	}

	response, err := s.callJSON(ctx, http.MethodPost, "/v1/agents/register", map[string]any{
		"challenge_id":        challenge["challenge_id"],
		"challenge_signature": signature,
	}, "")
	if err != nil {
		return nil, err
	}

	if accessToken := stringArg(response, "access_token"); accessToken != "" {
		s.setAccessToken(accessToken)
	}
	return response, nil
}

func (s *Server) handlePublishArtifact(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/artifacts", args)
}

func (s *Server) handleQueryPeerStatus(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/queries", args)
}

func (s *Server) handleGetQueryResult(ctx context.Context, args map[string]any) (any, error) {
	queryID := stringArg(args, "query_id")
	if queryID == "" {
		return nil, fmt.Errorf("query_id is required")
	}
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/queries/"+queryID, nil)
}

func (s *Server) handleGrantPermission(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/policy-grants", args)
}

func (s *Server) handleListAllowedPeers(ctx context.Context, _ map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/peers", nil)
}

func (s *Server) handleSendRequestToPeer(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/requests", args)
}

func (s *Server) handleListIncomingRequests(ctx context.Context, _ map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/requests/incoming", nil)
}

func (s *Server) handleRespondToRequest(ctx context.Context, args map[string]any) (any, error) {
	requestID := stringArg(args, "request_id")
	if requestID == "" {
		return nil, fmt.Errorf("request_id is required")
	}

	body := map[string]any{
		"response": args["response"],
		"message":  args["message"],
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/requests/"+requestID+"/respond", body)
}

func (s *Server) handleListPendingApprovals(ctx context.Context, _ map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/approvals", nil)
}

func (s *Server) handleResolveApproval(ctx context.Context, args map[string]any) (any, error) {
	approvalID := stringArg(args, "approval_id")
	if approvalID == "" {
		return nil, fmt.Errorf("approval_id is required")
	}

	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/approvals/"+approvalID+"/resolve", map[string]any{
		"decision": args["decision"],
	})
}

func (s *Server) callAuthedJSON(ctx context.Context, method, path string, body any) (any, error) {
	accessToken := s.getAccessToken()
	if accessToken == "" {
		return nil, fmt.Errorf("no authenticated agent session; call register_agent first")
	}
	return s.callJSON(ctx, method, path, body, accessToken)
}

func (s *Server) callJSON(ctx context.Context, method, path string, body any, accessToken string) (map[string]any, error) {
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		requestBody = bytes.NewReader(data)
	}

	req := httptest.NewRequest(method, path, requestBody).WithContext(ctx)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	}

	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)

	var payload map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			return nil, fmt.Errorf("decode HTTP response: %w", err)
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}

	if rec.Code >= http.StatusBadRequest {
		if message := stringArg(payload, "error"); message != "" {
			return nil, fmt.Errorf("%s", message)
		}
		return nil, fmt.Errorf("HTTP %d", rec.Code)
	}

	return payload, nil
}

func signRegistrationChallenge(challenge, encodedPrivateKey string) (string, error) {
	privateKeyBytes, err := decodeBase64(encodedPrivateKey)
	if err != nil {
		return "", fmt.Errorf("private_key must be base64-encoded Ed25519 key material")
	}

	switch len(privateKeyBytes) {
	case ed25519.SeedSize:
		privateKeyBytes = ed25519.NewKeyFromSeed(privateKeyBytes)
	case ed25519.PrivateKeySize:
	default:
		return "", fmt.Errorf("private_key must decode to a 32-byte seed or 64-byte Ed25519 private key")
	}

	signature := ed25519.Sign(ed25519.PrivateKey(privateKeyBytes), []byte(challenge))
	return base64.StdEncoding.EncodeToString(signature), nil
}

func decodeBase64(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(trimmed)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("decode base64 value")
}

func stringArg(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func objectSchema(properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func arraySchema(items map[string]any, description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       items,
	}
}
