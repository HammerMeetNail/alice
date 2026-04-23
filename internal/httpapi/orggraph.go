package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"alice/internal/core"
	"alice/internal/orggraph"
)

// Route shapes implemented by this file (dispatched via prefix patterns in
// router.go):
//   POST   /v1/org/teams                               handleCreateTeam
//   GET    /v1/org/teams                               handleListTeams
//   GET    /v1/org/teams/:team_id/members              handleListTeamMembers
//   POST   /v1/org/teams/:team_id/members              handleAddTeamMember
//   DELETE /v1/org/teams/:team_id                      handleTeamDelete (team)
//   DELETE /v1/org/teams/:team_id/members/:user_email  handleTeamDelete (member)
//   POST   /v1/org/manager-edges                       handleAssignManager
//   GET    /v1/org/manager-edges/:user_email           handleGetManagerChain
//   DELETE /v1/org/manager-edges/:user_email           handleRevokeManager
//
// Subjects are addressed by email to match the rest of the HTTP surface
// (grants, requests); internal handlers resolve email → user_id scoped
// to the caller's org so cross-org isolation stays server-side.

type createTeamRequest struct {
	Name         string `json:"name"`
	ParentTeamID string `json:"parent_team_id,omitempty"`
}

type teamMemberRequest struct {
	UserEmail string `json:"user_email"`
	Role      string `json:"role,omitempty"`
}

type managerEdgeRequest struct {
	UserEmail    string `json:"user_email"`
	ManagerEmail string `json:"manager_email"`
}

func (r *router) handleCreateTeam(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}

	var input createTeamRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	team, err := r.services.OrgGraph.CreateTeam(req.Context(), agent, input.Name, input.ParentTeamID)
	if err != nil {
		writeOrgGraphError(w, err, "create team failed")
		return
	}
	r.auditOrgGraph(req, agent, "orggraph.team_created", "team", team.TeamID, map[string]any{
		"name":           team.Name,
		"parent_team_id": team.ParentTeamID,
	})
	writeJSON(w, http.StatusOK, teamJSON(team))
}

func (r *router) handleListTeams(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}
	limit, offset := parsePagination(req)
	teams, err := r.services.OrgGraph.ListTeams(req.Context(), agent, limit, offset)
	if err != nil {
		writeServiceError(w, err, "list teams failed")
		return
	}
	out := make([]map[string]any, 0, len(teams))
	for _, t := range teams {
		out = append(out, teamJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": out, "limit": limit, "offset": offset})
}

// handleListTeamMembers dispatches GET /v1/org/teams/:team_id/members.
func (r *router) handleListTeamMembers(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}
	teamID, ok := extractTeamMembersTail(req.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "GET /v1/org/teams/:team_id/members")
		return
	}
	limit, offset := parsePagination(req)
	members, err := r.services.OrgGraph.ListTeamMembers(req.Context(), agent, teamID, limit, offset)
	if err != nil {
		writeOrgGraphError(w, err, "list team members failed")
		return
	}
	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		out = append(out, teamMemberJSON(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out, "limit": limit, "offset": offset})
}

// handleAddTeamMember dispatches POST /v1/org/teams/:team_id/members.
func (r *router) handleAddTeamMember(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}
	teamID, ok := extractTeamMembersTail(req.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "POST /v1/org/teams/:team_id/members")
		return
	}
	var input teamMemberRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	user, err := r.resolveUserByEmail(req, agent.OrgID, input.UserEmail)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	role := core.TeamMemberRole(strings.TrimSpace(input.Role))
	member, err := r.services.OrgGraph.AddTeamMember(req.Context(), agent, teamID, user.UserID, role)
	if err != nil {
		writeOrgGraphError(w, err, "add team member failed")
		return
	}
	r.auditOrgGraph(req, agent, "orggraph.team_member_added", "team", teamID, map[string]any{
		"user_id": member.UserID,
		"role":    string(member.Role),
	})
	writeJSON(w, http.StatusOK, teamMemberJSON(member))
}

// handleTeamDelete dispatches both DELETE /v1/org/teams/:team_id and
// DELETE /v1/org/teams/:team_id/members/:user_email based on path shape.
func (r *router) handleTeamDelete(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}

	tail := strings.TrimPrefix(req.URL.Path, "/v1/org/teams/")
	parts := strings.Split(tail, "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		teamID := parts[0]
		if err := r.services.OrgGraph.DeleteTeam(req.Context(), agent, teamID); err != nil {
			writeOrgGraphError(w, err, "delete team failed")
			return
		}
		r.auditOrgGraph(req, agent, "orggraph.team_deleted", "team", teamID, nil)
		writeJSON(w, http.StatusOK, map[string]any{"team_id": teamID, "deleted": true})
	case len(parts) == 3 && parts[1] == "members" && parts[0] != "" && parts[2] != "":
		teamID, email := parts[0], parts[2]
		user, err := r.resolveUserByEmail(req, agent.OrgID, email)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err := r.services.OrgGraph.RemoveTeamMember(req.Context(), agent, teamID, user.UserID); err != nil {
			writeOrgGraphError(w, err, "remove team member failed")
			return
		}
		r.auditOrgGraph(req, agent, "orggraph.team_member_removed", "team", teamID, map[string]any{
			"user_id": user.UserID,
		})
		writeJSON(w, http.StatusOK, map[string]any{"team_id": teamID, "user_id": user.UserID, "removed": true})
	default:
		writeError(w, http.StatusNotFound, "DELETE /v1/org/teams/:team_id or /v1/org/teams/:team_id/members/:user_email")
	}
}

