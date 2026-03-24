package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/approvals"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/queries"
	"alice/internal/requests"
)

// ipBucket is a simple token-bucket rate limiter entry per IP address.
type ipBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// ipRateLimiter holds per-IP token buckets for unauthenticated endpoint protection.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	// rate is tokens added per nanosecond (10 per minute = 10/60e9 per ns)
	rate  float64
	burst float64
}

func newIPRateLimiter(ratePerMin, burst float64) *ipRateLimiter {
	return &ipRateLimiter{
		buckets: make(map[string]*ipBucket),
		rate:    ratePerMin / 60e9,
		burst:   burst,
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{tokens: l.burst, lastSeen: time.Now()}
		l.buckets[ip] = b
	}
	l.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastSeen)
	b.tokens += float64(elapsed) * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type router struct {
	services    services.Container
	mux         *http.ServeMux
	rateLimiter *ipRateLimiter
}

type currentAgentContextKey struct{}
type currentUserContextKey struct{}

func NewRouter(services services.Container) http.Handler {
	r := &router{
		services:    services,
		mux:         http.NewServeMux(),
		rateLimiter: newIPRateLimiter(10, 10),
	}
	r.routes()
	return r.securityHeaders(r.mux)
}

func (r *router) routes() {
	r.mux.HandleFunc("GET /healthz", r.handleHealthz)
	r.mux.Handle("POST /v1/agents/register/challenge", r.rateLimit(r.limitBody(http.HandlerFunc(r.handleBeginRegisterAgent))))
	r.mux.Handle("POST /v1/agents/register", r.rateLimit(r.limitBody(http.HandlerFunc(r.handleRegisterAgent))))
	r.mux.Handle("POST /v1/artifacts", r.limitBody(r.requireAuth(http.HandlerFunc(r.handlePublishArtifact))))
	r.mux.Handle("POST /v1/artifacts/", r.limitBody(r.requireAuth(http.HandlerFunc(r.handleCorrectArtifact))))
	r.mux.Handle("POST /v1/policy-grants", r.limitBody(r.requireAuth(http.HandlerFunc(r.handleGrantPermission))))
	r.mux.Handle("DELETE /v1/policy-grants/", r.requireAuth(http.HandlerFunc(r.handleRevokePermission)))
	r.mux.Handle("GET /v1/peers", r.requireAuth(http.HandlerFunc(r.handleListAllowedPeers)))
	r.mux.Handle("POST /v1/queries", r.limitBody(r.requireAuth(http.HandlerFunc(r.handleQueryPeerStatus))))
	r.mux.Handle("GET /v1/queries/", r.requireAuth(http.HandlerFunc(r.handleGetQueryResult)))
	r.mux.Handle("POST /v1/requests", r.limitBody(r.requireAuth(http.HandlerFunc(r.handleSendRequestToPeer))))
	r.mux.Handle("GET /v1/requests/incoming", r.requireAuth(http.HandlerFunc(r.handleListIncomingRequests)))
	r.mux.Handle("POST /v1/requests/", r.limitBody(r.requireAuth(http.HandlerFunc(r.handleRespondToRequest))))
	r.mux.Handle("GET /v1/approvals", r.requireAuth(http.HandlerFunc(r.handleListPendingApprovals)))
	r.mux.Handle("POST /v1/approvals/", r.limitBody(r.requireAuth(http.HandlerFunc(r.handleResolveApproval))))
	r.mux.Handle("GET /v1/audit/summary", r.requireAuth(http.HandlerFunc(r.handleAuditSummary)))
}

func (r *router) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type registerAgentRequest struct {
	OrgSlug    string `json:"org_slug"`
	OwnerEmail string `json:"owner_email"`
	AgentName  string `json:"agent_name"`
	ClientType string `json:"client_type"`
	PublicKey  string `json:"public_key"`
}

