#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/smoke-common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/smoke-common.sh"

export AGENDEV_SMOKE_LOG_SCOPE="smoke-sandbox"

usage() {
  cat <<'EOF'
Usage:
  smoke-sandbox.sh preflight
  smoke-sandbox.sh run
EOF
}

case "${1:-}" in
  preflight)
    preflight_json
    ;;
  run)
    run_smoke
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
