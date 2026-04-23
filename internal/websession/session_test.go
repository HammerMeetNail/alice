package websession

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubLookup struct {
	mu      sync.Mutex
	targets map[string]AdminTarget
	err     error
}

func (s *stubLookup) LookupAdmin(_ context.Context, orgSlug, email string) (AdminTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return AdminTarget{}, s.err
	}
	key := orgSlug + "|" + email
	target, ok := s.targets[key]
	if !ok {
		return AdminTarget{}, ErrAdminNotFound
	}
	return target, nil
}

type captureMailer struct {
	mu   sync.Mutex
	to   string
	body string
	err  error
}

func (m *captureMailer) Send(_ context.Context, to, _ /*subject*/ string, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.to = to
	m.body = body
	return nil
}

func (m *captureMailer) code(t *testing.T) string {
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

func newService(t *testing.T, lookup AdminLookup, mailer Mailer, now func() time.Time) *Service {
	t.Helper()
	return NewService(Options{
		Lookup:       lookup,
		Mailer:       mailer,
		Clock:        now,
		SessionTTL:   time.Hour,
		SignInTTL:    time.Minute,
		MaxAttempts:  3,
		CookieSecure: true,
		CookiePath:   "/admin",
	})
}

func TestSignInAndVerifyIssuesSession(t *testing.T) {
	lookup := &stubLookup{targets: map[string]AdminTarget{
		"acme|alice@example.com": {UserID: "u1", OrgID: "o1", AgentID: "a1", Email: "alice@example.com"},
	}}
	mailer := &captureMailer{}
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	svc := newService(t, lookup, mailer, func() time.Time { return now })

	signInID, err := svc.StartSignIn(context.Background(), "ACME", "Alice@example.com")
	if err != nil {
		t.Fatalf("StartSignIn: %v", err)
	}
	if signInID == "" {
		t.Fatalf("sign-in id must be non-empty")
	}
	if mailer.to != "alice@example.com" {
		t.Fatalf("mailer to = %q", mailer.to)
	}

	code := mailer.code(t)
	session, err := svc.VerifySignIn(signInID, code)
	if err != nil {
		t.Fatalf("VerifySignIn: %v", err)
	}
	if session.UserID != "u1" || session.OrgID != "o1" || session.AgentID != "a1" {
		t.Fatalf("unexpected session target: %#v", session)
	}
	if session.CSRFToken == "" || session.SessionID == "" {
		t.Fatalf("session missing IDs: %#v", session)
	}

	fetched, err := svc.Lookup(session.SessionID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if fetched.CSRFToken != session.CSRFToken {
		t.Fatalf("lookup csrf = %q want %q", fetched.CSRFToken, session.CSRFToken)
	}
}

func TestVerifyRejectsWrongCode(t *testing.T) {
	lookup := &stubLookup{targets: map[string]AdminTarget{
		"acme|alice@example.com": {UserID: "u1", OrgID: "o1", AgentID: "a1", Email: "alice@example.com"},
	}}
	mailer := &captureMailer{}
	svc := newService(t, lookup, mailer, nil)

	signInID, err := svc.StartSignIn(context.Background(), "acme", "alice@example.com")
	if err != nil {
		t.Fatalf("StartSignIn: %v", err)
	}

	if _, err := svc.VerifySignIn(signInID, "000000"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("first wrong code err = %v", err)
	}
	if _, err := svc.VerifySignIn(signInID, "111111"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("second wrong code err = %v", err)
	}
	if _, err := svc.VerifySignIn(signInID, "222222"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("third wrong code err = %v", err)
	}
	// Fourth attempt should be locked out.
	if _, err := svc.VerifySignIn(signInID, mailer.code(t)); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("lockout err = %v", err)
	}
}

func TestExpiredSignInIsRejected(t *testing.T) {
	lookup := &stubLookup{targets: map[string]AdminTarget{
		"acme|alice@example.com": {UserID: "u1", OrgID: "o1", AgentID: "a1", Email: "alice@example.com"},
	}}
	mailer := &captureMailer{}
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	svc := newService(t, lookup, mailer, func() time.Time { return clock })

	signInID, err := svc.StartSignIn(context.Background(), "acme", "alice@example.com")
	if err != nil {
		t.Fatalf("StartSignIn: %v", err)
	}
	clock = now.Add(2 * time.Minute)

	if _, err := svc.VerifySignIn(signInID, mailer.code(t)); !errors.Is(err, ErrSignInExpired) {
		t.Fatalf("expired sign-in err = %v", err)
	}
}

func TestSignInNotAdminPropagates(t *testing.T) {
	lookup := &stubLookup{err: ErrNotAdmin}
	mailer := &captureMailer{}
	svc := newService(t, lookup, mailer, nil)

	if _, err := svc.StartSignIn(context.Background(), "acme", "alice@example.com"); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("StartSignIn err = %v", err)
	}
}

func TestSignInRequiresOrgAndEmail(t *testing.T) {
	lookup := &stubLookup{}
	mailer := &captureMailer{}
	svc := newService(t, lookup, mailer, nil)

	if _, err := svc.StartSignIn(context.Background(), "", "alice@example.com"); !errors.Is(err, ErrAdminNotFound) {
		t.Fatalf("missing org err = %v", err)
	}
	if _, err := svc.StartSignIn(context.Background(), "acme", ""); !errors.Is(err, ErrAdminNotFound) {
		t.Fatalf("missing email err = %v", err)
	}
}

