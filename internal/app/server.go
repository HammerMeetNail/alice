package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"alice/internal/actions"
	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/approvals"
	"alice/internal/artifacts"
	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/email"
	"alice/internal/gatekeeper"
	"alice/internal/httpapi"
	"alice/internal/metrics"
	"alice/internal/orggraph"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/requests"
	"alice/internal/riskpolicy"
	"alice/internal/storage"
	"alice/internal/storage/memory"
	"alice/internal/storage/postgres"
	"alice/internal/webui"
	"alice/internal/websession"

	"github.com/prometheus/client_golang/prometheus"
)

type Server struct {
	httpServer     *http.Server
	metricsServer  *http.Server
	closeFn        func() error
}

type repositories interface {
	storage.OrganizationRepository
	storage.UserRepository
	storage.AgentRepository
	storage.AgentRegistrationChallengeRepository
	storage.AgentTokenRepository
	storage.ArtifactRepository
	storage.PolicyGrantRepository
	storage.QueryRepository
	storage.RequestRepository
	storage.ApprovalRepository
	storage.AuditRepository
	storage.EmailVerificationRepository
	storage.AgentApprovalRepository
	storage.RiskPolicyRepository
	storage.ActionRepository
	storage.UserPreferencesRepository
	storage.OrgGraphRepository
	storage.Transactor
}

func NewServer(cfg config.Config) (*Server, error) {
	container, repos, pgStore, closeFn, err := newContainerWithRepos(cfg)
	if err != nil {
		return nil, err
	}

	handler, err := buildHTTPHandler(cfg, container, repos, pgStore)
	if err != nil {
		if closeFn != nil {
			_ = closeFn()
		}
		return nil, err
	}

	srv := &Server{
		httpServer: &http.Server{
			Addr:              cfg.ListenAddr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
		closeFn: closeFn,
	}

	// Start a separate Prometheus metrics listener when configured.
	if cfg.MetricsAddr != "" {
		var db metrics.DBStatsGetter
		if pgStore != nil {
			db = pgStore
		}
		if regErr := metrics.Register(prometheus.DefaultRegisterer, db); regErr != nil {
			slog.Warn("metrics registration failed; some metrics will be unavailable", "err", regErr)
		}
		srv.metricsServer = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           metrics.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}

	return srv, nil
}

// buildHTTPHandler composes the JSON API with the optional admin UI. When
// the admin UI is disabled the JSON API handler is returned as-is, so
// /admin/* falls through to the default 404 behaviour of the API mux.
func buildHTTPHandler(cfg config.Config, container services.Container, repos repositories, pgStore *postgres.Store) (http.Handler, error) {
	var pinger func(ctx context.Context) error
	if pgStore != nil {
		pinger = pgStore.Ping
	}

	apiHandler := httpapi.NewRouter(httpapi.RouterOptions{
		Services:              container,
		Pinger:                pinger,
		TLSTerminated:         cfg.TLSTerminated,
		TrustedProxies:        httpapi.ParseCIDRs(cfg.TrustedProxies),
		AgentRatePerMin:       cfg.RateLimitAgentPerMin,
		AdminSignInRatePerMin: cfg.RateLimitAdminSignInPerMin,
	})
	if !cfg.AdminUIEnabled {
		return apiHandler, nil
	}

	sender := email.NewSenderFromConfig(cfg)
	if sender == nil {
		return nil, errors.New("admin UI is enabled but SMTP is not configured; set ALICE_SMTP_HOST to send sign-in codes or ALICE_SMTP_HOST=noop for local development")
	}

	sessionSvc := websession.NewService(websession.Options{
		Lookup:        webui.NewAdminLookup(adminRepoAdapter{repos: repos}),
		Mailer:        sender,
		SessionTTL:    cfg.AdminUISessionTTL,
		SignInTTL:     cfg.AdminUISignInTTL,
		CookieSecure:  !cfg.AdminUIDevMode,
		SessionCookie: "alice_admin_session",
		CSRFCookie:    "alice_admin_csrf",
		CookiePath:    "/admin",
	})

	adminHandler, err := webui.NewHandler(webui.Options{
		Sessions:         sessionSvc,
		Services:         adminServices{container: container},
		AllowedOrigins:   cfg.AdminUIAllowedOrigins,
		DevMode:          cfg.AdminUIDevMode,
		SignInRatePerMin: cfg.RateLimitAdminSignInPerMin,
		TrustedProxies:   httpapi.ParseCIDRs(cfg.TrustedProxies),
	})
	if err != nil {
		return nil, fmt.Errorf("build admin UI: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/admin/", adminHandler)
	mux.Handle("/", apiHandler)
	return mux, nil
}

// adminRepoAdapter narrows the full repositories interface down to the
// AdminRepo surface the webui lookup needs.
type adminRepoAdapter struct {
	repos repositories
}

func (a adminRepoAdapter) FindOrganizationBySlug(ctx context.Context, slug string) (core.Organization, bool, error) {
	return a.repos.FindOrganizationBySlug(ctx, slug)
}

func (a adminRepoAdapter) FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error) {
	return a.repos.FindUserByEmail(ctx, orgID, email)
}

func (a adminRepoAdapter) FindAgentByUserID(ctx context.Context, userID string) (core.Agent, bool, error) {
	return a.repos.FindAgentByUserID(ctx, userID)
}

// adminServices adapts services.Container to webui.Services. It exposes
// exactly the admin-gated methods the UI needs, routed through the same
// service instances the JSON API uses.
type adminServices struct {
	container services.Container
}

func (a adminServices) ListPendingAgentApprovals(ctx context.Context, orgID, callerAgentID string, limit, offset int) ([]core.AgentApproval, error) {
	return a.container.Agents.ListPendingAgentApprovals(ctx, orgID, callerAgentID, limit, offset)
}

func (a adminServices) ReviewAgentApproval(ctx context.Context, orgID, targetAgentID, callerAgentID, decision, reason string) error {
	return a.container.Agents.ReviewAgentApproval(ctx, orgID, targetAgentID, callerAgentID, decision, reason)
}

func (a adminServices) RotateInviteToken(ctx context.Context, orgID, callerAgentID string) (string, error) {
	return a.container.Agents.RotateInviteToken(ctx, orgID, callerAgentID)
}

func (a adminServices) AuditSummary(ctx context.Context, agentID string, since time.Time, limit, offset int, filter audit.SummaryFilter) ([]core.AuditEvent, error) {
	return a.container.Audit.Summary(ctx, agentID, since, limit, offset, filter)
}

func (a adminServices) RecordAudit(ctx context.Context, eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) error {
	_, err := a.container.Audit.Record(ctx, eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision, riskLevel, policyBasis, metadata)
	return err
}

func (s *Server) Start() error {
	if s.metricsServer != nil {
		go func() {
			slog.Info("metrics listener starting", "addr", s.metricsServer.Addr)
			if err := s.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics listener stopped", "err", err)
			}
		}()
	}
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error
	errs = append(errs, s.httpServer.Shutdown(ctx))
	if s.metricsServer != nil {
		errs = append(errs, s.metricsServer.Shutdown(ctx))
	}
	if s.closeFn != nil {
		errs = append(errs, s.closeFn())
	}
	return errors.Join(errs...)
}

