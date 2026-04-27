#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

DB_URL="postgres://alice:alice@127.0.0.1:5432/alice?sslmode=disable"
ALICE_PORT="${ALICE_LISTEN_ADDR:-:8080}"
ALICE_PORT="${ALICE_PORT#:}"

# Ensure port is free (kill any stale server from a prior run)
fuser -k "$ALICE_PORT/tcp" 2>/dev/null || true

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
echo "=== Server ready on http://localhost:$ALICE_PORT ==="
echo ""
echo "Keep this terminal running. In a NEW terminal, run:"
echo ""
echo "  export ALICE_SERVER_URL=http://localhost:$ALICE_PORT"
echo "  opencode"
echo ""
echo "Then type this prompt in the opencode TUI:"
echo "  Create a fizzbuzz web app at examples/fizzbuzz/index.html."
echo "  Count 1-100: Fizz for 3, Buzz for 5, FizzBuzz for 15."
echo "  Nice CSS: dark background, glassmorphism card, color-coded cells."
echo ""
echo "The alice-auto plugin will auto-register on session start"
echo "and publish a status_delta when the session goes idle."
echo "Press Ctrl+C to stop the server."
echo ""
wait "$SERVER_PID" || true
