package agents_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
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

// capturingSender records sent emails and allows tests to extract the last OTP code.
type capturingSender struct {
	mu   sync.Mutex
	sent []capturedEmail
}

type capturedEmail struct {
	To      string
	Subject string
	Body    string
}

func (s *capturingSender) Send(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, capturedEmail{To: to, Subject: subject, Body: body})
	return nil
}

// LastCode extracts the 6-digit code from the last sent email body.
// It looks for a line starting with "Your Alice verification code is: ".
func (s *capturingSender) LastCode() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return "", false
	}
	body := s.sent[len(s.sent)-1].Body
	const prefix = "Your Alice verification code is: "
	for _, line := range splitLines(body) {
		if len(line) > len(prefix) && line[:len(prefix)] == prefix {
			code := line[len(prefix):]
			if len(code) == 6 {
				return code, true
			}
		}
	}
	return "", false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func newAgentServiceWithOTP(ttl time.Duration, maxAttempts int) (*agents.Service, *capturingSender, *memory.Store) {
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL:    5 * time.Minute,
		AuthTokenTTL:        time.Hour,
		DefaultOrgName:      "Test Org",
		EmailOTPTTL:         ttl,
		EmailOTPMaxAttempts: maxAttempts,
	}
	sender := &capturingSender{}
	svc := agents.NewService(store, store, store, store, store, cfg, store).
		WithEmailSender(sender, store)
	return svc, sender, store
}

