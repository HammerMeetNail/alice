#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

DB_URL="postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable"
ALICE_PORT="${ALICE_LISTEN_ADDR:-:8080}"
ALICE_PORT="${ALICE_PORT#:}"

echo "=== alice fizzbuzz demo ==="
echo ""

# 1. Build binaries
if [ ! -x bin/alice-mcp-server ] || [ ! -x bin/alice ]; then
  echo "--- Building binaries ---"
  make build-mcp-server build-cli
  echo ""
fi

# 2. Ensure Postgres is running
echo "--- Starting Postgres ---"
if podman inspect alice-db >/dev/null 2>&1; then
  podman start alice-db >/dev/null 2>&1 || true
else
  podman run -d --name alice-db -p 5432:5432 \
    -e POSTGRES_USER=alice -e POSTGRES_PASSWORD=alice -e POSTGRES_DB=alice \
    docker.io/library/postgres:16-alpine
fi

for i in $(seq 1 30); do
  if podman exec alice-db pg_isready -U alice >/dev/null 2>&1; then
    break
  fi
  if [ $i -eq 30 ]; then
    echo "error: Postgres did not become ready" >&2
    exit 1
  fi
  sleep 0.3
done

# 3. Start coordination server with Postgres
echo "--- Starting coordination server on :$ALICE_PORT ---"
ALICE_LISTEN_ADDR=":$ALICE_PORT" ALICE_DATABASE_URL="$DB_URL" go run ./cmd/server &
SERVER_PID=$!
trap 'kill $SERVER_PID 2>/dev/null; echo ""; echo "--- server stopped ---"' EXIT

for i in $(seq 1 30); do
  if curl -sf "http://localhost:$ALICE_PORT/healthz" >/dev/null 2>&1; then
    break
  fi
  if [ $i -eq 30 ]; then
    echo "error: server did not become ready" >&2
    exit 1
  fi
  sleep 0.3
done

export ALICE_SERVER_URL="http://localhost:$ALICE_PORT"

echo ""
echo "Server ready. Launching OpenCode..."
echo ""
echo "Type this prompt in the TUI:"
echo "  Create a fizzbuzz web app at examples/fizzbuzz/index.html."
echo "  Count 1-100: Fizz for 3, Buzz for 5, FizzBuzz for 15."
echo "  Nice CSS: dark background, glassmorphism card, color-coded cells."
echo ""
echo "What happens:"
echo "  - alice-auto plugin registers with the server"
echo "  - Agent builds fizzbuzz, publishes status at milestones"
echo "  - Status persists in Postgres (survives restarts)"
echo ""

# 4. Launch OpenCode
opencode

echo ""
echo "--- demo complete ---"
echo "Server stopped. Status is persistent in Postgres."
echo "To resume: make demo"
echo ""
echo "Serve the result: python3 -m http.server 8080 -d examples/fizzbuzz"
echo ""
echo "Teardown (remove Postgres data): podman rm -f alice-db"
