package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/queries"
)

type router struct {
	services services.Container
	mux      *http.ServeMux
}

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
	r.mux.HandleFunc("POST /v1/agents/register", r.handleRegisterAgent)
	r.mux.HandleFunc("POST /v1/artifacts", r.handlePublishArtifact)
	r.mux.HandleFunc("POST /v1/policy-grants", r.handleGrantPermission)
	r.mux.HandleFunc("GET /v1/peers", r.handleListAllowedPeers)
	r.mux.HandleFunc("POST /v1/queries", r.handleQueryPeerStatus)
	r.mux.HandleFunc("GET /v1/queries/", r.handleGetQueryResult)
	r.mux.HandleFunc("GET /v1/audit/summary", r.handleAuditSummary)
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

func (r *router) handleRegisterAgent(w http.ResponseWriter, req *http.Request) {
	var input registerAgentRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	org, user, agent, err := r.services.Agents.RegisterAgent(input.OrgSlug, input.OwnerEmail, input.AgentName, input.ClientType, input.PublicKey, input.Capabilities)
	if err != nil {
		writeServiceError(w, err, "agent registration failed")
		return
	}

	if _, err := r.services.Audit.Record("agent.registered", "agent", agent.AgentID, org.OrgID, agent.AgentID, "", "allow", "", nil, map[string]any{
		"owner_email": user.Email,
	}); err != nil {
		log.Printf("audit record failed for agent registration: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agent.AgentID,
		"org_id":   org.OrgID,
		"status":   agent.Status,
	})
}

type publishArtifactRequest struct {
	Artifact core.Artifact `json:"artifact"`
}

func (r *router) handlePublishArtifact(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := r.requireAgent(w, req)
	if !ok {
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
	agent, user, ok := r.requireAgent(w, req)
	if !ok {
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
	agent, user, ok := r.requireAgent(w, req)
	if !ok {
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
	agent, user, ok := r.requireAgent(w, req)
	if !ok {
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
	agent, _, ok := r.requireAgent(w, req)
	if !ok {
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
	agent, _, ok := r.requireAgent(w, req)
	if !ok {
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

func (r *router) requireAgent(w http.ResponseWriter, req *http.Request) (core.Agent, core.User, bool) {
	agentID := strings.TrimSpace(req.Header.Get("X-Agent-ID"))
	if agentID == "" {
		writeError(w, http.StatusUnauthorized, "missing X-Agent-ID header")
		return core.Agent{}, core.User{}, false
	}

	agent, user, err := r.services.Agents.RequireAgent(agentID)
	if err != nil {
		if errors.Is(err, agents.ErrUnknownAgent) || errors.Is(err, agents.ErrUnknownAgentOwner) {
			writeError(w, http.StatusUnauthorized, err.Error())
			return core.Agent{}, core.User{}, false
		}
		writeError(w, http.StatusInternalServerError, "agent authentication failed")
		return core.Agent{}, core.User{}, false
	}

	return agent, user, true
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

func writeServiceError(w http.ResponseWriter, err error, fallback string) {
	if core.IsValidationError(err) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, fallback)
}
