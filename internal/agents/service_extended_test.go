package agents_test

import (
	"context"
	"testing"
	"time"

	"alice/internal/agents"
	"alice/internal/core"
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

func TestUpdateVerificationMode_Admin(t *testing.T) {
	svc, _ := newAgentServiceWithApprovals()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "verifmodeorg", "admin@example.com", "Admin", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	complete, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	org, err := svc.UpdateVerificationMode(ctx, complete.Agent, "email_otp")
	if err != nil {
		t.Fatalf("UpdateVerificationMode: %v", err)
	}
	if org.VerificationMode != "email_otp" {
		t.Fatalf("expected email_otp, got %q", org.VerificationMode)
	}
}

func TestUpdateVerificationMode_NonAdmin(t *testing.T) {
	svc, _ := newAgentServiceWithApprovals()
	ctx := context.Background()

	// Register admin.
	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "verifmode2", "admin@example.com", "Admin", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)

	// Register member.
	pubKeyB64b, _, privKeyB := generateKeyPair(t)
	resB, _ := svc.BeginRegistration(ctx, "verifmode2", "member@example.com", "Member", "edge", pubKeyB64b, "")
	sigB := signChallenge(t, resB.Payload, privKeyB)
	completeB, err := svc.CompleteRegistration(ctx, resB.Challenge.ChallengeID, sigB)
	if err != nil {
		t.Fatalf("CompleteRegistration B: %v", err)
	}

	_, err = svc.UpdateVerificationMode(ctx, completeB.Agent, "email_otp")
	if err != agents.ErrNotOrgAdmin {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
}

func TestUpdateGatekeeperTuning_Admin(t *testing.T) {
	svc, _ := newAgentServiceWithApprovals()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "tuneorg", "admin@example.com", "Admin", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	complete, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	threshold := 0.85
	window := 72 * time.Hour
	org, err := svc.UpdateGatekeeperTuning(ctx, complete.Agent, &threshold, &window)
	if err != nil {
		t.Fatalf("UpdateGatekeeperTuning: %v", err)
	}
	if org.GatekeeperConfidenceThreshold == nil || *org.GatekeeperConfidenceThreshold != threshold {
		t.Fatalf("threshold not persisted, got %v", org.GatekeeperConfidenceThreshold)
	}
	if org.GatekeeperLookbackWindow == nil || *org.GatekeeperLookbackWindow != window {
		t.Fatalf("window not persisted, got %v", org.GatekeeperLookbackWindow)
	}

	// Clearing reverts both overrides to nil.
	org, err = svc.UpdateGatekeeperTuning(ctx, complete.Agent, nil, nil)
	if err != nil {
		t.Fatalf("clear UpdateGatekeeperTuning: %v", err)
	}
	if org.GatekeeperConfidenceThreshold != nil || org.GatekeeperLookbackWindow != nil {
		t.Fatalf("expected both overrides cleared, got %+v", org)
	}
}

func TestUpdateGatekeeperTuning_NonAdmin(t *testing.T) {
	svc, _ := newAgentServiceWithApprovals()
	ctx := context.Background()

	pubKeyA, _, privA := generateKeyPair(t)
	resA, _ := svc.BeginRegistration(ctx, "tuneorg2", "admin@example.com", "Admin", "edge", pubKeyA, "")
	sigA := signChallenge(t, resA.Payload, privA)
	if _, err := svc.CompleteRegistration(ctx, resA.Challenge.ChallengeID, sigA); err != nil {
		t.Fatalf("CompleteRegistration A: %v", err)
	}

	pubKeyB, _, privB := generateKeyPair(t)
	resB, _ := svc.BeginRegistration(ctx, "tuneorg2", "member@example.com", "Member", "edge", pubKeyB, "")
	sigB := signChallenge(t, resB.Payload, privB)
	completeB, err := svc.CompleteRegistration(ctx, resB.Challenge.ChallengeID, sigB)
	if err != nil {
		t.Fatalf("CompleteRegistration B: %v", err)
	}

	threshold := 0.7
	if _, err := svc.UpdateGatekeeperTuning(ctx, completeB.Agent, &threshold, nil); err != agents.ErrNotOrgAdmin {
		t.Fatalf("expected ErrNotOrgAdmin for member, got %v", err)
	}
}

func TestUpdateGatekeeperTuning_Validation(t *testing.T) {
	svc, _ := newAgentServiceWithApprovals()
	ctx := context.Background()

	pubKey, _, priv := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "tuneorg3", "admin@example.com", "Admin", "edge", pubKey, "")
	sig := signChallenge(t, res.Payload, priv)
	complete, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	badThresholds := []float64{-0.1, 0, 1.1, 2}
	for _, v := range badThresholds {
		thr := v
		_, err := svc.UpdateGatekeeperTuning(ctx, complete.Agent, &thr, nil)
		if _, ok := err.(core.ValidationError); !ok {
			t.Fatalf("threshold=%v: expected ValidationError, got %v", v, err)
		}
	}

	badWindows := []time.Duration{0, -1 * time.Hour, 366 * 24 * time.Hour}
	for _, v := range badWindows {
		d := v
		_, err := svc.UpdateGatekeeperTuning(ctx, complete.Agent, nil, &d)
		if _, ok := err.(core.ValidationError); !ok {
			t.Fatalf("window=%v: expected ValidationError, got %v", v, err)
		}
	}
}

func TestDeleteSelf_Success(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	res, _ := svc.BeginRegistration(ctx, "delselforg", "delself@example.com", "Del Agent", "edge", pubKeyB64, "")
	sig := signChallenge(t, res.Payload, privKey)
	complete, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	if err := svc.DeleteSelf(ctx, complete.Agent); err != nil {
		t.Fatalf("DeleteSelf: %v", err)
	}

	// After deletion the agent's token should be revoked; AuthenticateAgent must fail.
	_, _, authErr := svc.AuthenticateAgent(ctx, complete.AccessToken)
	if authErr == nil {
		t.Fatal("expected authentication error after DeleteSelf")
	}
}

func TestRequireAgent_UnknownAgent(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	_, _, err := svc.RequireAgent(ctx, "nonexistent-agent-id")
	if err != agents.ErrUnknownAgent {
		t.Fatalf("expected ErrUnknownAgent, got %v", err)
	}
}

func TestResendVerificationEmail_NotFound(t *testing.T) {
	svc, _, _ := newAgentServiceWithOTP(10*time.Minute, 5)
	ctx := context.Background()

	// No pending verification exists for this agent ID.
	err := svc.ResendVerificationEmail(ctx, "nonexistent-agent-id")
	if err != agents.ErrVerificationNotFound {
		t.Fatalf("expected ErrVerificationNotFound, got %v", err)
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
