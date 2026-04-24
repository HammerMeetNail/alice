package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"alice/internal/actions"
	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/approvals"
	"alice/internal/audit"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/metrics"
	"alice/internal/queries"
	"alice/internal/requests"
	"alice/internal/riskpolicy"
	"alice/internal/storage"
)

// ipBucket is a simple token-bucket rate limiter entry per IP address.
type ipBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// ipRateLimiter holds per-IP token buckets for unauthenticated endpoint protection.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	// rate is tokens added per nanosecond (10 per minute = 10/60e9 per ns)
	rate  float64
	burst float64
}

func newIPRateLimiter(ratePerMin, burst float64) *ipRateLimiter {
	return &ipRateLimiter{
		buckets: make(map[string]*ipBucket),
		rate:    ratePerMin / 60e9,
		burst:   burst,
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{tokens: l.burst, lastSeen: time.Now()}
		l.buckets[ip] = b
	}
	l.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastSeen)
	b.tokens += float64(elapsed) * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// agentBucket is a per-agent token-bucket entry.
type agentBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// agentRateLimiter holds per-agent token buckets for authenticated endpoint protection.
type agentRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*agentBucket
	rate    float64 // tokens per nanosecond
	burst   float64
}

func newAgentRateLimiter(ratePerMin float64) *agentRateLimiter {
	if ratePerMin <= 0 {
		ratePerMin = 60
	}
	burst := ratePerMin // allow full burst equal to 1-minute quota
	return &agentRateLimiter{
		buckets: make(map[string]*agentBucket),
		rate:    ratePerMin / 60e9,
		burst:   burst,
	}
}

func (l *agentRateLimiter) allow(agentID string) bool {
	l.mu.Lock()
	b, ok := l.buckets[agentID]
	if !ok {
		b = &agentBucket{tokens: l.burst, lastSeen: time.Now()}
		l.buckets[agentID] = b
	}
	l.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastSeen)
	b.tokens += float64(elapsed) * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// requestIDContextKey carries the request ID through the context.
type requestIDContextKey struct{}

// loggerContextKey carries a slog.Logger pre-seeded with the request ID.
type loggerContextKey struct{}

// agentIDHolder is a mutable slot injected by accessLog so that requireAuth
// can backfill the agent ID after authentication succeeds. The holder is
// read by accessLog after the handler chain returns, giving the log line an
// accurate agent_id even though requireAuth stores auth info in a derived
// request context that accessLog never sees directly.
type agentIDHolder struct{ id string }

type agentIDHolderKey struct{}

// RouterOptions configures the HTTP router.
type RouterOptions struct {
	// Services is the service container (required).
	Services services.Container
	// Pinger, when non-nil, is called by GET /readyz to check DB health.
	Pinger func(ctx context.Context) error
	// TLSTerminated adds Strict-Transport-Security when true.
	TLSTerminated bool
	// TrustedProxies is a list of parsed CIDRs whose X-Forwarded-For is trusted.
	TrustedProxies []*net.IPNet
	// AgentRatePerMin is the per-agent rate on heavy endpoints (default 60).
	AgentRatePerMin float64
	// AdminSignInRatePerMin is the per-IP rate on /admin/sign-in endpoints.
	// Used by the top-level handler in app/server.go.
	AdminSignInRatePerMin float64
}

type router struct {
	services       services.Container
	mux            *http.ServeMux
	rateLimiter    *ipRateLimiter
	agentLimiter   *agentRateLimiter
	pinger         func(ctx context.Context) error
	tlsTerminated  bool
	trustedProxies []*net.IPNet
}

type currentAgentContextKey struct{}
type currentUserContextKey struct{}

// ParseCIDRs parses a list of CIDR strings and returns the parsed nets.
// Unparseable entries are silently skipped (logged at WARN).
func ParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, s := range cidrs {
		_, ipNet, err := net.ParseCIDR(strings.TrimSpace(s))
		if err != nil {
			slog.Warn("could not parse trusted proxy CIDR; skipping", "cidr", s, "err", err)
			continue
		}
		out = append(out, ipNet)
	}
	return out
}

func NewRouter(opts RouterOptions) http.Handler {
	r := &router{
		services:       opts.Services,
		mux:            http.NewServeMux(),
		rateLimiter:    newIPRateLimiter(10, 10),
		agentLimiter:   newAgentRateLimiter(opts.AgentRatePerMin),
		pinger:         opts.Pinger,
		tlsTerminated:  opts.TLSTerminated,
		trustedProxies: opts.TrustedProxies,
	}
	r.routes()
	handler := http.Handler(r.mux)
	handler = metrics.InstrumentHandler(handler)
	handler = r.accessLog(handler)
	handler = r.requestID(handler)
	handler = r.securityHeaders(handler)
	return handler
}

func (r *router) routes() {
	r.mux.HandleFunc("GET /healthz", r.handleHealthz)
	r.mux.HandleFunc("GET /livez", r.handleLivez)
	r.mux.HandleFunc("GET /readyz", r.handleReadyz)
	r.mux.Handle("POST /v1/agents/register/challenge", r.rateLimit(r.limitBody(http.HandlerFunc(r.handleBeginRegisterAgent))))
	r.mux.Handle("POST /v1/agents/register", r.rateLimit(r.limitBody(http.HandlerFunc(r.handleRegisterAgent))))
	// Email verification endpoints: require auth but are exempt from email-verified check.
	r.mux.Handle("POST /v1/agents/verify-email", r.limitBody(r.requireAuth(http.HandlerFunc(r.handleVerifyEmail))))
	r.mux.Handle("POST /v1/agents/resend-verification", r.requireAuth(http.HandlerFunc(r.handleResendVerification)))
	// All other authenticated routes enforce email verification.
	// Heavy write endpoints also enforce per-agent rate limiting.
	r.mux.Handle("POST /v1/artifacts", r.limitBody(r.requireVerifiedAuth(r.agentRateLimit("artifacts", http.HandlerFunc(r.handlePublishArtifact)))))
	r.mux.Handle("POST /v1/artifacts/", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleCorrectArtifact))))
	r.mux.Handle("POST /v1/policy-grants", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleGrantPermission))))
	r.mux.Handle("DELETE /v1/policy-grants/", r.requireVerifiedAuth(http.HandlerFunc(r.handleRevokePermission)))
	r.mux.Handle("GET /v1/peers", r.requireVerifiedAuth(http.HandlerFunc(r.handleListAllowedPeers)))
	r.mux.Handle("POST /v1/queries", r.limitBody(r.requireVerifiedAuth(r.agentRateLimit("queries", http.HandlerFunc(r.handleQueryPeerStatus)))))
	r.mux.Handle("GET /v1/queries/", r.requireVerifiedAuth(http.HandlerFunc(r.handleGetQueryResult)))
	r.mux.Handle("POST /v1/requests", r.limitBody(r.requireVerifiedAuth(r.agentRateLimit("requests", http.HandlerFunc(r.handleSendRequestToPeer)))))
	r.mux.Handle("GET /v1/requests/incoming", r.requireVerifiedAuth(http.HandlerFunc(r.handleListIncomingRequests)))
	r.mux.Handle("GET /v1/requests/sent", r.requireVerifiedAuth(http.HandlerFunc(r.handleListSentRequests)))
	r.mux.Handle("POST /v1/requests/", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleRespondToRequest))))
	r.mux.Handle("GET /v1/approvals", r.requireVerifiedAuth(http.HandlerFunc(r.handleListPendingApprovals)))
	r.mux.Handle("POST /v1/approvals/", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleResolveApproval))))
	r.mux.Handle("GET /v1/audit/summary", r.requireVerifiedAuth(http.HandlerFunc(r.handleAuditSummary)))
	r.mux.Handle("POST /v1/orgs/rotate-invite-token", r.requireVerifiedAuth(http.HandlerFunc(r.handleRotateInviteToken)))
	r.mux.Handle("POST /v1/orgs/verification-mode", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleUpdateVerificationMode))))
	r.mux.Handle("POST /v1/orgs/gatekeeper-tuning", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleUpdateGatekeeperTuning))))
	r.mux.Handle("POST /v1/orgs/risk-policy", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleApplyRiskPolicy))))
	r.mux.Handle("GET /v1/orgs/risk-policies", r.requireVerifiedAuth(http.HandlerFunc(r.handleListRiskPolicies)))
	r.mux.Handle("POST /v1/orgs/risk-policies/", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleActivateRiskPolicy))))
	r.mux.Handle("POST /v1/actions", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleCreateAction))))
	r.mux.Handle("GET /v1/actions", r.requireVerifiedAuth(http.HandlerFunc(r.handleListActions)))
	r.mux.Handle("POST /v1/actions/", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleActionAction))))
	r.mux.Handle("POST /v1/users/me/operator-enabled", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleSetOperatorEnabled))))
	r.mux.Handle("DELETE /v1/users/me", r.requireVerifiedAuth(http.HandlerFunc(r.handleDeleteSelf)))
	r.mux.Handle("GET /v1/orgs/pending-agents", r.requireVerifiedAuth(http.HandlerFunc(r.handleListPendingAgents)))
	r.mux.Handle("POST /v1/orgs/agents/", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleReviewAgent))))
	r.mux.Handle("DELETE /v1/orgs/", r.requireVerifiedAuth(http.HandlerFunc(r.handleDeleteOrg)))
	r.mux.Handle("POST /v1/org/teams", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleCreateTeam))))
	r.mux.Handle("GET /v1/org/teams", r.requireVerifiedAuth(http.HandlerFunc(r.handleListTeams)))
	r.mux.Handle("GET /v1/org/teams/", r.requireVerifiedAuth(http.HandlerFunc(r.handleListTeamMembers)))
	r.mux.Handle("POST /v1/org/teams/", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleAddTeamMember))))
	r.mux.Handle("DELETE /v1/org/teams/", r.requireVerifiedAuth(http.HandlerFunc(r.handleTeamDelete)))
	r.mux.Handle("POST /v1/org/manager-edges", r.limitBody(r.requireVerifiedAuth(http.HandlerFunc(r.handleAssignManager))))
	r.mux.Handle("GET /v1/org/manager-edges/", r.requireVerifiedAuth(http.HandlerFunc(r.handleGetManagerChain))) // GET /v1/org/manager-edges/:user_id
	r.mux.Handle("DELETE /v1/org/manager-edges/", r.requireVerifiedAuth(http.HandlerFunc(r.handleRevokeManager)))
}

