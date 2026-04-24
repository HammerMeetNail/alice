// Package webui serves the browser-facing admin surface. It is a
// feature-flagged sibling of the JSON API: the handler mounts under /admin/
// and is only registered when ALICE_ADMIN_UI_ENABLED=true.
//
// Design goals (see docs/plans/admin-ui.md in history):
//   - no new trust boundary: all state changes go through the same services
//     the JSON API uses, with identical permission checks and audit events
//   - short-lived cookie sessions issued by email OTP sign-in (no passwords)
//   - strict Content-Security-Policy, no inline scripts, no CDN for core assets
//   - CSRF via the double-submit cookie pattern (hidden form input + cookie)
//   - CORS allow-list is explicit; empty list disables CORS (same-origin only)
//   - off by default; /admin/* returns 404 when the feature flag is false.
package webui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"alice/internal/agents"
	"alice/internal/audit"
	"alice/internal/core"
	"alice/internal/websession"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Services is the narrow surface webui needs from the main service container.
// It deliberately duplicates only the admin-relevant methods so this package
// doesn't transitively import every service.
//
// RecordAudit mirrors audit.Service.Record — the admin UI emits the same
// audit events the JSON API handlers do, so downstream log processors can
// treat browser and CLI activity identically. Only the error is returned;
// the stored event is not consumed.
type Services interface {
	ListPendingAgentApprovals(ctx context.Context, orgID, callerAgentID string, limit, offset int) ([]core.AgentApproval, error)
	ReviewAgentApproval(ctx context.Context, orgID, targetAgentID, callerAgentID, decision, reason string) error
	RotateInviteToken(ctx context.Context, orgID, callerAgentID string) (string, error)
	AuditSummary(ctx context.Context, agentID string, since time.Time, limit, offset int, filter audit.SummaryFilter) ([]core.AuditEvent, error)
	RecordAudit(ctx context.Context, eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) error
}

// Options bundles everything NewHandler needs. Required fields: Sessions,
// Services, Audit. Optional: AllowedOrigins, DevMode, Logger,
// SignInRatePerMin, TrustedProxies.
type Options struct {
	Sessions       *websession.Service
	Services       Services
	AllowedOrigins []string
	DevMode        bool
	Logger         *slog.Logger
	// SignInRatePerMin is the maximum number of POST /admin/sign-in and
	// POST /admin/sign-in/verify requests allowed per IP per minute.
	// Zero disables the per-IP rate limit (useful in tests).
	SignInRatePerMin float64
	// TrustedProxies is the list of CIDRs from which X-Forwarded-For is
	// trusted for IP-based rate limiting. Mirrors the JSON API setting.
	TrustedProxies []*net.IPNet
}

// Handler serves the admin UI under /admin/*.
type Handler struct {
	opts           Options
	mux            *http.ServeMux
	templates      *template.Template
	staticHandler  http.Handler
	allowedOrigins map[string]struct{}
	logger         *slog.Logger
	signinLimiter  *signinRateLimiter // nil when rate limiting is disabled
}

