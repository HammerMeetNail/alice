// Package orggraph implements team membership and the manager reporting
// graph that back the `team_scope` and `manager_scope` visibility modes.
// Writes are admin-gated; reads are available to any member of the org
// for membership listings. Visibility decisions consume the read helpers
// through the Evaluator adapter so the queries service never imports this
// package directly.
//
// Invariants:
//   - cross-org edges are rejected at the service layer. Teams live in a
//     single org; manager edges only join users in the same org.
//   - teams are a sealed visibility scope: a parent pointer is stored for
//     display purposes, but visibility decisions never walk it.
//   - manager edges are append-only. AssignManager revokes the prior
//     active edge before creating a new one; the storage layer enforces
//     at-most-one-active-edge per user.
//   - cycle detection runs before any write: if the proposed manager is
//     already downstream of the user, the call returns ErrManagerCycle.
package orggraph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

// MaxManagerChainDepth caps the upward walk performed by visibility
// decisions. The cap protects against data corruption that slipped past
// cycle detection and bounds the per-query cost of manager-scope checks.
const MaxManagerChainDepth = 10

// Errors the service returns. HTTP handlers translate these into specific
// status codes.
var (
	ErrTeamNotFound        = storage.ErrTeamNotFound
	ErrTeamMemberNotFound  = storage.ErrTeamMemberNotFound
	ErrNotOrgAdmin         = errors.New("caller is not an org admin")
	ErrCrossOrg            = errors.New("subject belongs to a different org")
	ErrSelfManager         = errors.New("user cannot be their own manager")
	ErrManagerCycle        = errors.New("proposed edge would form a cycle")
	ErrTeamNameRequired    = errors.New("team name is required")
	ErrInvalidMemberRole   = errors.New("team member role must be 'member' or 'lead'")
	ErrUserNotFound        = errors.New("user not found")
)

// UserLookup is the minimum directory surface the service uses to look up
// callers and subjects. The agents service implements it naturally.
type UserLookup interface {
	FindUserByID(ctx context.Context, userID string) (core.User, bool, error)
}

// Service owns writes and cycle detection; reads (for visibility
// enforcement) go through the Evaluator exposed alongside this service.
type Service struct {
	store storage.OrgGraphRepository
	users UserLookup
}

// NewService constructs the service. users may be nil in test fixtures
// that bypass admin-gating; production callers always pass a lookup.
func NewService(store storage.OrgGraphRepository, users UserLookup) *Service {
	return &Service{store: store, users: users}
}

// CreateTeam creates a new team in the caller's org. Admin-gated.
// parentTeamID may be empty. When non-empty it must belong to the same
// org as the caller.
func (s *Service) CreateTeam(ctx context.Context, agent core.Agent, name, parentTeamID string) (core.Team, error) {
	if strings.TrimSpace(name) == "" {
		return core.Team{}, ErrTeamNameRequired
	}
	if err := s.requireAdmin(ctx, agent); err != nil {
		return core.Team{}, err
	}
	if parentTeamID != "" {
		parent, ok, err := s.store.FindTeamByID(ctx, parentTeamID)
		if err != nil {
			return core.Team{}, fmt.Errorf("find parent team: %w", err)
		}
		if !ok {
			return core.Team{}, fmt.Errorf("%w: parent team", ErrTeamNotFound)
		}
		if parent.OrgID != agent.OrgID {
			return core.Team{}, ErrCrossOrg
		}
	}

	team := core.Team{
		TeamID:       id.New("team"),
		OrgID:        agent.OrgID,
		Name:         strings.TrimSpace(name),
		ParentTeamID: parentTeamID,
		CreatedAt:    time.Now().UTC(),
	}
	saved, err := s.store.SaveTeam(ctx, team)
	if err != nil {
		return core.Team{}, fmt.Errorf("save team: %w", err)
	}
	slog.Info("team created", "team_id", saved.TeamID, "org_id", saved.OrgID, "actor", agent.OwnerUserID)
	return saved, nil
}

// DeleteTeam removes a team from the caller's org. Admin-gated. The
// underlying storage layer cascades member rows.
func (s *Service) DeleteTeam(ctx context.Context, agent core.Agent, teamID string) error {
	team, err := s.teamInOrg(ctx, agent, teamID)
	if err != nil {
		return err
	}
	if err := s.requireAdmin(ctx, agent); err != nil {
		return err
	}
	if err := s.store.DeleteTeam(ctx, team.TeamID); err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	slog.Info("team deleted", "team_id", team.TeamID, "actor", agent.OwnerUserID)
	return nil
}

