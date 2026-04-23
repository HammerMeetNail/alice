package memory_test

import (
	"context"
	"testing"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
	"alice/internal/storage/memory"
)

// TestMemoryOrgGraph_RoundTrip exercises the memory store's
// OrgGraphRepository directly. The orggraph service tests already cover
// the high-level behaviour; this file exists so the in-package coverage
// counters attribute the hits to this file as well.
func TestMemoryOrgGraph_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	teamA := core.Team{TeamID: id.New("team"), OrgID: "org1", Name: "eng", CreatedAt: time.Now().UTC()}
	teamB := core.Team{TeamID: id.New("team"), OrgID: "org1", Name: "ops", CreatedAt: time.Now().UTC().Add(-time.Hour)}
	store.SaveTeam(ctx, teamA)
	store.SaveTeam(ctx, teamB)

	teams, err := store.ListTeamsForOrg(ctx, "org1", 10, 0)
	if err != nil || len(teams) != 2 {
		t.Fatalf("list: got %d err=%v", len(teams), err)
	}
	if teams[0].TeamID != teamA.TeamID {
		t.Fatalf("expected newest team first, got %s", teams[0].TeamID)
	}

	// Pagination: offset 1 returns only the second team.
	pageTwo, _ := store.ListTeamsForOrg(ctx, "org1", 10, 1)
	if len(pageTwo) != 1 {
		t.Fatalf("expected 1 team on page two, got %d", len(pageTwo))
	}

	// Offset past the end returns an empty slice, not an error.
	past, _ := store.ListTeamsForOrg(ctx, "org1", 10, 5)
	if len(past) != 0 {
		t.Fatalf("expected empty slice past end, got %d", len(past))
	}

	// Team membership round-trip.
	alice := "user_alice"
	bob := "user_bob"
	store.SaveTeamMember(ctx, core.TeamMember{TeamID: teamA.TeamID, UserID: alice, Role: core.TeamMemberRoleMember, JoinedAt: time.Now().UTC()})
	store.SaveTeamMember(ctx, core.TeamMember{TeamID: teamA.TeamID, UserID: bob, Role: core.TeamMemberRoleLead, JoinedAt: time.Now().UTC().Add(time.Second)})

	members, _ := store.ListTeamMembers(ctx, teamA.TeamID, 10, 0)
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}

	shared, err := store.UsersShareTeam(ctx, alice, bob)
	if err != nil || !shared {
		t.Fatalf("expected shared team, got shared=%v err=%v", shared, err)
	}

	teamsForAlice, _ := store.ListTeamsForUser(ctx, alice)
	if len(teamsForAlice) != 1 || teamsForAlice[0].TeamID != teamA.TeamID {
		t.Fatalf("expected 1 team for alice, got %v", teamsForAlice)
	}

	// Remove alice and confirm the roster and shared-team check both react.
	if err := store.DeleteTeamMember(ctx, teamA.TeamID, alice); err != nil {
		t.Fatalf("DeleteTeamMember: %v", err)
	}
	shared, _ = store.UsersShareTeam(ctx, alice, bob)
	if shared {
		t.Fatal("expected no shared team after removal")
	}

	// Removing a non-member surfaces a sentinel error.
	if err := store.DeleteTeamMember(ctx, teamA.TeamID, alice); err != storage.ErrTeamMemberNotFound {
		t.Fatalf("expected ErrTeamMemberNotFound on double-delete, got %v", err)
	}

	// Deleting the whole team cascades membership and removes teamA from
	// ListTeamsForOrg.
	if err := store.DeleteTeam(ctx, teamA.TeamID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}
	if _, ok, _ := store.FindTeamByID(ctx, teamA.TeamID); ok {
		t.Fatal("team should not be found after delete")
	}
	if err := store.DeleteTeam(ctx, teamA.TeamID); err != storage.ErrTeamNotFound {
		t.Fatalf("expected ErrTeamNotFound on double-delete, got %v", err)
	}

	// Listing members of a deleted team returns ErrTeamNotFound.
	if _, err := store.ListTeamMembers(ctx, teamA.TeamID, 10, 0); err != storage.ErrTeamNotFound {
		t.Fatalf("expected ErrTeamNotFound from ListTeamMembers, got %v", err)
	}
}

func TestMemoryOrgGraph_ManagerEdges(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	// Assign; reassign; revoke; confirm each transition leaves the
	// storage in the expected state.
	first := core.ManagerEdge{EdgeID: id.New("edge"), UserID: "u1", ManagerUserID: "m1", EffectiveAt: time.Now().UTC()}
	if _, err := store.SaveManagerEdge(ctx, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	got, ok, _ := store.FindCurrentManagerEdge(ctx, "u1")
	if !ok || got.ManagerUserID != "m1" {
		t.Fatalf("expected m1, got %+v", got)
	}

	second := core.ManagerEdge{EdgeID: id.New("edge"), UserID: "u1", ManagerUserID: "m2", EffectiveAt: time.Now().UTC().Add(time.Second)}
	if _, err := store.SaveManagerEdge(ctx, second); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, _, _ = store.FindCurrentManagerEdge(ctx, "u1")
	if got.ManagerUserID != "m2" {
		t.Fatalf("expected m2 after replace, got %s", got.ManagerUserID)
	}

	// WalkManagerChain follows edges upward.
	// u1 → m2; m2 → m3
	if _, err := store.SaveManagerEdge(ctx, core.ManagerEdge{EdgeID: id.New("edge"), UserID: "m2", ManagerUserID: "m3", EffectiveAt: time.Now().UTC().Add(2 * time.Second)}); err != nil {
		t.Fatalf("save m2→m3: %v", err)
	}
	chain, _ := store.WalkManagerChain(ctx, "u1", 10)
	if len(chain) != 2 {
		t.Fatalf("expected 2-hop chain, got %d", len(chain))
	}

	// Revoke u1 → m2. Chain should go back to empty from u1.
	if err := store.RevokeCurrentManagerEdge(ctx, "u1", time.Now().UTC().Add(3*time.Second)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	chain, _ = store.WalkManagerChain(ctx, "u1", 10)
	if len(chain) != 0 {
		t.Fatalf("expected empty chain after revoke, got %d", len(chain))
	}

	// Revoking when there's no active edge is a no-op.
	if err := store.RevokeCurrentManagerEdge(ctx, "u1", time.Now().UTC()); err != nil {
		t.Fatalf("double revoke: %v", err)
	}

	// maxDepth=0 falls back to default and still walks.
	chain, _ = store.WalkManagerChain(ctx, "m2", 0)
	if len(chain) != 1 || chain[0].ManagerUserID != "m3" {
		t.Fatalf("expected 1-hop from m2, got %+v", chain)
	}
}
