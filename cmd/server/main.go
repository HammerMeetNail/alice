package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"alice/internal/app"
	"alice/internal/config"
)

func main() {
	cfg := config.FromEnv()
	server, err := app.NewServer(cfg)
	if err != nil {
		slog.Error("server bootstrap failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown error", "err", err)
		}
	}()

	slog.Info("alice coordination server listening", "addr", cfg.ListenAddr)
	if err := server.Start(); err != nil && err != http.ErrServerClosed {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