// ListTeams returns the caller's org's teams. Read-only; no admin check
// because team membership rosters are not sensitive in the ambient model.
func (s *Service) ListTeams(ctx context.Context, agent core.Agent, limit, offset int) ([]core.Team, error) {
	return s.store.ListTeamsForOrg(ctx, agent.OrgID, limit, offset)
}

// AddTeamMember adds a user to a team. Admin-gated. The user and the team
// must both belong to the caller's org.
func (s *Service) AddTeamMember(ctx context.Context, agent core.Agent, teamID, userID string, role core.TeamMemberRole) (core.TeamMember, error) {
	if role == "" {
		role = core.TeamMemberRoleMember
	}
	if role != core.TeamMemberRoleMember && role != core.TeamMemberRoleLead {
		return core.TeamMember{}, ErrInvalidMemberRole
	}
	team, err := s.teamInOrg(ctx, agent, teamID)
	if err != nil {
		return core.TeamMember{}, err
	}
	if err := s.requireAdmin(ctx, agent); err != nil {
		return core.TeamMember{}, err
	}
	if err := s.requireSameOrgUser(ctx, agent, userID); err != nil {
		return core.TeamMember{}, err
	}

	member := core.TeamMember{
		TeamID:   team.TeamID,
		UserID:   userID,
		Role:     role,
		JoinedAt: time.Now().UTC(),
	}
	if err := s.store.SaveTeamMember(ctx, member); err != nil {
		return core.TeamMember{}, fmt.Errorf("save team member: %w", err)
	}
	slog.Info("team member added", "team_id", team.TeamID, "user_id", userID, "role", role, "actor", agent.OwnerUserID)
	return member, nil
}

// RemoveTeamMember removes a user from a team. Admin-gated.
func (s *Service) RemoveTeamMember(ctx context.Context, agent core.Agent, teamID, userID string) error {
	team, err := s.teamInOrg(ctx, agent, teamID)
	if err != nil {
		return err
	}
	if err := s.requireAdmin(ctx, agent); err != nil {
		return err
	}
	if err := s.store.DeleteTeamMember(ctx, team.TeamID, userID); err != nil {
		return err
	}
	slog.Info("team member removed", "team_id", team.TeamID, "user_id", userID, "actor", agent.OwnerUserID)
	return nil
}

// ListTeamMembers returns the roster of a team. Any member of the org may
// list the roster; membership lists are not sensitive in the ambient
// model and org admins would need them audit-visible anyway.
func (s *Service) ListTeamMembers(ctx context.Context, agent core.Agent, teamID string, limit, offset int) ([]core.TeamMember, error) {
	if _, err := s.teamInOrg(ctx, agent, teamID); err != nil {
		return nil, err
	}
	return s.store.ListTeamMembers(ctx, teamID, limit, offset)
}

// AssignManager sets managerUserID as the active manager for userID.
// Admin-gated. Cycle detection runs before the write: if managerUserID is
// already downstream of userID, the call returns ErrManagerCycle.
func (s *Service) AssignManager(ctx context.Context, agent core.Agent, userID, managerUserID string) (core.ManagerEdge, error) {
	if userID == "" || managerUserID == "" {
		return core.ManagerEdge{}, fmt.Errorf("user_id and manager_user_id are required")
	}
	if userID == managerUserID {
		return core.ManagerEdge{}, ErrSelfManager
	}
	if err := s.requireAdmin(ctx, agent); err != nil {
		return core.ManagerEdge{}, err
	}
	if err := s.requireSameOrgUser(ctx, agent, userID); err != nil {
		return core.ManagerEdge{}, err
	}
	if err := s.requireSameOrgUser(ctx, agent, managerUserID); err != nil {
		return core.ManagerEdge{}, err
	}

	// Cycle detection: walking upward from the proposed manager must
	// never land on userID. Walking from userID itself would be
	// incorrect because the prior edge is about to be revoked.
	chain, err := s.store.WalkManagerChain(ctx, managerUserID, MaxManagerChainDepth)
	if err != nil {
		return core.ManagerEdge{}, fmt.Errorf("walk proposed chain: %w", err)
	}
	for _, edge := range chain {
		if edge.ManagerUserID == userID {
			return core.ManagerEdge{}, ErrManagerCycle
		}
	}

	edge := core.ManagerEdge{
		EdgeID:        id.New("medge"),
		UserID:        userID,
		ManagerUserID: managerUserID,
		EffectiveAt:   time.Now().UTC(),
	}
	saved, err := s.store.SaveManagerEdge(ctx, edge)
	if err != nil {
		return core.ManagerEdge{}, fmt.Errorf("save manager edge: %w", err)
	}
	slog.Info("manager edge created", "edge_id", saved.EdgeID, "user_id", userID, "manager_user_id", managerUserID, "actor", agent.OwnerUserID)
	return saved, nil
}

