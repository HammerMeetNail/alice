package agents_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"alice/internal/agents"
	"alice/internal/config"
	"alice/internal/storage/memory"
)

func newAgentService() *agents.Service {
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     time.Hour,
		DefaultOrgName:   "Test Org",
	}
	return agents.NewService(store, store, store, store, store, cfg, store)
}

func generateKeyPair(t *testing.T) (publicKeyB64, privateKeyB64 string, privateKey ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), priv
}

func signChallenge(t *testing.T, challengePayload string, privateKey ed25519.PrivateKey) string {
	t.Helper()
	sig := ed25519.Sign(privateKey, []byte(challengePayload))
	return base64.StdEncoding.EncodeToString(sig)
}

func TestRegistration_FullFlow(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)

	challenge, payload, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if challenge.ChallengeID == "" {
		t.Fatal("expected non-empty challenge ID")
	}

	sig := signChallenge(t, payload, privKey)
	org, user, agent, accessToken, expiresAt, err := svc.CompleteRegistration(ctx, challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	if org.OrgID == "" || user.UserID == "" || agent.AgentID == "" || accessToken == "" {
		t.Fatal("expected non-empty registration results")
	}
	if expiresAt.Before(time.Now()) {
		t.Fatal("expected token to expire in the future")
	}
}

func TestRegistration_ExpiredChallenge(t *testing.T) {
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL: -time.Millisecond, // expires immediately
		AuthTokenTTL:     time.Hour,
		DefaultOrgName:   "Test Org",
	}
	svc := agents.NewService(store, store, store, store, store, cfg, store)
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)

	challenge, payload, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sig := signChallenge(t, payload, privKey)
	_, _, _, _, _, err = svc.CompleteRegistration(ctx, challenge.ChallengeID, sig)
	if err == nil {
		t.Fatal("expected expired challenge error")
	}
}

func TestRegistration_UsedChallenge(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)

	challenge, payload, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sig := signChallenge(t, payload, privKey)

	// First completion succeeds
	if _, _, _, _, _, err = svc.CompleteRegistration(ctx, challenge.ChallengeID, sig); err != nil {
		t.Fatalf("first CompleteRegistration: %v", err)
	}

	// Second completion should fail with used challenge
	_, _, _, _, _, err = svc.CompleteRegistration(ctx, challenge.ChallengeID, sig)
	if err == nil {
		t.Fatal("expected error on second use of same challenge")
	}
}

func TestRegistration_InvalidSignature(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, _ := generateKeyPair(t)
	_, _, wrongPrivKey := generateKeyPair(t)

	challenge, payload, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sig := signChallenge(t, payload, wrongPrivKey)
	_, _, _, _, _, err = svc.CompleteRegistration(ctx, challenge.ChallengeID, sig)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestAuthentication_ValidToken(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	challenge, payload, _ := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64)
	sig := signChallenge(t, payload, privKey)
	_, _, registeredAgent, accessToken, _, err := svc.CompleteRegistration(ctx, challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	authAgent, authUser, err := svc.AuthenticateAgent(ctx, accessToken)
	if err != nil {
		t.Fatalf("AuthenticateAgent: %v", err)
	}
	if authAgent.AgentID != registeredAgent.AgentID {
		t.Fatalf("expected agent %s, got %s", registeredAgent.AgentID, authAgent.AgentID)
	}
	if authUser.Email != "alice@example.com" {
		t.Fatalf("unexpected user email %s", authUser.Email)
	}
}

func TestAuthentication_InvalidToken(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	_, _, err := svc.AuthenticateAgent(ctx, "not-a-real-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestAuthentication_ExpiredToken(t *testing.T) {
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     -time.Millisecond, // expires immediately
		DefaultOrgName:   "Test Org",
	}
	svc := agents.NewService(store, store, store, store, store, cfg, store)
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	challenge, payload, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	sig := signChallenge(t, payload, privKey)
	_, _, _, accessToken, _, err := svc.CompleteRegistration(ctx, challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	_, _, err = svc.AuthenticateAgent(ctx, accessToken)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestRegistration_MissingFields(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	cases := []struct {
		name      string
		orgSlug   string
		email     string
		agentName string
		clientType string
		publicKey string
	}{
		{"missing org_slug", "", "alice@example.com", "Alice", "edge", "key"},
		{"missing owner_email", "org", "", "Alice", "edge", "key"},
		{"missing agent_name", "org", "alice@example.com", "", "edge", "key"},
		{"missing client_type", "org", "alice@example.com", "Alice", "", "key"},
		{"missing public_key", "org", "alice@example.com", "Alice", "edge", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.BeginRegistration(ctx, tc.orgSlug, tc.email, tc.agentName, tc.clientType, tc.publicKey)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
