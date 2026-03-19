package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/approvals"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/queries"
	"alice/internal/requests"
)

type router struct {
	services services.Container
	mux      *http.ServeMux
}

type currentAgentContextKey struct{}
type currentUserContextKey struct{}

func NewRouter(services services.Container) http.Handler {
	r := &router{
		services: services,
		mux:      http.NewServeMux(),
	}
	r.routes()
	return r.mux
}

func (r *router) routes() {
	r.mux.HandleFunc("GET /healthz", r.handleHealthz)
	r.mux.HandleFunc("POST /v1/agents/register/challenge", r.handleBeginRegisterAgent)
	r.mux.HandleFunc("POST /v1/agents/register", r.handleRegisterAgent)
	r.mux.Handle("POST /v1/artifacts", r.requireAuth(http.HandlerFunc(r.handlePublishArtifact)))
	r.mux.Handle("POST /v1/policy-grants", r.requireAuth(http.HandlerFunc(r.handleGrantPermission)))
	r.mux.Handle("GET /v1/peers", r.requireAuth(http.HandlerFunc(r.handleListAllowedPeers)))
	r.mux.Handle("POST /v1/queries", r.requireAuth(http.HandlerFunc(r.handleQueryPeerStatus)))
	r.mux.Handle("GET /v1/queries/", r.requireAuth(http.HandlerFunc(r.handleGetQueryResult)))
	r.mux.Handle("POST /v1/requests", r.requireAuth(http.HandlerFunc(r.handleSendRequestToPeer)))
	r.mux.Handle("GET /v1/requests/incoming", r.requireAuth(http.HandlerFunc(r.handleListIncomingRequests)))
	r.mux.Handle("POST /v1/requests/", r.requireAuth(http.HandlerFunc(r.handleRespondToRequest)))
	r.mux.Handle("GET /v1/approvals", r.requireAuth(http.HandlerFunc(r.handleListPendingApprovals)))
	r.mux.Handle("POST /v1/approvals/", r.requireAuth(http.HandlerFunc(r.handleResolveApproval)))
	r.mux.Handle("GET /v1/audit/summary", r.requireAuth(http.HandlerFunc(r.handleAuditSummary)))
}

func (r *router) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type registerAgentRequest struct {
	OrgSlug      string   `json:"org_slug"`
	OwnerEmail   string   `json:"owner_email"`
	AgentName    string   `json:"agent_name"`
	ClientType   string   `json:"client_type"`
	PublicKey    string   `json:"public_key"`
	Capabilities []string `json:"capabilities"`
}

func (r *router) handleBeginRegisterAgent(w http.ResponseWriter, req *http.Request) {
	var input registerAgentRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	challenge, payload, err := r.services.Agents.BeginRegistration(input.OrgSlug, input.OwnerEmail, input.AgentName, input.ClientType, input.PublicKey, input.Capabilities)
	if err != nil {
		writeServiceError(w, err, "registration challenge failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": challenge.ChallengeID,
		"challenge":    payload,
		"algorithm":    "ed25519",
		"expires_at":   challenge.ExpiresAt,
	})
}

type completeRegisterAgentRequest struct {
	ChallengeID        string `json:"challenge_id"`
	ChallengeSignature string `json:"challenge_signature"`
}

func (r *router) handleRegisterAgent(w http.ResponseWriter, req *http.Request) {
	var input completeRegisterAgentRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	org, user, agent, accessToken, expiresAt, err := r.services.Agents.CompleteRegistration(input.ChallengeID, input.ChallengeSignature)
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrUnknownRegistrationChallenge):
			writeError(w, http.StatusNotFound, err.Error())
			return
		case errors.Is(err, agents.ErrExpiredRegistrationChallenge),
			errors.Is(err, agents.ErrUsedRegistrationChallenge),
			errors.Is(err, agents.ErrInvalidRegistrationSignature):
			writeAuthError(w, err.Error())
			return
		default:
			writeServiceError(w, err, "agent registration failed")
			return
		}
	}

	if _, err := r.services.Audit.Record("agent.registered", "agent", agent.AgentID, org.OrgID, agent.AgentID, "", "allow", "", nil, map[string]any{
		"owner_email":      user.Email,
		"auth_method":      "ed25519_challenge",
		"token_expires_at": expiresAt,
	}); err != nil {
		log.Printf("audit record failed for agent registration: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id":     agent.AgentID,
		"org_id":       org.OrgID,
		"status":       agent.Status,
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_at":   expiresAt,
	})
}

type publishArtifactRequest struct {
	Artifact core.Artifact `json:"artifact"`
}