func (r *router) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleLivez reports that the process is alive. Always returns 200.
func (r *router) handleLivez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz reports that the server is ready to serve traffic. When a
// Pinger is configured it verifies DB reachability before returning 200.
func (r *router) handleReadyz(w http.ResponseWriter, req *http.Request) {
	if r.pinger != nil {
		if err := r.pinger(req.Context()); err != nil {
			slog.Error("readyz: db ping failed", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
				"reason": "database unreachable",
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type registerAgentRequest struct {
	OrgSlug     string `json:"org_slug"`
	OwnerEmail  string `json:"owner_email"`
	AgentName   string `json:"agent_name"`
	ClientType  string `json:"client_type"`
	PublicKey   string `json:"public_key"`
	InviteToken string `json:"invite_token"`
}

func (r *router) handleBeginRegisterAgent(w http.ResponseWriter, req *http.Request) {
	var input registerAgentRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	result, err := r.services.Agents.BeginRegistration(req.Context(), input.OrgSlug, input.OwnerEmail, input.AgentName, input.ClientType, input.PublicKey, input.InviteToken)
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrInviteTokenRequired):
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":   "invite_token_required",
				"message": err.Error(),
			})
		case errors.Is(err, agents.ErrInvalidInviteToken):
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":   "invalid_invite_token",
				"message": err.Error(),
			})
		default:
			writeServiceError(w, err, "registration challenge failed")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": result.Challenge.ChallengeID,
		"challenge":    result.Payload,
		"algorithm":    "ed25519",
		"expires_at":   result.Challenge.ExpiresAt,
	})
}

type completeRegisterAgentRequest struct {
	ChallengeID        string `json:"challenge_id"`
	ChallengeSignature string `json:"challenge_signature"`
}

func (r *router) handleRegisterAgent(w http.ResponseWriter, req *http.Request) {
	var input completeRegisterAgentRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	regResult, err := r.services.Agents.CompleteRegistration(req.Context(), input.ChallengeID, input.ChallengeSignature)
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrUnknownRegistrationChallenge):
			writeError(w, http.StatusNotFound, err.Error())
			return
		case errors.Is(err, agents.ErrExpiredRegistrationChallenge),
			errors.Is(err, agents.ErrUsedRegistrationChallenge),
			errors.Is(err, agents.ErrInvalidRegistrationSignature):
			writeAuthError(w, err.Error())
			return
		case errors.Is(err, agents.ErrOrgSlugTaken):
			writeError(w, http.StatusConflict, err.Error())
			return
		default:
			writeServiceError(w, err, "agent registration failed")
			return
		}
	}
	org := regResult.Org
	user := regResult.User
	agent := regResult.Agent
	accessToken := regResult.AccessToken
	expiresAt := regResult.TokenExpiresAt

	if _, err := r.services.Audit.Record(req.Context(), "agent.registered", "agent", agent.AgentID, org.OrgID, agent.AgentID, "", "allow", "", nil, map[string]any{
		"owner_email":      user.Email,
		"auth_method":      "ed25519_challenge",
		"token_expires_at": expiresAt,
		"status":           agent.Status,
	}); err != nil {
		slog.Error("audit record failed", "op", "agent_registration", "err", err)
	}

	if agent.Status == "pending_email_verification" {
		if _, err := r.services.Audit.Record(req.Context(), "agent.email_verification_sent", "agent", agent.AgentID, org.OrgID, agent.AgentID, "", "allow", "", nil, map[string]any{
			"email": user.Email,
		}); err != nil {
			slog.Error("audit record failed", "op", "email_verification_sent", "err", err)
		}
	}

	resp := map[string]any{
		"agent_id":     agent.AgentID,
		"org_id":       org.OrgID,
		"status":       agent.Status,
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_at":   expiresAt,
	}
	if regResult.FirstInviteToken != "" {
		resp["first_invite_token"] = regResult.FirstInviteToken
	}
	writeJSON(w, http.StatusOK, resp)
}

type verifyEmailRequest struct {
	Code string `json:"code"`
}

