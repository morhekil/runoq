#!/usr/bin/env bats

load test_helper

@test "live lifecycle smoke validates the full runoq eval lane" {
  if [[ "${RUNOQ_SMOKE_LIFECYCLE:-0}" != "1" ]]; then
    skip "Set RUNOQ_SMOKE_LIFECYCLE=1 plus the required RUNOQ_SMOKE_* variables to run live lifecycle smoke."
  fi

  run "$RUNOQ_ROOT/scripts/smoke-lifecycle.sh" preflight
  [ "$status" -eq 0 ]
  if [[ "$(printf '%s' "$output" | jq -r '.ready')" != "true" ]]; then
    skip "Live lifecycle smoke preflight is not ready: $output"
  fi

  run "$RUNOQ_ROOT/scripts/smoke-lifecycle.sh" run

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.lifecycle.all_issues_done')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.lifecycle.queue_order_ok')" = "true" ]
}