func (r *router) handleBeginRegisterAgent(w http.ResponseWriter, req *http.Request) {
	var input registerAgentRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	challenge, payload, err := r.services.Agents.BeginRegistration(req.Context(), input.OrgSlug, input.OwnerEmail, input.AgentName, input.ClientType, input.PublicKey)
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
		writeDecodeError(w, err)
		return
	}

	org, user, agent, accessToken, expiresAt, err := r.services.Agents.CompleteRegistration(req.Context(), input.ChallengeID, input.ChallengeSignature)
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

	if _, err := r.services.Audit.Record(req.Context(), "agent.registered", "agent", agent.AgentID, org.OrgID, agent.AgentID, "", "allow", "", nil, map[string]any{
		"owner_email":      user.Email,
		"auth_method":      "ed25519_challenge",
		"token_expires_at": expiresAt,
	}); err != nil {
		slog.Error("audit record failed", "op", "agent_registration", "err", err)
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
		writeDecodeError(w, err)
		return
	}

	artifact, err := r.services.Artifacts.PublishArtifact(req.Context(), agent, user, input.Artifact)
	if err != nil {
		writeServiceError(w, err, "artifact publish failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "artifact.published", "artifact", artifact.ArtifactID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"artifact_type": artifact.Type,
	}); err != nil {
		slog.Error("audit record failed", "op", "artifact_publish", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id": artifact.ArtifactID,
		"stored":      true,
	})
}

type correctArtifactRequest struct {
	Artifact core.Artifact `json:"artifact"`
}

func (r *router) handleCorrectArtifact(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	artifactID, ok := trimActionPath(req.URL.Path, "/v1/artifacts/", "/correct")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var input correctArtifactRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	corrected, err := r.services.Artifacts.CorrectArtifact(req.Context(), agent, user, artifactID, input.Artifact)
	if err != nil {
		writeServiceError(w, err, "artifact correction failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "artifact.corrected", "artifact", corrected.ArtifactID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"artifact_type":        corrected.Type,
		"supersedes_artifact_id": artifactID,
	}); err != nil {
		slog.Error("audit record failed", "op", "artifact_correct", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id":            corrected.ArtifactID,
		"supersedes_artifact_id": artifactID,
		"stored":                 true,
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
		writeDecodeError(w, err)
		return
	}

	granteeUser, exists, err := r.services.Agents.FindUserByEmail(req.Context(), agent.OrgID, input.GranteeUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve grantee user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "grantee user not found")
		return
	}

	grant, err := r.services.Policy.Grant(req.Context(), agent.OrgID, user, granteeUser, input.ScopeType, input.ScopeRef, input.AllowedArtifactTypes, input.MaxSensitivity, input.AllowedPurposes)
	if err != nil {
		writeServiceError(w, err, "grant creation failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "policy.grant.created", "policy_grant", grant.PolicyGrantID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, []string{"grant:" + grant.PolicyGrantID}, map[string]any{
		"grantee_email": granteeUser.Email,
		"scope_ref":     grant.ScopeRef,
	}); err != nil {
		slog.Error("audit record failed", "op", "grant_create", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"policy_grant_id": grant.PolicyGrantID,
	})
}

func (r *router) handleRevokePermission(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	grantID := strings.TrimPrefix(req.URL.Path, "/v1/policy-grants/")
	if grantID == "" {
		writeError(w, http.StatusBadRequest, "grant_id is required")
		return
	}

	grant, err := r.services.Policy.RevokeGrant(req.Context(), grantID, user.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "grant not found or not owned by caller")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "policy.grant.revoked", "policy_grant", grant.PolicyGrantID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, []string{"grant:" + grant.PolicyGrantID}, map[string]any{
		"scope_ref": grant.ScopeRef,
	}); err != nil {
		slog.Error("audit record failed", "op", "grant_revoke", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"policy_grant_id": grant.PolicyGrantID,
		"revoked":         true,
	})
}