// RevokeManager clears the active manager edge for userID. Admin-gated.
// The row is not deleted; revoked_at is stamped so history survives.
func (s *Service) RevokeManager(ctx context.Context, agent core.Agent, userID string) error {
	if err := s.requireAdmin(ctx, agent); err != nil {
		return err
	}
	if err := s.requireSameOrgUser(ctx, agent, userID); err != nil {
		return err
	}
	return s.store.RevokeCurrentManagerEdge(ctx, userID, time.Now().UTC())
}

// ManagerChain returns the upward walk from userID (direct manager
// first). Any member of userID's org may read the chain today; reporting
// lines are a first-class org artifact and the spec explicitly expects
// managers to be visible to their reports.
func (s *Service) ManagerChain(ctx context.Context, agent core.Agent, userID string) ([]core.ManagerEdge, error) {
	if err := s.requireSameOrgUser(ctx, agent, userID); err != nil {
		return nil, err
	}
	return s.store.WalkManagerChain(ctx, userID, MaxManagerChainDepth)
}

// --- visibility helpers (adapter-friendly, read-only) ---

// Evaluator is the narrow surface queries.Service calls to evaluate
// team_scope / manager_scope visibility. It is exposed as a plain
// interface so the queries package does not import orggraph.
type Evaluator interface {
	UserSharesTeamWith(ctx context.Context, viewerUserID, ownerUserID string) (bool, error)
	ViewerInOwnerManagerChain(ctx context.Context, viewerUserID, ownerUserID string) (bool, error)
}

// AsEvaluator returns a read-only adapter suitable for passing into the
// queries service. The adapter has no side effects and no authorization
// gating of its own — visibility decisions are policy, and the caller has
// already established the viewer's identity.
func (s *Service) AsEvaluator() Evaluator {
	return readEvaluator{store: s.store}
}

type readEvaluator struct {
	store storage.OrgGraphRepository
}

func (e readEvaluator) UserSharesTeamWith(ctx context.Context, viewerUserID, ownerUserID string) (bool, error) {
	if viewerUserID == "" || ownerUserID == "" {
		return false, nil
	}
	if viewerUserID == ownerUserID {
		return true, nil
	}
	return e.store.UsersShareTeam(ctx, viewerUserID, ownerUserID)
}

func (e readEvaluator) ViewerInOwnerManagerChain(ctx context.Context, viewerUserID, ownerUserID string) (bool, error) {
	if viewerUserID == "" || ownerUserID == "" {
		return false, nil
	}
	if viewerUserID == ownerUserID {
		return false, nil
	}
	chain, err := e.store.WalkManagerChain(ctx, ownerUserID, MaxManagerChainDepth)
	if err != nil {
		return false, err
	}
	for _, edge := range chain {
		if edge.ManagerUserID == viewerUserID {
			return true, nil
		}
	}
	return false, nil
}

// --- internals ---

func (s *Service) requireAdmin(ctx context.Context, agent core.Agent) error {
	if s.users == nil {
		return nil
	}
	user, ok, err := s.users.FindUserByID(ctx, agent.OwnerUserID)
	if err != nil {
		return fmt.Errorf("find acting user: %w", err)
	}
	if !ok {
		return ErrUserNotFound
	}
	if user.Role != core.UserRoleAdmin {
		return ErrNotOrgAdmin
	}
	return nil
}

func (s *Service) requireSameOrgUser(ctx context.Context, agent core.Agent, userID string) error {
	if s.users == nil {
		return nil
	}
	user, ok, err := s.users.FindUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("find subject user: %w", err)
	}
	if !ok {
		return ErrUserNotFound
	}
	if user.OrgID != agent.OrgID {
		return ErrCrossOrg
	}
	return nil
}

func (s *Service) teamInOrg(ctx context.Context, agent core.Agent, teamID string) (core.Team, error) {
	team, ok, err := s.store.FindTeamByID(ctx, teamID)
	if err != nil {
		return core.Team{}, fmt.Errorf("find team: %w", err)
	}
	if !ok {
		return core.Team{}, ErrTeamNotFound
	}
	if team.OrgID != agent.OrgID {
		return core.Team{}, ErrCrossOrg
	}
	return team, nil
}
