#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  run.sh [--issue N] [--dry-run]
EOF
}

parse_args() {
  issue_number=""
  dry_run=false

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --issue)
        [[ $# -ge 2 ]] || runoq::die "--issue requires a value"
        issue_number="$2"
        shift 2
        ;;
      --dry-run)
        dry_run=true
        shift
        ;;
      *)
        usage >&2
        exit 1
        ;;
    esac
  done
}

run_production() {
  local orchestrator="${RUNOQ_ORCHESTRATOR_BIN:-$(cd "$(dirname "$0")" && pwd)/orchestrator.sh}"
  local repo
  repo="$(runoq::repo)"
  local args=("$repo")
  if [[ -n "$issue_number" ]]; then
    args+=(--issue "$issue_number")
  fi
  if [[ "$dry_run" == "true" ]]; then
    args+=(--dry-run)
  fi
  "$orchestrator" run "${args[@]}"
}

main() {
  parse_args "$@"

  if [[ "${RUNOQ_TEST_RUN_MODE:-}" == "fixture" ]]; then
    # shellcheck source=./scripts/run-fixture.sh
    source "$(cd "$(dirname "$0")" && pwd)/run-fixture.sh"
    fixture_mode_run "$issue_number" "$dry_run"
  else
    run_production
  fi
}

main "$@"
