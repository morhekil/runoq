#!/usr/bin/env bats

load test_helper

@test "live smoke validates GitHub sandbox flows" {
  if [[ "${AGENDEV_SMOKE:-0}" != "1" ]]; then
    skip "Set AGENDEV_SMOKE=1 plus the required AGENDEV_SMOKE_* variables to run live GitHub smoke tests."
  fi

  run "$AGENDEV_ROOT/scripts/live-smoke.sh" preflight
  [ "$status" -eq 0 ]
  if [[ "$(printf '%s' "$output" | jq -r '.ready')" != "true" ]]; then
    skip "Live smoke preflight is not ready: $output"
  fi

  run "$AGENDEV_ROOT/scripts/live-smoke.sh" run

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "ok" ]
  [ "$(printf '%s' "$output" | jq -r '.checks | join(\",\")')" = "github_app_auth,labels_present,issue_created,issue_comment_attribution,permission_check,pr_created,pr_comment_attribution" ]
}
