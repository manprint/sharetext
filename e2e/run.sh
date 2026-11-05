#!/usr/bin/env bash
# Build sharetext, start it on a throwaway database and run the browser suite.
# To test a running server instead: SHARETEXT_URL=https://host npm test
set -euo pipefail
cd "$(dirname "$0")"
root=$(cd .. && pwd)
work=$(mktemp -d)
port=${E2E_PORT:-18765}
pid=
cleanup() {
  if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

(cd "$root" && CGO_ENABLED=0 go build -o "$work/sharetext" ./cmd/server)
export ADMIN_USER=${ADMIN_USER:-e2e-admin} ADMIN_PASS=${ADMIN_PASS:-e2e-admin-password}
PORT=$port DB_PATH="$work/e2e.db" RATE_LIMIT_ENABLED=false CREATE_RATE_LIMIT_ENABLED=false \
  "$work/sharetext" >"$work/server.log" 2>&1 &
pid=$!
for _ in $(seq 50); do
  curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done

if ! SHARETEXT_URL="http://127.0.0.1:$port" npm test; then
  echo "--- server log"; tail -n 50 "$work/server.log"
  exit 1
fi
