package webui

import (
	"context"
	"strings"

	"alice/internal/core"
	"alice/internal/websession"
)

// AdminRepo is the narrow storage surface the admin-UI sign-in flow needs to
// resolve an (org slug, email) pair to the backing user, org, and agent. It
// is satisfied by the repositories the app layer already wires up.
type AdminRepo interface {
	FindOrganizationBySlug(ctx context.Context, slug string) (core.Organization, bool, error)
	FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error)
	FindAgentByUserID(ctx context.Context, userID string) (core.Agent, bool, error)
}

// NewAdminLookup wraps AdminRepo so the websession service can resolve
// sign-in attempts through it.
func NewAdminLookup(repos AdminRepo) websession.AdminLookup {
	return &adminLookup{repos: repos}
}

type adminLookup struct {
	repos AdminRepo
}

func (l *adminLookup) LookupAdmin(ctx context.Context, orgSlug, email string) (websession.AdminTarget, error) {
	slug := strings.ToLower(strings.TrimSpace(orgSlug))
	addr := strings.ToLower(strings.TrimSpace(email))

	org, ok, err := l.repos.FindOrganizationBySlug(ctx, slug)
	if err != nil {
		return websession.AdminTarget{}, err
	}
	if !ok {
		return websession.AdminTarget{}, websession.ErrAdminNotFound
	}

	user, ok, err := l.repos.FindUserByEmail(ctx, org.OrgID, addr)
	if err != nil {
		return websession.AdminTarget{}, err
	}
	if !ok {
		return websession.AdminTarget{}, websession.ErrAdminNotFound
	}
	if user.Role != core.UserRoleAdmin {
		return websession.AdminTarget{}, websession.ErrNotAdmin
	}

	agent, ok, err := l.repos.FindAgentByUserID(ctx, user.UserID)
	if err != nil {
		return websession.AdminTarget{}, err
	}
	if !ok {
		// The user is an admin but has no registered agent. Downstream
		// admin calls key off AgentID, so we treat this as a missing
		// admin; the sign-in page advises running `alice register` first.
		return websession.AdminTarget{}, websession.ErrAdminNotFound
	}

	return websession.AdminTarget{
		UserID:  user.UserID,
		OrgID:   org.OrgID,
		AgentID: agent.AgentID,
		Email:   user.Email,
	}, nil
}
