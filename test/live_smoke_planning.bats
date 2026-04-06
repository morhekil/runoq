#!/usr/bin/env bats

# Resolve real binaries before test_helper prepends test/helpers (which contains fake stubs).
GH_BIN="$(command -v gh)"
export GH_BIN
RUNOQ_CLAUDE_BIN="$(command -v claude)"
export RUNOQ_CLAUDE_BIN

load test_helper

@test "live planning smoke validates the tick-based planning workflow" {
  if [[ "${RUNOQ_SMOKE_PLANNING:-0}" != "1" ]]; then
    skip "Set RUNOQ_SMOKE_PLANNING=1 plus the required RUNOQ_SMOKE_* variables to run live planning smoke."
  fi

  run "$RUNOQ_ROOT/scripts/smoke-planning.sh" preflight
  [ "$status" -eq 0 ]
  if [[ "$(printf '%s' "$output" | jq -r '.ready')" != "true" ]]; then
    skip "Live planning smoke preflight is not ready: $output"
  fi

  run "$RUNOQ_ROOT/scripts/smoke-planning.sh" run

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.planning.milestones')" -ge 1 ]
  [ "$(printf '%s' "$output" | jq -r '.planning.tasks')" -ge 1 ]
  [ "$(printf '%s' "$output" | jq -r '.planning.has_discovery_milestone')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.comment_interactions')" -ge 1 ]
}