// NewHandler builds the admin-UI HTTP handler. It returns an error only when
// the embedded templates fail to parse — which is a build-time problem, not
// a runtime one, but we propagate it so the server fails fast.
func NewHandler(opts Options) (*Handler, error) {
	if opts.Sessions == nil {
		return nil, errors.New("webui: Sessions is required")
	}
	if opts.Services == nil {
		return nil, errors.New("webui: Services is required")
	}
	tpls, err := template.New("").Funcs(template.FuncMap{
		"fmtTime": func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("sub static fs: %w", err)
	}

	allowed := map[string]struct{}{}
	for _, origin := range opts.AllowedOrigins {
		trimmed := strings.TrimSpace(origin)
		if trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	h := &Handler{
		opts:           opts,
		mux:            http.NewServeMux(),
		templates:      tpls,
		staticHandler:  http.StripPrefix("/admin/static/", http.FileServerFS(sub)),
		allowedOrigins: allowed,
		logger:         logger,
	}
	if opts.SignInRatePerMin > 0 {
		h.signinLimiter = newSigninRateLimiter(opts.SignInRatePerMin, opts.SignInRatePerMin)
	}
	h.registerRoutes()
	return h, nil
}

// ServeHTTP is the public entry point. It layers CORS → HTTPS enforcement →
// security headers → session resolution around the inner mux.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		h.handlePreflight(w, r)
		return
	}
	if !h.enforceHTTPS(w, r) {
		return
	}
	h.applyCORS(w, r)
	h.applySecurityHeaders(w)
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	h.mux.HandleFunc("GET /admin/static/", h.staticHandler.ServeHTTP)

	h.mux.HandleFunc("GET /admin/", h.handleRoot)
	h.mux.HandleFunc("GET /admin/sign-in", h.handleSignInPage)
	h.mux.Handle("POST /admin/sign-in", h.signInRateLimit(http.HandlerFunc(h.handleStartSignIn)))
	h.mux.HandleFunc("GET /admin/sign-in/verify", h.handleVerifyPage)
	h.mux.Handle("POST /admin/sign-in/verify", h.signInRateLimit(http.HandlerFunc(h.handleCompleteSignIn)))
	h.mux.Handle("POST /admin/sign-out", h.requireSession(h.requireCSRF(http.HandlerFunc(h.handleSignOut))))
	h.mux.Handle("GET /admin/dashboard", h.requireSession(http.HandlerFunc(h.handleDashboard)))
	h.mux.Handle("GET /admin/pending-agents", h.requireSession(http.HandlerFunc(h.handlePendingAgents)))
	h.mux.Handle("POST /admin/pending-agents/review", h.requireSession(h.requireCSRF(http.HandlerFunc(h.handleReviewAgent))))
	h.mux.Handle("GET /admin/invite-token", h.requireSession(http.HandlerFunc(h.handleInviteTokenPage)))
	h.mux.Handle("POST /admin/invite-token/rotate", h.requireSession(h.requireCSRF(http.HandlerFunc(h.handleRotateInvite))))
	h.mux.Handle("GET /admin/audit", h.requireSession(http.HandlerFunc(h.handleAudit)))
}

// signInRateLimit wraps a handler with a per-IP rate limiter on the admin
// sign-in endpoints. When no limiter is configured (SignInRatePerMin == 0)
// the handler is returned as-is.
func (h *Handler) signInRateLimit(next http.Handler) http.Handler {
	if h.signinLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := adminClientIP(r, h.opts.TrustedProxies)
		if !h.signinLimiter.allow(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// applySecurityHeaders is called for every admin response. HTML pages get
// the same strict CSP; static assets get the same headers plus their own
// Content-Type from the file server.
func (h *Handler) applySecurityHeaders(w http.ResponseWriter) {
	h.setCSP(w)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if _, ok := w.Header()["Cache-Control"]; !ok {
		w.Header().Set("Cache-Control", "no-store")
	}
}

func (h *Handler) setCSP(w http.ResponseWriter) {
	// Strict: first-party only, no inline scripts, no eval, no remote CDN.
	// form-action 'self' keeps POSTs bound to the same origin.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self'; "+
			"style-src 'self'; "+
			"img-src 'self' data:; "+
			"base-uri 'self'; "+
			"form-action 'self'; "+
			"frame-ancestors 'none'")
}

// enforceHTTPS rejects plaintext requests outside dev mode. Requests that
// arrive via an HTTPS-terminating proxy are accepted only when the request
// originates from a configured trusted proxy CIDR and carries an
// X-Forwarded-Proto: https header. When no TrustedProxies are configured the
// header is never trusted — callers must either use direct TLS or configure
// the allowed proxy range. Returns true when the request is allowed to proceed.
func (h *Handler) enforceHTTPS(w http.ResponseWriter, r *http.Request) bool {
	if h.opts.DevMode {
		return true
	}
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") && h.isTrustedProxy(r) {
		return true
	}
	h.applySecurityHeaders(w)
	http.Error(w, "HTTPS required for admin UI", http.StatusBadRequest)
	return false
}

