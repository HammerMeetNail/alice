package agents_test

import (
	"context"
	"testing"

	"alice/internal/agents"
)

func TestFindUserByEmail_Found(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "findorg", "find@example.com", "Find Agent", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	complete, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	user, found, err := svc.FindUserByEmail(ctx, complete.Org.OrgID, "find@example.com")
	if err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if !found {
		t.Fatal("expected user to be found")
	}
	if user.UserID != complete.User.UserID {
		t.Fatalf("UserID mismatch: got %s want %s", user.UserID, complete.User.UserID)
	}
}

func TestFindUserByEmail_NotFound(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	_, found, err := svc.FindUserByEmail(ctx, "nonexistent-org", "nobody@example.com")
	if err != nil {
		t.Fatalf("FindUserByEmail: %v", err)
	}
	if found {
		t.Fatal("expected user not to be found")
	}
}

func TestFindUserByID_Found(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "findbyidorg", "findbyid@example.com", "Find Agent", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	complete, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	user, found, err := svc.FindUserByID(ctx, complete.User.UserID)
	if err != nil {
		t.Fatalf("FindUserByID: %v", err)
	}
	if !found || user.UserID != complete.User.UserID {
		t.Fatalf("FindUserByID: found=%v id=%s", found, user.UserID)
	}
}

func TestFindUserByID_NotFound(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()
	_, found, err := svc.FindUserByID(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("FindUserByID: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func TestFindAgentByUserID_Found(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "abyuidorg", "abyuid@example.com", "Agent", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	complete, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	agent, found, err := svc.FindAgentByUserID(ctx, complete.User.UserID)
	if err != nil {
		t.Fatalf("FindAgentByUserID: %v", err)
	}
	if !found || agent.AgentID != complete.Agent.AgentID {
		t.Fatalf("FindAgentByUserID: found=%v id=%s", found, agent.AgentID)
	}
}

func TestFindAgentByUserID_NotFound(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()
	_, found, err := svc.FindAgentByUserID(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("FindAgentByUserID: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func TestListPendingAgentApprovals_NotAdmin(t *testing.T) {
	svc, _ := newAgentServiceWithApprovals()
	ctx := context.Background()

	// Register two users in same org: first is admin, second is member.
	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "adminorg", "admin@example.com", "Admin", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)

	pubKeyB64b, _, privKeyB := generateKeyPair(t)
	resB, _ := svc.BeginRegistration(ctx, "adminorg", "member@example.com", "Member", "edge", pubKeyB64b, "")
	sigB := signChallenge(t, resB.Payload, privKeyB)
	completeB, err := svc.CompleteRegistration(ctx, resB.Challenge.ChallengeID, sigB)
	if err != nil {
		t.Fatalf("CompleteRegistration B: %v", err)
	}

	// The second user (member) should get ErrNotOrgAdmin.
	_, err = svc.ListPendingAgentApprovals(ctx, completeB.Org.OrgID, completeB.Agent.AgentID, 50, 0)
	if err != agents.ErrNotOrgAdmin {
		t.Fatalf("expected ErrNotOrgAdmin for member, got %v", err)
	}
}