// registerWithOTP performs begin+complete registration and returns the agent and access token.
func registerWithOTP(t *testing.T, svc *agents.Service) (agentID, accessToken string) {
	t.Helper()
	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	result, err := svc.BeginRegistration(ctx, "testorg-otp", "otp@example.com", "OTP Agent", "edge", pubB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(result.Payload)))
	completeRes, err := svc.CompleteRegistration(ctx, result.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	return completeRes.Agent.AgentID, completeRes.AccessToken
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

	regResult, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if regResult.Challenge.ChallengeID == "" {
		t.Fatal("expected non-empty challenge ID")
	}

	sig := signChallenge(t, regResult.Payload, privKey)
	completeRes, err := svc.CompleteRegistration(ctx, regResult.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	if completeRes.Org.OrgID == "" || completeRes.User.UserID == "" || completeRes.Agent.AgentID == "" || completeRes.AccessToken == "" {
		t.Fatal("expected non-empty registration results")
	}
	if completeRes.TokenExpiresAt.Before(time.Now()) {
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

	regResult, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sig := signChallenge(t, regResult.Payload, privKey)
	_, err = svc.CompleteRegistration(ctx, regResult.Challenge.ChallengeID, sig)
	if err == nil {
		t.Fatal("expected expired challenge error")
	}
}

func TestRegistration_UsedChallenge(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)

	regResult, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sig := signChallenge(t, regResult.Payload, privKey)

	// First completion succeeds
	if _, err = svc.CompleteRegistration(ctx, regResult.Challenge.ChallengeID, sig); err != nil {
		t.Fatalf("first CompleteRegistration: %v", err)
	}

	// Second completion should fail with used challenge
	_, err = svc.CompleteRegistration(ctx, regResult.Challenge.ChallengeID, sig)
	if err == nil {
		t.Fatal("expected error on second use of same challenge")
	}
}

func TestRegistration_InvalidSignature(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, _ := generateKeyPair(t)
	_, _, wrongPrivKey := generateKeyPair(t)

	regResult, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sig := signChallenge(t, regResult.Payload, wrongPrivKey)
	_, err = svc.CompleteRegistration(ctx, regResult.Challenge.ChallengeID, sig)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestAuthentication_ValidToken(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()

	pubKeyB64, _, privKey := generateKeyPair(t)
	regResult, _ := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64, "")
	sig := signChallenge(t, regResult.Payload, privKey)
	completeRes, err := svc.CompleteRegistration(ctx, regResult.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	authAgent, authUser, err := svc.AuthenticateAgent(ctx, completeRes.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateAgent: %v", err)
	}
	if authAgent.AgentID != completeRes.Agent.AgentID {
		t.Fatalf("expected agent %s, got %s", completeRes.Agent.AgentID, authAgent.AgentID)
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
	regResult, err := svc.BeginRegistration(ctx, "testorg", "alice@example.com", "Alice Agent", "edge", pubKeyB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	sig := signChallenge(t, regResult.Payload, privKey)
	completeRes, err := svc.CompleteRegistration(ctx, regResult.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	_, _, err = svc.AuthenticateAgent(ctx, completeRes.AccessToken)
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
			_, err := svc.BeginRegistration(ctx, tc.orgSlug, tc.email, tc.agentName, tc.clientType, tc.publicKey, "")
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestEmailOTP_RegistrationSetsPendingStatus(t *testing.T) {
	svc, sender, _ := newAgentServiceWithOTP(10*time.Minute, 5)

	agentID, _ := registerWithOTP(t, svc)

	// A verification email should have been sent.
	code, ok := sender.LastCode()
	if !ok {
		t.Fatal("expected OTP email to be sent")
	}
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got %q", code)
	}

	// Agent ID should be non-empty.
	if agentID == "" {
		t.Fatal("expected non-empty agent ID")
	}
}

func TestEmailOTP_CorrectCodeActivatesAgent(t *testing.T) {
	svc, sender, _ := newAgentServiceWithOTP(10*time.Minute, 5)
	ctx := context.Background()

	agentID, _ := registerWithOTP(t, svc)

	code, ok := sender.LastCode()
	if !ok {
		t.Fatal("expected OTP email to be sent")
	}

	if err := svc.VerifyEmail(ctx, agentID, code); err != nil {
		t.Fatalf("VerifyEmail with correct code: %v", err)
	}
}

func TestEmailOTP_WrongCodeIncrementsAttempts(t *testing.T) {
	svc, _, _ := newAgentServiceWithOTP(10*time.Minute, 5)
	ctx := context.Background()

	agentID, _ := registerWithOTP(t, svc)

	err := svc.VerifyEmail(ctx, agentID, "000000")
	if !errors.Is(err, agents.ErrInvalidVerificationCode) {
		t.Fatalf("expected ErrInvalidVerificationCode, got %v", err)
	}
}

func TestEmailOTP_MaxAttemptsLockout(t *testing.T) {
	svc, _, _ := newAgentServiceWithOTP(10*time.Minute, 2)
	ctx := context.Background()

	agentID, _ := registerWithOTP(t, svc)

	// Use up all allowed attempts.
	_ = svc.VerifyEmail(ctx, agentID, "000000")
	_ = svc.VerifyEmail(ctx, agentID, "000000")

	// Next attempt should be locked out.
	err := svc.VerifyEmail(ctx, agentID, "000000")
	if !errors.Is(err, agents.ErrVerificationMaxAttempts) {
		t.Fatalf("expected ErrVerificationMaxAttempts, got %v", err)
	}
}

func TestEmailOTP_ExpiredCode(t *testing.T) {
	svc, _, _ := newAgentServiceWithOTP(-time.Millisecond, 5) // expires immediately
	ctx := context.Background()

	agentID, _ := registerWithOTP(t, svc)

	err := svc.VerifyEmail(ctx, agentID, "123456")
	if !errors.Is(err, agents.ErrVerificationExpired) {
		t.Fatalf("expected ErrVerificationExpired, got %v", err)
	}
}

func TestEmailOTP_ResendRateLimit(t *testing.T) {
	svc, _, _ := newAgentServiceWithOTP(10*time.Minute, 5)
	ctx := context.Background()

	agentID, _ := registerWithOTP(t, svc)

	// Immediate resend should be rejected.
	err := svc.ResendVerificationEmail(ctx, agentID)
	if !errors.Is(err, agents.ErrResendTooSoon) {
		t.Fatalf("expected ErrResendTooSoon, got %v", err)
	}
}

func TestEmailOTP_NoOTPWhenNoSender(t *testing.T) {
	// Without an email sender, registration should proceed as active immediately.
	svc := newAgentService()
	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	res, err := svc.BeginRegistration(ctx, "testorg-nomail", "nomail@example.com", "NoMail Agent", "edge", pubB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(res.Payload)))
	completeRes, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	if completeRes.Agent.Status != "active" {
		t.Fatalf("expected agent status active when no sender, got %q", completeRes.Agent.Status)
	}
}

// --- Invite Token Tests ---

func registerAgentDirect(t *testing.T, svc *agents.Service, orgSlug, email, inviteToken string) (agentID, accessToken string) {
	t.Helper()
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	res, err := svc.BeginRegistration(ctx, orgSlug, email, "Test Agent", "edge", pubB64, inviteToken)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(res.Payload)))
	completeRes, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	return completeRes.Agent.AgentID, completeRes.AccessToken
}

func TestInviteToken_FirstRegistrationGeneratesToken(t *testing.T) {
	svc := newAgentService()
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	res, err := svc.BeginRegistration(ctx, "inviteorg", "first@example.com", "First Agent", "edge", pubB64, "")
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(res.Payload)))
	completeRes, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	if completeRes.FirstInviteToken == "" {
		t.Fatal("expected FirstInviteToken to be set on first registration")
	}
}

