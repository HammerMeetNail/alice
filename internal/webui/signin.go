package webui

import (
	"errors"
	"net/http"
	"strings"

	"alice/internal/websession"
)

type signInPageData struct {
	OrgSlug string
	Email   string
	Error   string
}

type signInVerifyPageData struct {
	SignInID string
	Error    string
}

func (h *Handler) handleSignInPage(w http.ResponseWriter, r *http.Request) {
	// If already signed in, redirect to dashboard.
	if cookie, err := r.Cookie(h.opts.Sessions.Options().SessionCookie); err == nil {
		if _, err := h.opts.Sessions.Lookup(cookie.Value); err == nil {
			http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
			return
		}
	}
	h.renderTemplate(w, "signin.html", signInPageData{})
}

func (h *Handler) handleStartSignIn(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	orgSlug := strings.TrimSpace(r.Form.Get("org_slug"))
	email := strings.TrimSpace(r.Form.Get("email"))

	if orgSlug == "" || email == "" {
		h.renderTemplate(w, "signin.html", signInPageData{
			OrgSlug: orgSlug,
			Email:   email,
			Error:   "Both org slug and email are required.",
		})
		return
	}

	signInID, err := h.opts.Sessions.StartSignIn(r.Context(), orgSlug, email)
	if err != nil {
		h.logger.Info("admin sign-in start failed", "org_slug", orgSlug, "err", err)
		h.renderTemplate(w, "signin.html", signInPageData{
			OrgSlug: orgSlug,
			Email:   email,
			Error:   adminErrMessage(err),
		})
		return
	}

	h.renderTemplate(w, "signin_verify.html", signInVerifyPageData{SignInID: signInID})
}

func (h *Handler) handleVerifyPage(w http.ResponseWriter, r *http.Request) {
	signInID := strings.TrimSpace(r.URL.Query().Get("id"))
	if signInID == "" {
		http.Redirect(w, r, "/admin/sign-in", http.StatusSeeOther)
		return
	}
	h.renderTemplate(w, "signin_verify.html", signInVerifyPageData{SignInID: signInID})
}

func (h *Handler) handleCompleteSignIn(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	signInID := strings.TrimSpace(r.Form.Get("sign_in_id"))
	code := strings.TrimSpace(r.Form.Get("code"))

	if signInID == "" || code == "" {
		h.renderTemplate(w, "signin_verify.html", signInVerifyPageData{
			SignInID: signInID,
			Error:    "Sign-in ID and code are both required.",
		})
		return
	}

	session, err := h.opts.Sessions.VerifySignIn(signInID, code)
	if err != nil {
		h.logger.Info("admin sign-in verify failed", "sign_in_id", signInID, "err", err)
		if errors.Is(err, websession.ErrSignInNotFound) || errors.Is(err, websession.ErrSignInExpired) || errors.Is(err, websession.ErrTooManyAttempts) {
			h.renderTemplate(w, "signin.html", signInPageData{
				Error: adminErrMessage(err),
			})
			return
		}
		h.renderTemplate(w, "signin_verify.html", signInVerifyPageData{
			SignInID: signInID,
			Error:    adminErrMessage(err),
		})
		return
	}

	http.SetCookie(w, h.opts.Sessions.SessionCookie(session.SessionID, session.ExpiresAt))
	http.SetCookie(w, h.opts.Sessions.CSRFCookie(session.CSRFToken, session.ExpiresAt))
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

func (h *Handler) handleSignOut(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromCtx(r.Context())
	if ok {
		h.opts.Sessions.SignOut(session.SessionID)
	}
	http.SetCookie(w, h.opts.Sessions.ClearCookie(h.opts.Sessions.Options().SessionCookie))
	http.SetCookie(w, h.opts.Sessions.ClearCookie(h.opts.Sessions.Options().CSRFCookie))
	http.Redirect(w, r, "/admin/sign-in", http.StatusSeeOther)
}
