package app

import (
	"context"
	"net/http"
	"time"

	"alice/internal/agents"
	"alice/internal/app/services"
	"alice/internal/artifacts"
	"alice/internal/audit"
	"alice/internal/config"
	"alice/internal/httpapi"
	"alice/internal/policy"
	"alice/internal/queries"
	"alice/internal/storage/memory"
)

type Server struct {
	httpServer *http.Server
}

func NewServer(cfg config.Config) *Server {
	store := memory.New()
	agentService := agents.NewService(store, cfg)
	artifactService := artifacts.NewService(store)
	policyService := policy.NewService(store)
	queryService := queries.NewService(store, artifactService, policyService)
	auditService := audit.NewService(store)

	container := services.Container{
		Agents:    agentService,
		Artifacts: artifactService,
		Policy:    policyService,
		Queries:   queryService,
		Audit:     auditService,
	}

	return &Server{
		httpServer: &http.Server{
			Addr:              cfg.ListenAddr,
			Handler:           httpapi.NewRouter(container),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
