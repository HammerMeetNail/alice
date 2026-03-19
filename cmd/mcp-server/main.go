package main

import (
	"context"
	"log"
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
		log.Fatalf("mcp bootstrap failed: %v", err)
	}
	if closeFn != nil {
		defer func() {
			if err := closeFn(); err != nil {
				log.Printf("mcp shutdown error: %v", err)
			}
		}()
	}

	server := mcp.NewServer(
		httpapi.NewRouter(container),
		mcp.WithAccessToken(os.Getenv("ALICE_MCP_ACCESS_TOKEN")),
	)

	if err := server.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("mcp server exited: %v", err)
	}
}
