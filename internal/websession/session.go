// Package websession implements short-lived browser session management for
// the admin UI. Sessions are created by an email OTP sign-in flow: an admin
// submits an org slug plus their email, receives a 6-digit code at that
// address, and exchanges the code for a session cookie. Sessions and pending
// sign-ins live in memory — a server restart invalidates all of them, which
// is an intentional MVP trade-off.
//
// CSRF is handled with the double-submit cookie pattern: every session owns
// a CSRF token that is both set as a cookie and rendered into a hidden form
// input. Mutating handlers accept either an X-CSRF-Token header or a
// csrf_token form value and compare it against the session's token.
package websession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AdminTarget is the resolved user/agent a sign-in attempt refers to.
type AdminTarget struct {
	UserID  string
	OrgID   string
	AgentID string
	Email   string
}

// AdminLookup resolves an (org_slug, email) pair to the admin user's
// identifiers. Implementations must return ErrAdminNotFound when the pair
// cannot be matched (org unknown, user unknown, or no registered agent),
// and ErrNotAdmin when the resolved user is not an org admin.
type AdminLookup interface {
	LookupAdmin(ctx context.Context, orgSlug, email string) (AdminTarget, error)
}

// Mailer is the tiny surface websession needs from an email sender. It
// matches email.Sender so the app can plug in its existing sender directly.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

var (
	ErrAdminNotFound   = errors.New("no admin found for that org and email")
	ErrNotAdmin        = errors.New("user is not an org admin")
	ErrSignInNotFound  = errors.New("sign-in not found")
	ErrSignInExpired   = errors.New("sign-in expired")
	ErrTooManyAttempts = errors.New("too many sign-in attempts")
	ErrInvalidCode     = errors.New("invalid sign-in code")
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
	ErrCSRFMismatch    = errors.New("CSRF token mismatch")
)

// Options bundles the dependencies and knobs the session service needs.
type Options struct {
	Lookup        AdminLookup
	Mailer        Mailer
	Clock         func() time.Time
	SessionTTL    time.Duration
	SignInTTL     time.Duration
	MaxAttempts   int
	CookieSecure  bool
	SessionCookie string
	CSRFCookie    string
	CookiePath    string
}

