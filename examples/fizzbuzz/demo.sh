#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

echo "=== alice fizzbuzz demo setup ==="
echo ""

# 1. Build the MCP server
if [ ! -x bin/alice-mcp-server ]; then
  echo "--- Building MCP server ---"
  make build-mcp-server
  echo ""
fi

# 2. Ensure examples/fizzbuzz exists
mkdir -p examples/fizzbuzz

echo "Setup complete. Now run:"
echo ""
echo "  opencode"
echo ""
echo "Then type this prompt in the TUI:"
echo ""
echo "  Create a fizzbuzz web app at examples/fizzbuzz/index.html."
echo "  Count 1-100: Fizz for 3, Buzz for 5, FizzBuzz for 15."
echo "  Nice CSS: dark background, glassmorphism card, color-coded cells."
echo "  Before you start, call register_agent to set up an alice session."
echo "  Then publish a status_delta at start and finish via publish_artifact."
echo ""
echo "Registration params (or ask the user):"
echo "  org_slug: demo"
echo "  owner_email: demo@example.com"
echo "  agent_name: opencode-demo"
echo "  client_type: opencode"
echo ""
echo "Expected flow:"
echo "  1. Agent calls register_agent"
echo "  2. Agent calls publish_artifact (status_delta: started)"
echo "  3. Agent creates examples/fizzbuzz/index.html"
echo "  4. Agent calls publish_artifact (status_delta: completed)"
echo ""
echo "Serve the result: python3 -m http.server 8080 -d examples/fizzbuzz"
