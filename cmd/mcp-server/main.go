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
)

func main() {
	accessToken := os.Getenv("ALICE_MCP_ACCESS_TOKEN")

	serverURL := strings.TrimSpace(os.Getenv("ALICE_SERVER_URL"))
	if serverURL != "" {
		// Remote mode: forward all tool calls to a remote coordination server.
		// No local database or embedded stack is needed.
		server := mcp.NewServer(
			nil,
			mcp.WithServerURL(serverURL, os.Getenv("ALICE_SERVER_TLS_CA")),
			mcp.WithAccessToken(accessToken),
		)
		if err := server.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
			slog.Error("mcp server exited", "err", err)
			os.Exit(1)
		}
		return
	}

	// Embedded mode: run the full coordination stack in-process.
	// Uses PostgreSQL when ALICE_DATABASE_URL is set, otherwise in-memory.
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

	if err := server.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		slog.Error("mcp server exited", "err", err)
		os.Exit(1)
	}
}
