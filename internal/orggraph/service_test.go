package orggraph_test

import (
	"context"
	"errors"
	"testing"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/orggraph"
	"alice/internal/storage/memory"
)

type fixture struct {
	ctx       context.Context
	store     *memory.Store
	svc       *orggraph.Service
	orgID     string
	otherOrg  string
	admin     core.Agent
	member    core.Agent
	alice     core.User
	bob       core.User
	carol     core.User
	stranger  core.User
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	store := memory.New()

	org := core.Organization{OrgID: id.New("org"), Slug: "acme", Status: "active"}
	store.UpsertOrganization(ctx, org)
	other := core.Organization{OrgID: id.New("org"), Slug: "other", Status: "active"}
	store.UpsertOrganization(ctx, other)

	adminUser := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "admin@acme.test", Status: "active", Role: core.UserRoleAdmin}
	memberUser := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "member@acme.test", Status: "active", Role: core.UserRoleMember}
	alice := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "alice@acme.test", Status: "active", Role: core.UserRoleMember}
	bob := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "bob@acme.test", Status: "active", Role: core.UserRoleMember}
	carol := core.User{UserID: id.New("user"), OrgID: org.OrgID, Email: "carol@acme.test", Status: "active", Role: core.UserRoleMember}
	stranger := core.User{UserID: id.New("user"), OrgID: other.OrgID, Email: "stranger@other.test", Status: "active", Role: core.UserRoleMember}
	for _, u := range []core.User{adminUser, memberUser, alice, bob, carol, stranger} {
		store.UpsertUser(ctx, u)
	}

	adminAgent := core.Agent{AgentID: id.New("agent"), OrgID: org.OrgID, OwnerUserID: adminUser.UserID, Status: "active"}
	memberAgent := core.Agent{AgentID: id.New("agent"), OrgID: org.OrgID, OwnerUserID: memberUser.UserID, Status: "active"}
	store.UpsertAgent(ctx, adminAgent)
	store.UpsertAgent(ctx, memberAgent)

	svc := orggraph.NewService(store, store)
	return fixture{
		ctx: ctx, store: store, svc: svc,
		orgID: org.OrgID, otherOrg: other.OrgID,
		admin: adminAgent, member: memberAgent,
		alice: alice, bob: bob, carol: carol, stranger: stranger,
	}
}

func TestCreateTeamRequiresAdmin(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateTeam(f.ctx, f.member, "eng", ""); !errors.Is(err, orggraph.ErrNotOrgAdmin) {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
}

func TestCreateTeamRejectsEmptyName(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateTeam(f.ctx, f.admin, "   ", ""); !errors.Is(err, orggraph.ErrTeamNameRequired) {
		t.Fatalf("expected ErrTeamNameRequired, got %v", err)
	}
}

func TestCreateTeamRejectsCrossOrgParent(t *testing.T) {
	f := newFixture(t)
	foreignTeam := core.Team{TeamID: id.New("team"), OrgID: f.otherOrg, Name: "ops"}
	f.store.SaveTeam(f.ctx, foreignTeam)
	if _, err := f.svc.CreateTeam(f.ctx, f.admin, "eng", foreignTeam.TeamID); !errors.Is(err, orggraph.ErrCrossOrg) {
		t.Fatalf("expected ErrCrossOrg, got %v", err)
	}
}

func TestAddAndListTeamMembers(t *testing.T) {
	f := newFixture(t)
	team, err := f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if _, err := f.svc.AddTeamMember(f.ctx, f.admin, team.TeamID, f.alice.UserID, core.TeamMemberRoleLead); err != nil {
		t.Fatalf("add lead: %v", err)
	}
	if _, err := f.svc.AddTeamMember(f.ctx, f.admin, team.TeamID, f.bob.UserID, ""); err != nil {
		t.Fatalf("add member: %v", err)
	}
	members, err := f.svc.ListTeamMembers(f.ctx, f.member, team.TeamID, 0, 0)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	// Non-admin adding is forbidden.
	if _, err := f.svc.AddTeamMember(f.ctx, f.member, team.TeamID, f.carol.UserID, ""); !errors.Is(err, orggraph.ErrNotOrgAdmin) {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
}

func TestAddTeamMemberRejectsCrossOrgUser(t *testing.T) {
	f := newFixture(t)
	team, _ := f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	if _, err := f.svc.AddTeamMember(f.ctx, f.admin, team.TeamID, f.stranger.UserID, ""); !errors.Is(err, orggraph.ErrCrossOrg) {
		t.Fatalf("expected ErrCrossOrg, got %v", err)
	}
}

func TestRemoveTeamMemberIsAdminGated(t *testing.T) {
	f := newFixture(t)
	team, _ := f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	f.svc.AddTeamMember(f.ctx, f.admin, team.TeamID, f.alice.UserID, "")
	if err := f.svc.RemoveTeamMember(f.ctx, f.member, team.TeamID, f.alice.UserID); !errors.Is(err, orggraph.ErrNotOrgAdmin) {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
	if err := f.svc.RemoveTeamMember(f.ctx, f.admin, team.TeamID, f.alice.UserID); err != nil {
		t.Fatalf("admin remove: %v", err)
	}
}

func TestAssignManagerCycleDetection(t *testing.T) {
	f := newFixture(t)
	// Build alice → bob → carol (bob reports to carol; alice reports to bob)
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.bob.UserID, f.carol.UserID); err != nil {
		t.Fatalf("assign bob→carol: %v", err)
	}
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.bob.UserID); err != nil {
		t.Fatalf("assign alice→bob: %v", err)
	}
	// Now try carol → alice (alice is downstream, so this closes a cycle).
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.carol.UserID, f.alice.UserID); !errors.Is(err, orggraph.ErrManagerCycle) {
		t.Fatalf("expected ErrManagerCycle, got %v", err)
	}
}