// Session is one signed-in admin's browser session. CSRFToken is stable for
// the lifetime of the session; rotating it on privilege change is a future
// hook (admins rarely change role mid-session, so it's acceptable to leave
// it stable for now).
type Session struct {
	SessionID string
	UserID    string
	OrgID     string
	AgentID   string
	Email     string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SignIn is a pending sign-in attempt.
type SignIn struct {
	SignInID  string
	Target    AdminTarget
	CodeHash  string
	CreatedAt time.Time
	ExpiresAt time.Time
	Attempts  int
	Used      bool
}

// Service tracks active sessions and pending sign-ins in memory.
type Service struct {
	opts     Options
	mu       sync.Mutex
	sessions map[string]*Session
	signIns  map[string]*SignIn
}

func NewService(opts Options) *Service {
	if opts.Clock == nil {
		opts.Clock = func() time.Time { return time.Now().UTC() }
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 24 * time.Hour
	}
	if opts.SignInTTL <= 0 {
		opts.SignInTTL = 10 * time.Minute
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if opts.SessionCookie == "" {
		opts.SessionCookie = "alice_admin_session"
	}
	if opts.CSRFCookie == "" {
		opts.CSRFCookie = "alice_admin_csrf"
	}
	if opts.CookiePath == "" {
		opts.CookiePath = "/admin"
	}
	return &Service{
		opts:     opts,
		sessions: map[string]*Session{},
		signIns:  map[string]*SignIn{},
	}
}

// Options returns the effective options the service was built with.
func (s *Service) Options() Options { return s.opts }

// StartSignIn resolves the target admin, generates an OTP, emails it, and
// returns the sign-in ID the caller must present alongside the code.
func (s *Service) StartSignIn(ctx context.Context, orgSlug, email string) (string, error) {
	if s.opts.Lookup == nil || s.opts.Mailer == nil {
		return "", errors.New("websession service not configured for sign-in")
	}

	normalizedOrg := strings.ToLower(strings.TrimSpace(orgSlug))
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	if normalizedOrg == "" || normalizedEmail == "" {
		return "", ErrAdminNotFound
	}

	target, err := s.opts.Lookup.LookupAdmin(ctx, normalizedOrg, normalizedEmail)
	if err != nil {
		return "", err
	}

	code, err := generateOTP()
	if err != nil {
		return "", fmt.Errorf("generate OTP: %w", err)
	}
	signInID, err := randomToken(24)
	if err != nil {
		return "", fmt.Errorf("generate sign-in id: %w", err)
	}

	now := s.opts.Clock()
	signIn := &SignIn{
		SignInID:  signInID,
		Target:    target,
		CodeHash:  hashCode(code),
		CreatedAt: now,
		ExpiresAt: now.Add(s.opts.SignInTTL),
	}

	s.mu.Lock()
	s.signIns[signInID] = signIn
	s.mu.Unlock()

	subject := "Your Alice admin sign-in code"
	body := fmt.Sprintf(
		"Your Alice admin sign-in code is: %s\n\nThis code expires in %s.\n\nIf you did not request this, you can ignore this email.\n",
		code,
		s.opts.SignInTTL.String(),
	)
	if err := s.opts.Mailer.Send(ctx, target.Email, subject, body); err != nil {
		s.mu.Lock()
		delete(s.signIns, signInID)
		s.mu.Unlock()
		return "", fmt.Errorf("send sign-in email: %w", err)
	}
	return signInID, nil
}

// VerifySignIn consumes a pending sign-in, issues a session, and returns it.
// The caller is responsible for setting the session and CSRF cookies.
func (s *Service) VerifySignIn(signInID, code string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	signIn, ok := s.signIns[signInID]
	if !ok {
		return Session{}, ErrSignInNotFound
	}
	now := s.opts.Clock()
	if signIn.Used || now.After(signIn.ExpiresAt) {
		delete(s.signIns, signInID)
		return Session{}, ErrSignInExpired
	}
	if signIn.Attempts >= s.opts.MaxAttempts {
		return Session{}, ErrTooManyAttempts
	}

	submitted := hashCode(strings.TrimSpace(code))
	if subtle.ConstantTimeCompare([]byte(signIn.CodeHash), []byte(submitted)) != 1 {
		signIn.Attempts++
		return Session{}, ErrInvalidCode
	}

	signIn.Used = true
	delete(s.signIns, signInID)

	sessionID, err := randomToken(32)
	if err != nil {
		return Session{}, fmt.Errorf("generate session id: %w", err)
	}
	csrfToken, err := randomToken(24)
	if err != nil {
		return Session{}, fmt.Errorf("generate csrf token: %w", err)
	}

	session := &Session{
		SessionID: sessionID,
		UserID:    signIn.Target.UserID,
		OrgID:     signIn.Target.OrgID,
		AgentID:   signIn.Target.AgentID,
		Email:     signIn.Target.Email,
		CSRFToken: csrfToken,
		CreatedAt: now,
		ExpiresAt: now.Add(s.opts.SessionTTL),
	}
	s.sessions[sessionID] = session
	return *session, nil
}

// Lookup returns the session for a cookie value, or an error when the session
// is unknown or expired. Expired sessions are swept as a side effect.
func (s *Service) Lookup(sessionID string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	now := s.opts.Clock()
	if now.After(session.ExpiresAt) {
		delete(s.sessions, sessionID)
		return Session{}, ErrSessionExpired
	}
	return *session, nil
}

// SignOut deletes the session with the given ID. Unknown IDs are silently
// ignored so handlers can call this during cookie cleanup without branching.
func (s *Service) SignOut(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// Sweep removes expired sessions and sign-ins. Callers may invoke this
// periodically from a background goroutine.
func (s *Service) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Clock()
	for id, session := range s.sessions {
		if now.After(session.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
	for id, signIn := range s.signIns {
		if signIn.Used || now.After(signIn.ExpiresAt) {
			delete(s.signIns, id)
		}
	}
}

// ValidateCSRF compares submitted against the session's stored CSRF token
// using constant-time comparison. The session is refreshed from storage so a
// deleted/expired session fails closed.
func (s *Service) ValidateCSRF(sessionID, submitted string) error {
	if strings.TrimSpace(submitted) == "" {
		return ErrCSRFMismatch
	}
	session, err := s.Lookup(sessionID)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(submitted)) != 1 {
		return ErrCSRFMismatch
	}
	return nil
}

// SessionCookie returns the signed-in session cookie. Attributes follow the
// plan: HttpOnly + SameSite=Strict and Secure when the service is configured
// for HTTPS. Path is scoped to the admin mount so it doesn't leak to the API.
func (s *Service) SessionCookie(sessionID string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     s.opts.SessionCookie,
		Value:    sessionID,
		Path:     s.opts.CookiePath,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

// CSRFCookie mirrors the CSRF token into a cookie. It is intentionally not
// HttpOnly so client-side code could read it for AJAX submissions — form
// handlers rely on a hidden input, so the cookie is belt-and-braces.
func (s *Service) CSRFCookie(token string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     s.opts.CSRFCookie,
		Value:    token,
		Path:     s.opts.CookiePath,
		Expires:  expiresAt,
		HttpOnly: false,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

// ClearCookie returns an immediate-expiry cookie for name. Secure + SameSite
// must match the original cookie so browsers actually overwrite it.
func (s *Service) ClearCookie(name string) *http.Cookie {
	httpOnly := name == s.opts.SessionCookie
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     s.opts.CookiePath,
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
