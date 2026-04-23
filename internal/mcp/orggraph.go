package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// orggraph-flavored MCP handlers. All admin-gated writes require
// confirm=true so a misbehaving model cannot casually mutate the graph.

func (s *Server) handleCreateTeam(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	name := stringArg(args, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	body := map[string]any{"name": name}
	if parent := stringArg(args, "parent_team_id"); parent != "" {
		body["parent_team_id"] = parent
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/org/teams", body)
}

func (s *Server) handleListTeams(ctx context.Context, args map[string]any) (any, error) {
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/org/teams"+paginationQuery(args, ""), nil)
}

func (s *Server) handleAddTeamMember(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	teamID := stringArg(args, "team_id")
	email := stringArg(args, "user_email")
	if teamID == "" || email == "" {
		return nil, fmt.Errorf("team_id and user_email are required")
	}
	body := map[string]any{"user_email": email}
	if role := stringArg(args, "role"); role != "" {
		body["role"] = role
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/org/teams/"+url.PathEscape(teamID)+"/members", body)
}

func (s *Server) handleRemoveTeamMember(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	teamID := stringArg(args, "team_id")
	email := stringArg(args, "user_email")
	if teamID == "" || email == "" {
		return nil, fmt.Errorf("team_id and user_email are required")
	}
	return s.callAuthedJSON(ctx, http.MethodDelete, "/v1/org/teams/"+url.PathEscape(teamID)+"/members/"+url.PathEscape(email), nil)
}

func (s *Server) handleListTeamMembers(ctx context.Context, args map[string]any) (any, error) {
	teamID := stringArg(args, "team_id")
	if teamID == "" {
		return nil, fmt.Errorf("team_id is required")
	}
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/org/teams/"+url.PathEscape(teamID)+"/members"+paginationQuery(args, ""), nil)
}

func (s *Server) handleAssignManager(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	user := stringArg(args, "user_email")
	mgr := stringArg(args, "manager_email")
	if user == "" || mgr == "" {
		return nil, fmt.Errorf("user_email and manager_email are required")
	}
	return s.callAuthedJSON(ctx, http.MethodPost, "/v1/org/manager-edges", map[string]any{
		"user_email":    user,
		"manager_email": mgr,
	})
}

func (s *Server) handleRevokeManager(ctx context.Context, args map[string]any) (any, error) {
	if args["confirm"] != true {
		return nil, fmt.Errorf("refusing to perform sensitive action without confirm=true; re-run with confirm=true if intended")
	}
	user := stringArg(args, "user_email")
	if user == "" {
		return nil, fmt.Errorf("user_email is required")
	}
	return s.callAuthedJSON(ctx, http.MethodDelete, "/v1/org/manager-edges/"+url.PathEscape(user), nil)
}

func (s *Server) handleGetManagerChain(ctx context.Context, args map[string]any) (any, error) {
	user := stringArg(args, "user_email")
	if user == "" {
		return nil, fmt.Errorf("user_email is required")
	}
	return s.callAuthedJSON(ctx, http.MethodGet, "/v1/org/manager-edges/"+url.PathEscape(user), nil)
}
