package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/approvals"
	"alice/internal/artifacts"
	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/httpapi"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/requests"
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
	agentService := agents.NewService(repos, repos, repos, repos, repos, cfg, repos)
	artifactService := artifacts.NewService(repos)
	policyService := policy.NewService(repos)
	queryService := queries.NewService(repos, artifactService, policyService, repos, repos)
	requestService := requests.NewService(repos, repos, repos)
	approvalService := approvals.NewService(repos, repos, repos, repos)
	auditService := audit.NewService(repos)

	return services.Container{
		Agents:    agentService,
		Artifacts: artifactService,
		Policy:    policyService,
		Queries:   queryService,
		Requests:  requestService,
		Approvals: approvalService,
		Audit:     auditService,
	}
}