func TestInviteToken_SecondRegistrationWithoutTokenFails(t *testing.T) {
	store := memory.New()
	ctx := context.Background()
	cfg := config.Config{AuthChallengeTTL: 5 * time.Minute, AuthTokenTTL: time.Hour, DefaultOrgName: "Test Org"}
	svc := agents.NewService(store, store, store, store, store, cfg, store).WithApprovalRepository(store)

	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, err := svc.BeginRegistration(ctx, "inviteorg3", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub2), "")
	if err != nil {
		t.Fatalf("first reg BeginRegistration: %v", err)
	}
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	completeRes2, err := svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	if completeRes2.FirstInviteToken == "" {
		t.Fatal("expected first invite token")
	}
	// Enable invite_token mode.
	if err := store.UpdateOrgVerificationMode(ctx, completeRes2.Agent.OrgID, "invite_token"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	// Second registration without invite token should fail.
	pub3, _, _ := ed25519.GenerateKey(rand.Reader)
	_, err = svc.BeginRegistration(ctx, "inviteorg3", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub3), "")
	if !errors.Is(err, agents.ErrInviteTokenRequired) {
		t.Fatalf("expected ErrInviteTokenRequired, got %v", err)
	}
}

func TestInviteToken_CorrectTokenAllowsRegistration(t *testing.T) {
	store := memory.New()
	ctx := context.Background()
	cfg := config.Config{AuthChallengeTTL: 5 * time.Minute, AuthTokenTTL: time.Hour, DefaultOrgName: "Test Org"}
	svc := agents.NewService(store, store, store, store, store, cfg, store).WithApprovalRepository(store)

	// First registration.
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, err := svc.BeginRegistration(ctx, "inviteorg4", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	if err != nil {
		t.Fatalf("first BeginRegistration: %v", err)
	}
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, err := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)
	if err != nil {
		t.Fatalf("first CompleteRegistration: %v", err)
	}
	firstToken := completeRes1.FirstInviteToken

	// Enable invite_token mode.
	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "invite_token"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	// Second registration with correct token should succeed.
	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, err := svc.BeginRegistration(ctx, "inviteorg4", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), firstToken)
	if err != nil {
		t.Fatalf("second BeginRegistration: %v", err)
	}
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	if _, err = svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2); err != nil {
		t.Fatalf("second CompleteRegistration: %v", err)
	}
}