func TestSessionLookupRespectsExpiry(t *testing.T) {
	lookup := &stubLookup{targets: map[string]AdminTarget{
		"acme|alice@example.com": {UserID: "u1", OrgID: "o1", AgentID: "a1", Email: "alice@example.com"},
	}}
	mailer := &captureMailer{}
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	svc := newService(t, lookup, mailer, func() time.Time { return clock })

	signInID, err := svc.StartSignIn(context.Background(), "acme", "alice@example.com")
	if err != nil {
		t.Fatalf("StartSignIn: %v", err)
	}
	session, err := svc.VerifySignIn(signInID, mailer.code(t))
	if err != nil {
		t.Fatalf("VerifySignIn: %v", err)
	}

	clock = now.Add(2 * time.Hour)
	if _, err := svc.Lookup(session.SessionID); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired lookup err = %v", err)
	}
	if _, err := svc.Lookup(session.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("sweep lookup err = %v", err)
	}
}

func TestSignOutClearsSession(t *testing.T) {
	lookup := &stubLookup{targets: map[string]AdminTarget{
		"acme|alice@example.com": {UserID: "u1", OrgID: "o1", AgentID: "a1", Email: "alice@example.com"},
	}}
	mailer := &captureMailer{}
	svc := newService(t, lookup, mailer, nil)

	signInID, err := svc.StartSignIn(context.Background(), "acme", "alice@example.com")
	if err != nil {
		t.Fatalf("StartSignIn: %v", err)
	}
	session, err := svc.VerifySignIn(signInID, mailer.code(t))
	if err != nil {
		t.Fatalf("VerifySignIn: %v", err)
	}

	svc.SignOut(session.SessionID)
	if _, err := svc.Lookup(session.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("post-signout lookup err = %v", err)
	}
}

func TestValidateCSRFMatchesAndRejects(t *testing.T) {
	lookup := &stubLookup{targets: map[string]AdminTarget{
		"acme|alice@example.com": {UserID: "u1", OrgID: "o1", AgentID: "a1", Email: "alice@example.com"},
	}}
	mailer := &captureMailer{}
	svc := newService(t, lookup, mailer, nil)

	signInID, err := svc.StartSignIn(context.Background(), "acme", "alice@example.com")
	if err != nil {
		t.Fatalf("StartSignIn: %v", err)
	}
	session, err := svc.VerifySignIn(signInID, mailer.code(t))
	if err != nil {
		t.Fatalf("VerifySignIn: %v", err)
	}

	if err := svc.ValidateCSRF(session.SessionID, session.CSRFToken); err != nil {
		t.Fatalf("matching token should pass: %v", err)
	}
	if err := svc.ValidateCSRF(session.SessionID, "wrong"); !errors.Is(err, ErrCSRFMismatch) {
		t.Fatalf("mismatched token err = %v", err)
	}
	if err := svc.ValidateCSRF(session.SessionID, ""); !errors.Is(err, ErrCSRFMismatch) {
		t.Fatalf("empty token err = %v", err)
	}
	if err := svc.ValidateCSRF("unknown-session", session.CSRFToken); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("unknown session err = %v", err)
	}
}

func TestCookieAttributes(t *testing.T) {
	svc := NewService(Options{
		CookieSecure: true,
		CookiePath:   "/admin",
	})
	sessionCookie := svc.SessionCookie("sess", time.Now().Add(time.Hour))
	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie attrs = %#v", sessionCookie)
	}
	if sessionCookie.Path != "/admin" {
		t.Fatalf("session cookie path = %q", sessionCookie.Path)
	}

	csrfCookie := svc.CSRFCookie("tok", time.Now().Add(time.Hour))
	if csrfCookie.HttpOnly {
		t.Fatalf("csrf cookie must not be HttpOnly")
	}
	if !csrfCookie.Secure || csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("csrf cookie attrs = %#v", csrfCookie)
	}

	clear := svc.ClearCookie(svc.Options().SessionCookie)
	if clear.MaxAge != -1 {
		t.Fatalf("clear cookie max-age = %d", clear.MaxAge)
	}
}

func TestSweepRemovesExpired(t *testing.T) {
	lookup := &stubLookup{targets: map[string]AdminTarget{
		"acme|alice@example.com": {UserID: "u1", OrgID: "o1", AgentID: "a1", Email: "alice@example.com"},
	}}
	mailer := &captureMailer{}
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	svc := newService(t, lookup, mailer, func() time.Time { return clock })

	signInID, err := svc.StartSignIn(context.Background(), "acme", "alice@example.com")
	if err != nil {
		t.Fatalf("StartSignIn: %v", err)
	}
	session, err := svc.VerifySignIn(signInID, mailer.code(t))
	if err != nil {
		t.Fatalf("VerifySignIn: %v", err)
	}

	clock = now.Add(2 * time.Hour)
	svc.Sweep()
	svc.mu.Lock()
	_, sessionOK := svc.sessions[session.SessionID]
	svc.mu.Unlock()
	if sessionOK {
		t.Fatalf("Sweep should delete expired session")
	}
}
