package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"alice/internal/app"
	"alice/internal/config"
	"alice/internal/httpapi"
	"alice/internal/mcp"
	"alice/internal/tracker"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accessToken := os.Getenv("ALICE_MCP_ACCESS_TOKEN")

	serverURL := strings.TrimSpace(os.Getenv("ALICE_SERVER_URL"))
	if serverURL != "" {
		server := mcp.NewServer(
			nil,
			mcp.WithServerURL(serverURL, os.Getenv("ALICE_SERVER_TLS_CA")),
			mcp.WithAccessToken(accessToken),
		)
		startTracker(ctx, server)
		if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
			slog.Error("mcp server exited", "err", err)
			os.Exit(1)
		}
		return
	}

	cfg := config.FromEnv()
	container, closeFn, err := app.NewContainer(cfg)
	if err != nil {
		slog.Error("mcp bootstrap failed", "err", err)
		os.Exit(1)
	}
	if closeFn != nil {
		defer func() {
			if err := closeFn(); err != nil {
				slog.Error("mcp shutdown error", "err", err)
			}
		}()
	}

	server := mcp.NewServer(
		httpapi.NewRouter(container),
		mcp.WithAccessToken(accessToken),
	)
	startTracker(ctx, server)

	if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
		slog.Error("mcp server exited", "err", err)
		os.Exit(1)
	}
}

func startTracker(ctx context.Context, server *mcp.Server) {
	trackerCfg, enabled := tracker.ConfigFromEnv()
	if !enabled {
		return
	}

	reg := mcp.TrackerRegistration{
		OrgSlug:    trackerCfg.OrgSlug,
		OwnerEmail: trackerCfg.OwnerEmail,
		AgentName:  trackerCfg.AgentName,
		ClientType: trackerCfg.ClientType,
	}

	t := tracker.New(
		trackerCfg,
		server.PublishArtifact,
		func(ctx context.Context) error { return server.AutoRegister(ctx, reg) },
		server.HasSession,
	)
	go t.Run(ctx)
}