func (r *router) handleAssignManager(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}
	var input managerEdgeRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	user, err := r.resolveUserByEmail(req, agent.OrgID, input.UserEmail)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	manager, err := r.resolveUserByEmail(req, agent.OrgID, input.ManagerEmail)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	edge, err := r.services.OrgGraph.AssignManager(req.Context(), agent, user.UserID, manager.UserID)
	if err != nil {
		writeOrgGraphError(w, err, "assign manager failed")
		return
	}
	r.auditOrgGraph(req, agent, "orggraph.manager_assigned", "user", user.UserID, map[string]any{
		"edge_id":         edge.EdgeID,
		"manager_user_id": manager.UserID,
	})
	writeJSON(w, http.StatusOK, managerEdgeJSON(edge))
}

// handleGetManagerChain dispatches GET /v1/org/manager-edges/:user_email.
func (r *router) handleGetManagerChain(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}
	email := strings.TrimPrefix(req.URL.Path, "/v1/org/manager-edges/")
	email = strings.Trim(email, "/")
	if email == "" {
		writeError(w, http.StatusNotFound, "GET /v1/org/manager-edges/:user_email")
		return
	}
	user, err := r.resolveUserByEmail(req, agent.OrgID, email)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	chain, err := r.services.OrgGraph.ManagerChain(req.Context(), agent, user.UserID)
	if err != nil {
		writeOrgGraphError(w, err, "read manager chain failed")
		return
	}
	out := make([]map[string]any, 0, len(chain))
	for _, edge := range chain {
		out = append(out, managerEdgeJSON(edge))
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": user.UserID, "chain": out})
}

// handleRevokeManager dispatches DELETE /v1/org/manager-edges/:user_email.
func (r *router) handleRevokeManager(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.OrgGraph == nil {
		writeError(w, http.StatusNotImplemented, "org graph is not configured")
		return
	}
	email := strings.TrimPrefix(req.URL.Path, "/v1/org/manager-edges/")
	email = strings.Trim(email, "/")
	if email == "" {
		writeError(w, http.StatusNotFound, "DELETE /v1/org/manager-edges/:user_email")
		return
	}
	user, err := r.resolveUserByEmail(req, agent.OrgID, email)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := r.services.OrgGraph.RevokeManager(req.Context(), agent, user.UserID); err != nil {
		writeOrgGraphError(w, err, "revoke manager failed")
		return
	}
	r.auditOrgGraph(req, agent, "orggraph.manager_revoked", "user", user.UserID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"user_id": user.UserID, "revoked": true})
}

// --- shared helpers for this file ---

func (r *router) resolveUserByEmail(req *http.Request, orgID, email string) (core.User, error) {
	if strings.TrimSpace(email) == "" {
		return core.User{}, errors.New("user email is required")
	}
	user, ok, err := r.services.Agents.FindUserByEmail(req.Context(), orgID, email)
	if err != nil {
		return core.User{}, err
	}
	if !ok {
		return core.User{}, errors.New("user not found")
	}
	return user, nil
}

func writeOrgGraphError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, orggraph.ErrNotOrgAdmin):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, orggraph.ErrCrossOrg):
		// Cross-org leakage is hidden as a 404 to match the rest of the
		// API (see agents.FindUserByEmail); callers must not be able to
		// probe for the existence of other orgs' users/teams.
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, orggraph.ErrTeamNotFound), errors.Is(err, orggraph.ErrTeamMemberNotFound), errors.Is(err, orggraph.ErrUserNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, orggraph.ErrManagerCycle), errors.Is(err, orggraph.ErrSelfManager), errors.Is(err, orggraph.ErrInvalidMemberRole), errors.Is(err, orggraph.ErrTeamNameRequired):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeServiceError(w, err, fallback)
	}
}

func (r *router) auditOrgGraph(req *http.Request, agent core.Agent, kind, subjectType, subjectID string, meta map[string]any) {
	if r.services.Audit == nil {
		return
	}
	if _, err := r.services.Audit.Record(req.Context(), kind, subjectType, subjectID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL0, nil, meta); err != nil {
		slog.Error("audit record failed", "op", kind, "err", err)
	}
}

// extractTeamMembersTail returns :team_id when the path is
// /v1/org/teams/:team_id/members (possibly with a trailing slash).
func extractTeamMembersTail(path string) (string, bool) {
	tail := strings.TrimPrefix(path, "/v1/org/teams/")
	tail = strings.TrimSuffix(tail, "/")
	parts := strings.Split(tail, "/")
	if len(parts) == 2 && parts[1] == "members" && parts[0] != "" {
		return parts[0], true
	}
	return "", false
}

func teamJSON(t core.Team) map[string]any {
	out := map[string]any{
		"team_id":    t.TeamID,
		"org_id":     t.OrgID,
		"name":       t.Name,
		"created_at": t.CreatedAt,
	}
	if t.ParentTeamID != "" {
		out["parent_team_id"] = t.ParentTeamID
	}
	return out
}

func teamMemberJSON(m core.TeamMember) map[string]any {
	return map[string]any{
		"team_id":   m.TeamID,
		"user_id":   m.UserID,
		"role":      string(m.Role),
		"joined_at": m.JoinedAt,
	}
}

func managerEdgeJSON(e core.ManagerEdge) map[string]any {
	out := map[string]any{
		"edge_id":         e.EdgeID,
		"user_id":         e.UserID,
		"manager_user_id": e.ManagerUserID,
		"effective_at":    e.EffectiveAt,
	}
	if e.RevokedAt != nil {
		out["revoked_at"] = *e.RevokedAt
	}
	return out
}