func (r *router) handleVerifyEmail(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input verifyEmailRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(input.Code) == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	if err := r.services.Agents.VerifyEmail(req.Context(), agent.AgentID, input.Code); err != nil {
		switch {
		case errors.Is(err, agents.ErrVerificationNotFound):
			writeError(w, http.StatusNotFound, "no pending email verification found")
		case errors.Is(err, agents.ErrVerificationExpired):
			if _, auditErr := r.services.Audit.Record(req.Context(), "agent.email_verification_failed", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "deny", "", nil, map[string]any{"reason": "expired"}); auditErr != nil {
				slog.Error("audit record failed", "op", "email_verify_failed", "err", auditErr)
			}
			writeError(w, http.StatusGone, "verification code expired")
		case errors.Is(err, agents.ErrVerificationMaxAttempts):
			if _, auditErr := r.services.Audit.Record(req.Context(), "agent.email_verification_failed", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "deny", "", nil, map[string]any{"reason": "max_attempts"}); auditErr != nil {
				slog.Error("audit record failed", "op", "email_verify_failed", "err", auditErr)
			}
			writeError(w, http.StatusTooManyRequests, "max verification attempts exceeded")
		case errors.Is(err, agents.ErrInvalidVerificationCode):
			if _, auditErr := r.services.Audit.Record(req.Context(), "agent.email_verification_failed", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "deny", "", nil, map[string]any{"reason": "invalid_code"}); auditErr != nil {
				slog.Error("audit record failed", "op", "email_verify_failed", "err", auditErr)
			}
			writeError(w, http.StatusUnauthorized, "invalid verification code")
		default:
			writeServiceError(w, err, "email verification failed")
		}
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "agent.email_verified", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "allow", "", nil, nil); err != nil {
		slog.Error("audit record failed", "op", "email_verify", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agent.AgentID,
		"status":   "active",
		"verified": true,
	})
}

func (r *router) handleResendVerification(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	if err := r.services.Agents.ResendVerificationEmail(req.Context(), agent.AgentID); err != nil {
		switch {
		case errors.Is(err, agents.ErrVerificationNotFound):
			writeError(w, http.StatusNotFound, "no pending email verification found")
			return
		case errors.Is(err, agents.ErrResendTooSoon):
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		default:
			writeServiceError(w, err, "resend verification failed")
			return
		}
	}

	if _, err := r.services.Audit.Record(req.Context(), "agent.email_verification_sent", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "allow", "", nil, nil); err != nil {
		slog.Error("audit record failed", "op", "email_resend", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": agent.AgentID,
		"sent":     true,
	})
}

type publishArtifactRequest struct {
	Artifact core.Artifact `json:"artifact"`
}

func (r *router) handlePublishArtifact(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input publishArtifactRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	artifact, err := r.services.Artifacts.PublishArtifact(req.Context(), agent, user, input.Artifact)
	if err != nil {
		writeServiceError(w, err, "artifact publish failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "artifact.published", "artifact", artifact.ArtifactID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"artifact_type": artifact.Type,
	}); err != nil {
		slog.Error("audit record failed", "op", "artifact_publish", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id": artifact.ArtifactID,
		"stored":      true,
	})
}

type correctArtifactRequest struct {
	Artifact core.Artifact `json:"artifact"`
}

func (r *router) handleCorrectArtifact(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	artifactID, ok := trimActionPath(req.URL.Path, "/v1/artifacts/", "/correct")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var input correctArtifactRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	corrected, err := r.services.Artifacts.CorrectArtifact(req.Context(), agent, user, artifactID, input.Artifact)
	if err != nil {
		writeServiceError(w, err, "artifact correction failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "artifact.corrected", "artifact", corrected.ArtifactID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"artifact_type":        corrected.Type,
		"supersedes_artifact_id": artifactID,
	}); err != nil {
		slog.Error("audit record failed", "op", "artifact_correct", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_id":            corrected.ArtifactID,
		"supersedes_artifact_id": artifactID,
		"stored":                 true,
	})
}

type grantPermissionRequest struct {
	GranteeUserEmail     string              `json:"grantee_user_email"`
	ScopeType            string              `json:"scope_type"`
	ScopeRef             string              `json:"scope_ref"`
	AllowedArtifactTypes []core.ArtifactType `json:"allowed_artifact_types"`
	MaxSensitivity       core.Sensitivity    `json:"max_sensitivity"`
	AllowedPurposes      []core.QueryPurpose `json:"allowed_purposes"`
}

func (r *router) handleGrantPermission(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input grantPermissionRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	granteeUser, exists, err := r.services.Agents.FindUserByEmail(req.Context(), agent.OrgID, input.GranteeUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve grantee user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "grantee user not found")
		return
	}

	grant, err := r.services.Policy.Grant(req.Context(), agent.OrgID, user, granteeUser, input.ScopeType, input.ScopeRef, input.AllowedArtifactTypes, input.MaxSensitivity, input.AllowedPurposes)
	if err != nil {
		writeServiceError(w, err, "grant creation failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "policy.grant.created", "policy_grant", grant.PolicyGrantID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, []string{"grant:" + grant.PolicyGrantID}, map[string]any{
		"grantee_email": granteeUser.Email,
		"scope_ref":     grant.ScopeRef,
	}); err != nil {
		slog.Error("audit record failed", "op", "grant_create", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"policy_grant_id": grant.PolicyGrantID,
	})
}

func (r *router) handleRevokePermission(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	grantID := strings.TrimPrefix(req.URL.Path, "/v1/policy-grants/")
	if grantID == "" {
		writeError(w, http.StatusBadRequest, "grant_id is required")
		return
	}

	grant, err := r.services.Policy.RevokeGrant(req.Context(), grantID, user.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "grant not found or not owned by caller")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "policy.grant.revoked", "policy_grant", grant.PolicyGrantID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, []string{"grant:" + grant.PolicyGrantID}, map[string]any{
		"scope_ref": grant.ScopeRef,
	}); err != nil {
		slog.Error("audit record failed", "op", "grant_revoke", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"policy_grant_id": grant.PolicyGrantID,
		"revoked":         true,
	})
}

func (r *router) handleListAllowedPeers(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	limit, offset := parsePagination(req)
	grants, err := r.services.Policy.ListAllowedPeers(req.Context(), user.UserID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list allowed peers")
		return
	}
	peers := make([]map[string]any, 0, len(grants))
	for _, grant := range grants {
		owner, exists, err := r.services.Agents.FindUserByID(req.Context(), grant.GrantorUserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve grant owner")
			return
		}
		if !exists {
			continue
		}

		peers = append(peers, map[string]any{
			"user_email":             owner.Email,
			"allowed_purposes":       grant.AllowedPurposes,
			"allowed_artifact_types": grant.AllowedArtifactTypes,
		})
	}

	if _, err := r.services.Audit.Record(req.Context(), "policy.allowed_peers.listed", "agent", agent.AgentID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL0, nil, nil); err != nil {
		slog.Error("audit record failed", "op", "list_peers", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peers":       peers,
		"next_cursor": nextCursor(len(grants), limit, offset),
	})
}

type queryPeerStatusRequest struct {
	ToUserEmail    string              `json:"to_user_email"`
	Purpose        core.QueryPurpose   `json:"purpose"`
	Question       string              `json:"question"`
	RequestedTypes []core.ArtifactType `json:"requested_types"`
	ProjectScope   []string            `json:"project_scope"`
	TimeWindow     core.TimeWindow     `json:"time_window"`
}