func TestAssignManagerRejectsSelf(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.alice.UserID); !errors.Is(err, orggraph.ErrSelfManager) {
		t.Fatalf("expected ErrSelfManager, got %v", err)
	}
}

func TestAssignManagerRejectsCrossOrg(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.stranger.UserID, f.alice.UserID); !errors.Is(err, orggraph.ErrCrossOrg) {
		t.Fatalf("expected ErrCrossOrg for subject, got %v", err)
	}
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.stranger.UserID); !errors.Is(err, orggraph.ErrCrossOrg) {
		t.Fatalf("expected ErrCrossOrg for manager, got %v", err)
	}
}

func TestAssignManagerReplacesPriorEdge(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.bob.UserID); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.carol.UserID); err != nil {
		t.Fatalf("replace: %v", err)
	}
	edge, ok, _ := f.store.FindCurrentManagerEdge(f.ctx, f.alice.UserID)
	if !ok || edge.ManagerUserID != f.carol.UserID {
		t.Fatalf("expected carol as current manager, got ok=%v edge=%+v", ok, edge)
	}
}

func TestRevokeManagerClearsEdge(t *testing.T) {
	f := newFixture(t)
	f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.bob.UserID)
	if err := f.svc.RevokeManager(f.ctx, f.admin, f.alice.UserID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, ok, _ := f.store.FindCurrentManagerEdge(f.ctx, f.alice.UserID); ok {
		t.Fatal("expected no active edge after revoke")
	}
}

func TestEvaluatorTeamScope(t *testing.T) {
	f := newFixture(t)
	team, _ := f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	f.svc.AddTeamMember(f.ctx, f.admin, team.TeamID, f.alice.UserID, "")
	f.svc.AddTeamMember(f.ctx, f.admin, team.TeamID, f.bob.UserID, "")

	eval := f.svc.AsEvaluator()
	ok, err := eval.UserSharesTeamWith(f.ctx, f.alice.UserID, f.bob.UserID)
	if err != nil || !ok {
		t.Fatalf("expected shared team, got ok=%v err=%v", ok, err)
	}
	ok, _ = eval.UserSharesTeamWith(f.ctx, f.alice.UserID, f.carol.UserID)
	if ok {
		t.Fatal("expected no shared team with carol")
	}
}

func TestDeleteTeamIsAdminGated(t *testing.T) {
	f := newFixture(t)
	team, _ := f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	if err := f.svc.DeleteTeam(f.ctx, f.member, team.TeamID); !errors.Is(err, orggraph.ErrNotOrgAdmin) {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
	if err := f.svc.DeleteTeam(f.ctx, f.admin, team.TeamID); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	if _, ok, _ := f.store.FindTeamByID(f.ctx, team.TeamID); ok {
		t.Fatal("team should be gone after delete")
	}
}

func TestDeleteTeamRejectsCrossOrg(t *testing.T) {
	f := newFixture(t)
	foreignTeam := core.Team{TeamID: id.New("team"), OrgID: f.otherOrg, Name: "ops"}
	f.store.SaveTeam(f.ctx, foreignTeam)
	if err := f.svc.DeleteTeam(f.ctx, f.admin, foreignTeam.TeamID); !errors.Is(err, orggraph.ErrCrossOrg) {
		t.Fatalf("expected ErrCrossOrg, got %v", err)
	}
}

func TestDeleteTeamMissing(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.DeleteTeam(f.ctx, f.admin, "nonesuch"); !errors.Is(err, orggraph.ErrTeamNotFound) {
		t.Fatalf("expected ErrTeamNotFound, got %v", err)
	}
}

func TestAddTeamMemberRejectsInvalidRole(t *testing.T) {
	f := newFixture(t)
	team, _ := f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	if _, err := f.svc.AddTeamMember(f.ctx, f.admin, team.TeamID, f.alice.UserID, core.TeamMemberRole("manager")); !errors.Is(err, orggraph.ErrInvalidMemberRole) {
		t.Fatalf("expected ErrInvalidMemberRole, got %v", err)
	}
}

func TestListTeamsReturnsOrgTeams(t *testing.T) {
	f := newFixture(t)
	f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	f.svc.CreateTeam(f.ctx, f.admin, "ops", "")
	teams, err := f.svc.ListTeams(f.ctx, f.member, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(teams))
	}
}

