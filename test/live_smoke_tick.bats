#!/usr/bin/env bats

# Resolve real gh before test_helper prepends fake helpers.
GH_BIN="$(command -v gh)"
export GH_BIN

load test_helper

@test "live tick smoke validates the iterative tick workflow" {
  if [[ "${RUNOQ_SMOKE_TICK:-0}" != "1" ]]; then
    skip "Set RUNOQ_SMOKE_TICK=1 plus the required RUNOQ_SMOKE_* variables to run live tick smoke."
  fi

  run "$RUNOQ_ROOT/scripts/smoke-tick.sh" preflight
  [ "$status" -eq 0 ]
  if [[ "$(printf '%s' "$output" | jq -r '.ready')" != "true" ]]; then
    skip "Live tick smoke preflight is not ready: $output"
  fi

  run "$RUNOQ_ROOT/scripts/smoke-tick.sh" run

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.steps | length')" -ge 14 ]
  [ "$(printf '%s' "$output" | jq -r '.failures | length')" = "0" ]
}