func (r *router) handleQueryPeerStatus(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input queryPeerStatusRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	if err := core.ValidateQueryInput(input.ToUserEmail, input.Purpose, input.RequestedTypes, input.TimeWindow); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetUser, exists, err := r.services.Agents.FindUserByEmail(req.Context(), agent.OrgID, input.ToUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target user not found")
		return
	}
	targetAgent, exists, err := r.services.Agents.FindAgentByUserID(req.Context(), targetUser.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target agent")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target agent not found")
		return
	}

	query := core.Query{
		QueryID:        id.New("query"),
		OrgID:          agent.OrgID,
		FromAgentID:    agent.AgentID,
		FromUserID:     user.UserID,
		ToAgentID:      targetAgent.AgentID,
		ToUserID:       targetUser.UserID,
		Purpose:        input.Purpose,
		Question:       input.Question,
		RequestedTypes: input.RequestedTypes,
		ProjectScope:   input.ProjectScope,
		TimeWindow:     input.TimeWindow,
		RiskLevel:      core.RiskLevelL1,
		State:          core.QueryStateQueued,
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      time.Now().UTC().Add(5 * time.Minute),
	}

	response, err := r.services.Queries.Evaluate(req.Context(), query)
	if err != nil {
		if errors.Is(err, queries.ErrPermissionDenied) {
			if _, auditErr := r.services.Audit.Record(req.Context(), "query.denied", "query", query.QueryID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "deny", core.RiskLevelL1, nil, map[string]any{
				"to_user_email": targetUser.Email,
			}); auditErr != nil {
				slog.Error("audit record failed", "op", "query_denied", "err", auditErr)
			}
			writeError(w, http.StatusForbidden, "query is not allowed")
			return
		}
		writeServiceError(w, err, "query evaluation failed")
		return
	}

	if response.ApprovalState == core.ApprovalStatePending {
		if _, err := r.services.Audit.Record(req.Context(), "query.approval_requested", "query", query.QueryID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "allow", core.RiskLevelL1, response.PolicyBasis, map[string]any{
			"artifact_count": len(response.Artifacts),
		}); err != nil {
			slog.Error("audit record failed", "op", "query_approval_requested", "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"query_id":       query.QueryID,
			"status":         core.QueryStatePendingApproval,
			"approval_state": response.ApprovalState,
		})
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "query.completed", "query", query.QueryID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "allow", core.RiskLevelL1, response.PolicyBasis, map[string]any{
		"artifact_count": len(response.Artifacts),
	}); err != nil {
		slog.Error("audit record failed", "op", "query_complete", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"query_id": query.QueryID,
		"status":   core.QueryStateCompleted,
	})
}

func (r *router) handleGetQueryResult(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	queryID := strings.TrimPrefix(req.URL.Path, "/v1/queries/")
	query, response, found, err := r.services.Queries.FindResult(req.Context(), queryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load query result")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "query not found")
		return
	}
	if query.FromAgentID != agent.AgentID && query.ToAgentID != agent.AgentID {
		writeError(w, http.StatusForbidden, "query result is not visible to this agent")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"query_id": query.QueryID,
		"state":    query.State,
		"response": response,
	})
}

func (r *router) handleAuditSummary(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var since time.Time
	if raw := req.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = parsed
	}

	limit, offset := parsePagination(req)
	events, err := r.services.Audit.Summary(req.Context(), agent.AgentID, since, limit, offset, audit.SummaryFilter{
		EventKind:   req.URL.Query().Get("event_kind"),
		SubjectType: req.URL.Query().Get("subject_type"),
		Decision:    req.URL.Query().Get("decision"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load audit summary")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":      events,
		"next_cursor": nextCursor(len(events), limit, offset),
	})
}

type sendRequestToPeerRequest struct {
	ToUserEmail       string         `json:"to_user_email"`
	RequestType       string         `json:"request_type"`
	Title             string         `json:"title"`
	Content           string         `json:"content"`
	StructuredPayload map[string]any `json:"structured_payload"`
}

func (r *router) handleSendRequestToPeer(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input sendRequestToPeerRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := core.ValidateRequestInput(input.ToUserEmail, input.RequestType, input.Title, input.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetUser, exists, err := r.services.Agents.FindUserByEmail(req.Context(), agent.OrgID, input.ToUserEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target user")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target user not found")
		return
	}
	targetAgent, exists, err := r.services.Agents.FindAgentByUserID(req.Context(), targetUser.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve target agent")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "target agent not found")
		return
	}

	requestRecord, err := r.services.Requests.Send(req.Context(), core.Request{
		OrgID:             agent.OrgID,
		FromAgentID:       agent.AgentID,
		FromUserID:        user.UserID,
		ToAgentID:         targetAgent.AgentID,
		ToUserID:          targetUser.UserID,
		RequestType:       input.RequestType,
		Title:             input.Title,
		Content:           input.Content,
		StructuredPayload: input.StructuredPayload,
		RiskLevel:         core.RiskLevelL1,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request creation failed")
		return
	}

	if _, err := r.services.Audit.Record(req.Context(), "request.created", "request", requestRecord.RequestID, agent.OrgID, agent.AgentID, targetAgent.AgentID, "allow", requestRecord.RiskLevel, nil, map[string]any{
		"request_type":  input.RequestType,
		"to_user_email": targetUser.Email,
	}); err != nil {
		slog.Error("audit record failed", "op", "request_create", "err", err)
	}

	payload := map[string]any{
		"request_id": requestRecord.RequestID,
		"state":      requestRecord.State,
	}
	if requestRecord.ResponseMessage != "" {
		payload["response_message"] = requestRecord.ResponseMessage
	}
	writeJSON(w, http.StatusOK, payload)
}

func (r *router) handleListIncomingRequests(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	limit, offset := parsePagination(req)
	requestsList, err := r.services.Requests.ListIncoming(req.Context(), agent.AgentID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load incoming requests")
		return
	}

	items := make([]map[string]any, 0, len(requestsList))
	for _, requestRecord := range requestsList {
		sender, exists, err := r.services.Agents.FindUserByID(req.Context(), requestRecord.FromUserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve request sender")
			return
		}
		if !exists {
			continue
		}

		items = append(items, map[string]any{
			"request_id":      requestRecord.RequestID,
			"from_user_email": sender.Email,
			"request_type":    requestRecord.RequestType,
			"title":           requestRecord.Title,
			"state":           requestRecord.State,
			"approval_state":  requestRecord.ApprovalState,
			"created_at":      requestRecord.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"requests":    items,
		"next_cursor": nextCursor(len(requestsList), limit, offset),
	})
}

func (r *router) handleListSentRequests(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	limit, offset := parsePagination(req)
	requestsList, err := r.services.Requests.ListSent(req.Context(), agent.AgentID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load sent requests")
		return
	}

	items := make([]map[string]any, 0, len(requestsList))
	for _, requestRecord := range requestsList {
		recipient, exists, err := r.services.Agents.FindUserByID(req.Context(), requestRecord.ToUserID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve request recipient")
			return
		}
		if !exists {
			continue
		}

		items = append(items, map[string]any{
			"request_id":    requestRecord.RequestID,
			"to_user_email": recipient.Email,
			"request_type":  requestRecord.RequestType,
			"title":         requestRecord.Title,
			"state":         requestRecord.State,
			"approval_state": requestRecord.ApprovalState,
			"response_message": requestRecord.ResponseMessage,
			"created_at":    requestRecord.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"requests":    items,
		"next_cursor": nextCursor(len(requestsList), limit, offset),
	})
}

type respondToRequestRequest struct {
	Response core.RequestResponseAction `json:"response"`
	Message  string                     `json:"message"`
}

func (r *router) handleRespondToRequest(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	requestID, ok := trimActionPath(req.URL.Path, "/v1/requests/", "/respond")
	if !ok {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}

	var input respondToRequestRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := core.ValidateRequestResponseInput(requestID, input.Response); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	requestRecord, approval, err := r.services.Requests.Respond(req.Context(), agent, requestID, input.Response, input.Message)
	if err != nil {
		switch {
		case errors.Is(err, requests.ErrUnknownRequest):
			writeError(w, http.StatusNotFound, err.Error())
			return
		case errors.Is(err, requests.ErrRequestNotVisible):
			writeError(w, http.StatusForbidden, err.Error())
			return
		case errors.Is(err, requests.ErrRequestAlreadyClosed):
			writeError(w, http.StatusConflict, err.Error())
			return
		case errors.Is(err, requests.ErrExpiredRequest):
			writeError(w, http.StatusGone, err.Error())
			return
		default:
			writeError(w, http.StatusInternalServerError, "request response failed")
			return
		}
	}

	eventKind := "request.responded"
	metadata := map[string]any{
		"state": requestRecord.State,
	}
	if approval != nil {
		eventKind = "request.approval_requested"
		metadata["approval_id"] = approval.ApprovalID
	}
	if _, err := r.services.Audit.Record(req.Context(), eventKind, "request", requestRecord.RequestID, requestRecord.OrgID, agent.AgentID, requestRecord.FromAgentID, "allow", requestRecord.RiskLevel, nil, metadata); err != nil {
		slog.Error("audit record failed", "op", "request_respond", "err", err)
	}

	payload := map[string]any{
		"request_id":     requestRecord.RequestID,
		"state":          requestRecord.State,
		"approval_state": requestRecord.ApprovalState,
	}
	if approval != nil {
		payload["approval_id"] = approval.ApprovalID
	}
	writeJSON(w, http.StatusOK, payload)
}

func (r *router) handleListPendingApprovals(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	limit, offset := parsePagination(req)
	approvalsList, err := r.services.Approvals.ListPending(req.Context(), agent.AgentID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load pending approvals")
		return
	}
	items := make([]map[string]any, 0, len(approvalsList))
	for _, approval := range approvalsList {
		items = append(items, map[string]any{
			"approval_id":  approval.ApprovalID,
			"subject_type": approval.SubjectType,
			"subject_id":   approval.SubjectID,
			"reason":       approval.Reason,
			"created_at":   approval.CreatedAt,
			"expires_at":   approval.ExpiresAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approvals":   items,
		"next_cursor": nextCursor(len(approvalsList), limit, offset),
	})
}

type resolveApprovalRequest struct {
	Decision core.ApprovalState `json:"decision"`
}

func (r *router) handleResolveApproval(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	approvalID, ok := trimActionPath(req.URL.Path, "/v1/approvals/", "/resolve")
	if !ok {
		writeError(w, http.StatusNotFound, "approval not found")
		return
	}

	var input resolveApprovalRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := core.ValidateApprovalResolutionInput(approvalID, input.Decision); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	approval, requestRecord, err := r.services.Approvals.Resolve(req.Context(), agent, approvalID, input.Decision)
	if err != nil {
		switch {
		case errors.Is(err, approvals.ErrUnknownApproval):
			writeError(w, http.StatusNotFound, err.Error())
			return
		case errors.Is(err, approvals.ErrApprovalNotVisible):
			writeError(w, http.StatusForbidden, err.Error())
			return
		case errors.Is(err, approvals.ErrApprovalResolved):
			writeError(w, http.StatusConflict, err.Error())
			return
		case errors.Is(err, approvals.ErrExpiredApproval):
			writeError(w, http.StatusGone, err.Error())
			return
		default:
			writeError(w, http.StatusInternalServerError, "approval resolution failed")
			return
		}
	}

	if _, err := r.services.Audit.Record(req.Context(), "approval.resolved", "approval", approval.ApprovalID, approval.OrgID, agent.AgentID, requestRecord.FromAgentID, "allow", requestRecord.RiskLevel, nil, map[string]any{
		"decision":      approval.State,
		"request_id":    requestRecord.RequestID,
		"request_state": requestRecord.State,
	}); err != nil {
		slog.Error("audit record failed", "op", "approval_resolve", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"approval_id": approval.ApprovalID,
		"state":       approval.State,
		"request_id":  requestRecord.RequestID,
	})
}

func (r *router) handleRotateInviteToken(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	rawToken, err := r.services.Agents.RotateInviteToken(req.Context(), agent.OrgID, agent.AgentID)
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrUnknownAgent):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, agents.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeServiceError(w, err, "rotate invite token failed")
		}
		return
	}

	if _, auditErr := r.services.Audit.Record(req.Context(), "org.invite_token_rotated", "org", agent.OrgID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, nil); auditErr != nil {
		slog.Error("audit record failed", "op", "rotate_invite_token", "err", auditErr)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"invite_token": rawToken,
	})
}