func TestInviteToken_WrongTokenFails(t *testing.T) {
	store := memory.New()
	ctx := context.Background()
	cfg := config.Config{AuthChallengeTTL: 5 * time.Minute, AuthTokenTTL: time.Hour, DefaultOrgName: "Test Org"}
	svc := agents.NewService(store, store, store, store, store, cfg, store).WithApprovalRepository(store)

	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, _ := svc.BeginRegistration(ctx, "inviteorg5", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, _ := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)

	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "invite_token"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	_, err := svc.BeginRegistration(ctx, "inviteorg5", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), "wrongtoken")
	if !errors.Is(err, agents.ErrInvalidInviteToken) {
		t.Fatalf("expected ErrInvalidInviteToken, got %v", err)
	}
}

func TestInviteToken_RotateGeneratesNewToken(t *testing.T) {
	store := memory.New()
	ctx := context.Background()
	cfg := config.Config{AuthChallengeTTL: 5 * time.Minute, AuthTokenTTL: time.Hour, DefaultOrgName: "Test Org"}
	svc := agents.NewService(store, store, store, store, store, cfg, store).WithApprovalRepository(store)

	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, _ := svc.BeginRegistration(ctx, "rotateorg", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, _ := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)
	firstToken := completeRes1.FirstInviteToken

	// Rotate token.
	newToken, err := svc.RotateInviteToken(ctx, completeRes1.Agent.OrgID, completeRes1.Agent.AgentID)
	if err != nil {
		t.Fatalf("RotateInviteToken: %v", err)
	}
	if newToken == "" || newToken == firstToken {
		t.Fatal("expected new non-empty token different from first token")
	}

	// Enable invite_token mode.
	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "invite_token"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	// Old token should fail.
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	_, err = svc.BeginRegistration(ctx, "rotateorg", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), firstToken)
	if !errors.Is(err, agents.ErrInvalidInviteToken) {
		t.Fatalf("expected ErrInvalidInviteToken with old token, got %v", err)
	}

	// New token should work.
	pub3, priv3, _ := ed25519.GenerateKey(rand.Reader)
	res3, err := svc.BeginRegistration(ctx, "rotateorg", "third@example.com", "Third", "edge", base64.StdEncoding.EncodeToString(pub3), newToken)
	if err != nil {
		t.Fatalf("BeginRegistration with new token: %v", err)
	}
	sig3 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv3, []byte(res3.Payload)))
	if _, err = svc.CompleteRegistration(ctx, res3.Challenge.ChallengeID, sig3); err != nil {
		t.Fatalf("CompleteRegistration with new token: %v", err)
	}
}

// --- Admin Approval Tests ---

func newAgentServiceWithApprovals() (*agents.Service, *memory.Store) {
	store := memory.New()
	cfg := config.Config{AuthChallengeTTL: 5 * time.Minute, AuthTokenTTL: time.Hour, DefaultOrgName: "Test Org"}
	svc := agents.NewService(store, store, store, store, store, cfg, store).WithApprovalRepository(store)
	return svc, store
}

func TestAdminApproval_FirstRegistrantGetsAdminRole(t *testing.T) {
	svc, store := newAgentServiceWithApprovals()
	ctx := context.Background()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	res, _ := svc.BeginRegistration(ctx, "approvalorg", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub), "")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(res.Payload)))
	completeRes, err := svc.CompleteRegistration(ctx, res.Challenge.ChallengeID, sig)
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}
	if completeRes.Agent.Status != "active" {
		t.Fatalf("expected active status for first agent, got %q", completeRes.Agent.Status)
	}
	if completeRes.User.Role != "admin" {
		t.Fatalf("expected admin role for first user, got %q", completeRes.User.Role)
	}

	// Enable admin_approval mode.
	if err := store.UpdateOrgVerificationMode(ctx, completeRes.Agent.OrgID, "admin_approval"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}
}