func NewContainer(cfg config.Config) (services.Container, func() error, error) {
	container, _, _, closeFn, err := newContainerWithRepos(cfg)
	return container, closeFn, err
}

// newContainerWithRepos is like NewContainer but also returns the
// repositories handle so the admin UI can reach the storage layer for
// the org-slug lookup the JSON surface doesn't expose today.
// pgStore is non-nil only when PostgreSQL is in use; it is used for
// health-check pinging and DB-pool metrics.
func newContainerWithRepos(cfg config.Config) (services.Container, repositories, *postgres.Store, func() error, error) {
	if cfg.DatabaseURL != "" {
		store, err := postgres.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			return services.Container{}, nil, nil, nil, fmt.Errorf("open postgres store: %w", err)
		}
		if err := store.Migrate(context.Background()); err != nil {
			_ = store.Close()
			return services.Container{}, nil, nil, nil, fmt.Errorf("migrate postgres store: %w", err)
		}
		return buildContainer(store, cfg), store, store, store.Close, nil
	}

	store := memory.New()
	return buildContainer(store, cfg), store, nil, nil, nil
}

func buildContainer(repos repositories, cfg config.Config) services.Container {
	agentService := agents.NewService(repos, repos, repos, repos, repos, cfg, repos).
		WithApprovalRepository(repos)

	// Wire up email sender when SMTP is configured.
	if sender := email.NewSenderFromConfig(cfg); sender != nil {
		agentService = agentService.WithEmailSender(sender, repos)
	}

	artifactService := artifacts.NewService(repos)
	policyService := policy.NewService(repos)
	riskPolicyService := riskpolicy.NewService(repos, repos)
	orgGraphService := orggraph.NewService(repos, repos)
	queryService := queries.NewService(repos, artifactService, policyService, repos, repos).
		WithRiskPolicyEvaluator(riskPolicyService.AsQueriesEvaluator()).
		WithOrgGraph(orgGraphService.AsEvaluator())
	gatekeeperService := gatekeeper.NewService(queryService, gatekeeper.Options{
		ConfidenceThreshold: cfg.GatekeeperConfidenceThreshold,
		LookbackWindow:      cfg.GatekeeperLookbackWindow,
	}).WithOrgLookup(repos)
	approvalService := approvals.NewService(repos, repos, repos, repos)

	var auditSinks []audit.Sink
	if cfg.AuditLogFile != "" {
		f, err := os.OpenFile(cfg.AuditLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			slog.Error("failed to open audit log file", "path", cfg.AuditLogFile, "err", err)
		} else {
			auditSinks = append(auditSinks, audit.NewJSONSink(f))
			slog.Info("audit log file configured", "path", cfg.AuditLogFile)
		}
	}
	auditService := audit.NewService(repos, auditSinks...)

	requestService := requests.NewService(repos, repos, repos).
		WithAutoAnswerer(gatekeeperService.AsRequestsAutoAnswerer()).
		WithAuditRecorder(auditService)

	actionService := actions.NewService(repos, repos, repos, repos).
		WithRiskPolicyEvaluator(riskPolicyService).
		WithExecutor(actions.NewAcknowledgeBlockerExecutor(repos))

	return services.Container{
		Agents:     agentService,
		Artifacts:  artifactService,
		Policy:     policyService,
		Queries:    queryService,
		Requests:   requestService,
		Approvals:  approvalService,
		Audit:      auditService,
		RiskPolicy: riskPolicyService,
		Actions:    actionService,
		OrgGraph:   orgGraphService,
	}
}
