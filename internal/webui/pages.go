package webui

import (
	"net/http"
	"strings"
	"time"

	"alice/internal/audit"
	"alice/internal/core"
	"alice/internal/websession"
)

type layoutCommon struct {
	Email     string
	OrgID     string
	CSRFToken string
}

func commonData(session websession.Session) layoutCommon {
	return layoutCommon{
		Email:     session.Email,
		OrgID:     session.OrgID,
		CSRFToken: session.CSRFToken,
	}
}

type dashboardData struct {
	Common layoutCommon
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	session := mustSession(r.Context())
	h.renderTemplate(w, "dashboard.html", dashboardData{Common: commonData(session)})
}

type pendingAgentsData struct {
	Common  layoutCommon
	Pending []core.AgentApproval
	Error   string
	Notice  string
}

func (h *Handler) handlePendingAgents(w http.ResponseWriter, r *http.Request) {
	session := mustSession(r.Context())
	notice := strings.TrimSpace(r.URL.Query().Get("notice"))
	pending, err := h.opts.Services.ListPendingAgentApprovals(r.Context(), session.OrgID, session.AgentID, 50, 0)
	data := pendingAgentsData{Common: commonData(session), Pending: pending, Notice: notice}
	if err != nil {
		data.Error = adminErrMessage(err)
	}
	h.renderTemplate(w, "pending.html", data)
}

func (h *Handler) handleReviewAgent(w http.ResponseWriter, r *http.Request) {
	session := mustSession(r.Context())
	// ParseForm is idempotent: the CSRF middleware may have already called
	// it when reading csrf_token from the body; if CSRF came in via the
	// header, this is the first parse. Either way it's safe.
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	agentID := strings.TrimSpace(r.Form.Get("agent_id"))
	decision := strings.TrimSpace(r.Form.Get("decision"))
	reason := strings.TrimSpace(r.Form.Get("reason"))

	if agentID == "" || (decision != "approved" && decision != "rejected") {
		http.Error(w, "agent_id and decision=approved|rejected required", http.StatusBadRequest)
		return
	}

	if err := h.opts.Services.ReviewAgentApproval(r.Context(), session.OrgID, agentID, session.AgentID, decision, reason); err != nil {
		h.logger.Warn("admin review agent failed", "agent_id", agentID, "err", err)
		http.Error(w, adminErrMessage(err), http.StatusBadRequest)
		return
	}

	// The underlying agents service already writes an `agent.approval_*`
	// audit event inside its transaction, so we don't duplicate it here —
	// we just follow up with a separate marker tying the decision to the
	// browser session for downstream log filtering.
	if err := h.opts.Services.RecordAudit(r.Context(), "admin_ui.agent_reviewed", "agent", agentID, session.OrgID, session.AgentID, agentID, decision, core.RiskLevelL1, nil, map[string]any{
		"decision":   decision,
		"reason":     reason,
		"session_id": session.SessionID,
	}); err != nil {
		h.logger.Warn("admin_ui.agent_reviewed audit record failed", "err", err)
	}

	notice := "Agent " + decision + "."
	http.Redirect(w, r, "/admin/pending-agents?notice="+urlEscape(notice), http.StatusSeeOther)
}

type inviteTokenData struct {
	Common   layoutCommon
	NewToken string
	Error    string
}

func (h *Handler) handleInviteTokenPage(w http.ResponseWriter, r *http.Request) {
	session := mustSession(r.Context())
	h.renderTemplate(w, "invite.html", inviteTokenData{Common: commonData(session)})
}

func (h *Handler) handleRotateInvite(w http.ResponseWriter, r *http.Request) {
	session := mustSession(r.Context())
	raw, err := h.opts.Services.RotateInviteToken(r.Context(), session.OrgID, session.AgentID)
	data := inviteTokenData{Common: commonData(session)}
	if err != nil {
		h.logger.Warn("admin rotate invite token failed", "err", err)
		data.Error = adminErrMessage(err)
	} else {
		data.NewToken = raw
		// Match the JSON API handler: the service emits no audit event, so
		// the surface that invokes it records one. Keep the event kind,
		// subject, and decision identical to the CLI/MCP path.
		if auditErr := h.opts.Services.RecordAudit(r.Context(), "org.invite_token_rotated", "org", session.OrgID, session.OrgID, session.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
			"session_id": session.SessionID,
		}); auditErr != nil {
			h.logger.Warn("org.invite_token_rotated audit record failed", "err", auditErr)
		}
	}
	h.renderTemplate(w, "invite.html", data)
}

type auditData struct {
	Common layoutCommon
	Events []core.AuditEvent
	Filter auditFilterView
	Error  string
}

type auditFilterView struct {
	EventKind   string
	SubjectType string
	Decision    string
	Since       string
}

func (h *Handler) handleAudit(w http.ResponseWriter, r *http.Request) {
	session := mustSession(r.Context())

	filter := audit.SummaryFilter{
		EventKind:   strings.TrimSpace(r.URL.Query().Get("event_kind")),
		SubjectType: strings.TrimSpace(r.URL.Query().Get("subject_type")),
		Decision:    strings.TrimSpace(r.URL.Query().Get("decision")),
	}
	view := auditFilterView{
		EventKind:   filter.EventKind,
		SubjectType: filter.SubjectType,
		Decision:    filter.Decision,
		Since:       strings.TrimSpace(r.URL.Query().Get("since")),
	}
	var since time.Time
	if view.Since != "" {
		parsed, err := time.Parse(time.RFC3339, view.Since)
		if err != nil {
			h.renderTemplate(w, "audit.html", auditData{
				Common: commonData(session),
				Filter: view,
				Error:  "`since` must be RFC3339, e.g. 2026-04-23T10:00:00Z",
			})
			return
		}
		since = parsed
	}

	events, err := h.opts.Services.AuditSummary(r.Context(), session.AgentID, since, 100, 0, filter)
	data := auditData{Common: commonData(session), Events: events, Filter: view}
	if err != nil {
		data.Error = adminErrMessage(err)
	}
	h.renderTemplate(w, "audit.html", data)
}

// urlEscape is a tiny helper: net/url.QueryEscape replaces spaces with '+'
// which is correct inside a query string but looks odd in a single-token
// flash notice. Replace with %20 for readability, fall back to QueryEscape
// for special characters.
func urlEscape(s string) string {
	escaped := strings.Builder{}
	for _, r := range s {
		switch {
		case r == ' ':
			escaped.WriteString("%20")
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			escaped.WriteRune(r)
		default:
			escaped.WriteString(percentEncode(r))
		}
	}
	return escaped.String()
}

func percentEncode(r rune) string {
	const hex = "0123456789ABCDEF"
	b := []byte(string(r))
	out := make([]byte, 0, 3*len(b))
	for _, c := range b {
		out = append(out, '%', hex[c>>4], hex[c&0x0f])
	}
	return string(out)
}

