#!/usr/bin/env bash
# Runs the bepilot agent API on :9090 for local frontend development.
# Brings up Postgres first (auto_migrate in configs/config.yaml handles schema).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

# Fixed port for the run-web dev flow, regardless of BEPILOT_HTTP_ADDR in .env.
export BEPILOT_HTTP_ADDR=":9090"

make up

echo "==> bepilot agent starting on ${BEPILOT_HTTP_ADDR}"
exec go run ./cmd/server -config configs/config.yaml