type updateVerificationModeRequest struct {
	VerificationMode string `json:"verification_mode"`
}

func (r *router) handleUpdateVerificationMode(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input updateVerificationModeRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	org, err := r.services.Agents.UpdateVerificationMode(req.Context(), agent, input.VerificationMode)
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, agents.ErrUnknownAgentOwner):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeServiceError(w, err, "update verification mode failed")
		}
		return
	}

	if _, auditErr := r.services.Audit.Record(req.Context(), "org.verification_mode_updated", "org", agent.OrgID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"verification_mode": org.VerificationMode,
	}); auditErr != nil {
		slog.Error("audit record failed", "op", "update_verification_mode", "err", auditErr)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"org_id":            org.OrgID,
		"verification_mode": org.VerificationMode,
	})
}

type updateGatekeeperTuningRequest struct {
	// ConfidenceThreshold must be in (0, 1]. An absent key clears any
	// previously-set override; a zero value or a bare `null` does the same.
	// Callers that want to preserve the existing value should omit the key.
	ConfidenceThreshold *float64 `json:"confidence_threshold"`
	// LookbackWindow accepts a Go-style duration string such as "720h" or
	// "336h30m". Empty clears the override. Must not exceed 365 days.
	LookbackWindow string `json:"lookback_window"`
	// Clear, when true, forces both overrides to nil regardless of the other
	// fields. Useful for "revert to server defaults".
	Clear bool `json:"clear"`
}

func (r *router) handleUpdateGatekeeperTuning(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	var input updateGatekeeperTuningRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}

	var (
		threshold *float64
		window    *time.Duration
	)
	if !input.Clear {
		threshold = input.ConfidenceThreshold
		if strings.TrimSpace(input.LookbackWindow) != "" {
			parsed, err := time.ParseDuration(strings.TrimSpace(input.LookbackWindow))
			if err != nil {
				writeError(w, http.StatusBadRequest, "lookback_window must be a Go duration string (e.g. 720h)")
				return
			}
			window = &parsed
		}
	}

	org, err := r.services.Agents.UpdateGatekeeperTuning(req.Context(), agent, threshold, window)
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, agents.ErrUnknownAgentOwner):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeServiceError(w, err, "update gatekeeper tuning failed")
		}
		return
	}

	auditMetadata := map[string]any{}
	if org.GatekeeperConfidenceThreshold != nil {
		auditMetadata["confidence_threshold"] = *org.GatekeeperConfidenceThreshold
	} else {
		auditMetadata["confidence_threshold"] = nil
	}
	if org.GatekeeperLookbackWindow != nil {
		auditMetadata["lookback_window"] = org.GatekeeperLookbackWindow.String()
	} else {
		auditMetadata["lookback_window"] = nil
	}
	if _, auditErr := r.services.Audit.Record(req.Context(), "org.gatekeeper_tuning_updated", "org", agent.OrgID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, auditMetadata); auditErr != nil {
		slog.Error("audit record failed", "op", "update_gatekeeper_tuning", "err", auditErr)
	}

	resp := map[string]any{
		"org_id": org.OrgID,
	}
	if org.GatekeeperConfidenceThreshold != nil {
		resp["confidence_threshold"] = *org.GatekeeperConfidenceThreshold
	} else {
		resp["confidence_threshold"] = nil
	}
	if org.GatekeeperLookbackWindow != nil {
		resp["lookback_window"] = org.GatekeeperLookbackWindow.String()
	} else {
		resp["lookback_window"] = nil
	}
	writeJSON(w, http.StatusOK, resp)
}

type applyRiskPolicyRequest struct {
	Name   string          `json:"name"`
	Source json.RawMessage `json:"source"`
}