func TestAdminApproval_SecondRegistrantGetsPendingApproval(t *testing.T) {
	svc, store := newAgentServiceWithApprovals()
	ctx := context.Background()

	// First registration (becomes admin).
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, _ := svc.BeginRegistration(ctx, "approvalorg2", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, _ := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)

	// Enable admin_approval mode.
	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "admin_approval"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	// Second registration.
	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, _ := svc.BeginRegistration(ctx, "approvalorg2", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), "")
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	completeRes2, err := svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2)
	if err != nil {
		t.Fatalf("second CompleteRegistration: %v", err)
	}
	if completeRes2.Agent.Status != "pending_admin_approval" {
		t.Fatalf("expected pending_admin_approval for second agent, got %q", completeRes2.Agent.Status)
	}
}

func TestAdminApproval_AdminCanApprove(t *testing.T) {
	svc, store := newAgentServiceWithApprovals()
	ctx := context.Background()

	// First registration (becomes admin).
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, _ := svc.BeginRegistration(ctx, "approvalorg3", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, _ := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)

	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "admin_approval"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	// Second registration (member, pending approval).
	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, _ := svc.BeginRegistration(ctx, "approvalorg3", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), "")
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	completeRes2, _ := svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2)

	// Admin approves.
	if err := svc.ReviewAgentApproval(ctx, completeRes1.Agent.OrgID, completeRes2.Agent.AgentID, completeRes1.Agent.AgentID, "approved", "looks good"); err != nil {
		t.Fatalf("ReviewAgentApproval: %v", err)
	}

	// Agent should now be active.
	ag2Updated, ok, err := store.FindAgentByID(ctx, completeRes2.Agent.AgentID)
	if err != nil || !ok {
		t.Fatalf("FindAgentByID after approval: %v %v", ok, err)
	}
	if ag2Updated.Status != "active" {
		t.Fatalf("expected active status after approval, got %q", ag2Updated.Status)
	}
}

func TestAdminApproval_AdminCanReject(t *testing.T) {
	svc, store := newAgentServiceWithApprovals()
	ctx := context.Background()

	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, _ := svc.BeginRegistration(ctx, "approvalorg4", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, _ := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)

	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "admin_approval"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, _ := svc.BeginRegistration(ctx, "approvalorg4", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), "")
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	completeRes2, _ := svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2)

	if err := svc.ReviewAgentApproval(ctx, completeRes1.Agent.OrgID, completeRes2.Agent.AgentID, completeRes1.Agent.AgentID, "rejected", "not trusted"); err != nil {
		t.Fatalf("ReviewAgentApproval reject: %v", err)
	}

	ag2Updated, ok, err := store.FindAgentByID(ctx, completeRes2.Agent.AgentID)
	if err != nil || !ok {
		t.Fatalf("FindAgentByID after rejection: %v %v", ok, err)
	}
	if ag2Updated.Status != "rejected" {
		t.Fatalf("expected rejected status, got %q", ag2Updated.Status)
	}
}

func TestAdminApproval_NonAdminGetsError(t *testing.T) {
	svc, store := newAgentServiceWithApprovals()
	ctx := context.Background()

	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, _ := svc.BeginRegistration(ctx, "approvalorg5", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, _ := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)

	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "admin_approval"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, _ := svc.BeginRegistration(ctx, "approvalorg5", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), "")
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	completeRes2, _ := svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2)

	pub3, priv3, _ := ed25519.GenerateKey(rand.Reader)
	res3, _ := svc.BeginRegistration(ctx, "approvalorg5", "third@example.com", "Third", "edge", base64.StdEncoding.EncodeToString(pub3), "")
	sig3 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv3, []byte(res3.Payload)))
	completeRes3, _ := svc.CompleteRegistration(ctx, res3.Challenge.ChallengeID, sig3)

	// ag2 (member) tries to review ag3.
	err := svc.ReviewAgentApproval(ctx, completeRes1.Agent.OrgID, completeRes3.Agent.AgentID, completeRes2.Agent.AgentID, "approved", "")
	if !errors.Is(err, agents.ErrNotOrgAdmin) {
		t.Fatalf("expected ErrNotOrgAdmin, got %v", err)
	}
}

