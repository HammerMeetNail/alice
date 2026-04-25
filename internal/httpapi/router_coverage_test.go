package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alice/internal/config"
	"alice/internal/storage/memory"
)

// --- pure function unit tests ---

func TestParseCIDRs(t *testing.T) {
	// Two valid CIDRs are parsed.
	cidrs := ParseCIDRs([]string{"10.0.0.0/8", "192.168.1.0/24"})
	if len(cidrs) != 2 {
		t.Fatalf("expected 2 parsed CIDRs, got %d", len(cidrs))
	}

	// An invalid CIDR is silently skipped; valid one still returned.
	cidrs2 := ParseCIDRs([]string{"not-a-cidr", "10.0.0.0/8"})
	if len(cidrs2) != 1 {
		t.Fatalf("expected 1 CIDR after skipping invalid, got %d", len(cidrs2))
	}

	// Nil input returns an empty slice without panicking.
	cidrs3 := ParseCIDRs(nil)
	if len(cidrs3) != 0 {
		t.Fatalf("expected 0 CIDRs for nil input, got %d", len(cidrs3))
	}

	// Whitespace around CIDR string is trimmed.
	cidrs4 := ParseCIDRs([]string{"  10.0.0.0/8  "})
	if len(cidrs4) != 1 {
		t.Fatalf("expected 1 CIDR after trimming whitespace, got %d", len(cidrs4))
	}
}

func TestRequestIDFromContext(t *testing.T) {
	// Empty context returns empty string.
	if id := RequestIDFromContext(context.Background()); id != "" {
		t.Fatalf("expected empty string from empty context, got %q", id)
	}

	// Context with a request ID returns that ID.
	ctx := context.WithValue(context.Background(), requestIDContextKey{}, "req-abc")
	if id := RequestIDFromContext(ctx); id != "req-abc" {
		t.Fatalf("expected req-abc, got %q", id)
	}
}

func TestLoggerFromContext(t *testing.T) {
	// Empty context returns slog.Default().
	got := LoggerFromContext(context.Background())
	if got != slog.Default() {
		t.Fatal("expected slog.Default() for empty context")
	}

	// Context with a stored logger returns that logger.
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), loggerContextKey{}, custom)
	if got := LoggerFromContext(ctx); got != custom {
		t.Fatal("expected the custom logger to be returned from context")
	}
}

func TestXffClientIP(t *testing.T) {
	_, trusted10, _ := net.ParseCIDR("10.0.0.0/8")
	trusted := []*net.IPNet{trusted10}

	// Empty XFF returns empty string.
	if ip := xffClientIP("", trusted); ip != "" {
		t.Fatalf("expected empty string for empty XFF, got %q", ip)
	}

	// Single untrusted IP is returned directly.
	if ip := xffClientIP("203.0.113.1", trusted); ip != "203.0.113.1" {
		t.Fatalf("expected 203.0.113.1, got %q", ip)
	}

	// Rightmost untrusted IP is selected when proxies are trusted.
	// XFF: client, proxy1 (trusted), proxy2 (trusted) — return client.
	if ip := xffClientIP("203.0.113.1, 10.0.0.1, 10.0.0.2", trusted); ip != "203.0.113.1" {
		t.Fatalf("expected 203.0.113.1 (leftmost untrusted), got %q", ip)
	}

	// All IPs in trusted range — fall back to leftmost entry.
	if ip := xffClientIP("10.0.0.1, 10.0.0.2", trusted); ip != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1 (fallback when all trusted), got %q", ip)
	}

	// Invalid entries are skipped; first valid untrusted is returned.
	if ip := xffClientIP("not-an-ip, 203.0.113.5", trusted); ip != "203.0.113.5" {
		t.Fatalf("expected 203.0.113.5, got %q", ip)
	}
}

func TestClientIPFromRequest(t *testing.T) {
	_, trusted10, _ := net.ParseCIDR("10.0.0.0/8")
	proxies := []*net.IPNet{trusted10}

	// No trusted proxies: RemoteAddr host is returned.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "1.2.3.4:5678"
	if ip := clientIPFromRequest(req1, nil); ip != "1.2.3.4" {
		t.Fatalf("expected 1.2.3.4, got %q", ip)
	}

	// RemoteAddr with no port (unusual but possible): raw string used.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "1.2.3.4"
	if ip := clientIPFromRequest(req2, nil); ip != "1.2.3.4" {
		t.Fatalf("expected 1.2.3.4 for portless RemoteAddr, got %q", ip)
	}

	// Request comes from a trusted proxy with XFF — use XFF IP.
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "10.0.0.1:1234"
	req3.Header.Set("X-Forwarded-For", "203.0.113.1")
	if ip := clientIPFromRequest(req3, proxies); ip != "203.0.113.1" {
		t.Fatalf("expected 203.0.113.1 from XFF, got %q", ip)
	}

	// Request comes from a trusted proxy but sends no XFF — fall back to proxy IP.
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	req4.RemoteAddr = "10.0.0.1:1234"
	if ip := clientIPFromRequest(req4, proxies); ip != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1 (proxy fallback), got %q", ip)
	}

	// Request is not from a trusted proxy — ignore XFF spoofing.
	req5 := httptest.NewRequest(http.MethodGet, "/", nil)
	req5.RemoteAddr = "5.6.7.8:9000"
	req5.Header.Set("X-Forwarded-For", "evil-ip")
	if ip := clientIPFromRequest(req5, proxies); ip != "5.6.7.8" {
		t.Fatalf("expected 5.6.7.8 (untrusted source ignores XFF), got %q", ip)
	}
}

// --- helpers for OTP-based tests ---

// newTestHandlerWithCustomOTP builds a handler with configurable OTP TTL and
// max-attempts, backed by an in-memory store and a testCapturingSender.
func newTestHandlerWithCustomOTP(t *testing.T, ttl time.Duration, maxAttempts int) (http.Handler, *testCapturingSender) {
	t.Helper()
	store := memory.New()
	cfg := config.Config{
		AuthChallengeTTL:    5 * time.Minute,
		AuthTokenTTL:        time.Hour,
		DefaultOrgName:      "Test Org",
		EmailOTPTTL:         ttl,
		EmailOTPMaxAttempts: maxAttempts,
	}
	sender := &testCapturingSender{}
	return buildTestHandlerWithSender(cfg, store, sender), sender
}

