package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/approvals"
	"alice/internal/artifacts"
	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/email"
	"alice/internal/gatekeeper"
	"alice/internal/httpapi"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/requests"
	"alice/internal/riskpolicy"
	"alice/internal/storage"
	"alice/internal/storage/memory"
	"alice/internal/storage/postgres"
)

type Server struct {
	httpServer *http.Server
	closeFn    func() error
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
	storage.Transactor
}

func NewServer(cfg config.Config) (*Server, error) {
	container, closeFn, err := NewContainer(cfg)
	if err != nil {
		return nil, err
	}

	return &Server{
		httpServer: &http.Server{
			Addr:              cfg.ListenAddr,
			Handler:           httpapi.NewRouter(container),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
		closeFn: closeFn,
	}, nil
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	shutdownErr := s.httpServer.Shutdown(ctx)
	if s.closeFn == nil {
		return shutdownErr
	}
	return errors.Join(shutdownErr, s.closeFn())
}

func NewContainer(cfg config.Config) (services.Container, func() error, error) {
	if cfg.DatabaseURL != "" {
		store, err := postgres.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			return services.Container{}, nil, fmt.Errorf("open postgres store: %w", err)
		}
		if err := store.Migrate(context.Background()); err != nil {
			_ = store.Close()
			return services.Container{}, nil, fmt.Errorf("migrate postgres store: %w", err)
		}
		return buildContainer(store, cfg), store.Close, nil
	}

	store := memory.New()
	return buildContainer(store, cfg), nil, nil
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
	queryService := queries.NewService(repos, artifactService, policyService, repos, repos).
		WithRiskPolicyEvaluator(riskPolicyService.AsQueriesEvaluator())
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

	return services.Container{
		Agents:     agentService,
		Artifacts:  artifactService,
		Policy:     policyService,
		Queries:    queryService,
		Requests:   requestService,
		Approvals:  approvalService,
		Audit:      auditService,
		RiskPolicy: riskPolicyService,
	}
}
