package mcp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
				"public_key":          stringSchema("Optional base64-encoded Ed25519 public key. If omitted, a keypair is generated automatically."),
				"private_key":         stringSchema("Optional base64-encoded Ed25519 private key. If omitted, a keypair is generated automatically."),
				"challenge_id":        stringSchema("Optional challenge id for explicit registration completion."),
				"challenge_signature": stringSchema("Optional base64-encoded challenge signature for explicit registration completion."),
				"invite_token":        stringSchema("Optional invite token required when the org has invite_token verification mode."),
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
		"submit_correction": {
			Name:        "submit_correction",
			Description: "Publish a corrected version of a previously published artifact. The caller must own the original artifact.",
			InputSchema: objectSchema(map[string]any{
				"artifact_id": stringSchema("ID of the artifact being corrected."),
				"artifact": map[string]any{
					"type":        "object",
					"description": "Corrected artifact payload.",
				},
			}),
			Handler: s.handleSubmitCorrection,
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
				"confirm":                map[string]any{"type": "boolean", "description": "Set true to confirm you want to create this grant."},
			}),
			Handler: s.handleGrantPermission,
		},
		"revoke_permission": {
			Name:        "revoke_permission",
			Description: "Revoke a previously created permission grant. Only the grantor can revoke.",
			InputSchema: objectSchema(map[string]any{
				"policy_grant_id": stringSchema("Grant identifier to revoke."),
				"confirm":         map[string]any{"type": "boolean", "description": "Set true to confirm you want to revoke this grant."},
			}),
			Handler: s.handleRevokePermission,
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
			InputSchema: objectSchema(map[string]any{
				"limit":  map[string]any{"type": "integer", "description": "Max items to return."},
				"cursor": stringSchema("Opaque pagination cursor from a previous response."),
			}),
			Handler: s.handleListIncomingRequests,
		},
		"list_sent_requests": {
			Name:        "list_sent_requests",
			Description: "List requests sent by the authenticated agent, including their current state.",
			InputSchema: objectSchema(map[string]any{
				"limit":  map[string]any{"type": "integer", "description": "Max items to return."},
				"cursor": stringSchema("Opaque pagination cursor from a previous response."),
			}),
			Handler: s.handleListSentRequests,
		},
		"get_audit_summary": {
			Name:        "get_audit_summary",
			Description: "Retrieve a summary of recent audit events for the authenticated agent.",
			InputSchema: objectSchema(map[string]any{
				"since":  stringSchema("Optional RFC3339 timestamp to filter events after this time."),
				"limit":  map[string]any{"type": "integer", "description": "Max items to return."},
				"cursor": stringSchema("Opaque pagination cursor from a previous response."),
			}),
			Handler: s.handleGetAuditSummary,
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
			InputSchema: objectSchema(map[string]any{
				"limit":  map[string]any{"type": "integer", "description": "Max items to return."},
				"cursor": stringSchema("Opaque pagination cursor from a previous response."),
			}),
			Handler: s.handleListPendingApprovals,
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
		"verify_email": {
			Name:        "verify_email",
			Description: "Submit the one-time email verification code to activate the agent session.",
			InputSchema: objectSchema(map[string]any{
				"code": stringSchema("6-digit verification code received by email."),
			}),
			Handler: s.handleVerifyEmail,
		},
		"resend_verification_email": {
			Name:        "resend_verification_email",
			Description: "Request a new email verification code (rate-limited to one resend per 60 seconds).",
			InputSchema: objectSchema(map[string]any{}),
			Handler:     s.handleResendVerificationEmail,
		},
		"rotate_invite_token": {
			Name:        "rotate_invite_token",
			Description: "Rotate the org's invite token, invalidating the previous one. Caller must belong to the org.",
			InputSchema: objectSchema(map[string]any{
				"confirm": map[string]any{"type": "boolean", "description": "Set true to confirm you want to rotate the invite token."},
			}),
			Handler: s.handleRotateInviteToken,
		},
		"list_pending_agents": {
			Name:        "list_pending_agents",
			Description: "List agents awaiting admin approval in the caller's org. Caller must be an org admin.",
			InputSchema: objectSchema(map[string]any{
				"limit":  map[string]any{"type": "integer", "description": "Max items to return."},
				"cursor": stringSchema("Opaque pagination cursor from a previous response."),
			}),
			Handler: s.handleListPendingAgents,
		},
		"review_agent": {
			Name:        "review_agent",
			Description: "Approve or reject a pending agent registration. Caller must be an org admin.",
			InputSchema: objectSchema(map[string]any{
				"agent_id": stringSchema("Agent ID to review."),
				"decision": stringSchema("'approved' or 'rejected'."),
				"reason":   stringSchema("Optional reason for the decision."),
				"confirm":  map[string]any{"type": "boolean", "description": "Set true to confirm you want to approve or reject this agent."},
			}),
			Handler: s.handleReviewAgent,
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

	// Auto-generate a keypair when none is provided so callers never need to supply key material.
	publicKeyB64 := stringArg(args, "public_key")
	privateKeyB64 := stringArg(args, "private_key")
	if publicKeyB64 == "" {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return nil, fmt.Errorf("generate keypair: %w", err)
		}
		publicKeyB64 = base64.StdEncoding.EncodeToString(pub)
		privateKeyB64 = base64.StdEncoding.EncodeToString(priv)
	}

	body := map[string]any{
		"org_slug":     args["org_slug"],
		"owner_email":  args["owner_email"],
		"agent_name":   args["agent_name"],
		"client_type":  args["client_type"],
		"public_key":   publicKeyB64,
		"invite_token": stringArg(args, "invite_token"),
	}
	challenge, err := s.callJSON(ctx, http.MethodPost, "/v1/agents/register/challenge", body, "")
	if err != nil {
		return nil, err
	}

	if privateKeyB64 == "" {
		challenge["next_step"] = "sign the challenge string and call register_agent again with challenge_id and challenge_signature, or provide private_key for one-shot local bootstrap"
		return challenge, nil
	}
	privateKey := privateKeyB64

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

func (s *Server) handleSubmitCorrection(ctx context.Context, args map[string]any) (any, error) {
	artifactID := stringArg(args, "artifact_id")
	if artifactID == "" {
		return nil, fmt.Errorf("artifact_id is required")
	}
	body := map[string]any{
		"artifact": args["artifact"],
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/artifacts/"+artifactID+"/correct", body)
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
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/policy-grants", args)
}

func (s *Server) handleRevokePermission(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	grantID := stringArg(args, "policy_grant_id")
	if grantID == "" {
		return nil, fmt.Errorf("policy_grant_id is required")
	}
	return s.callAuthedJSON(ctx, http.MethodDelete, "/v1/policy-grants/"+grantID, nil)
}

func (s *Server) handleListAllowedPeers(ctx context.Context, _ map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/peers", nil)
}

func (s *Server) handleSendRequestToPeer(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/requests", args)
}

func (s *Server) handleListIncomingRequests(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/requests/incoming"+paginationQuery(args, ""), nil)
}

func (s *Server) handleListSentRequests(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/requests/sent"+paginationQuery(args, ""), nil)
}

func (s *Server) handleGetAuditSummary(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/audit/summary"+paginationQuery(args, stringArg(args, "since")), nil)
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

func (s *Server) handleListPendingApprovals(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/approvals"+paginationQuery(args, ""), nil)
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

func (s *Server) handleVerifyEmail(ctx context.Context, args map[string]any) (any, error) {
	code := stringArg(args, "code")
	if code == "" {
		return nil, fmt.Errorf("code is required")
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/agents/verify-email", map[string]any{
		"code": code,
	})
}

func (s *Server) handleResendVerificationEmail(ctx context.Context, _ map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/agents/resend-verification", nil)
}

func (s *Server) handleRotateInviteToken(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/orgs/rotate-invite-token", nil)
}

func (s *Server) handleListPendingAgents(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/orgs/pending-agents"+paginationQuery(args, ""), nil)
}

func (s *Server) handleReviewAgent(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	agentID := stringArg(args, "agent_id")
	if agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	decision := stringArg(args, "decision")
	if decision == "" {
		return nil, fmt.Errorf("decision is required")
	}
	body := map[string]any{
		"decision": decision,
		"reason":   stringArg(args, "reason"),
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/orgs/agents/"+agentID+"/review", body)
}

// paginationQuery builds a query string with optional limit, cursor, and a
// pre-existing since parameter (may be empty). The returned string is either
// empty or starts with "?".
func paginationQuery(args map[string]any, since string) string {
	params := ""
	add := func(k, v string) {
		if params == "" {
			params = "?" + k + "=" + v
		} else {
			params += "&" + k + "=" + v
		}
	}
	if since != "" {
		add("since", since)
	}
	if limit, ok := args["limit"]; ok {
		switch v := limit.(type) {
		case float64:
			add("limit", fmt.Sprintf("%d", int(v)))
		case int:
			add("limit", fmt.Sprintf("%d", v))
		}
	}
	if cursor := stringArg(args, "cursor"); cursor != "" {
		add("cursor", cursor)
	}
	return params
}

func (s *Server) callAuthedJSON(ctx context.Context, method, path string, body any) (any, error) {
	accessToken := s.getAccessToken()
	if accessToken == "" {
		return nil, fmt.Errorf("no authenticated agent session; call register_agent first")
	}
	return s.callJSON(ctx, method, path, body, accessToken)
}

func (s *Server) callJSON(ctx context.Context, method, path string, body any, accessToken string) (map[string]any, error) {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
	}

	token := strings.TrimSpace(accessToken)

	var statusCode int
	var responseBytes []byte

	if s.baseURL != "" {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP request: %w", err)
		}
		defer resp.Body.Close()
		responseBytes, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}
		statusCode = resp.StatusCode
	} else {
		req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes)).WithContext(ctx)
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, req)
		statusCode = rec.Code
		responseBytes = rec.Body.Bytes()
	}

	var payload map[string]any
	if len(responseBytes) > 0 {
		if err := json.Unmarshal(responseBytes, &payload); err != nil {
			return nil, fmt.Errorf("decode HTTP response: %w", err)
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}

	if statusCode >= http.StatusBadRequest {
		if errCode := stringArg(payload, "error"); errCode != "" {
			if detail := stringArg(payload, "message"); detail != "" {
				return nil, fmt.Errorf("%s: %s", errCode, detail)
			}
			return nil, fmt.Errorf("%s", errCode)
		}
		return nil, fmt.Errorf("HTTP %d", statusCode)
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