func (r *router) handleListAllowedPeers(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	limit, offset := parsePagination(req)
	grants, err := r.services.Policy.ListAllowedPeers(req.Context(), user.UserID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list allowed peers")
		return
	}
	peers := make([]map[string]any, 0, len(grants))
	for _, grant := range grants {
		owner, exists, err := r.services.Agents.FindUserByID(req.Context(), grant.GrantorUserID)
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

	if _, err := r.services.Audit.Record(req.Context(), "policy.allowed_peers.listed", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL0, nil, nil); err != nil {
		slog.Error("audit record failed", "op", "list_peers", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peers":       peers,
		"next_cursor": nextCursor(len(grants), limit, offset),
	})
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
		writeDecodeError(w, err)
		return
	}

	if err := core.ValidateQueryInput(input.ToUserEmail, input.Purpose, input.RequestedTypes, input.TimeWindow); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetUser, exists, err := r.services.Agents.FindUserByEmail(req.Context(), agent.OrgID, input.ToUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target user not found")
		return
	}
	targetAgent, exists, err := r.services.Agents.FindAgentByUserID(req.Context(), targetUser.UserID)
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

	response, err := r.services.Queries.Evaluate(req.Context(), query)
	if err != nil {
		if errors.Is(err, queries.ErrPermissionDenied) {
			if _, auditErr := r.services.Audit.Record(req.Context(), "query.denied", "query", query.QueryID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "deny", core.RiskLevelL1, nil, map[string]any{
				"to_user_email": targetUser.Email,
			}); auditErr != nil {
				slog.Error("audit record failed", "op", "query_denied", "err", auditErr)
			}
			writeError(w, http.StatusForbidden, "query is not allowed")
			return
		}
		writeServiceError(w, err, "query evaluation failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "query.completed", "query", query.QueryID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "allow", core.RiskLevelL1, response.PolicyBasis, map[string]any{
		"artifact_count": len(response.Artifacts),
	}); err != nil {
		slog.Error("audit record failed", "op", "query_complete", "err", err)
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
	query, response, found, err := r.services.Queries.FindResult(req.Context(), queryID)
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

	limit, offset := parsePagination(req)
	events, err := r.services.Audit.Summary(req.Context(), agent.AgentID, since, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load audit summary")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":      events,
		"next_cursor": nextCursor(len(events), limit, offset),
	})
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
		writeDecodeError(w, err)
		return
	}
	if err := core.ValidateRequestInput(input.ToUserEmail, input.RequestType, input.Title, input.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetUser, exists, err := r.services.Agents.FindUserByEmail(req.Context(), agent.OrgID, input.ToUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target user not found")
		return
	}
	targetAgent, exists, err := r.services.Agents.FindAgentByUserID(req.Context(), targetUser.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target agent")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target agent not found")
		return
	}

	requestRecord, err := r.services.Requests.Send(req.Context(), core.Request{
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

	if _, err := r.services.Audit.Record(req.Context(), "request.created", "request", requestRecord.RequestID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "allow", requestRecord.RiskLevel, nil, map[string]any{
		"request_type":  input.RequestType,
		"to_user_email": targetUser.Email,
	}); err != nil {
		slog.Error("audit record failed", "op", "request_create", "err", err)
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

	limit, offset := parsePagination(req)
	requestsList, err := r.services.Requests.ListIncoming(req.Context(), agent.AgentID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load incoming requests")
		return
	}

	items := make([]map[string]any, 0, len(requestsList))
	for _, requestRecord := range requestsList {
		sender, exists, err := r.services.Agents.FindUserByID(req.Context(), requestRecord.FromUserID)
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

	writeJSON(w, http.StatusOK, map[string]any{
		"requests":    items,
		"next_cursor": nextCursor(len(requestsList), limit, offset),
	})
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
		writeDecodeError(w, err)
		return
	}
	if err := core.ValidateRequestResponseInput(requestID, input.Response); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	requestRecord, approval, err := r.services.Requests.Respond(req.Context(), agent, requestID, input.Response, input.Message)
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
		case errors.Is(err, requests.ErrExpiredRequest):
			writeError(w, http.StatusGone, err.Error())
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
	if _, err := r.services.Audit.Record(req.Context(), eventKind, "request", requestRecord.RequestID, requestRecord.OrgID, agent.AgentID, requestRecord.FromAgentID, "allow", requestRecord.RiskLevel, nil, metadata); err != nil {
		slog.Error("audit record failed", "op", "request_respond", "err", err)
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

	limit, offset := parsePagination(req)
	approvalsList, err := r.services.Approvals.ListPending(req.Context(), agent.AgentID, limit, offset)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"approvals":   items,
		"next_cursor": nextCursor(len(approvalsList), limit, offset),
	})
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
		writeDecodeError(w, err)
		return
	}
	if err := core.ValidateApprovalResolutionInput(approvalID, input.Decision); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	approval, requestRecord, err := r.services.Approvals.Resolve(req.Context(), agent, approvalID, input.Decision)
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
		case errors.Is(err, approvals.ErrExpiredApproval):
			writeError(w, http.StatusGone, err.Error())
			return
		default:
			writeError(w, http.StatusInternalServerError, "approval resolution failed")
			return
		}
	}

	if _, err := r.services.Audit.Record(req.Context(), "approval.resolved", "approval", approval.ApprovalID, approval.OrgID, agent.AgentID, requestRecord.FromAgentID, "allow", requestRecord.RiskLevel, nil, map[string]any{
		"decision":      approval.State,
		"request_id":    requestRecord.RequestID,
		"request_state": requestRecord.State,
	}); err != nil {
		slog.Error("audit record failed", "op", "approval_resolve", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"approval_id": approval.ApprovalID,
		"state":       approval.State,
		"request_id":  requestRecord.RequestID,
	})
}

