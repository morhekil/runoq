#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  loop.sh [--backoff N]

Options:
  --backoff N   Seconds to wait when tick has no work (default: 30)
EOF
}

main() {
  trap 'exit 0' INT TERM
  local backoff=30

  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help)
        usage
        exit 0
        ;;
      --backoff)
        [[ $# -ge 2 ]] || runoq::die "Missing value for --backoff."
        backoff="$2"
        shift 2
        ;;
      *)
        runoq::die "Unknown option: $1"
        ;;
    esac
  done

  local tick_script
  tick_script="$(runoq::root)/scripts/tick.sh"

  while true; do
    local status=0
    "$tick_script" || status=$?

    case "$status" in
      0)
        # Work done, more available — loop immediately
        ;;
      2)
        # Nothing to do — back off
        runoq::info "waiting ${backoff}s before next tick"
        sleep "$backoff"
        ;;
      *)
        # Error — stop
        runoq::die "tick exited with status $status"
        ;;
    esac
  done
}

main "$@"