func (r *router) handlePublishArtifact(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input publishArtifactRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	artifact, err := r.services.Artifacts.PublishArtifact(agent, user, input.Artifact)
	if err != nil {
		writeServiceError(w, err, "artifact publish failed")
		return
	}

	if _, err := r.services.Audit.Record("artifact.published", "artifact", artifact.ArtifactID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"artifact_type": artifact.Type,
	}); err != nil {
		log.Printf("audit record failed for artifact publish: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id": artifact.ArtifactID,
		"stored":      true,
	})
}

type grantPermissionRequest struct {
	GranteeUserEmail     string              `json:"grantee_user_email"`
	ScopeType            string              `json:"scope_type"`
	ScopeRef             string              `json:"scope_ref"`
	AllowedArtifactTypes []core.ArtifactType `json:"allowed_artifact_types"`
	MaxSensitivity       core.Sensitivity    `json:"max_sensitivity"`
	AllowedPurposes      []core.QueryPurpose `json:"allowed_purposes"`
}

func (r *router) handleGrantPermission(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input grantPermissionRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	granteeUser, exists, err := r.services.Agents.FindUserByEmail(input.GranteeUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve grantee user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "grantee user not found")
		return
	}

	grant, err := r.services.Policy.Grant(agent.OrgID, user, granteeUser, input.ScopeType, input.ScopeRef, input.AllowedArtifactTypes, input.MaxSensitivity, input.AllowedPurposes)
	if err != nil {
		writeServiceError(w, err, "grant creation failed")
		return
	}

	if _, err := r.services.Audit.Record("policy.grant.created", "policy_grant", grant.PolicyGrantID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, []string{"grant:" + grant.PolicyGrantID}, map[string]any{
		"grantee_email": granteeUser.Email,
		"scope_ref":     grant.ScopeRef,
	}); err != nil {
		log.Printf("audit record failed for grant creation: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"policy_grant_id": grant.PolicyGrantID,
	})
}

func (r *router) handleListAllowedPeers(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	grants, err := r.services.Policy.ListAllowedPeers(user.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list allowed peers")
		return
	}
	peers := make([]map[string]any, 0, len(grants))
	for _, grant := range grants {
		owner, exists, err := r.services.Agents.FindUserByID(grant.GrantorUserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve grant owner")
			return
		}
		if !exists {
			continue
		}

		peers = append(peers, map[string]any{
			"user_email":             owner.Email,
			"allowed_purposes":       grant.AllowedPurposes,
			"allowed_artifact_types": grant.AllowedArtifactTypes,
		})
	}

	if _, err := r.services.Audit.Record("policy.allowed_peers.listed", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL0, nil, nil); err != nil {
		log.Printf("audit record failed for allowed peers listing: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": peers})
}

type queryPeerStatusRequest struct {
	ToUserEmail    string              `json:"to_user_email"`
	Purpose        core.QueryPurpose   `json:"purpose"`
	Question       string              `json:"question"`
	RequestedTypes []core.ArtifactType `json:"requested_types"`
	ProjectScope   []string            `json:"project_scope"`
	TimeWindow     core.TimeWindow     `json:"time_window"`
}

func (r *router) handleQueryPeerStatus(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input queryPeerStatusRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := core.ValidateQueryInput(input.ToUserEmail, input.Purpose, input.RequestedTypes, input.TimeWindow); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetUser, exists, err := r.services.Agents.FindUserByEmail(input.ToUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target user not found")
		return
	}
	targetAgent, exists, err := r.services.Agents.FindAgentByUserID(targetUser.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target agent")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target agent not found")
		return
	}

	query := core.Query{
		QueryID:        id.New("query"),
		OrgID:          agent.OrgID,
		FromAgentID:    agent.AgentID,
		FromUserID:     user.UserID,
		ToAgentID:      targetAgent.AgentID,
		ToUserID:       targetUser.UserID,
		Purpose:        input.Purpose,
		Question:       input.Question,
		RequestedTypes: input.RequestedTypes,
		ProjectScope:   input.ProjectScope,
		TimeWindow:     input.TimeWindow,
		RiskLevel:      core.RiskLevelL1,
		State:          core.QueryStateQueued,
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
	}

	response, err := r.services.Queries.Evaluate(query)
	if err != nil {
		if errors.Is(err, queries.ErrPermissionDenied) {
			if _, auditErr := r.services.Audit.Record("query.denied", "query", query.QueryID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "deny", core.RiskLevelL1, nil, map[string]any{
				"to_user_email": targetUser.Email,
			}); auditErr != nil {
				log.Printf("audit record failed for denied query: %v", auditErr)
			}
			writeError(w, http.StatusForbidden, "query is not allowed")
			return
		}
		writeServiceError(w, err, "query evaluation failed")
		return
	}

	if _, err := r.services.Audit.Record("query.completed", "query", query.QueryID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "allow", core.RiskLevelL1, response.PolicyBasis, map[string]any{
		"artifact_count": len(response.Artifacts),
	}); err != nil {
		log.Printf("audit record failed for completed query: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"query_id": query.QueryID,
		"status":   core.QueryStateCompleted,
	})
}

