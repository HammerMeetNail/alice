package webui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"alice/internal/audit"
	"alice/internal/core"
	"alice/internal/websession"
)

// stubServices implements webui.Services for tests. Each method records the
// last call so tests can assert the admin UI called into the service layer
// the same way the CLI/MCP path would.
type stubServices struct {
	mu                 sync.Mutex
	pendingList        []core.AgentApproval
	pendingErr         error
	lastReviewOrg      string
	lastReviewTarget   string
	lastReviewCaller   string
	lastReviewDecision string
	lastReviewReason   string
	reviewErr          error
	rotateOrg          string
	rotateCaller       string
	rotateToken        string
	rotateErr          error
	auditAgent         string
	auditSince         time.Time
	auditFilter        audit.SummaryFilter
	auditResult        []core.AuditEvent
	auditErr           error
	recordedAudits     []recordedAudit
}

type recordedAudit struct {
	EventKind    string
	SubjectType  string
	SubjectID    string
	OrgID        string
	ActorAgentID string
	Decision     string
}

func (s *stubServices) ListPendingAgentApprovals(_ context.Context, _, _ string, _, _ int) ([]core.AgentApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingList, s.pendingErr
}

func (s *stubServices) ReviewAgentApproval(_ context.Context, orgID, targetAgentID, callerAgentID, decision, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReviewOrg = orgID
	s.lastReviewTarget = targetAgentID
	s.lastReviewCaller = callerAgentID
	s.lastReviewDecision = decision
	s.lastReviewReason = reason
	return s.reviewErr
}

func (s *stubServices) RotateInviteToken(_ context.Context, orgID, callerAgentID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotateOrg = orgID
	s.rotateCaller = callerAgentID
	return s.rotateToken, s.rotateErr
}

func (s *stubServices) AuditSummary(_ context.Context, agentID string, since time.Time, _, _ int, filter audit.SummaryFilter) ([]core.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditAgent = agentID
	s.auditSince = since
	s.auditFilter = filter
	return s.auditResult, s.auditErr
}

func (s *stubServices) RecordAudit(_ context.Context, eventKind, subjectType, subjectID, orgID, actorAgentID, _, decision string, _ core.RiskLevel, _ []string, _ map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordedAudits = append(s.recordedAudits, recordedAudit{
		EventKind:    eventKind,
		SubjectType:  subjectType,
		SubjectID:    subjectID,
		OrgID:        orgID,
		ActorAgentID: actorAgentID,
		Decision:     decision,
	})
	return nil
}

type stubLookupAdmin struct {
	target websession.AdminTarget
	err    error
}

func (s *stubLookupAdmin) LookupAdmin(_ context.Context, _, _ string) (websession.AdminTarget, error) {
	if s.err != nil {
		return websession.AdminTarget{}, s.err
	}
	return s.target, nil
}

type capturingMailer struct {
	mu   sync.Mutex
	to   string
	body string
}

func (m *capturingMailer) Send(_ context.Context, to, _ string, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.to = to
	m.body = body
	return nil
}

func (m *capturingMailer) otp(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	const prefix = "Your Alice admin sign-in code is: "
	for _, line := range strings.Split(m.body, "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no OTP line in mail body: %q", m.body)
	return ""
}