func (r *router) handleApplyRiskPolicy(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.RiskPolicy == nil {
		writeError(w, http.StatusNotImplemented, "risk policy engine is not configured")
		return
	}

	var input applyRiskPolicyRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(input.Source) == 0 {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}

	policy, err := r.services.RiskPolicy.Apply(req.Context(), agent, strings.TrimSpace(input.Name), input.Source)
	if err != nil {
		switch {
		case errors.Is(err, riskpolicy.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeServiceError(w, err, "apply risk policy failed")
		}
		return
	}

	if _, auditErr := r.services.Audit.Record(req.Context(), "policy.applied", "risk_policy", policy.PolicyID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"policy_id": policy.PolicyID,
		"version":   policy.Version,
		"name":      policy.Name,
	}); auditErr != nil {
		slog.Error("audit record failed", "op", "apply_risk_policy", "err", auditErr)
	}

	writeJSON(w, http.StatusOK, riskPolicyJSON(policy))
}

func (r *router) handleListRiskPolicies(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.RiskPolicy == nil {
		writeError(w, http.StatusNotImplemented, "risk policy engine is not configured")
		return
	}

	limit, offset := parsePagination(req)
	policies, err := r.services.RiskPolicy.History(req.Context(), agent, limit, offset)
	if err != nil {
		writeServiceError(w, err, "list risk policies failed")
		return
	}

	items := make([]map[string]any, 0, len(policies))
	for _, policy := range policies {
		items = append(items, riskPolicyJSON(policy))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"policies":    items,
		"next_cursor": nextCursor(len(policies), limit, offset),
	})
}

func (r *router) handleActivateRiskPolicy(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.RiskPolicy == nil {
		writeError(w, http.StatusNotImplemented, "risk policy engine is not configured")
		return
	}

	// Path form: /v1/orgs/risk-policies/<id>/activate
	path := strings.TrimPrefix(req.URL.Path, "/v1/orgs/risk-policies/")
	path = strings.TrimSuffix(path, "/activate")
	policyID := strings.Trim(path, "/")
	if policyID == "" || !strings.HasSuffix(req.URL.Path, "/activate") {
		writeError(w, http.StatusNotFound, "policy id is required (POST /v1/orgs/risk-policies/:id/activate)")
		return
	}

	policy, err := r.services.RiskPolicy.Activate(req.Context(), agent, policyID)
	if err != nil {
		switch {
		case errors.Is(err, riskpolicy.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, storage.ErrRiskPolicyNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			if _, ok := err.(core.ForbiddenError); ok {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			writeServiceError(w, err, "activate risk policy failed")
		}
		return
	}

	if _, auditErr := r.services.Audit.Record(req.Context(), "policy.activated", "risk_policy", policy.PolicyID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"policy_id": policy.PolicyID,
		"version":   policy.Version,
	}); auditErr != nil {
		slog.Error("audit record failed", "op", "activate_risk_policy", "err", auditErr)
	}

	writeJSON(w, http.StatusOK, riskPolicyJSON(policy))
}

func riskPolicyJSON(policy core.RiskPolicy) map[string]any {
	out := map[string]any{
		"policy_id":  policy.PolicyID,
		"org_id":     policy.OrgID,
		"name":       policy.Name,
		"version":    policy.Version,
		"source":     json.RawMessage(policy.Source),
		"created_at": policy.CreatedAt,
	}
	if policy.ActiveAt != nil {
		out["active_at"] = *policy.ActiveAt
	}
	if policy.CreatedByUserID != "" {
		out["created_by_user_id"] = policy.CreatedByUserID
	}
	return out
}

type createActionRequest struct {
	RequestID   string         `json:"request_id"`
	Kind        string         `json:"kind"`
	Inputs      map[string]any `json:"inputs"`
	RiskLevel   string         `json:"risk_level"`
	RequestType string         `json:"request_type"`
}

func (r *router) handleCreateAction(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.Actions == nil {
		writeError(w, http.StatusNotImplemented, "operator phase is not configured")
		return
	}

	var input createActionRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(input.Kind) == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return
	}

	riskLevel := core.RiskLevel(input.RiskLevel)
	if riskLevel == "" {
		riskLevel = core.RiskLevelL1
	}

	action, err := r.services.Actions.CreateFromServicesParams(req.Context(), services.ActionCreateParams{
		OrgID:       agent.OrgID,
		OwnerUser:   user,
		OwnerAgent:  agent,
		RequestID:   input.RequestID,
		Kind:        core.ActionKind(input.Kind),
		Inputs:      input.Inputs,
		RiskLevel:   riskLevel,
		RequestType: input.RequestType,
	})
	if err != nil {
		switch {
		case errors.Is(err, actions.ErrOperatorNotEnabled):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, actions.ErrUnknownActionKind):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, actions.ErrActionPolicyDenied):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeServiceError(w, err, "create action failed")
		}
		return
	}

	if _, auditErr := r.services.Audit.Record(req.Context(), "action.created", "action", action.ActionID, action.OrgID, agent.AgentID, "", string(action.State), action.RiskLevel, nil, map[string]any{
		"kind":       string(action.Kind),
		"state":      string(action.State),
		"request_id": action.RequestID,
	}); auditErr != nil {
		slog.Error("audit record failed", "op", "create_action", "err", auditErr)
	}

	writeJSON(w, http.StatusOK, actionJSON(action))
}

func (r *router) handleListActions(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.Actions == nil {
		writeError(w, http.StatusNotImplemented, "operator phase is not configured")
		return
	}

	limit, offset := parsePagination(req)
	filter := storage.ActionFilter{Limit: limit, Offset: offset}
	if state := strings.TrimSpace(req.URL.Query().Get("state")); state != "" {
		filter.State = core.ActionState(state)
	}
	items, err := r.services.Actions.List(req.Context(), agent, filter)
	if err != nil {
		writeServiceError(w, err, "list actions failed")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, actionJSON(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"actions":     out,
		"next_cursor": nextCursor(len(items), limit, offset),
	})
}

// handleActionAction is the shared handler for /v1/actions/:id/{approve|cancel|execute}.
func (r *router) handleActionAction(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.Actions == nil {
		writeError(w, http.StatusNotImplemented, "operator phase is not configured")
		return
	}

	tail := strings.TrimPrefix(req.URL.Path, "/v1/actions/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeError(w, http.StatusNotFound, "POST /v1/actions/:id/{approve|cancel|execute}")
		return
	}
	actionID, action := parts[0], parts[1]

	var (
		result core.Action
		err    error
	)
	switch action {
	case "approve":
		result, err = r.services.Actions.Approve(req.Context(), agent, actionID)
	case "cancel":
		result, err = r.services.Actions.Cancel(req.Context(), agent, actionID)
	case "execute":
		result, err = r.services.Actions.Execute(req.Context(), agent, actionID)
	default:
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown action %q", action))
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, actions.ErrActionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, actions.ErrActionForbidden):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, actions.ErrActionNotExecutable):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeServiceError(w, err, "action "+action+" failed")
		}
		return
	}

	eventKind := "action." + action + "d"
	if action == "cancel" {
		eventKind = "action.cancelled"
	}
	if action == "execute" {
		if result.State == core.ActionStateExecuted {
			eventKind = "action.executed"
		} else {
			eventKind = "action.failed"
		}
	}
	if _, auditErr := r.services.Audit.Record(req.Context(), eventKind, "action", result.ActionID, result.OrgID, agent.AgentID, "", string(result.State), result.RiskLevel, nil, map[string]any{
		"kind":           string(result.Kind),
		"state":          string(result.State),
		"request_id":     result.RequestID,
		"failure_reason": result.FailureReason,
	}); auditErr != nil {
		slog.Error("audit record failed", "op", eventKind, "err", auditErr)
	}

	writeJSON(w, http.StatusOK, actionJSON(result))
}

type setOperatorEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (r *router) handleSetOperatorEnabled(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}
	if r.services.Actions == nil {
		writeError(w, http.StatusNotImplemented, "operator phase is not configured")
		return
	}

	var input setOperatorEnabledRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := r.services.Actions.SetOperatorEnabled(req.Context(), agent, input.Enabled); err != nil {
		writeServiceError(w, err, "set operator_enabled failed")
		return
	}
	if _, auditErr := r.services.Audit.Record(req.Context(), "user.operator_enabled_updated", "user", agent.OwnerUserID, agent.OrgID, agent.AgentID, "", "allow", core.RiskLevelL1, nil, map[string]any{
		"enabled": input.Enabled,
	}); auditErr != nil {
		slog.Error("audit record failed", "op", "set_operator_enabled", "err", auditErr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":          agent.OwnerUserID,
		"operator_enabled": input.Enabled,
	})
}

func actionJSON(action core.Action) map[string]any {
	out := map[string]any{
		"action_id":     action.ActionID,
		"org_id":        action.OrgID,
		"owner_user_id": action.OwnerUserID,
		"kind":          action.Kind,
		"state":         action.State,
		"risk_level":    action.RiskLevel,
		"created_at":    action.CreatedAt,
		"expires_at":    action.ExpiresAt,
	}
	if action.RequestID != "" {
		out["request_id"] = action.RequestID
	}
	if len(action.Inputs) > 0 {
		out["inputs"] = action.Inputs
	}
	if len(action.Result) > 0 {
		out["result"] = action.Result
	}
	if action.FailureReason != "" {
		out["failure_reason"] = action.FailureReason
	}
	if action.ExecutedAt != nil {
		out["executed_at"] = *action.ExecutedAt
	}
	return out
}

func (r *router) handleListPendingAgents(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	limit, offset := parsePagination(req)
	approvals, err := r.services.Agents.ListPendingAgentApprovals(req.Context(), agent.OrgID, agent.AgentID, limit, offset)
	if err != nil {
		switch {
		case errors.Is(err, agents.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeServiceError(w, err, "list pending agents failed")
		}
		return
	}

	items := make([]map[string]any, 0, len(approvals))
	for _, approval := range approvals {
		items = append(items, map[string]any{
			"approval_id":  approval.ApprovalID,
			"agent_id":     approval.AgentID,
			"org_id":       approval.OrgID,
			"requested_at": approval.RequestedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pending_agents": items,
		"next_cursor":    nextCursor(len(approvals), limit, offset),
	})
}

type reviewAgentRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func (r *router) handleReviewAgent(w http.ResponseWriter, req *http.Request) {
	callerAgent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	targetAgentID := strings.TrimPrefix(req.URL.Path, "/v1/orgs/agents/")
	targetAgentID = strings.TrimSuffix(targetAgentID, "/review")
	targetAgentID = strings.Trim(targetAgentID, "/")
	if targetAgentID == "" {
		writeError(w, http.StatusNotFound, "agent id is required")
		return
	}

	var input reviewAgentRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeDecodeError(w, err)
		return
	}
	if input.Decision != "approved" && input.Decision != "rejected" {
		writeError(w, http.StatusBadRequest, "decision must be 'approved' or 'rejected'")
		return
	}

	if err := r.services.Agents.ReviewAgentApproval(req.Context(), callerAgent.OrgID, targetAgentID, callerAgent.AgentID, input.Decision, input.Reason); err != nil {
		switch {
		case errors.Is(err, agents.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, agents.ErrAgentApprovalNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, agents.ErrUnknownAgent):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeServiceError(w, err, "review agent failed")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent_id": targetAgentID,
		"decision": input.Decision,
	})
}

func (r *router) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ip := r.clientIP(req)
		if !r.rateLimiter.allow(ip) {
			metrics.RecordRateLimitRejection("registration")
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// agentRateLimit limits per-agent request rates on heavy endpoints.
// It must be applied after requireAuth (needs agent in context).
func (r *router) agentRateLimit(limiterName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		agent, _, ok := currentActor(req)
		if ok {
			if !r.agentLimiter.allow(agent.AgentID) {
				metrics.RecordRateLimitRejection(limiterName)
				w.Header().Set("Retry-After", "60")
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

// clientIP returns the best-effort client IP, honouring X-Forwarded-For only
// when the request originated from a trusted proxy.
func (r *router) clientIP(req *http.Request) string {
	return clientIPFromRequest(req, r.trustedProxies)
}

// clientIPFromRequest is the package-level form of clientIP so that exported
// middleware (e.g. NewAdminSignInLimiter) can reuse the same logic without
// holding a *router reference.
func clientIPFromRequest(req *http.Request, trustedProxies []*net.IPNet) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		// RemoteAddr has no port (unusual but possible in tests).
		host = req.RemoteAddr
	}
	remoteIP := net.ParseIP(host)

	// If the remote address is a trusted proxy, use X-Forwarded-For.
	if len(trustedProxies) > 0 && remoteIP != nil {
		for _, cidr := range trustedProxies {
			if cidr.Contains(remoteIP) {
				xff := req.Header.Get("X-Forwarded-For")
				if xff == "" {
					// Trusted proxy sent no XFF — fall back to the proxy's own IP
					// rather than returning an empty string.
					return host
				}
				return xffClientIP(xff, trustedProxies)
			}
		}
	}
	return host
}

// xffClientIP walks X-Forwarded-For from right to left and returns the
// rightmost IP that is NOT in the trusted-proxies list.
func xffClientIP(xff string, trusted []*net.IPNet) string {
	if xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		inTrusted := false
		for _, cidr := range trusted {
			if cidr.Contains(ip) {
				inTrusted = true
				break
			}
		}
		if !inTrusted {
			return candidate
		}
	}
	return strings.TrimSpace(parts[0])
}

func (r *router) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		// Add CSP for the JSON API: disallow everything except the response itself.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		// Emit HSTS when the server is TLS-terminated or the request arrived via HTTPS.
		if r.tlsTerminated || req.Header.Get("X-Forwarded-Proto") == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, req)
	})
}

// responseCapture wraps http.ResponseWriter to capture the status code and bytes written.
type responseCapture struct {
	http.ResponseWriter
	status int
	size   int
}

func (rc *responseCapture) WriteHeader(status int) {
	rc.status = status
	rc.ResponseWriter.WriteHeader(status)
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	n, err := rc.ResponseWriter.Write(b)
	rc.size += n
	return n, err
}

// requestID reads X-Request-ID from the incoming request (or generates a new
// one) and injects it into the context and the response header. It also stores
// a slog.Logger pre-seeded with the request ID under loggerContextKey so that
// handlers can call LoggerFromContext(ctx) without repeating the attribute.
func (r *router) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reqID := strings.TrimSpace(req.Header.Get("X-Request-ID"))
		if reqID == "" {
			reqID = id.New("req")
		}
		// Propagate W3C trace context headers to the outgoing response so
		// downstream callers can correlate traces.
		if tp := req.Header.Get("Traceparent"); tp != "" {
			w.Header().Set("Traceparent", tp)
		}
		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(req.Context(), requestIDContextKey{}, reqID)
		logger := slog.Default().With("request_id", reqID)
		ctx = context.WithValue(ctx, loggerContextKey{}, logger)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// accessLog wraps the mux with a per-request structured log line.
