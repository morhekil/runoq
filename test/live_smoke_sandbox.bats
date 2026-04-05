#!/usr/bin/env bats

# Resolve the real gh before test_helper prepends test/helpers (which contains a fake gh stub).
GH_BIN="$(command -v gh)"
export GH_BIN

load test_helper

@test "live smoke validates GitHub sandbox flows" {
  if [[ "${RUNOQ_SMOKE:-0}" != "1" ]]; then
    skip "Set RUNOQ_SMOKE=1 plus the required RUNOQ_SMOKE_* variables to run live GitHub smoke tests."
  fi

  run "$RUNOQ_ROOT/scripts/smoke-sandbox.sh" preflight
  [ "$status" -eq 0 ]
  if [[ "$(printf '%s' "$output" | jq -r '.ready')" != "true" ]]; then
    skip "Live smoke preflight is not ready: $output"
  fi

  run "$RUNOQ_ROOT/scripts/smoke-sandbox.sh" run

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.checks | join(",")')" = "github_app_auth,labels_present,issue_created,issue_comment_attribution,permission_check,pr_created,pr_comment_attribution" ]
}