// fixture builds a ready-to-use webui handler plus the stubs the tests poke.
type fixture struct {
	handler  *Handler
	sessions *websession.Service
	services *stubServices
	lookup   *stubLookupAdmin
	mailer   *capturingMailer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	lookup := &stubLookupAdmin{target: websession.AdminTarget{
		UserID:  "u-admin",
		OrgID:   "o-acme",
		AgentID: "a-admin-agent",
		Email:   "admin@example.com",
	}}
	mailer := &capturingMailer{}
	sessions := websession.NewService(websession.Options{
		Lookup:        lookup,
		Mailer:        mailer,
		SessionTTL:    time.Hour,
		SignInTTL:     10 * time.Minute,
		MaxAttempts:   5,
		CookieSecure:  false, // so we can exercise the handler via httptest without TLS
		SessionCookie: "alice_admin_session",
		CSRFCookie:    "alice_admin_csrf",
		CookiePath:    "/admin",
	})
	services := &stubServices{}
	h, err := NewHandler(Options{
		Sessions: sessions,
		Services: services,
		DevMode:  true, // allow plaintext HTTP in tests
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &fixture{handler: h, sessions: sessions, services: services, lookup: lookup, mailer: mailer}
}

// signIn exercises the happy-path sign-in flow and returns the cookies the
// test can replay on subsequent requests.
func (f *fixture) signIn(t *testing.T) (session, csrf *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	form := url.Values{"org_slug": {"acme"}, "email": {"admin@example.com"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/sign-in", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in start status = %d body=%s", rec.Code, rec.Body.String())
	}

	code := f.mailer.otp(t)

	// Extract sign-in ID from the verify page (hidden input).
	signInID := extractHiddenInput(t, rec.Body.String(), "sign_in_id")

	rec2 := httptest.NewRecorder()
	verifyForm := url.Values{"sign_in_id": {signInID}, "code": {code}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/sign-in/verify", strings.NewReader(verifyForm.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	f.handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("verify status = %d body=%s", rec2.Code, rec2.Body.String())
	}

	for _, c := range rec2.Result().Cookies() {
		switch c.Name {
		case "alice_admin_session":
			session = c
		case "alice_admin_csrf":
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatalf("expected session + csrf cookies, got %+v", rec2.Result().Cookies())
	}
	return session, csrf
}

func extractHiddenInput(t *testing.T, body, name string) string {
	t.Helper()
	needle := `name="` + name + `" value="`
	_, rest, ok := strings.Cut(body, needle)
	if !ok {
		t.Fatalf("hidden input %q not found in body: %s", name, body)
	}
	value, _, ok := strings.Cut(rest, `"`)
	if !ok {
		t.Fatalf("hidden input %q malformed in body", name)
	}
	return value
}

func TestSignInCookieAttributes(t *testing.T) {
	f := newFixture(t)
	// Re-build with CookieSecure=true so we can assert Secure + HttpOnly.
	f.sessions = websession.NewService(websession.Options{
		Lookup:        f.lookup,
		Mailer:        f.mailer,
		SessionTTL:    time.Hour,
		SignInTTL:     10 * time.Minute,
		MaxAttempts:   5,
		CookieSecure:  true,
		SessionCookie: "alice_admin_session",
		CSRFCookie:    "alice_admin_csrf",
		CookiePath:    "/admin",
	})
	h, err := NewHandler(Options{Sessions: f.sessions, Services: f.services, DevMode: true})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	f.handler = h

	session, csrf := f.signIn(t)
	if !session.HttpOnly {
		t.Fatalf("session cookie must be HttpOnly: %+v", session)
	}
	if !session.Secure {
		t.Fatalf("session cookie must be Secure when CookieSecure=true: %+v", session)
	}
	if session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie SameSite = %v", session.SameSite)
	}
	if session.Path != "/admin" {
		t.Fatalf("session cookie path = %q", session.Path)
	}
	if csrf.HttpOnly {
		t.Fatalf("csrf cookie must not be HttpOnly")
	}
	if !csrf.Secure {
		t.Fatalf("csrf cookie must be Secure")
	}
	if csrf.SameSite != http.SameSiteStrictMode {
		t.Fatalf("csrf cookie SameSite = %v", csrf.SameSite)
	}
}

func TestSignInVerifyDenotesWrongCode(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	form := url.Values{"org_slug": {"acme"}, "email": {"admin@example.com"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/sign-in", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	f.handler.ServeHTTP(rec, req)
	signInID := extractHiddenInput(t, rec.Body.String(), "sign_in_id")

	rec2 := httptest.NewRecorder()
	verifyForm := url.Values{"sign_in_id": {signInID}, "code": {"000000"}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/sign-in/verify", strings.NewReader(verifyForm.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	f.handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("wrong code status = %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "That code is not valid.") {
		t.Fatalf("wrong-code message missing; body=%s", rec2.Body.String())
	}
}

func TestDashboardRequiresSession(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated dashboard status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/sign-in" {
		t.Fatalf("redirect = %q", loc)
	}
}

func TestDashboardShowsEmail(t *testing.T) {
	f := newFixture(t)
	session, _ := f.signIn(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin@example.com") {
		t.Fatalf("dashboard missing email; body=%s", rec.Body.String())
	}
}

func TestCSPHeaderPresentOnHTML(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatalf("CSP header missing")
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("CSP missing strict script-src: %q", csp)
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("CSP must not allow unsafe-inline/unsafe-eval: %q", csp)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("X-Frame-Options = %q", rec.Header().Get("X-Frame-Options"))
	}
	// No inline <script> in shipped HTML.
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("shipped HTML contains inline script tag: %s", rec.Body.String())
	}
}

func TestCSRFMissingReturns403(t *testing.T) {
	f := newFixture(t)
	session, _ := f.signIn(t)

	form := url.Values{"agent_id": {"a-target"}, "decision": {"approved"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/pending-agents/review", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCSRFMismatchReturns403(t *testing.T) {
	f := newFixture(t)
	session, _ := f.signIn(t)

	form := url.Values{
		"agent_id":   {"a-target"},
		"decision":   {"approved"},
		"csrf_token": {"obviously-not-the-real-token"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/pending-agents/review", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatched CSRF status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReviewAgentCallsServiceWithSessionIdentity(t *testing.T) {
	f := newFixture(t)
	session, csrf := f.signIn(t)

	form := url.Values{
		"agent_id":   {"a-pending"},
		"decision":   {"approved"},
		"reason":     {"looks legit"},
		"csrf_token": {csrf.Value},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/pending-agents/review", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("review status = %d body=%s", rec.Code, rec.Body.String())
	}
	if f.services.lastReviewOrg != "o-acme" || f.services.lastReviewTarget != "a-pending" || f.services.lastReviewCaller != "a-admin-agent" {
		t.Fatalf("service not called with session identity: %+v", f.services)
	}
	if f.services.lastReviewDecision != "approved" || f.services.lastReviewReason != "looks legit" {
		t.Fatalf("decision/reason mismatch: %+v", f.services)
	}
}

func TestRotateInviteTokenRendersTokenOnce(t *testing.T) {
	f := newFixture(t)
	f.services.rotateToken = "rotated-abc123"
	session, csrf := f.signIn(t)

	form := url.Values{"csrf_token": {csrf.Value}}
	req := httptest.NewRequest(http.MethodPost, "/admin/invite-token/rotate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "rotated-abc123") {
		t.Fatalf("token not surfaced in body: %s", rec.Body.String())
	}
	if f.services.rotateOrg != "o-acme" || f.services.rotateCaller != "a-admin-agent" {
		t.Fatalf("rotate service not called with session identity: %+v", f.services)
	}
}

func TestAuditFilterPassedThrough(t *testing.T) {
	f := newFixture(t)
	f.services.auditResult = []core.AuditEvent{{AuditEventID: "e1", EventKind: "policy.applied", Decision: "allow"}}
	session, _ := f.signIn(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/audit?event_kind=policy.applied&decision=allow&since=2026-04-01T00:00:00Z", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status = %d body=%s", rec.Code, rec.Body.String())
	}
	if f.services.auditAgent != "a-admin-agent" {
		t.Fatalf("audit agent = %q", f.services.auditAgent)
	}
	if f.services.auditFilter.EventKind != "policy.applied" || f.services.auditFilter.Decision != "allow" {
		t.Fatalf("audit filter = %+v", f.services.auditFilter)
	}
	if f.services.auditSince.IsZero() {
		t.Fatalf("audit since not parsed")
	}
}

func TestCORSDisallowedPreflightReturns403(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodOptions, "/admin/sign-in", nil)
	req.Header.Set("Origin", "https://attacker.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disallowed preflight status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCORSAllowListedPreflightSucceeds(t *testing.T) {
	lookup := &stubLookupAdmin{target: websession.AdminTarget{UserID: "u", OrgID: "o", AgentID: "a", Email: "e@x"}}
	mailer := &capturingMailer{}
	sessions := websession.NewService(websession.Options{Lookup: lookup, Mailer: mailer, CookieSecure: false})
	h, err := NewHandler(Options{
		Sessions:       sessions,
		Services:       &stubServices{},
		AllowedOrigins: []string{"https://admin.example.com"},
		DevMode:        true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodOptions, "/admin/sign-in", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.com" {
		t.Fatalf("allow-origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow-credentials = %q", got)
	}
}

func TestCORSEmptyAllowListDisallowsAllPreflight(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodOptions, "/admin/sign-in", nil)
	req.Header.Set("Origin", "https://anywhere.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty-allow-list preflight = %d (expected 403)", rec.Code)
	}
}

func TestPlaintextRejectedWithoutDevMode(t *testing.T) {
	lookup := &stubLookupAdmin{}
	mailer := &capturingMailer{}
	sessions := websession.NewService(websession.Options{Lookup: lookup, Mailer: mailer, CookieSecure: true})
	h, err := NewHandler(Options{Sessions: sessions, Services: &stubServices{}, DevMode: false})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil) // no TLS
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("plaintext status = %d", rec.Code)
	}
}

func TestForwardedProtoHTTPSIsAccepted(t *testing.T) {
	lookup := &stubLookupAdmin{}
	mailer := &capturingMailer{}
	sessions := websession.NewService(websession.Options{Lookup: lookup, Mailer: mailer, CookieSecure: true})
	h, err := NewHandler(Options{Sessions: sessions, Services: &stubServices{}, DevMode: false})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("x-forwarded-proto=https status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStaticAssetsServed(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/static/css/app.css", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("static status = %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "topbar") {
		t.Fatalf("static css missing content: %q", string(body))
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatalf("CSP should be set on static responses too")
	}
}

func TestSignOutClearsCookiesAndSession(t *testing.T) {
	f := newFixture(t)
	session, csrf := f.signIn(t)

	form := url.Values{"csrf_token": {csrf.Value}}
	req := httptest.NewRequest(http.MethodPost, "/admin/sign-out", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("sign-out status = %d", rec.Code)
	}
	// Cookie reset writes an expired Set-Cookie for both names.
	clearedSession := false
	clearedCSRF := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "alice_admin_session" && c.MaxAge < 0 {
			clearedSession = true
		}
		if c.Name == "alice_admin_csrf" && c.MaxAge < 0 {
			clearedCSRF = true
		}
	}
	if !clearedSession || !clearedCSRF {
		t.Fatalf("sign-out did not clear both cookies: %+v", rec.Result().Cookies())
	}
	// Session is now invalid server-side.
	if _, err := f.sessions.Lookup(session.Value); !errors.Is(err, websession.ErrSessionNotFound) {
		t.Fatalf("session lookup after sign-out err = %v", err)
	}
}

// stubRepo implements AdminRepo for LookupAdmin tests.
type stubRepo struct {
	org     core.Organization
	orgOK   bool
	orgErr  error
	user    core.User
	userOK  bool
	userErr error
	agent   core.Agent
	agentOK bool
	agentErr error
}

func (s stubRepo) FindOrganizationBySlug(context.Context, string) (core.Organization, bool, error) {
	return s.org, s.orgOK, s.orgErr
}
func (s stubRepo) FindUserByEmail(context.Context, string, string) (core.User, bool, error) {
	return s.user, s.userOK, s.userErr
}
func (s stubRepo) FindAgentByUserID(context.Context, string) (core.Agent, bool, error) {
	return s.agent, s.agentOK, s.agentErr
}

func TestLookupAdminHappyPath(t *testing.T) {
	lookup := NewAdminLookup(stubRepo{
		org:     core.Organization{OrgID: "o1"},
		orgOK:   true,
		user:    core.User{UserID: "u1", Email: "admin@example.com", Role: core.UserRoleAdmin},
		userOK:  true,
		agent:   core.Agent{AgentID: "a1"},
		agentOK: true,
	})
	target, err := lookup.LookupAdmin(context.Background(), "acme", "admin@example.com")
	if err != nil {
		t.Fatalf("LookupAdmin: %v", err)
	}
	if target.UserID != "u1" || target.OrgID != "o1" || target.AgentID != "a1" {
		t.Fatalf("unexpected target: %+v", target)
	}
}

func TestLookupAdminRejectsNonAdmin(t *testing.T) {
	lookup := NewAdminLookup(stubRepo{
		org:    core.Organization{OrgID: "o1"},
		orgOK:  true,
		user:   core.User{UserID: "u1", Role: core.UserRoleMember},
		userOK: true,
	})
	_, err := lookup.LookupAdmin(context.Background(), "acme", "member@example.com")
	if !errors.Is(err, websession.ErrNotAdmin) {
		t.Fatalf("LookupAdmin err = %v", err)
	}
}

func TestLookupAdminRequiresAgent(t *testing.T) {
	lookup := NewAdminLookup(stubRepo{
		org:    core.Organization{OrgID: "o1"},
		orgOK:  true,
		user:   core.User{UserID: "u1", Role: core.UserRoleAdmin},
		userOK: true,
	})
	_, err := lookup.LookupAdmin(context.Background(), "acme", "admin@example.com")
	if !errors.Is(err, websession.ErrAdminNotFound) {
		t.Fatalf("admin-without-agent err = %v", err)
	}
}

func TestLookupAdminRejectsUnknownOrg(t *testing.T) {
	lookup := NewAdminLookup(stubRepo{})
	_, err := lookup.LookupAdmin(context.Background(), "nope", "who@example.com")
	if !errors.Is(err, websession.ErrAdminNotFound) {
		t.Fatalf("unknown-org err = %v", err)
	}
}

func TestLookupAdminRejectsUnknownUser(t *testing.T) {
	lookup := NewAdminLookup(stubRepo{
		org:   core.Organization{OrgID: "o1"},
		orgOK: true,
	})
	_, err := lookup.LookupAdmin(context.Background(), "acme", "unknown@example.com")
	if !errors.Is(err, websession.ErrAdminNotFound) {
		t.Fatalf("unknown-user err = %v", err)
	}
}

func TestSignInPageShownWhenNotSignedIn(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Admin sign-in") {
		t.Fatalf("sign-in body missing heading: %s", rec.Body.String())
	}
}

