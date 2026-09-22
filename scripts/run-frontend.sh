#!/usr/bin/env bash
# Runs the frontend dev server (Vite's default port, e.g. 5173), pointed at
# the bepilot agent's AG-UI endpoint.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR/frontend"

if [ ! -d node_modules ]; then
  echo "==> installing frontend dependencies"
  npm install
fi

export VITE_AGENT_URL="${VITE_AGENT_URL:-http://localhost:9090/v1/ag-ui/run}"

echo "==> frontend dev server starting (agent: ${VITE_AGENT_URL})"
exec npm run dev
