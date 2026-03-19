package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"alice/internal/app"
	"alice/internal/config"
)

func main() {
	cfg := config.FromEnv()
	server := app.NewServer(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}()

	log.Printf("alice coordination server listening on %s", cfg.ListenAddr)
	if err := server.Start(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server exited: %v", err)
	}
}