func TestSignInPageRedirectsWhenAlreadySignedIn(t *testing.T) {
	f := newFixture(t)
	session, _ := f.signIn(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("signed-in sign-in redirect = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/dashboard" {
		t.Fatalf("redirect = %q", loc)
	}
}

func TestStartSignInMissingFields(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/sign-in", strings.NewReader("org_slug=acme"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	f.handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "Both org slug and email are required") {
		t.Fatalf("missing-field error not shown: %s", rec.Body.String())
	}
}

func TestStartSignInSurfacesLookupError(t *testing.T) {
	f := newFixture(t)
	f.lookup.err = websession.ErrNotAdmin
	rec := httptest.NewRecorder()
	form := url.Values{"org_slug": {"acme"}, "email": {"member@example.com"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/sign-in", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	f.handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "not an org admin") {
		t.Fatalf("expected not-admin message; body=%s", rec.Body.String())
	}
}

func TestVerifyPageWithoutIDRedirects(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in/verify", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("verify-no-id status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/sign-in" {
		t.Fatalf("redirect = %q", loc)
	}
}

func TestVerifyPageWithIDShowsForm(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in/verify?id=abc", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify page status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Enter code") {
		t.Fatalf("verify page missing heading: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `value="abc"`) {
		t.Fatalf("sign_in_id not preserved: %s", rec.Body.String())
	}
}

func TestPendingAgentsListsAndRenders(t *testing.T) {
	f := newFixture(t)
	f.services.pendingList = []core.AgentApproval{
		{ApprovalID: "ap1", AgentID: "a-pending", OrgID: "o-acme", RequestedAt: time.Now().UTC()},
	}
	session, _ := f.signIn(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/pending-agents", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pending status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "a-pending") {
		t.Fatalf("pending agent ID missing from body: %s", rec.Body.String())
	}
}

func TestInviteTokenPageShowsForm(t *testing.T) {
	f := newFixture(t)
	session, _ := f.signIn(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/invite-token", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite page status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Rotate invite token") {
		t.Fatalf("invite page body missing rotate button: %s", rec.Body.String())
	}
}

func TestReviewAgentRecordsAuditForAdminUISession(t *testing.T) {
	f := newFixture(t)
	session, csrf := f.signIn(t)

	form := url.Values{
		"agent_id":   {"a-target"},
		"decision":   {"approved"},
		"csrf_token": {csrf.Value},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/pending-agents/review", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("review status = %d", rec.Code)
	}
	if len(f.services.recordedAudits) != 1 {
		t.Fatalf("expected 1 audit record, got %+v", f.services.recordedAudits)
	}
	event := f.services.recordedAudits[0]
	if event.EventKind != "admin_ui.agent_reviewed" || event.SubjectID != "a-target" || event.ActorAgentID != "a-admin-agent" {
		t.Fatalf("unexpected audit event: %+v", event)
	}
}

func TestReviewAgentBadDecisionReturns400(t *testing.T) {
	f := newFixture(t)
	session, csrf := f.signIn(t)

	form := url.Values{
		"agent_id":   {"a-target"},
		"decision":   {"nope"},
		"csrf_token": {csrf.Value},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/pending-agents/review", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad-decision status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuditInvalidSinceReturnsError(t *testing.T) {
	f := newFixture(t)
	session, _ := f.signIn(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/audit?since=not-a-date", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("invalid-since status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "must be RFC3339") {
		t.Fatalf("expected RFC3339 error; body=%s", rec.Body.String())
	}
}

func TestAdminErrMessage(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		want  string
	}{
		{"admin not found", websession.ErrAdminNotFound, "No admin found"},
		{"not admin", websession.ErrNotAdmin, "not an org admin"},
		{"invalid code", websession.ErrInvalidCode, "not valid"},
		{"sign-in expired", websession.ErrSignInExpired, "Sign-in expired"},
		{"too many attempts", websession.ErrTooManyAttempts, "Too many attempts"},
		{"unknown error", errors.New("boom"), "Request failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := adminErrMessage(tc.err)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("adminErrMessage(%v) = %q, expected substring %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestApplyCORSBehavesWithOriginHeader(t *testing.T) {
	lookup := &stubLookupAdmin{target: websession.AdminTarget{}}
	mailer := &capturingMailer{}
	sessions := websession.NewService(websession.Options{Lookup: lookup, Mailer: mailer, CookieSecure: false})
	h, err := NewHandler(Options{
		Sessions:       sessions,
		Services:       &stubServices{},
		AllowedOrigins: []string{"https://admin.example.com"},
		DevMode:        true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Allow-listed GET receives CORS headers.
	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.com" {
		t.Fatalf("allow-origin = %q", got)
	}

	// Non-allow-listed origin on a plain GET: no CORS headers, but the
	// response itself is still served (the browser will block the client).
	req2 := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	req2.Header.Set("Origin", "https://attacker.example.com")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("non-allow-listed origin should get no allow-origin header, got %q", got)
	}
}

func TestUrlEscapeEncodesSpecials(t *testing.T) {
	// Covers percentEncode indirectly via urlEscape for characters that
	// fall outside the safe set (unicode, ampersand, etc.).
	got := urlEscape("hello & world é!")
	if !strings.Contains(got, "%20") {
		t.Fatalf("missing space encoding: %q", got)
	}
	if !strings.Contains(got, "%26") {
		t.Fatalf("missing ampersand encoding: %q", got)
	}
	if !strings.Contains(got, "%C3%A9") {
		t.Fatalf("missing UTF-8 encoding of é: %q", got)
	}
}

func TestExpiredSessionIsClearedOnRequest(t *testing.T) {
	lookup := &stubLookupAdmin{target: websession.AdminTarget{UserID: "u", OrgID: "o", AgentID: "a", Email: "e@x"}}
	mailer := &capturingMailer{}
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	sessions := websession.NewService(websession.Options{
		Lookup:       lookup,
		Mailer:       mailer,
		SessionTTL:   time.Minute,
		SignInTTL:    time.Minute,
		MaxAttempts:  5,
		CookieSecure: false,
		Clock:        func() time.Time { return clock },
		CookiePath:   "/admin",
	})
	h, err := NewHandler(Options{Sessions: sessions, Services: &stubServices{}, DevMode: true})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// Sign in.
	rec := httptest.NewRecorder()
	form := url.Values{"org_slug": {"acme"}, "email": {"e@x"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/sign-in", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	signInID := extractHiddenInput(t, rec.Body.String(), "sign_in_id")

	rec2 := httptest.NewRecorder()
	verifyForm := url.Values{"sign_in_id": {signInID}, "code": {mailer.otp(t)}}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/sign-in/verify", strings.NewReader(verifyForm.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec2, req2)

	var sessionCookie *http.Cookie
	for _, c := range rec2.Result().Cookies() {
		if c.Name == "alice_admin_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatalf("no session cookie")
	}

	// Advance the clock past the session TTL.
	clock = now.Add(2 * time.Minute)

	req3 := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req3.AddCookie(sessionCookie)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusSeeOther {
		t.Fatalf("expired session redirect status = %d", rec3.Code)
	}
	// Cookie should be cleared.
	cleared := false
	for _, c := range rec3.Result().Cookies() {
		if c.Name == "alice_admin_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("expired session cookie not cleared: %+v", rec3.Result().Cookies())
	}
}

func TestRootRedirectsBasedOnSession(t *testing.T) {
	f := newFixture(t)

	// Without a session → sign-in.
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/sign-in" {
		t.Fatalf("unauthenticated root redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// With a session → dashboard.
	session, _ := f.signIn(t)
	req2 := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req2.AddCookie(session)
	rec2 := httptest.NewRecorder()
	f.handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther || rec2.Header().Get("Location") != "/admin/dashboard" {
		t.Fatalf("authenticated root redirect = %d %q", rec2.Code, rec2.Header().Get("Location"))
	}
}
