#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/smoke-common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/smoke-common.sh"

export RUNOQ_SMOKE_LOG_SCOPE="smoke-lifecycle"

usage() {
  cat <<'EOF'
Usage:
  smoke-lifecycle.sh preflight
  smoke-lifecycle.sh run
  smoke-lifecycle.sh cleanup (--repo OWNER/REPO | --run-id ID | --all)
EOF
}

case "${1:-}" in
  preflight)
    lifecycle_preflight_json
    ;;
  run)
    run_lifecycle
    ;;
  cleanup)
    shift
    cleanup_lifecycle "$@"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