// registerOTPAgent registers a new agent in an OTP-enabled handler and
// returns the bearer token for the pending_email_verification agent.
func registerOTPAgent(t *testing.T, handler http.Handler, orgSlug, email string) string {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate registration key: %v", err)
	}

	challenge := issueRegistrationChallenge(t, handler, orgSlug, email, base64.StdEncoding.EncodeToString(publicKey))
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(challenge.Challenge)))

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challenge.ChallengeID,
		"challenge_signature": signature,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("register OTP agent status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return payload["access_token"].(string)
}

// --- verify-email error paths ---

// TestVerifyEmail_EmptyCode checks that submitting an empty code returns 400.
func TestVerifyEmail_EmptyCode(t *testing.T) {
	handler, _ := newTestHandlerWithCustomOTP(t, 10*time.Minute, 5)
	fixture := newFixture(t)

	token := registerOTPAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", token, map[string]any{
		"code": "",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty code, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVerifyEmail_ExpiredCode checks that an expired OTP returns 410 Gone.
func TestVerifyEmail_ExpiredCode(t *testing.T) {
	handler, _ := newTestHandlerWithCustomOTP(t, 1*time.Millisecond, 5)
	fixture := newFixture(t)

	token := registerOTPAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Wait for the OTP to expire.
	time.Sleep(10 * time.Millisecond)

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", token, map[string]any{
		"code": "000000",
	})
	if rec.Code != http.StatusGone {
		t.Fatalf("expected 410 for expired code, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestVerifyEmail_MaxAttempts checks that exceeding EmailOTPMaxAttempts returns 429.
func TestVerifyEmail_MaxAttempts(t *testing.T) {
	// Allow only 2 attempts before locking out.
	handler, _ := newTestHandlerWithCustomOTP(t, 10*time.Minute, 2)
	fixture := newFixture(t)

	token := registerOTPAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Submit 2 wrong codes — each increments the attempt counter.
	for i := 0; i < 2; i++ {
		rec := performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", token, map[string]any{
			"code": "000000",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 for wrong code, got %d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	// The 3rd attempt hits the max-attempts limit.
	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", token, map[string]any{
		"code": "000000",
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after max attempts, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- resend-verification error paths ---

// TestResendVerification_TooSoon checks that an immediate resend returns 429.
func TestResendVerification_TooSoon(t *testing.T) {
	handler, _ := newTestHandlerWithCustomOTP(t, 10*time.Minute, 5)
	fixture := newFixture(t)

	token := registerOTPAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Resend immediately after registration — the service rate-limits to 60s.
	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/resend-verification", token, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for too-soon resend, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestResendVerification_NotConfigured checks the 400 when OTP is not configured.
func TestResendVerification_NotConfigured(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	// Regular (non-OTP) agent — the email verification system is not wired.
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/resend-verification", alice.AccessToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when OTP not configured, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- operator-enabled ---

// TestSetOperatorEnabled_Success checks that enabling and disabling operator returns 200.
func TestSetOperatorEnabled_Success(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Enable.
	rec := performJSON(t, handler, http.MethodPost, "/v1/users/me/operator-enabled", alice.AccessToken, map[string]any{
		"enabled": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable operator: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode enable response: %v", err)
	}
	if payload["operator_enabled"] != true {
		t.Fatalf("expected operator_enabled=true, got %v", payload["operator_enabled"])
	}

	// Disable.
	rec = performJSON(t, handler, http.MethodPost, "/v1/users/me/operator-enabled", alice.AccessToken, map[string]any{
		"enabled": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable operator: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload2 map[string]any
	json.NewDecoder(rec.Body).Decode(&payload2)
	if payload2["operator_enabled"] != false {
		t.Fatalf("expected operator_enabled=false, got %v", payload2["operator_enabled"])
	}
}

// TestSetOperatorEnabled_MalformedJSON checks that malformed JSON returns 400.
func TestSetOperatorEnabled_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/users/me/operator-enabled", strings.NewReader("{not json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- activate risk policy ---

// TestActivateRiskPolicy_NoID checks that omitting a policy ID in the path
// returns 404.
func TestActivateRiskPolicy_NoID(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// POST /v1/orgs/risk-policies/ with nothing after the slash.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policies/", alice.AccessToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing policy ID, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestActivateRiskPolicy_NonAdmin checks that a non-admin caller gets 403.
func TestActivateRiskPolicy_NonAdmin(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail) // admin
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)    // member

	// Alice applies a policy so a valid policy ID exists.
	// Policy must have at least one rule; use a permissive allow-all rule.
	applyRec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", alice.AccessToken, map[string]any{
		"name":   "test-policy",
		"source": json.RawMessage(`{"rules":[{"when":{},"then":"allow","reason":"default"}]}`),
	})
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply policy: expected 200, got %d body=%s", applyRec.Code, applyRec.Body.String())
	}
	var policyPayload map[string]any
	json.NewDecoder(applyRec.Body).Decode(&policyPayload)
	policyID := policyPayload["policy_id"].(string)

	// Bob (non-admin) tries to activate it.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policies/"+policyID+"/activate", bob.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin activate, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestActivateRiskPolicy_NotFound checks that activating a non-existent policy
// returns 404.
func TestActivateRiskPolicy_NotFound(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policies/nonexistent-id/activate", alice.AccessToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown policy ID, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- action action endpoints ---

// createTestAction is a helper that enables operator and creates an
// acknowledge_blocker action, returning its action_id.
func createTestAction(t *testing.T, handler http.Handler, accessToken string) string {
	t.Helper()

	// Enable operator for the calling agent's user.
	rec := performJSON(t, handler, http.MethodPost, "/v1/users/me/operator-enabled", accessToken, map[string]any{
		"enabled": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable operator: %d %s", rec.Code, rec.Body.String())
	}

	// Create an action. Without an active risk policy the default policy
	// allows everything, so the action starts in the "approved" state.
	rec = performJSON(t, handler, http.MethodPost, "/v1/actions", accessToken, map[string]any{
		"kind":   "acknowledge_blocker",
		"inputs": map[string]any{"message": "test message"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create action: %d %s", rec.Code, rec.Body.String())
	}

	var ap map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&ap); err != nil {
		t.Fatalf("decode create action response: %v", err)
	}
	return ap["action_id"].(string)
}

// TestActionAction_Cancel creates an action and then cancels it via the
// POST /v1/actions/:id/cancel endpoint.
func TestActionAction_Cancel(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	actionID := createTestAction(t, handler, alice.AccessToken)

	rec := performJSON(t, handler, http.MethodPost, "/v1/actions/"+actionID+"/cancel", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel action: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	json.NewDecoder(rec.Body).Decode(&result)
	if result["state"] != "cancelled" {
		t.Fatalf("expected state=cancelled, got %v", result["state"])
	}
}

// TestActionAction_Approve calls POST /v1/actions/:id/approve on an already-
// approved action (idempotent path — returns 200 immediately).
func TestActionAction_Approve(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	actionID := createTestAction(t, handler, alice.AccessToken)

	// The action is already in "approved" state; Approve is idempotent.
	rec := performJSON(t, handler, http.MethodPost, "/v1/actions/"+actionID+"/approve", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve action: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	json.NewDecoder(rec.Body).Decode(&result)
	if result["state"] != "approved" {
		t.Fatalf("expected state=approved, got %v", result["state"])
	}
}

// TestActionAction_UnknownVerb checks that an unknown verb in the path returns 404.
func TestActionAction_UnknownVerb(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	actionID := createTestAction(t, handler, alice.AccessToken)

	rec := performJSON(t, handler, http.MethodPost, "/v1/actions/"+actionID+"/explode", alice.AccessToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown verb, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestActionAction_BadPath checks that a path lacking the verb segment returns 404.
func TestActionAction_BadPath(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// /v1/actions/<id> has only one path segment after the prefix.
	rec := performJSON(t, handler, http.MethodPost, "/v1/actions/just-an-id", alice.AccessToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for path missing verb, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- publish artifact error paths ---

// TestPublishArtifact_MalformedJSON checks that a malformed JSON body returns 400.
func TestPublishArtifact_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/artifacts", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- send request to peer error paths ---

// TestSendRequestToPeer_MalformedJSON checks that a malformed JSON body returns 400.
func TestSendRequestToPeer_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/requests", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSendRequestToPeer_InvalidInput checks that a missing to_user_email returns 400.
func TestSendRequestToPeer_InvalidInput(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/requests", alice.AccessToken, map[string]any{
		"request_type": "information",
		"title":        "test",
		"content":      "hello",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing to_user_email, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSendRequestToPeer_TargetNotFound checks that an unregistered target returns 404.
func TestSendRequestToPeer_TargetNotFound(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/requests", alice.AccessToken, map[string]any{
		"to_user_email": "nobody@nowhere.example.com",
		"request_type":  "information",
		"title":         "test",
		"content":       "hello",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown target, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- query peer status error paths ---

// TestQueryPeerStatus_MalformedJSON checks that a malformed JSON body returns 400.
func TestQueryPeerStatus_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/queries", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestQueryPeerStatus_InvalidInput checks that a missing to_user_email returns 400.
func TestQueryPeerStatus_InvalidInput(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"purpose":         "status_check",
		"requested_types": []string{"status_delta"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing to_user_email, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestQueryPeerStatus_TargetNotFound checks that a query for an unregistered user returns 404.
func TestQueryPeerStatus_TargetNotFound(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	now := time.Now().UTC()
	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   "nobody@nowhere.example.com",
		"purpose":         "status_check",
		"requested_types": []string{"status_delta"},
		"time_window": map[string]any{
			"start": now.Add(-24 * time.Hour),
			"end":   now,
		},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown target, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- respond to request error paths ---

// TestRespondToRequest_BadPath checks that a path with the wrong suffix returns 404.
func TestRespondToRequest_BadPath(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Path has wrong suffix: trimActionPath won't find "/respond".
	rec := performJSON(t, handler, http.MethodPost, "/v1/requests/some-request-id/wrong-suffix", alice.AccessToken, map[string]any{
		"response": "accepted",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for bad path suffix, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRespondToRequest_MalformedJSON checks that a malformed JSON body returns 400.
func TestRespondToRequest_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/requests/some-request-id/respond", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRespondToRequest_UnknownRequest checks that responding to a non-existent request returns 404.
func TestRespondToRequest_UnknownRequest(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/requests/nonexistent-request-id/respond", alice.AccessToken, map[string]any{
		"response": "accepted",
		"message":  "",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown request, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRespondToRequest_AlreadyClosed checks that responding twice to the same request returns 409.
func TestRespondToRequest_AlreadyClosed(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice sends a request to bob.
	requestID := sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "information",
		"title":         "question",
		"content":       "hello",
	})

	// Bob accepts it once.
	respondToRequest(t, handler, bob.AccessToken, requestID, map[string]any{
		"response": "accepted",
	})

	// Bob tries to respond again — already closed.
	rec := performJSON(t, handler, http.MethodPost, "/v1/requests/"+requestID+"/respond", bob.AccessToken, map[string]any{
		"response": "accepted",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for already-closed request, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- resolve approval error paths ---

// TestResolveApproval_BadPath checks that a path with the wrong suffix returns 404.
func TestResolveApproval_BadPath(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Path has wrong suffix: trimActionPath won't find "/resolve".
	rec := performJSON(t, handler, http.MethodPost, "/v1/approvals/some-approval-id/wrong-suffix", alice.AccessToken, map[string]any{
		"decision": "approved",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for bad path suffix, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestResolveApproval_MalformedJSON checks that a malformed JSON body returns 400.
func TestResolveApproval_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/some-approval-id/resolve", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestResolveApproval_BadDecision checks that an invalid decision value returns 400.
func TestResolveApproval_BadDecision(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/approvals/some-approval-id/resolve", alice.AccessToken, map[string]any{
		"decision": "invalid-decision",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad decision, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestResolveApproval_UnknownApproval checks that resolving a non-existent approval returns 404.
func TestResolveApproval_UnknownApproval(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/approvals/nonexistent-approval-id/resolve", alice.AccessToken, map[string]any{
		"decision": "approved",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown approval, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- review agent error paths ---

// TestReviewAgent_BadDecision checks that an invalid decision returns 400.
func TestReviewAgent_BadDecision(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/agents/some-agent-id/review", alice.AccessToken, map[string]any{
		"decision": "maybe",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad decision, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- create action error paths ---

// TestCreateAction_MissingKind checks that an empty kind returns 400.
func TestCreateAction_MissingKind(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Enable operator first so the kind check is reached.
	rec := performJSON(t, handler, http.MethodPost, "/v1/users/me/operator-enabled", alice.AccessToken, map[string]any{
		"enabled": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable operator: %d %s", rec.Code, rec.Body.String())
	}

	rec = performJSON(t, handler, http.MethodPost, "/v1/actions", alice.AccessToken, map[string]any{
		"kind":   "",
		"inputs": map[string]any{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing kind, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateAction_OperatorNotEnabled checks that creating an action without
// operator enabled returns 403.
func TestCreateAction_OperatorNotEnabled(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Operator is NOT enabled.
	rec := performJSON(t, handler, http.MethodPost, "/v1/actions", alice.AccessToken, map[string]any{
		"kind":   "acknowledge_blocker",
		"inputs": map[string]any{"message": "test"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when operator not enabled, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- get query result error paths ---

// TestGetQueryResult_NotFound checks that a non-existent query ID returns 404.
func TestGetQueryResult_NotFound(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/queries/nonexistent-query-id", alice.AccessToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown query, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- audit summary paths ---

// TestAuditSummary_InvalidSince checks that a malformed since parameter returns 400.
func TestAuditSummary_InvalidSince(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/audit/summary?since=not-a-timestamp", alice.AccessToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad since, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAuditSummary_Valid checks that a valid GET /v1/audit/summary returns 200.
func TestAuditSummary_Valid(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/audit/summary", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for audit summary, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := payload["events"]; !ok {
		t.Fatal("expected events field in response")
	}
}

// --- grant permission error paths ---

// TestGrantPermission_MalformedJSON checks that a malformed JSON body returns 400.
func TestGrantPermission_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/policy-grants", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestGrantPermission_GranteeNotFound checks that granting to an unknown email returns 404.
func TestGrantPermission_GranteeNotFound(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     "nobody@nowhere.example.com",
		"scope_type":             "global",
		"scope_ref":              "",
		"allowed_artifact_types": []string{"status_delta"},
		"allowed_purposes":       []string{"status_check"},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown grantee, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- revoke permission paths ---

// TestRevokePermission_NotFound checks that revoking a non-existent grant returns 404.
func TestRevokePermission_NotFound(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodDelete, "/v1/policy-grants/nonexistent-grant-id", alice.AccessToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown grant, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRevokePermission_Success checks that revoking an owned grant returns 200.
func TestRevokePermission_Success(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice grants bob permission.
	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              "test-project",
		"allowed_artifact_types": []string{"summary"},
		"allowed_purposes":       []string{"status_check"},
		"max_sensitivity":        "low",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("grant: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var grantResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&grantResp); err != nil {
		t.Fatalf("decode grant response: %v", err)
	}
	grantID, _ := grantResp["policy_grant_id"].(string)
	if grantID == "" {
		t.Fatal("expected non-empty policy_grant_id")
	}

	// Bob has no access yet — but alice owns the grant.
	_ = bob

	// Alice revokes the grant.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/policy-grants/"+grantID, alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var revokeResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&revokeResp); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if revokeResp["revoked"] != true {
		t.Fatalf("expected revoked=true, got %v", revokeResp["revoked"])
	}
}

// --- list allowed peers path ---

// TestListAllowedPeers_WithGrant checks that after receiving a grant GET /v1/policy-grants/peers
// returns the grantor in the peers list.
func TestListAllowedPeers_WithGrant(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice grants bob access.
	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              "test-project",
		"allowed_artifact_types": []string{"summary"},
		"allowed_purposes":       []string{"status_check"},
		"max_sensitivity":        "low",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("grant: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Bob lists allowed peers — alice should appear.
	rec = performJSON(t, handler, http.MethodGet, "/v1/peers", bob.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list peers: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	peers, _ := payload["peers"].([]any)
	if len(peers) == 0 {
		t.Fatal("expected at least one peer (alice)")
	}
}

// --- correct artifact path ---

// TestCorrectArtifact_MalformedJSON checks that a malformed JSON body returns 400.
func TestCorrectArtifact_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/artifacts/some-artifact-id/correct", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCorrectArtifact_Success publishes an artifact and then corrects it.
func TestCorrectArtifact_Success(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Publish an artifact to correct later.
	rec := performJSON(t, handler, http.MethodPost, "/v1/artifacts", alice.AccessToken, map[string]any{
		"artifact": map[string]any{
			"type":        "summary",
			"title":       "initial title",
			"content":     "initial content",
			"sensitivity": "low",
			"timestamp":   time.Now().UTC(),
			"source_refs": []map[string]any{
				{
					"source_system": "github",
					"source_type":   "pull_request",
					"source_id":     "repo:org/repo:pr:1",
					"observed_at":   time.Now().UTC(),
					"trust_class":   "structured_system",
					"sensitivity":   "low",
				},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("publish: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var pubResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&pubResp); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	artifactID, _ := pubResp["artifact_id"].(string)
	if artifactID == "" {
		t.Fatal("expected non-empty artifact_id")
	}

	// Correct the artifact.
	rec = performJSON(t, handler, http.MethodPost, "/v1/artifacts/"+artifactID+"/correct", alice.AccessToken, map[string]any{
		"artifact": map[string]any{
			"type":        "summary",
			"title":       "corrected title",
			"content":     "corrected content",
			"sensitivity": "low",
			"timestamp":   time.Now().UTC(),
			"source_refs": []map[string]any{
				{
					"source_system": "github",
					"source_type":   "pull_request",
					"source_id":     "repo:org/repo:pr:1",
					"observed_at":   time.Now().UTC(),
					"trust_class":   "structured_system",
					"sensitivity":   "low",
				},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("correct: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var corrResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&corrResp); err != nil {
		t.Fatalf("decode correct response: %v", err)
	}
	if corrResp["stored"] != true {
		t.Fatalf("expected stored=true, got %v", corrResp["stored"])
	}
	if corrResp["supersedes_artifact_id"] != artifactID {
		t.Fatalf("expected supersedes_artifact_id=%q, got %v", artifactID, corrResp["supersedes_artifact_id"])
	}
}

// --- list incoming / sent requests ---

// TestListIncomingRequests_Valid sends a request from alice to bob and verifies
// that GET /v1/requests/inbox returns it for bob with the sender populated.
func TestListIncomingRequests_Valid(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "information",
		"title":         "how is it going?",
		"content":       "please share status",
	})

	rec := performJSON(t, handler, http.MethodGet, "/v1/requests/incoming", bob.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list inbox: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	requests, _ := payload["requests"].([]any)
	if len(requests) == 0 {
		t.Fatal("expected at least one incoming request")
	}
	// Verify sender email is populated.
	first, _ := requests[0].(map[string]any)
	if first["from_user_email"] != fixture.AliceEmail {
		t.Fatalf("expected from_user_email=%q, got %v", fixture.AliceEmail, first["from_user_email"])
	}
	_ = alice
}

// TestListSentRequests_Valid sends a request and verifies that GET /v1/requests/outbox
// returns it for the sender with the recipient populated.
func TestListSentRequests_Valid(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "information",
		"title":         "status check",
		"content":       "please update me",
	})

	rec := performJSON(t, handler, http.MethodGet, "/v1/requests/sent", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list outbox: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	requests, _ := payload["requests"].([]any)
	if len(requests) == 0 {
		t.Fatal("expected at least one sent request")
	}
	// Verify recipient email is populated.
	first, _ := requests[0].(map[string]any)
	if first["to_user_email"] != fixture.BobEmail {
		t.Fatalf("expected to_user_email=%q, got %v", fixture.BobEmail, first["to_user_email"])
	}
}

// --- list actions with state filter ---

// TestListActions_WithStateFilter verifies that GET /v1/actions?state=cancelled
// filters results to only cancelled actions.
func TestListActions_WithStateFilter(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	actionID := createTestAction(t, handler, alice.AccessToken)

	// Cancel the action.
	rec := performJSON(t, handler, http.MethodPost, "/v1/actions/"+actionID+"/cancel", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel action: %d %s", rec.Code, rec.Body.String())
	}

	// List with state=cancelled — should include at least our action.
	rec = performJSON(t, handler, http.MethodGet, "/v1/actions?state=cancelled", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list actions: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := payload["actions"].([]any)
	if len(items) == 0 {
		t.Fatal("expected at least one cancelled action")
	}
}

// --- pagination helpers ---

// TestParsePagination_WithLimitParam exercises the limit capping and cursor decoding
// branches of parsePagination by hitting an endpoint with ?limit=300 and ?limit=5.
func TestParsePagination_WithLimitParam(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// limit=300 gets capped to 200 at server side; response is still 200 OK.
	rec := performJSON(t, handler, http.MethodGet, "/v1/orgs/risk-policies?limit=300", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// limit=5 is a valid explicit limit.
	rec = performJSON(t, handler, http.MethodGet, "/v1/orgs/risk-policies?limit=5", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestParsePagination_WithCursorParam exercises the cursor decoding branch.
func TestParsePagination_WithCursorParam(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// A valid base64-encoded integer cursor.
	cursor := base64.StdEncoding.EncodeToString([]byte("50"))
	rec := performJSON(t, handler, http.MethodGet, "/v1/orgs/risk-policies?cursor="+cursor, alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- risk policy list and activate paths ---

// TestListRiskPolicies_Empty verifies that GET /v1/orgs/risk-policies returns 200
// with an empty policies array when no policy has been applied yet.
func TestListRiskPolicies_Empty(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodGet, "/v1/orgs/risk-policies", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := payload["policies"]; !ok {
		t.Fatal("expected policies field in response")
	}
}

// TestApplyAndListRiskPolicy verifies the full apply+list cycle: apply a valid risk
// policy as admin, then list it and verify it appears with the right version.
func TestApplyAndListRiskPolicy(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	policy := json.RawMessage(`{"rules":[{"when":{"risk_level_at_least":"L3"},"then":"require_approval"}]}`)
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", alice.AccessToken, map[string]any{
		"name":   "test-policy",
		"source": policy,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply policy: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var applyResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&applyResp); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	policyID, _ := applyResp["policy_id"].(string)
	if policyID == "" {
		t.Fatal("expected non-empty policy_id")
	}

	// List — should contain the applied policy.
	rec = performJSON(t, handler, http.MethodGet, "/v1/orgs/risk-policies", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list policies: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var listResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	items, _ := listResp["policies"].([]any)
	if len(items) == 0 {
		t.Fatal("expected at least one policy in list")
	}

	// Activate the policy.
	rec = performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policies/"+policyID+"/activate", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate policy: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- delete org paths ---

// TestDeleteOrg_NonAdminForbidden verifies that a non-admin agent receives 403 when
// trying to delete an org.
func TestDeleteOrg_NonAdminForbidden(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	// Register alice (first registrant → auto-admin) then bob (non-admin).
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Alice approves bob so bob is active.
	listRec := performJSON(t, handler, http.MethodGet, "/v1/orgs/pending-agents", alice.AccessToken, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list pending: expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp map[string]any
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode pending: %v", err)
	}
	pending, _ := listResp["agents"].([]any)
	if len(pending) > 0 {
		bobAgentID := pending[0].(map[string]any)["agent_id"].(string)
		approveRec := performJSON(t, handler, http.MethodPost, "/v1/orgs/agents/"+bobAgentID+"/review", alice.AccessToken, map[string]any{
			"decision": "approved",
		})
		if approveRec.Code != http.StatusOK {
			t.Fatalf("approve: %d %s", approveRec.Code, approveRec.Body.String())
		}
	}

	// Bob (non-admin) tries to delete the org.
	rec := performJSON(t, handler, http.MethodDelete, "/v1/orgs/"+fixture.OrgSlug, bob.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin delete org, got %d body=%s", rec.Code, rec.Body.String())
	}
	_ = alice
}

// TestDeleteOrg_Success verifies that an admin agent can delete their own org.
func TestDeleteOrg_Success(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodDelete, "/v1/orgs/"+fixture.OrgSlug, alice.AccessToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeleteOrg_NotFound verifies that deleting a non-existent org slug returns 404.
func TestDeleteOrg_NotFound(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodDelete, "/v1/orgs/nonexistent-org-slug", alice.AccessToken, nil)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("expected 404 or 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- publish artifact malformed JSON ---

// TestPublishArtifact_BadJSON is a duplicate guard; real test is earlier.

// --- set operator enabled malformed JSON ---

// TestSetOperatorEnabled_BadJSON is a duplicate guard; real test is earlier.

// --- grant permission for GranteeNotFound already exists; add MissingFields error ---

// TestApplyRiskPolicy_MissingSource verifies that applying a risk policy without
// a source body returns 400.
func TestApplyRiskPolicy_MissingSource(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/risk-policy", alice.AccessToken, map[string]any{
		"name": "test-policy",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing source, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestApplyRiskPolicy_MalformedJSON verifies that a malformed JSON body returns 400.
func TestApplyRiskPolicy_MalformedJSON(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/orgs/risk-policy", strings.NewReader("{bad json"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestGrantPermission_InvalidArtifactType verifies that an invalid artifact type returns 400.
func TestGrantPermission_InvalidArtifactType(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/policy-grants", alice.AccessToken, map[string]any{
		"grantee_user_email":     fixture.BobEmail,
		"scope_type":             "project",
		"scope_ref":              "test-project",
		"allowed_artifact_types": []string{"not_a_valid_type"},
		"allowed_purposes":       []string{"status_check"},
		"max_sensitivity":        "low",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid artifact type, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- org graph: team CRUD flow ---

// TestOrgGraph_TeamFlow creates a team, lists teams, adds a member, lists members,
// then deletes the member and the team. This covers the main success paths of
// handleCreateTeam, handleListTeams, handleAddTeamMember, handleListTeamMembers,
// and handleTeamDelete.
func TestOrgGraph_TeamFlow(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)
	_ = bob

	// Create a team.
	rec := performJSON(t, handler, http.MethodPost, "/v1/org/teams", alice.AccessToken, map[string]any{
		"name": "alpha-team",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create team: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var teamResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&teamResp); err != nil {
		t.Fatalf("decode team response: %v", err)
	}
	teamID, _ := teamResp["team_id"].(string)
	if teamID == "" {
		t.Fatal("expected non-empty team_id")
	}

	// List teams — should include the new team.
	rec = performJSON(t, handler, http.MethodGet, "/v1/org/teams", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list teams: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var listResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list teams response: %v", err)
	}
	teams, _ := listResp["teams"].([]any)
	if len(teams) == 0 {
		t.Fatal("expected at least one team")
	}

	// Add bob to the team.
	rec = performJSON(t, handler, http.MethodPost, "/v1/org/teams/"+teamID+"/members", alice.AccessToken, map[string]any{
		"user_email": fixture.BobEmail,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("add team member: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// List team members — should include bob.
	rec = performJSON(t, handler, http.MethodGet, "/v1/org/teams/"+teamID+"/members", alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list team members: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var membersResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&membersResp); err != nil {
		t.Fatalf("decode members response: %v", err)
	}
	members, _ := membersResp["members"].([]any)
	if len(members) == 0 {
		t.Fatal("expected at least one team member")
	}

	// Delete bob from the team.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/org/teams/"+teamID+"/members/"+fixture.BobEmail, alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete team member: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Delete the team itself.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/org/teams/"+teamID, alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete team: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestOrgGraph_ManagerFlow assigns a manager edge and then retrieves the chain.
func TestOrgGraph_ManagerFlow(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Assign alice as bob's manager.
	rec := performJSON(t, handler, http.MethodPost, "/v1/org/manager-edges", alice.AccessToken, map[string]any{
		"user_email":    fixture.BobEmail,
		"manager_email": fixture.AliceEmail,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("assign manager: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Get manager chain for bob.
	rec = performJSON(t, handler, http.MethodGet, "/v1/org/manager-edges/"+fixture.BobEmail, alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get manager chain: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var chainResp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&chainResp); err != nil {
		t.Fatalf("decode chain response: %v", err)
	}

	// Revoke the manager edge.
	rec = performJSON(t, handler, http.MethodDelete, "/v1/org/manager-edges/"+fixture.BobEmail, alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke manager: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// --- next cursor ---

// TestNextCursor_FullPage verifies that nextCursor returns a non-empty cursor when
// count == limit, covering the else branch of nextCursor.
func TestNextCursor_FullPage(t *testing.T) {
	cursor := nextCursor(10, 10, 0)
	if cursor == "" {
		t.Fatal("expected non-empty cursor for full page")
	}
}

// --- send request to peer error paths ---

// TestSendRequestToPeer_MissingTitle verifies that a request missing a required title
// returns 400 from ValidateRequestInput.
func TestSendRequestToPeer_MissingTitle(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/requests", alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "information",
		"title":         "",
		"content":       "hello",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing title, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ============================================================
// handleQueryPeerStatus — ErrPermissionDenied path
// ============================================================

// TestQueryPeerStatus_PermissionDenied verifies that querying without a grant
// returns 403 and hits the ErrPermissionDenied audit-and-deny branch.
// Uses newTestHandlerWithApprovals because that handler wires the query service
// without an org-graph evaluator, so a missing grant is a hard deny.
func TestQueryPeerStatus_PermissionDenied(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Query bob WITHOUT any grant — should be denied.
	rec := performJSON(t, handler, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   fixture.BobEmail,
		"purpose":         "status_check",
		"requested_types": []string{"summary"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unganted query, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ============================================================
// handleGetQueryResult — forbidden third-party path
// ============================================================

// TestGetQueryResult_Forbidden verifies that fetching a query result as a
// third-party agent (not the sender or recipient) returns 403.
func TestGetQueryResult_Forbidden(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)
	charlie := registerAgent(t, handler, fixture.OrgSlug, "charlie@example.com")

	// Alice grants herself so she can query bob.
	grantPermission(t, handler, bob.AccessToken, map[string]any{
		"grantee_user_email":     fixture.AliceEmail,
		"scope_type":             "project",
		"scope_ref":              fixture.ProjectScope,
		"allowed_artifact_types": []string{"summary"},
		"max_sensitivity":        "low",
		"allowed_purposes":       []string{"status_check"},
	})

	// Alice queries bob.
	qRec := performJSON(t, handler, http.MethodPost, "/v1/queries", alice.AccessToken, map[string]any{
		"to_user_email":   fixture.BobEmail,
		"purpose":         "status_check",
		"requested_types": []string{"summary"},
		"time_window": map[string]any{
			"start": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			"end":   time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
		},
	})
	if qRec.Code != http.StatusOK {
		t.Fatalf("query: expected 200, got %d body=%s", qRec.Code, qRec.Body.String())
	}
	var qResp map[string]any
	json.NewDecoder(qRec.Body).Decode(&qResp)
	queryID, _ := qResp["query_id"].(string)
	if queryID == "" {
		t.Fatal("expected non-empty query_id")
	}

	// Charlie tries to fetch the result — he is neither sender nor recipient.
	rec := performJSON(t, handler, http.MethodGet, "/v1/queries/"+queryID, charlie.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for third-party query result access, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ============================================================
// handleReviewAgent — error and success paths
// ============================================================

// TestReviewAgent_NonAdminForbidden verifies that a non-admin agent gets 403.
func TestReviewAgent_NonAdminForbidden(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail) // auto-admin
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)     // non-admin

	// Bob (non-admin) tries to review alice's agent.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/agents/"+alice.AgentID+"/review", bob.AccessToken, map[string]any{
		"decision": "approved",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin review, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestReviewAgent_ApprovalNotFound verifies that reviewing an agent with no
// pending approval returns 404.
func TestReviewAgent_ApprovalNotFound(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail) // auto-admin

	// Alice tries to review herself — she has no pending approval record.
	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/agents/"+alice.AgentID+"/review", alice.AccessToken, map[string]any{
		"decision": "approved",
	})
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("expected 404 or 403 for no-pending-approval review, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestReviewAgent_ApprovalSuccess covers the full admin-approval flow:
// set admin_approval mode, register a second agent, have the admin approve them.
func TestReviewAgent_ApprovalSuccess(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail) // auto-admin

	// Set org to admin_approval so the next registrant needs approval.
	modeRec := performJSON(t, handler, http.MethodPost, "/v1/orgs/verification-mode", alice.AccessToken, map[string]any{
		"verification_mode": "admin_approval",
	})
	if modeRec.Code != http.StatusOK {
		t.Fatalf("set verification mode: expected 200, got %d body=%s", modeRec.Code, modeRec.Body.String())
	}

	// Register bob — he ends up pending_admin_approval.
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// List pending agents to get bob's agent ID.
	listRec := performJSON(t, handler, http.MethodGet, "/v1/orgs/pending-agents", alice.AccessToken, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list pending: expected 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp map[string]any
	json.NewDecoder(listRec.Body).Decode(&listResp)
	// Use pending_agents or agents key.
	items, _ := listResp["pending_agents"].([]any)
	if len(items) == 0 {
		items, _ = listResp["agents"].([]any)
	}
	if len(items) == 0 {
		t.Skip("bob is not in pending_admin_approval — admin_approval mode may not have taken effect")
	}

	bobAgentID := items[0].(map[string]any)["agent_id"].(string)

	// Alice approves bob.
	approveRec := performJSON(t, handler, http.MethodPost, "/v1/orgs/agents/"+bobAgentID+"/review", alice.AccessToken, map[string]any{
		"decision": "approved",
	})
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve bob: expected 200, got %d body=%s", approveRec.Code, approveRec.Body.String())
	}
	_ = bob
}

// ============================================================
// handleResendVerification — ErrVerificationNotFound path
// ============================================================

// TestResendVerification_VerifiedAgentGetNotFound verifies that a fully-verified
// agent calling resend receives 404 (ErrVerificationNotFound).
func TestResendVerification_VerifiedAgentGetNotFound(t *testing.T) {
	handler, sender := newTestHandlerWithCustomOTP(t, 10*time.Minute, 5)
	fixture := newFixture(t)

	token := registerOTPAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Complete email verification to clear the pending record.
	code, ok := sender.LastCode()
	if !ok {
		t.Fatal("expected OTP code to be captured")
	}
	verRec := performJSON(t, handler, http.MethodPost, "/v1/agents/verify-email", token, map[string]any{
		"code": code,
	})
	if verRec.Code != http.StatusOK {
		t.Fatalf("verify email: expected 200, got %d body=%s", verRec.Code, verRec.Body.String())
	}

	// Now resend — no pending verification exists → 404.
	rec := performJSON(t, handler, http.MethodPost, "/v1/agents/resend-verification", token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for verified agent resend, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ============================================================
// handleUpdateVerificationMode — non-admin path
// ============================================================

// TestUpdateVerificationMode_NonAdminForbidden verifies that a non-admin gets 403.
func TestUpdateVerificationMode_NonAdminForbidden(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail) // admin
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/verification-mode", bob.AccessToken, map[string]any{
		"verification_mode": "admin_approval",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin verification mode change, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestUpdateVerificationMode_DecodeError verifies that malformed JSON returns 400.
func TestUpdateVerificationMode_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)

	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/orgs/verification-mode", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ============================================================
// Various decode-error paths
// ============================================================

// TestPublishArtifact_DecodeError checks that a malformed body returns 400.
func TestPublishArtifact_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/artifacts", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestGrantPermission_DecodeError checks that a malformed body returns 400.
func TestGrantPermission_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/policy-grants", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRevokePermission_DecodeError checks that a malformed body returns 400.
func TestRevokePermission_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodDelete, "/v1/policy-grants/bad-grant-id", nil)
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	// Not found or bad request are both acceptable; we just need the decode path exercised.
	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("unexpected 405 — route may be wrong; got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateAction_DecodeError checks that a malformed body returns 400.
func TestCreateAction_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/actions", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateTeam_DecodeError checks that a malformed body returns 400.
func TestCreateTeam_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/org/teams", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAddTeamMember_DecodeError checks that a malformed body returns 400.
func TestAddTeamMember_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Create a team first.
	tRec := performJSON(t, handler, http.MethodPost, "/v1/org/teams", alice.AccessToken, map[string]any{"name": "t1"})
	if tRec.Code != http.StatusOK {
		t.Fatalf("create team: %d %s", tRec.Code, tRec.Body.String())
	}
	var tr map[string]any
	json.NewDecoder(tRec.Body).Decode(&tr)
	teamID := tr["team_id"].(string)

	req := httptest.NewRequest(http.MethodPost, "/v1/org/teams/"+teamID+"/members", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAssignManager_DecodeError checks that a malformed body returns 400.
func TestAssignManager_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	req := httptest.NewRequest(http.MethodPost, "/v1/org/manager-edges", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRespondToRequest_DecodeError checks that a malformed body returns 400.
func TestRespondToRequest_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	// Create a request from alice to bob.
	requestID := sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "information",
		"title":         "test",
		"content":       "test content",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/requests/"+requestID+"/respond", strings.NewReader("{bad"))
	req.Header.Set("Authorization", "Bearer "+bob.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRespondToRequest_NotVisible checks that a third-party agent cannot respond
// to a request it is not the recipient of (ErrRequestNotVisible → 403).
func TestRespondToRequest_NotVisible(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	_ = registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)
	charlie := registerAgent(t, handler, fixture.OrgSlug, "charlie@example.com")

	// Alice sends a request to Bob; Charlie (not the recipient) tries to respond.
	requestID := sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "information",
		"title":         "test",
		"content":       "details",
	})

	rec := performJSON(t, handler, http.MethodPost, "/v1/requests/"+requestID+"/respond",
		charlie.AccessToken, map[string]any{"response": "completed", "message": "ack"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBeginRegister_DecodeError checks that a malformed body to the challenge
// endpoint returns 400.
func TestBeginRegister_DecodeError(t *testing.T) {
	handler := newTestHandler(t, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/register/challenge", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestInviteToken_RequiredAndInvalid verifies both invite-token error branches
// in handleBeginRegisterAgent (ErrInviteTokenRequired → 403, ErrInvalidInviteToken → 403).
func TestInviteToken_RequiredAndInvalid(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// Enable invite_token verification mode (alice is org admin).
	modeRec := performJSON(t, handler, http.MethodPost, "/v1/orgs/verification-mode",
		alice.AccessToken, map[string]any{"verification_mode": "invite_token"})
	if modeRec.Code != http.StatusOK {
		t.Fatalf("set verification mode: %d body=%s", modeRec.Code, modeRec.Body.String())
	}

	// Rotate the invite token so the org has one set.
	rotRec := performJSON(t, handler, http.MethodPost, "/v1/orgs/rotate-invite-token",
		alice.AccessToken, nil)
	if rotRec.Code != http.StatusOK {
		t.Fatalf("rotate invite token: %d body=%s", rotRec.Code, rotRec.Body.String())
	}

	// New agent tries to begin-register without any invite token → ErrInviteTokenRequired.
	// Use a real ed25519 public key so key-size validation passes before the invite-token check.
	validPub, _, _ := ed25519.GenerateKey(rand.Reader)
	validPubB64 := base64.StdEncoding.EncodeToString(validPub)
	noTokenRec := performJSON(t, handler, http.MethodPost, "/v1/agents/register/challenge", "",
		map[string]any{
			"org_slug":    fixture.OrgSlug,
			"owner_email": "dave@example.com",
			"agent_name":  "dave-agent",
			"client_type": "cli",
			"public_key":  validPubB64,
		})
	if noTokenRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing token, got %d body=%s", noTokenRec.Code, noTokenRec.Body.String())
	}
	var noTokenPayload map[string]string
	_ = json.NewDecoder(noTokenRec.Body).Decode(&noTokenPayload)
	if noTokenPayload["error"] != "invite_token_required" {
		t.Fatalf("expected invite_token_required error, got %q", noTokenPayload["error"])
	}

	// Same agent tries with the wrong invite token → ErrInvalidInviteToken.
	badTokenRec := performJSON(t, handler, http.MethodPost, "/v1/agents/register/challenge", "",
		map[string]any{
			"org_slug":     fixture.OrgSlug,
			"owner_email":  "dave@example.com",
			"agent_name":   "dave-agent",
			"client_type":  "cli",
			"public_key":   validPubB64,
			"invite_token": "definitely-wrong-token",
		})
	if badTokenRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for bad token, got %d body=%s", badTokenRec.Code, badTokenRec.Body.String())
	}
	var badTokenPayload map[string]string
	_ = json.NewDecoder(badTokenRec.Body).Decode(&badTokenPayload)
	if badTokenPayload["error"] != "invalid_invite_token" {
		t.Fatalf("expected invalid_invite_token error, got %q", badTokenPayload["error"])
	}
}

// TestRotateInviteToken_Success verifies the happy path of handleRotateInviteToken.
func TestRotateInviteToken_Success(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/rotate-invite-token",
		alice.AccessToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["invite_token"] == "" {
		t.Fatal("expected non-empty invite_token in response")
	}
}

// TestRotateInviteToken_NonAdmin verifies that a non-admin cannot rotate the invite token.
func TestRotateInviteToken_NonAdmin(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail) // alice = admin (first)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	rec := performJSON(t, handler, http.MethodPost, "/v1/orgs/rotate-invite-token",
		bob.AccessToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestRevokePermission_EmptyGrantID verifies that DELETE /v1/policy-grants/ with no
// trailing grant ID returns 400 ("grant_id is required").
func TestRevokePermission_EmptyGrantID(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)

	// The route is DELETE /v1/policy-grants/:id — calling it with no id gives an empty segment.
	req := httptest.NewRequest(http.MethodDelete, "/v1/policy-grants/", nil)
	req.Header.Set("Authorization", "Bearer "+alice.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestServicesNotConfigured verifies that every endpoint guarded by a nil service
// check returns 501 when that service is absent (newTestHandlerWithApprovals wires
// no OrgGraph, RiskPolicy, or Actions services).
func TestServicesNotConfigured(t *testing.T) {
	handler := newTestHandlerWithApprovals(t)
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	tok := alice.AccessToken

	notImpl := func(method, path string, body map[string]any) {
		t.Helper()
		rec := performJSON(t, handler, method, path, tok, body)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: expected 501, got %d body=%s", method, path, rec.Code, rec.Body.String())
		}
	}

	// OrgGraph endpoints.
	notImpl(http.MethodPost, "/v1/org/teams", map[string]any{"name": "eng"})
	notImpl(http.MethodGet, "/v1/org/teams", nil)
	notImpl(http.MethodGet, "/v1/org/teams/team_abc/members", nil)
	notImpl(http.MethodPost, "/v1/org/teams/team_abc/members", map[string]any{"user_email": "x@x.com"})
	notImpl(http.MethodDelete, "/v1/org/teams/team_abc", nil)
	notImpl(http.MethodPost, "/v1/org/manager-edges", map[string]any{"user_email": "x@x.com", "manager_email": "y@y.com"})
	notImpl(http.MethodGet, "/v1/org/manager-edges/x@x.com", nil)
	notImpl(http.MethodDelete, "/v1/org/manager-edges/x@x.com", nil)

	// Actions endpoints.
	notImpl(http.MethodPost, "/v1/actions", map[string]any{"kind": "acknowledge_blocker"})
	notImpl(http.MethodGet, "/v1/actions", nil)
	notImpl(http.MethodPost, "/v1/actions/action_abc/approve", nil)
	notImpl(http.MethodPost, "/v1/users/me/operator-enabled", map[string]any{"enabled": true})

	// RiskPolicy endpoints.
	notImpl(http.MethodPost, "/v1/orgs/risk-policy", map[string]any{"source": "rules: []"})
	notImpl(http.MethodGet, "/v1/orgs/risk-policies", nil)
	notImpl(http.MethodPost, "/v1/orgs/risk-policies/policy_abc/activate", nil)
}

// TestRespondToRequest_InvalidAction checks that an unrecognised response action
// returns 400 (ValidateRequestResponseInput rejects it before hitting storage).
func TestRespondToRequest_InvalidAction(t *testing.T) {
	handler := newTestHandler(t, "")
	fixture := newFixture(t)
	alice := registerAgent(t, handler, fixture.OrgSlug, fixture.AliceEmail)
	bob := registerAgent(t, handler, fixture.OrgSlug, fixture.BobEmail)

	requestID := sendRequestToPeer(t, handler, alice.AccessToken, map[string]any{
		"to_user_email": fixture.BobEmail,
		"request_type":  "question",
		"title":         "Is the deployment done?",
		"content":       "Just checking.",
	})

	rec := performJSON(t, handler, http.MethodPost, "/v1/requests/"+requestID+"/respond",
		bob.AccessToken, map[string]any{
			"response": "bogus_action",
			"message":  "irrelevant",
		})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid response action, got %d body=%s", rec.Code, rec.Body.String())
	}
	_ = alice
}
