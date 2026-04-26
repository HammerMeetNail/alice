#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

echo "=== alice fizzbuzz demo ==="
echo ""

# 1. Build binaries
if [ ! -x bin/alice-mcp-server ] || [ ! -x bin/alice ]; then
  echo "--- Building binaries ---"
  make build-mcp-server build-cli
  echo ""
fi

echo "Setup complete. Two ways to run:"
echo ""

echo "=== Option A: without a coordination server ==="
echo "  opencode"
echo ""
echo "  Then type: 'Create a fizzbuzz web app at examples/fizzbuzz/index.html.'"
echo ""
echo "  The agent uses alice MCP tools (in-memory store). No server needed."
echo ""

echo "=== Option B: with a coordination server (multi-user) ==="
echo "  1. Start the server:  make local"
echo "  2. export ALICE_SERVER_URL=http://localhost:8080"
echo "  3. opencode"
echo ""
echo "  Then type: 'Create a fizzbuzz web app at examples/fizzbuzz/index.html.'"
echo ""
echo "  The alice-auto plugin handles registration automatically."
echo "  The agent publishes status_delta at task boundaries per AGENTS.md."
echo "  Teammates can query status via alice query or query_peer_status."
echo ""

echo "Serve the result:"
echo "  python3 -m http.server 8080 -d examples/fizzbuzz"