func TestAdminApproval_RejectedTokenRejectedByAuth(t *testing.T) {
	svc, store := newAgentServiceWithApprovals()
	ctx := context.Background()

	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, _ := svc.BeginRegistration(ctx, "approvalorg6", "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	completeRes1, _ := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)

	if err := store.UpdateOrgVerificationMode(ctx, completeRes1.Agent.OrgID, "admin_approval"); err != nil {
		t.Fatalf("UpdateOrgVerificationMode: %v", err)
	}

	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, _ := svc.BeginRegistration(ctx, "approvalorg6", "second@example.com", "Second", "edge", base64.StdEncoding.EncodeToString(pub2), "")
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	completeRes2, _ := svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2)

	// Reject the second agent.
	if err := svc.ReviewAgentApproval(ctx, completeRes1.Agent.OrgID, completeRes2.Agent.AgentID, completeRes1.Agent.AgentID, "rejected", "no"); err != nil {
		t.Fatalf("ReviewAgentApproval: %v", err)
	}

	// Token should now be revoked.
	_, _, err := svc.AuthenticateAgent(ctx, completeRes2.AccessToken)
	if !errors.Is(err, agents.ErrRevokedAgentToken) {
		t.Fatalf("expected ErrRevokedAgentToken for rejected agent token, got %v", err)
	}
}

// TestDeletedOrgSlugCannotBeReused verifies that once an org is soft-deleted
// its slug is permanently blocked: a subsequent CompleteRegistration attempt
// using the same slug must fail with ErrOrgSlugTaken.
func TestDeletedOrgSlugCannotBeReused(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL: 5 * time.Minute,
		AuthTokenTTL:     time.Hour,
		DefaultOrgName:   "Test Org",
	}
	svc := agents.NewService(store, store, store, store, store, cfg, store)

	const orgSlug = "terminal-org"

	// --- Step 1: register an admin agent in the org. ---
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	res1, err := svc.BeginRegistration(ctx, orgSlug, "admin@example.com", "Admin", "edge", base64.StdEncoding.EncodeToString(pub1), "")
	if err != nil {
		t.Fatalf("BeginRegistration(1): %v", err)
	}
	sig1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv1, []byte(res1.Payload)))
	cr1, err := svc.CompleteRegistration(ctx, res1.Challenge.ChallengeID, sig1)
	if err != nil {
		t.Fatalf("CompleteRegistration(1): %v", err)
	}

	// Promote the user to admin so DeleteOrg is permitted.
	if err := store.UpdateUserRole(ctx, cr1.User.UserID, "admin"); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	// Reload user with updated role.
	adminUser, _, _ := store.FindUserByID(ctx, cr1.User.UserID)

	// --- Step 2: delete the org. ---
	if err := svc.DeleteOrg(ctx, cr1.Agent, adminUser, orgSlug); err != nil {
		t.Fatalf("DeleteOrg: %v", err)
	}

	// --- Step 3: attempt to re-register using the same org slug. ---
	pub2, priv2, _ := ed25519.GenerateKey(rand.Reader)
	res2, err := svc.BeginRegistration(ctx, orgSlug, "new@example.com", "New Agent", "edge", base64.StdEncoding.EncodeToString(pub2), "")
	if err != nil {
		t.Fatalf("BeginRegistration(2): %v", err)
	}
	sig2 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv2, []byte(res2.Payload)))
	_, err = svc.CompleteRegistration(ctx, res2.Challenge.ChallengeID, sig2)
	if !errors.Is(err, agents.ErrOrgSlugTaken) {
		t.Fatalf("expected ErrOrgSlugTaken after org deletion, got %v", err)
	}
}