func (r *router) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ip := clientIP(req)
		if !r.rateLimiter.allow(ip) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, req)
	})
}

func clientIP(req *http.Request) string {
	if forwarded := req.Header.Get("X-Forwarded-For"); forwarded != "" {
		if idx := strings.IndexByte(forwarded, ','); idx >= 0 {
			return strings.TrimSpace(forwarded[:idx])
		}
		return strings.TrimSpace(forwarded)
	}
	host := req.RemoteAddr
	if idx := strings.LastIndexByte(host, ':'); idx >= 0 {
		return host[:idx]
	}
	return host
}

func (r *router) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, req)
	})
}

func (r *router) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
		next.ServeHTTP(w, req)
	})
}

func (r *router) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		accessToken, ok := accessTokenFromRequest(req)
		if !ok {
			writeAuthError(w, "missing bearer token")
			return
		}

		agent, user, err := r.services.Agents.AuthenticateAgent(req.Context(), accessToken)
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
	if authorization == "" {
		return "", false
	}
	prefix := "bearer "
	if len(authorization) < len(prefix) || !strings.EqualFold(authorization[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(authorization[len(prefix):]), true
}

// parsePagination reads ?limit= and ?cursor= from the request.
// limit defaults to 50 and is capped at 200. cursor is a base64-encoded offset.
func parsePagination(req *http.Request) (limit, offset int) {
	limit = 50
	if raw := req.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	offset = 0
	if cursor := req.URL.Query().Get("cursor"); cursor != "" {
		if decoded, err := base64.StdEncoding.DecodeString(cursor); err == nil {
			if n, err := strconv.Atoi(string(decoded)); err == nil && n > 0 {
				offset = n
			}
		}
	}
	return limit, offset
}

// nextCursor returns a cursor string pointing to the next page, or empty string
// when results are fewer than limit (indicating the last page).
func nextCursor(count, limit, offset int) string {
	if count < limit {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
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

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body")
}
