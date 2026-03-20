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

claude_exec() {
  local claude_bin="${AGENDEV_CLAUDE_BIN:-claude}"
  command -v "$claude_bin" >/dev/null 2>&1 || agendev::die "Claude CLI not found: $claude_bin"
  "$claude_bin" "$@"
}

parse_args() {
  issue_number=""
  dry_run=false

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --issue)
        [[ $# -ge 2 ]] || agendev::die "--issue requires a value"
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

build_run_prompt() {
  jq -cn \
    --arg command "agendev run" \
    --arg issue "${issue_number:-}" \
    --argjson dry_run "$dry_run" '
    {
      command: $command,
      issue: (if $issue == "" then null else ($issue | tonumber) end),
      dry_run: $dry_run
    }
  '
}

run_production() {
  local prompt
  prompt="$(build_run_prompt)"
  claude_exec --print --agent github-orchestrator --add-dir "$AGENDEV_ROOT" -- "$prompt"
}

main() {
  parse_args "$@"

  if [[ "${AGENDEV_TEST_RUN_MODE:-}" == "fixture" ]]; then
    # shellcheck source=./scripts/run-fixture.sh
    source "$(cd "$(dirname "$0")" && pwd)/run-fixture.sh"
    fixture_mode_run "$issue_number" "$dry_run"
  else
    run_production
  fi
}

main "$@"
