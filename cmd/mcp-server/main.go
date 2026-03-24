package main

import (
	"context"
	"log/slog"
	"os"

	"alice/internal/app"
	"alice/internal/config"
	"alice/internal/httpapi"
	"alice/internal/mcp"
)

func main() {
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
		mcp.WithAccessToken(os.Getenv("ALICE_MCP_ACCESS_TOKEN")),
	)

	if err := server.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		slog.Error("mcp server exited", "err", err)
		os.Exit(1)
	}
}
