#!/usr/bin/env bats

load test_helper

@test "live smoke preflight requires explicit sandbox configuration" {
  run "$AGENDEV_ROOT/scripts/live-smoke.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.enabled')" = "false" ]
  [ "$(printf '%s' "$output" | jq -r '.missing | length')" = "6" ]
}

@test "live smoke preflight accepts explicit sandbox configuration" {
  key_path="$TEST_TMPDIR/app-key.pem"
  printf 'not-a-real-key\n' >"$key_path"
  export AGENDEV_SMOKE=1
  export AGENDEV_SMOKE_REPO="owner/sandbox"
  export AGENDEV_SMOKE_APP_ID="123"
  export AGENDEV_SMOKE_INSTALLATION_ID="456"
  export AGENDEV_SMOKE_APP_KEY="$key_path"
  export AGENDEV_SMOKE_PERMISSION_USER="sandbox-user"
  export AGENDEV_SMOKE_PERMISSION_LEVEL="write"

  run "$AGENDEV_ROOT/scripts/live-smoke.sh" preflight

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.ready')" = "true" ]
  [ "$(printf '%s' "$output" | jq -r '.repo')" = "owner/sandbox" ]
  [ "$(printf '%s' "$output" | jq -r '.permission_user')" = "sandbox-user" ]
  [ "$(printf '%s' "$output" | jq -r '.permission_level')" = "write" ]
}