func TestRemoveTeamMemberMissing(t *testing.T) {
	f := newFixture(t)
	team, _ := f.svc.CreateTeam(f.ctx, f.admin, "eng", "")
	if err := f.svc.RemoveTeamMember(f.ctx, f.admin, team.TeamID, f.alice.UserID); !errors.Is(err, orggraph.ErrTeamMemberNotFound) {
		t.Fatalf("expected ErrTeamMemberNotFound, got %v", err)
	}
}

func TestManagerChainReadable(t *testing.T) {
	f := newFixture(t)
	f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.bob.UserID)
	f.svc.AssignManager(f.ctx, f.admin, f.bob.UserID, f.carol.UserID)
	chain, err := f.svc.ManagerChain(f.ctx, f.member, f.alice.UserID)
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if len(chain) != 2 || chain[0].ManagerUserID != f.bob.UserID || chain[1].ManagerUserID != f.carol.UserID {
		t.Fatalf("unexpected chain %+v", chain)
	}
}

func TestRevokeManagerIsAdminGated(t *testing.T) {
	f := newFixture(t)
	f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.bob.UserID)
	if err := f.svc.RevokeManager(f.ctx, f.member, f.alice.UserID); !errors.Is(err, orggraph.ErrNotOrgAdmin) {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
}

func TestAssignManagerRejectsEmptyIDs(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.AssignManager(f.ctx, f.admin, "", f.bob.UserID); err == nil {
		t.Fatal("expected error for empty user")
	}
	if _, err := f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, ""); err == nil {
		t.Fatal("expected error for empty manager")
	}
}

func TestEvaluatorSelfAndEmptyIDs(t *testing.T) {
	f := newFixture(t)
	eval := f.svc.AsEvaluator()

	// Empty ids short-circuit to false without hitting the store.
	ok, _ := eval.UserSharesTeamWith(f.ctx, "", f.alice.UserID)
	if ok {
		t.Fatal("empty viewer must not match a team")
	}
	ok, _ = eval.ViewerInOwnerManagerChain(f.ctx, "", f.alice.UserID)
	if ok {
		t.Fatal("empty viewer must not match manager chain")
	}

	// Self always shares a "team" with itself (trivial case) for team_scope.
	ok, _ = eval.UserSharesTeamWith(f.ctx, f.alice.UserID, f.alice.UserID)
	if !ok {
		t.Fatal("self must trivially share team")
	}
}

func TestEvaluatorManagerScopeUpwardOnly(t *testing.T) {
	f := newFixture(t)
	// alice reports to bob; bob reports to carol.
	f.svc.AssignManager(f.ctx, f.admin, f.alice.UserID, f.bob.UserID)
	f.svc.AssignManager(f.ctx, f.admin, f.bob.UserID, f.carol.UserID)

	eval := f.svc.AsEvaluator()
	// Bob is in alice's upward chain → allow.
	ok, err := eval.ViewerInOwnerManagerChain(f.ctx, f.bob.UserID, f.alice.UserID)
	if err != nil || !ok {
		t.Fatalf("expected bob in alice's chain, got ok=%v err=%v", ok, err)
	}
	// Carol is in alice's upward chain too (transitive).
	ok, _ = eval.ViewerInOwnerManagerChain(f.ctx, f.carol.UserID, f.alice.UserID)
	if !ok {
		t.Fatal("expected carol in alice's chain")
	}
	// Alice is NOT in bob's upward chain (downward doesn't count).
	ok, _ = eval.ViewerInOwnerManagerChain(f.ctx, f.alice.UserID, f.bob.UserID)
	if ok {
		t.Fatal("downward relationship must not grant manager-scope visibility")
	}
	// Self never counts (we're checking viewer-is-ancestor).
	ok, _ = eval.ViewerInOwnerManagerChain(f.ctx, f.alice.UserID, f.alice.UserID)
	if ok {
		t.Fatal("self must not match manager scope")
	}
}
