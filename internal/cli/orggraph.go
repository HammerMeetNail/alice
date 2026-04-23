package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// cmdTeam dispatches `alice team <sub>` commands. create / delete /
// add-member / remove-member require admin-gating server-side; list /
// members are readable by any org member.
func cmdTeam(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice team create|list|delete|add-member|remove-member|members [flags]")
	}
	sub := args[0]
	rest := args[1:]

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	switch sub {
	case "create":
		fs := flag.NewFlagSet("team create", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		name := fs.String("name", "", "team name (required)")
		parent := fs.String("parent", "", "optional parent team id (display only)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("--name is required")
		}
		body := map[string]any{"name": *name}
		if *parent != "" {
			body["parent_team_id"] = *parent
		}
		resp, err := client.Do(ctx, http.MethodPost, "/v1/org/teams", body, false)
		if err != nil {
			return err
		}
		return r.Emit("team created", map[string]any{
			"team_id": stringFrom(resp, "team_id"),
			"name":    stringFrom(resp, "name"),
		}, false)

	case "list":
		resp, err := client.Do(ctx, http.MethodGet, "/v1/org/teams", nil, false)
		if err != nil {
			return err
		}
		return r.EmitList("teams", ExtractList(resp, "teams"), false)

	case "delete":
		if len(rest) == 0 {
			return errors.New("usage: alice team delete <team_id>")
		}
		teamID := rest[0]
		resp, err := client.Do(ctx, http.MethodDelete, "/v1/org/teams/"+url.PathEscape(teamID), nil, false)
		if err != nil {
			return err
		}
		return r.Emit("team deleted", map[string]any{"team_id": stringFrom(resp, "team_id")}, false)

	case "add-member":
		fs := flag.NewFlagSet("team add-member", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		teamID := fs.String("team", "", "team id (required)")
		email := fs.String("email", "", "user email (required)")
		role := fs.String("role", "", "optional role: member (default) or lead")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *teamID == "" || *email == "" {
			return errors.New("--team and --email are required")
		}
		body := map[string]any{"user_email": *email}
		if *role != "" {
			body["role"] = *role
		}
		resp, err := client.Do(ctx, http.MethodPost, "/v1/org/teams/"+url.PathEscape(*teamID)+"/members", body, false)
		if err != nil {
			return err
		}
		return r.Emit("team member added", map[string]any{
			"team_id": *teamID,
			"user_id": stringFrom(resp, "user_id"),
			"role":    stringFrom(resp, "role"),
		}, false)

	case "remove-member":
		fs := flag.NewFlagSet("team remove-member", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		teamID := fs.String("team", "", "team id (required)")
		email := fs.String("email", "", "user email (required)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *teamID == "" || *email == "" {
			return errors.New("--team and --email are required")
		}
		resp, err := client.Do(ctx, http.MethodDelete, "/v1/org/teams/"+url.PathEscape(*teamID)+"/members/"+url.PathEscape(*email), nil, false)
		if err != nil {
			return err
		}
		return r.Emit("team member removed", map[string]any{
			"team_id": *teamID,
			"user_id": stringFrom(resp, "user_id"),
		}, false)

	case "members":
		if len(rest) == 0 {
			return errors.New("usage: alice team members <team_id>")
		}
		teamID := rest[0]
		resp, err := client.Do(ctx, http.MethodGet, "/v1/org/teams/"+url.PathEscape(teamID)+"/members", nil, false)
		if err != nil {
			return err
		}
		return r.EmitList("members", ExtractList(resp, "members"), false)

	default:
		return fmt.Errorf("unknown team subcommand %q (valid: create, list, delete, add-member, remove-member, members)", sub)
	}
}

// cmdManager dispatches `alice manager <sub>` commands: set, revoke, chain.
// All writes are admin-gated server-side; chain is readable by anyone in
// the org for now.
func cmdManager(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice manager set|revoke|chain [flags]")
	}
	sub := args[0]
	rest := args[1:]

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	switch sub {
	case "set":
		fs := flag.NewFlagSet("manager set", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		user := fs.String("user", "", "user email whose manager is being set")
		manager := fs.String("manager", "", "manager email to assign")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *user == "" || *manager == "" {
			return errors.New("--user and --manager are required")
		}
		resp, err := client.Do(ctx, http.MethodPost, "/v1/org/manager-edges", map[string]any{
			"user_email":    *user,
			"manager_email": *manager,
		}, false)
		if err != nil {
			return err
		}
		return r.Emit("manager assigned", map[string]any{
			"edge_id":         stringFrom(resp, "edge_id"),
			"user_id":         stringFrom(resp, "user_id"),
			"manager_user_id": stringFrom(resp, "manager_user_id"),
		}, false)

	case "revoke":
		fs := flag.NewFlagSet("manager revoke", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		user := fs.String("user", "", "user email whose manager edge should be revoked")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *user == "" {
			return errors.New("--user is required")
		}
		resp, err := client.Do(ctx, http.MethodDelete, "/v1/org/manager-edges/"+url.PathEscape(*user), nil, false)
		if err != nil {
			return err
		}
		return r.Emit("manager revoked", map[string]any{"user_id": stringFrom(resp, "user_id")}, false)

	case "chain":
		fs := flag.NewFlagSet("manager chain", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		user := fs.String("user", "", "user email")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *user == "" {
			return errors.New("--user is required")
		}
		resp, err := client.Do(ctx, http.MethodGet, "/v1/org/manager-edges/"+url.PathEscape(*user), nil, false)
		if err != nil {
			return err
		}
		return r.EmitList("chain", ExtractList(resp, "chain"), false)

	default:
		return fmt.Errorf("unknown manager subcommand %q (valid: set, revoke, chain)", sub)
	}
}
