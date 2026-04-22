// Command alice is the first-class command-line surface for the alice
// coordination platform. It is the preferred way for human operators and
// non-MCP agents to register, publish artifacts, query peers, grant
// permissions, and respond to requests.
//
// The MCP server (cmd/mcp-server) and edge agent (cmd/edge-agent) remain
// available for MCP-native clients and long-running connectors, but every
// capability exposed by alice is reachable through this binary too.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"alice/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}