func (r *router) handleGetQueryResult(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	queryID := strings.TrimPrefix(req.URL.Path, "/v1/queries/")
	query, response, found, err := r.services.Queries.FindResult(queryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load query result")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "query not found")
		return
	}
	if query.FromAgentID != agent.AgentID && query.ToAgentID != agent.AgentID {
		writeError(w, http.StatusForbidden, "query result is not visible to this agent")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"query_id": query.QueryID,
		"state":    query.State,
		"response": response,
	})
}

func (r *router) handleAuditSummary(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var since time.Time
	if raw := req.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = parsed
	}

	events, err := r.services.Audit.Summary(agent.AgentID, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load audit summary")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

type sendRequestToPeerRequest struct {
	ToUserEmail       string         `json:"to_user_email"`
	RequestType       string         `json:"request_type"`
	Title             string         `json:"title"`
	Content           string         `json:"content"`
	StructuredPayload map[string]any `json:"structured_payload"`
}

func (r *router) handleSendRequestToPeer(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input sendRequestToPeerRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := core.ValidateRequestInput(input.ToUserEmail, input.RequestType, input.Title, input.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetUser, exists, err := r.services.Agents.FindUserByEmail(input.ToUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target user not found")
		return
	}
	targetAgent, exists, err := r.services.Agents.FindAgentByUserID(targetUser.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target agent")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target agent not found")
		return
	}

	requestRecord, err := r.services.Requests.Send(core.Request{
		OrgID:             agent.OrgID,
		FromAgentID:       agent.AgentID,
		FromUserID:        user.UserID,
		ToAgentID:         targetAgent.AgentID,
		ToUserID:          targetUser.UserID,
		RequestType:       input.RequestType,
		Title:             input.Title,
		Content:           input.Content,
		StructuredPayload: input.StructuredPayload,
		RiskLevel:         core.RiskLevelL1,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request creation failed")
		return
	}

	if _, err := r.services.Audit.Record("request.created", "request", requestRecord.RequestID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "allow", requestRecord.RiskLevel, nil, map[string]any{
		"request_type":  input.RequestType,
		"to_user_email": targetUser.Email,
	}); err != nil {
		log.Printf("audit record failed for request creation: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"request_id": requestRecord.RequestID,
		"state":      requestRecord.State,
	})
}

func (r *router) handleListIncomingRequests(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	requestsList, err := r.services.Requests.ListIncoming(agent.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load incoming requests")
		return
	}

	items := make([]map[string]any, 0, len(requestsList))
	for _, requestRecord := range requestsList {
		sender, exists, err := r.services.Agents.FindUserByID(requestRecord.FromUserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve request sender")
			return
		}
		if !exists {
			continue
		}

		items = append(items, map[string]any{
			"request_id":      requestRecord.RequestID,
			"from_user_email": sender.Email,
			"request_type":    requestRecord.RequestType,
			"title":           requestRecord.Title,
			"state":           requestRecord.State,
			"approval_state":  requestRecord.ApprovalState,
			"created_at":      requestRecord.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"requests": items})
}

type respondToRequestRequest struct {
	Response core.RequestResponseAction `json:"response"`
	Message  string                     `json:"message"`
}

func (r *router) handleRespondToRequest(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	requestID, ok := trimActionPath(req.URL.Path, "/v1/requests/", "/respond")
	if !ok {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	var input respondToRequestRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := core.ValidateRequestResponseInput(requestID, input.Response); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	requestRecord, approval, err := r.services.Requests.Respond(agent, requestID, input.Response, input.Message)
	if err != nil {
		switch {
		case errors.Is(err, requests.ErrUnknownRequest):
			writeError(w, http.StatusNotFound, err.Error())
			return
		case errors.Is(err, requests.ErrRequestNotVisible):
			writeError(w, http.StatusForbidden, err.Error())
			return
		case errors.Is(err, requests.ErrRequestAlreadyClosed):
			writeError(w, http.StatusConflict, err.Error())
			return
		default:
			writeError(w, http.StatusInternalServerError, "request response failed")
			return
		}
	}

	eventKind := "request.responded"
	metadata := map[string]any{
		"state": requestRecord.State,
	}
	if approval != nil {
		eventKind = "request.approval_requested"
		metadata["approval_id"] = approval.ApprovalID
	}
	if _, err := r.services.Audit.Record(eventKind, "request", requestRecord.RequestID, requestRecord.OrgID, agent.AgentID, requestRecord.FromAgentID, "allow", requestRecord.RiskLevel, nil, metadata); err != nil {
		log.Printf("audit record failed for request response: %v", err)
	}

	payload := map[string]any{
		"request_id":     requestRecord.RequestID,
		"state":          requestRecord.State,
		"approval_state": requestRecord.ApprovalState,
	}
	if approval != nil {
		payload["approval_id"] = approval.ApprovalID
	}
	writeJSON(w, http.StatusOK, payload)
}

func (r *router) handleListPendingApprovals(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	approvalsList, err := r.services.Approvals.ListPending(agent.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load pending approvals")
		return
	}
	items := make([]map[string]any, 0, len(approvalsList))
	for _, approval := range approvalsList {
		items = append(items, map[string]any{
			"approval_id":  approval.ApprovalID,
			"subject_type": approval.SubjectType,
			"subject_id":   approval.SubjectID,
			"reason":       approval.Reason,
			"created_at":   approval.CreatedAt,
			"expires_at":   approval.ExpiresAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": items})
}

type resolveApprovalRequest struct {
	Decision core.ApprovalState `json:"decision"`
}

func (r *router) handleResolveApproval(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	approvalID, ok := trimActionPath(req.URL.Path, "/v1/approvals/", "/resolve")
	if !ok {
		writeError(w, http.StatusNotFound, "approval not found")
		return
	}

	var input resolveApprovalRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := core.ValidateApprovalResolutionInput(approvalID, input.Decision); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	approval, requestRecord, err := r.services.Approvals.Resolve(agent, approvalID, input.Decision)
	if err != nil {
		switch {
		case errors.Is(err, approvals.ErrUnknownApproval):
			writeError(w, http.StatusNotFound, err.Error())
			return
		case errors.Is(err, approvals.ErrApprovalNotVisible):
			writeError(w, http.StatusForbidden, err.Error())
			return
		case errors.Is(err, approvals.ErrApprovalResolved):
			writeError(w, http.StatusConflict, err.Error())
			return
		default:
			writeError(w, http.StatusInternalServerError, "approval resolution failed")
			return
		}
	}

	if _, err := r.services.Audit.Record("approval.resolved", "approval", approval.ApprovalID, approval.OrgID, agent.AgentID, requestRecord.FromAgentID, "allow", requestRecord.RiskLevel, nil, map[string]any{
		"decision":      approval.State,
		"request_id":    requestRecord.RequestID,
		"request_state": requestRecord.State,
	}); err != nil {
		log.Printf("audit record failed for approval resolution: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"approval_id": approval.ApprovalID,
		"state":       approval.State,
		"request_id":  requestRecord.RequestID,
	})
}

func (r *router) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		accessToken, ok := accessTokenFromRequest(req)
		if !ok {
			writeAuthError(w, "missing bearer token")
			return
		}

		agent, user, err := r.services.Agents.AuthenticateAgent(accessToken)
		if err != nil {
			switch {
			case errors.Is(err, agents.ErrUnknownAgentToken),
				errors.Is(err, agents.ErrInvalidAgentToken),
				errors.Is(err, agents.ErrExpiredAgentToken),
				errors.Is(err, agents.ErrRevokedAgentToken),
				errors.Is(err, agents.ErrUnknownAgent),
				errors.Is(err, agents.ErrUnknownAgentOwner):
				writeAuthError(w, "invalid or expired access token")
				return
			default:
				writeError(w, http.StatusInternalServerError, "agent authentication failed")
				return
			}
		}

		ctx := context.WithValue(req.Context(), currentAgentContextKey{}, agent)
		ctx = context.WithValue(ctx, currentUserContextKey{}, user)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func currentActor(req *http.Request) (core.Agent, core.User, bool) {
	agent, ok := req.Context().Value(currentAgentContextKey{}).(core.Agent)
	if !ok {
		return core.Agent{}, core.User{}, false
	}
	user, ok := req.Context().Value(currentUserContextKey{}).(core.User)
	if !ok {
		return core.Agent{}, core.User{}, false
	}
	return agent, user, true
}

func accessTokenFromRequest(req *http.Request) (string, bool) {
	authorization := strings.TrimSpace(req.Header.Get("Authorization"))
	if authorization != "" {
		prefix := "bearer "
		if len(authorization) < len(prefix) || !strings.EqualFold(authorization[:len(prefix)], prefix) {
			return "", false
		}
		return strings.TrimSpace(authorization[len(prefix):]), true
	}

	token := strings.TrimSpace(req.Header.Get("X-Agent-Token"))
	return token, token != ""
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]any{
		"error": message,
	})
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="alice"`)
	writeError(w, http.StatusUnauthorized, message)
}

func trimActionPath(path, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	value := strings.TrimPrefix(path, prefix)
	value = strings.TrimSuffix(value, suffix)
	value = strings.Trim(value, "/")
	return value, value != ""
}

func writeServiceError(w http.ResponseWriter, err error, fallback string) {
	if core.IsValidationError(err) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, fallback)
}