// Health-check endpoints are skipped to keep logs clean.
func (r *router) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Skip the health probes to avoid flooding access logs.
		if req.URL.Path == "/healthz" || req.URL.Path == "/livez" || req.URL.Path == "/readyz" {
			next.ServeHTTP(w, req)
			return
		}
		start := time.Now()
		rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}

		// Inject a mutable agent-ID slot into the context. requireAuth will
		// backfill it after a successful authentication so the log line sees
		// the real agent ID even though auth runs in a derived request context.
		holder := &agentIDHolder{}
		ctx := context.WithValue(req.Context(), agentIDHolderKey{}, holder)
		next.ServeHTTP(rc, req.WithContext(ctx))
		dur := time.Since(start)

		agentID := holder.id
		reqID, _ := req.Context().Value(requestIDContextKey{}).(string)

		level := slog.LevelInfo
		if rc.status >= 500 {
			level = slog.LevelError
		} else if rc.status == http.StatusTooManyRequests ||
			rc.status == http.StatusUnauthorized ||
			rc.status == http.StatusForbidden {
			level = slog.LevelWarn
		}

		slog.Log(req.Context(), level, "http request",
			"method", req.Method,
			"path", req.URL.Path,
			"status", rc.status,
			"duration_ms", dur.Milliseconds(),
			"bytes_out", rc.size,
			"ip", r.clientIP(req),
			"request_id", reqID,
			"agent_id", agentID,
		)
	})
}

// RequestIDFromContext returns the request ID stored by the requestID middleware,
// or empty string when called outside a request context.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDContextKey{}).(string)
	return v
}

// LoggerFromContext returns a slog.Logger pre-seeded with the request ID, or
// slog.Default() when called outside a request context.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerContextKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

func (r *router) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
		next.ServeHTTP(w, req)
	})
}

func (r *router) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		accessToken, ok := accessTokenFromRequest(req)
		if !ok {
			writeAuthError(w, "missing bearer token")
			return
		}

		agent, user, err := r.services.Agents.AuthenticateAgent(req.Context(), accessToken)
		if err != nil {
			switch {
			case errors.Is(err, agents.ErrUnknownAgentToken),
				errors.Is(err, agents.ErrInvalidAgentToken),
				errors.Is(err, agents.ErrExpiredAgentToken),
				errors.Is(err, agents.ErrRevokedAgentToken),
				errors.Is(err, agents.ErrUnknownAgent),
				errors.Is(err, agents.ErrUnknownAgentOwner):
				writeAuthError(w, "invalid or expired access token")
				return
			default:
				writeError(w, http.StatusInternalServerError, "agent authentication failed")
				return
			}
		}

		ctx := context.WithValue(req.Context(), currentAgentContextKey{}, agent)
		ctx = context.WithValue(ctx, currentUserContextKey{}, user)
		// Backfill the agent ID into the access-log holder if present.
		if h, ok := ctx.Value(agentIDHolderKey{}).(*agentIDHolder); ok && h != nil {
			h.id = agent.AgentID
		}
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// requireVerifiedAuth is like requireAuth but also rejects agents with
// status pending_email_verification, pending_admin_approval, or rejected with HTTP 403.
func (r *router) requireVerifiedAuth(next http.Handler) http.Handler {
	return r.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		agent, _, ok := currentActor(req)
		if !ok {
			writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
			return
		}
		switch agent.Status {
		case core.AgentStatusPendingEmailVerification:
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":   "email_verification_required",
				"message": "complete email verification before using this endpoint",
			})
			return
		case core.AgentStatusPendingAdminApproval:
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":   "admin_approval_pending",
				"message": "awaiting org admin approval — ask an org admin to run: alice review_agent " + agent.AgentID + " approved",
			})
			return
		case core.AgentStatusRejected:
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":   "agent_rejected",
				"message": "this agent has been rejected by an org admin",
			})
			return
		}
		next.ServeHTTP(w, req)
	}))
}

func currentActor(req *http.Request) (core.Agent, core.User, bool) {
	agent, ok := req.Context().Value(currentAgentContextKey{}).(core.Agent)
	if !ok {
		return core.Agent{}, core.User{}, false
	}
	user, ok := req.Context().Value(currentUserContextKey{}).(core.User)
	if !ok {
		return core.Agent{}, core.User{}, false
	}
	return agent, user, true
}

func accessTokenFromRequest(req *http.Request) (string, bool) {
	authorization := strings.TrimSpace(req.Header.Get("Authorization"))
	if authorization == "" {
		return "", false
	}
	prefix := "bearer "
	if len(authorization) < len(prefix) || !strings.EqualFold(authorization[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(authorization[len(prefix):]), true
}

// parsePagination reads ?limit= and ?cursor= from the request.
// limit defaults to 50 and is capped at 200. cursor is a base64-encoded offset.
func parsePagination(req *http.Request) (limit, offset int) {
	limit = 50
	if raw := req.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	offset = 0
	if cursor := req.URL.Query().Get("cursor"); cursor != "" {
		if decoded, err := base64.StdEncoding.DecodeString(cursor); err == nil {
			if n, err := strconv.Atoi(string(decoded)); err == nil && n > 0 {
				offset = n
			}
		}
	}
	return limit, offset
}

// nextCursor returns a cursor string pointing to the next page, or empty string
// when results are fewer than limit (indicating the last page).
func nextCursor(count, limit, offset int) string {
	if count < limit {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]any{
		"error": message,
	})
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="alice"`)
	writeError(w, http.StatusUnauthorized, message)
}

func trimActionPath(path, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	value := strings.TrimPrefix(path, prefix)
	value = strings.TrimSuffix(value, suffix)
	value = strings.Trim(value, "/")
	return value, value != ""
}

func writeServiceError(w http.ResponseWriter, err error, fallback string) {
	if core.IsValidationError(err) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if core.IsForbiddenError(err) {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, fallback)
}

// handleDeleteSelf soft-deletes the calling agent and its user account.
// All tokens are revoked first; the response is 204 No Content.
func (r *router) handleDeleteSelf(w http.ResponseWriter, req *http.Request) {
	agent, _, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	if err := r.services.Agents.DeleteSelf(req.Context(), agent); err != nil {
		writeServiceError(w, err, "delete self failed")
		return
	}

	if _, auditErr := r.services.Audit.Record(
		req.Context(),
		"user.deleted_self", "user", agent.OwnerUserID,
		agent.OrgID, agent.AgentID, "", "allow",
		core.RiskLevelL1, nil, nil,
	); auditErr != nil {
		slog.Error("audit record failed", "op", "delete_self", "err", auditErr)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteOrg soft-deletes the org identified by the trailing slug in the
// URL path. The caller must be an org admin whose org matches the slug.
func (r *router) handleDeleteOrg(w http.ResponseWriter, req *http.Request) {
	agent, user, ok := currentActor(req)
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing authenticated actor context")
		return
	}

	slug := strings.TrimPrefix(req.URL.Path, "/v1/orgs/")
	slug = strings.Trim(slug, "/")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "org slug is required in path")
		return
	}

	if err := r.services.Agents.DeleteOrg(req.Context(), agent, user, slug); err != nil {
		switch {
		case errors.Is(err, agents.ErrNotOrgAdmin):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, storage.ErrOrgNotFound):
			writeError(w, http.StatusNotFound, "org not found")
		case core.IsForbiddenError(err):
			writeError(w, http.StatusForbidden, err.Error())
		default:
			writeServiceError(w, err, "delete org failed")
		}
		return
	}

	if _, auditErr := r.services.Audit.Record(
		req.Context(),
		"org.deleted", "org", agent.OrgID,
		agent.OrgID, agent.AgentID, "", "allow",
		core.RiskLevelL1, nil, nil,
	); auditErr != nil {
		slog.Error("audit record failed", "op", "delete_org", "err", auditErr)
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON body")
}