// isTrustedProxy reports whether the request's remote IP falls within one of
// the configured TrustedProxies CIDRs. Returns false when no proxies are
// configured, ensuring X-Forwarded-Proto is never trusted by default.
func (h *Handler) isTrustedProxy(r *http.Request) bool {
	if len(h.opts.TrustedProxies) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range h.opts.TrustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// applyCORS writes Access-Control-Allow-* headers when the request's Origin
// header matches the allow-list. No headers are written when the list is
// empty, which keeps admin responses same-origin-only.
func (h *Handler) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || len(h.allowedOrigins) == 0 {
		return
	}
	if _, ok := h.allowedOrigins[origin]; !ok {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Vary", "Origin")
}

// handlePreflight answers OPTIONS requests. Disallowed origins receive 403
// so callers can see the allow-list is enforced instead of getting a
// silently-useless response.
func (h *Handler) handlePreflight(w http.ResponseWriter, r *http.Request) {
	h.applySecurityHeaders(w)
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || len(h.allowedOrigins) == 0 {
		http.Error(w, "CORS preflight denied", http.StatusForbidden)
		return
	}
	if _, ok := h.allowedOrigins[origin]; !ok {
		http.Error(w, "CORS preflight denied", http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
	w.Header().Set("Vary", "Origin")
	w.WriteHeader(http.StatusNoContent)
}

type sessionContextKey struct{}

// requireSession wraps handlers that need a valid session. Unauthenticated
// requests are redirected to the sign-in page for GETs, and rejected with
// 401 for other methods (so AJAX callers see a proper error).
func (h *Handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(h.opts.Sessions.Options().SessionCookie)
		if err != nil {
			h.unauthenticated(w, r)
			return
		}
		session, err := h.opts.Sessions.Lookup(cookie.Value)
		if err != nil {
			http.SetCookie(w, h.opts.Sessions.ClearCookie(h.opts.Sessions.Options().SessionCookie))
			http.SetCookie(w, h.opts.Sessions.ClearCookie(h.opts.Sessions.Options().CSRFCookie))
			h.unauthenticated(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), sessionContextKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireCSRF enforces the double-submit pattern on mutating handlers. The
// submitted token is taken from the X-CSRF-Token header, or (falling back)
// from the csrf_token form field; both must match the session's token.
func (h *Handler) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromCtx(r.Context())
		if !ok {
			http.Error(w, "missing session", http.StatusUnauthorized)
			return
		}
		submitted := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		if submitted == "" {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			submitted = strings.TrimSpace(r.Form.Get("csrf_token"))
		}
		if err := h.opts.Sessions.ValidateCSRF(session.SessionID, submitted); err != nil {
			http.Error(w, "CSRF token missing or invalid", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sessionFromCtx(ctx context.Context) (websession.Session, bool) {
	s, ok := ctx.Value(sessionContextKey{}).(websession.Session)
	return s, ok
}

// mustSession is the short form used inside handlers guarded by
// requireSession. A missing session is a programmer error (a handler was
// registered without the middleware) and panics rather than 500-ing, so
// the mismatch shows up loud in tests instead of silently rejecting the
// user.
func mustSession(ctx context.Context) websession.Session {
	s, ok := sessionFromCtx(ctx)
	if !ok {
		panic("webui: handler requires session; wrap it with requireSession")
	}
	return s
}

func (h *Handler) unauthenticated(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/admin/sign-in", http.StatusSeeOther)
		return
	}
	http.Error(w, "unauthenticated", http.StatusUnauthorized)
}

// handleRoot sends signed-in admins to the dashboard and everyone else to
// the sign-in page.
func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" && r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	cookie, err := r.Cookie(h.opts.Sessions.Options().SessionCookie)
	if err != nil {
		http.Redirect(w, r, "/admin/sign-in", http.StatusSeeOther)
		return
	}
	if _, err := h.opts.Sessions.Lookup(cookie.Value); err != nil {
		http.Redirect(w, r, "/admin/sign-in", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// renderTemplate is a small helper; it writes a 200 response with the
// security headers already applied.
func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		h.logger.Error("render template failed", "template", name, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// adminErrMessage translates known service errors into human-readable
// messages. Unknown errors get a generic fallback so we don't leak details.
func adminErrMessage(err error) string {
	switch {
	case errors.Is(err, websession.ErrAdminNotFound):
		return "No admin found for that org and email. Register an agent with `alice register` first."
	case errors.Is(err, websession.ErrNotAdmin):
		return "That account is not an org admin."
	case errors.Is(err, websession.ErrInvalidCode):
		return "That code is not valid."
	case errors.Is(err, websession.ErrSignInExpired), errors.Is(err, websession.ErrSignInNotFound):
		return "Sign-in expired. Start again."
	case errors.Is(err, websession.ErrTooManyAttempts):
		return "Too many attempts. Start sign-in again."
	case errors.Is(err, agents.ErrNotOrgAdmin):
		return "Your account is not an org admin."
	case errors.Is(err, agents.ErrUnknownAgent), errors.Is(err, agents.ErrAgentApprovalNotFound):
		return "Agent not found."
	case errors.Is(err, agents.ErrUnknownAgentOwner):
		return "Agent owner not found."
	}
	return "Request failed."
}
