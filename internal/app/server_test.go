package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
	"alice/internal/webui"
	"alice/internal/websession"
)

// TestAdminUIDisabledReturns404 confirms that when the admin UI feature flag
// is off, /admin/* falls through to the JSON API's default 404 handler.
func TestAdminUIDisabledReturns404(t *testing.T) {
	cfg := config.Config{
		ListenAddr:       ":0",
		AuthChallengeTTL: time.Minute,
		AuthTokenTTL:     time.Minute,
		AdminUIEnabled:   false,
	}
	container, repos, _, err := newContainerWithRepos(cfg)
	if err != nil {
		t.Fatalf("newContainerWithRepos: %v", err)
	}
	handler, err := buildHTTPHandler(cfg, container, repos)
	if err != nil {
		t.Fatalf("buildHTTPHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("admin disabled status = %d body=%s", rec.Code, rec.Body.String())
	}
	req2 := httptest.NewRequest(http.MethodPost, "/admin/sign-in", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("admin disabled POST status = %d", rec2.Code)
	}
}

// TestAdminUIEnabledServesSignIn confirms that when the feature flag is on
// AND SMTP is configured (noop sender is fine), the admin UI serves its
// sign-in page under /admin/sign-in.
func TestAdminUIEnabledServesSignIn(t *testing.T) {
	cfg := config.Config{
		ListenAddr:       ":0",
		AuthChallengeTTL: time.Minute,
		AuthTokenTTL:     time.Minute,
		AdminUIEnabled:   true,
		AdminUIDevMode:   true,
		SMTPHost:         "noop",
	}
	container, repos, _, err := newContainerWithRepos(cfg)
	if err != nil {
		t.Fatalf("newContainerWithRepos: %v", err)
	}
	handler, err := buildHTTPHandler(cfg, container, repos)
	if err != nil {
		t.Fatalf("buildHTTPHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/sign-in", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin enabled status = %d body=%s", rec.Code, rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
		t.Fatalf("expected CSP header when admin UI enabled")
	}

	// JSON API still reachable alongside the admin UI.
	req2 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", rec2.Code)
	}
}

// TestAdminUIWithoutSMTPRefuses confirms the feature flag alone is not
// enough — without SMTP the server refuses to come up, because sign-in codes
// would have nowhere to go.
func TestAdminUIWithoutSMTPRefuses(t *testing.T) {
	cfg := config.Config{
		ListenAddr:       ":0",
		AuthChallengeTTL: time.Minute,
		AuthTokenTTL:     time.Minute,
		AdminUIEnabled:   true,
		// SMTPHost deliberately empty.
	}
	container, repos, _, err := newContainerWithRepos(cfg)
	if err != nil {
		t.Fatalf("newContainerWithRepos: %v", err)
	}
	if _, err := buildHTTPHandler(cfg, container, repos); err == nil {
		t.Fatalf("expected buildHTTPHandler to fail when admin UI is enabled without SMTP")
	}
}

// captureMailer saves the most-recent OTP body so tests can extract the
// 6-digit code without reaching out to SMTP.
type captureMailer struct {
	mu   sync.Mutex
	body string
}

func (m *captureMailer) Send(_ context.Context, _, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.body = body
	return nil
}

func (m *captureMailer) otp(t *testing.T) string {
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

// TestAdminUIInviteRotationMatchesCLIAudit verifies the headline parity
// guarantee: a browser-issued invite-token rotation through /admin/* lands
// the same `org.invite_token_rotated` audit event the CLI/MCP path emits.
func TestAdminUIInviteRotationMatchesCLIAudit(t *testing.T) {
	cfg := config.Config{
		AuthChallengeTTL: time.Minute,
		AuthTokenTTL:     time.Minute,
		AdminUIEnabled:   true,
		AdminUIDevMode:   true,
	}
	store := memory.New()
	container := buildContainer(store, cfg)

	// Seed an admin user + admin agent directly in the store so we can
	// focus on the browser flow without going through ed25519.
	ctx := context.Background()
	now := time.Now().UTC()

	org, err := store.UpsertOrganization(ctx, core.Organization{
		OrgID:     id.New("org"),
		Name:      "Acme",
		Slug:      "acme",
		CreatedAt: now,
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}
	adminUser, err := store.UpsertUser(ctx, core.User{
		UserID:    id.New("user"),
		OrgID:     org.OrgID,
		Email:     "admin@example.com",
		CreatedAt: now,
		Status:    "active",
		Role:      core.UserRoleAdmin,
	})
	if err != nil {
		t.Fatalf("UpsertUser admin: %v", err)
	}
	adminAgent, err := store.UpsertAgent(ctx, core.Agent{
		AgentID:     id.New("agent"),
		OrgID:       org.OrgID,
		OwnerUserID: adminUser.UserID,
		AgentName:   "admin-agent",
		RuntimeKind: "cli",
		ClientType:  "cli",
		Status:      core.AgentStatusActive,
		LastSeenAt:  now,
	})
	if err != nil {
		t.Fatalf("UpsertAgent admin: %v", err)
	}

	mailer := &captureMailer{}
	sessions := websession.NewService(websession.Options{
		Lookup:       webui.NewAdminLookup(adminRepoAdapter{repos: store}),
		Mailer:       mailer,
		SessionTTL:   time.Hour,
		SignInTTL:    10 * time.Minute,
		MaxAttempts:  5,
		CookieSecure: false,
		CookiePath:   "/admin",
	})
	handler, err := webui.NewHandler(webui.Options{
		Sessions: sessions,
		Services: adminServices{container: container},
		DevMode:  true,
	})
	if err != nil {
		t.Fatalf("webui.NewHandler: %v", err)
	}

	// Step 1: start sign-in.
	req := httptest.NewRequest(http.MethodPost, "/admin/sign-in", strings.NewReader(url.Values{
		"org_slug": {"acme"},
		"email":    {"admin@example.com"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in start status = %d body=%s", rec.Code, rec.Body.String())
	}
	signInID := extractHiddenInput(t, rec.Body.String(), "sign_in_id")

	// Step 2: submit the OTP.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/admin/sign-in/verify", strings.NewReader(url.Values{
		"sign_in_id": {signInID},
		"code":       {mailer.otp(t)},
	}.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("sign-in verify status = %d body=%s", rec2.Code, rec2.Body.String())
	}

	var sessionCookie, csrfCookie *http.Cookie
	for _, c := range rec2.Result().Cookies() {
		switch c.Name {
		case "alice_admin_session":
			sessionCookie = c
		case "alice_admin_csrf":
			csrfCookie = c
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("expected session + csrf cookies")
	}

	// Step 3: rotate the invite token through the admin UI.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/admin/invite-token/rotate", strings.NewReader(url.Values{
		"csrf_token": {csrfCookie.Value},
	}.Encode()))
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req3.AddCookie(sessionCookie)
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("rotate status = %d body=%s", rec3.Code, rec3.Body.String())
	}

	// Step 4: verify the audit event matches the CLI-generated shape.
	events, err := container.Audit.Summary(ctx, adminAgent.AgentID, time.Time{}, 50, 0, audit.SummaryFilter{
		EventKind: "org.invite_token_rotated",
	})
	if err != nil {
		t.Fatalf("Audit.Summary: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one invite-rotation audit event, got %d", len(events))
	}
	event := events[0]
	if event.ActorAgentID != adminAgent.AgentID {
		t.Fatalf("actor = %q want %q", event.ActorAgentID, adminAgent.AgentID)
	}
	if event.OrgID != org.OrgID {
		t.Fatalf("org = %q want %q", event.OrgID, org.OrgID)
	}
	if event.Decision != "allow" {
		t.Fatalf("decision = %q want allow", event.Decision)
	}
}

// extractHiddenInput duplicates a small helper from internal/webui tests.
// Kept here so this package can exercise the admin UI without importing
// the test-only helpers from another package.
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
