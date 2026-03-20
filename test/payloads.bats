#!/usr/bin/env bats

load test_helper

setup_payload_repo() {
  local repo_dir="$1"
  make_git_repo "$repo_dir" "git@github.com:owner/example.git"
  mkdir -p "$repo_dir/src"
  echo "console.log('hello')" >"$repo_dir/src/app.ts"
  git -C "$repo_dir" add src/app.ts
  git -C "$repo_dir" commit -m "Add app" >/dev/null
  git -C "$repo_dir" rev-parse HEAD
}

wrap_payload_fixture() {
  local fixture="$1"
  local destination="$2"
  {
    echo "<!-- agendev:payload:codex-return -->"
    echo '```json'
    cat "$fixture"
    echo
    echo '```'
  } >"$destination"
}

@test "extract-payload returns the last fenced block" {
  run "$AGENDEV_ROOT/scripts/state.sh" extract-payload "$(fixture_path "payloads/codex-output-multi-block.txt")"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"status": "completed"'* ]]
  [[ "$output" != *"intermediate non-json block"* ]]
}

@test "extract-payload prefers the codex-return marker block" {
  run "$AGENDEV_ROOT/scripts/state.sh" extract-payload "$(fixture_path "payloads/codex-output-marked-block.txt")"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"status": "completed"'* ]]
  [[ "$output" == *'"notes": "use marked payload"'* ]]
  [[ "$output" != *"ignore trailing fenced block"* ]]
}

@test "validate-payload synthesizes a failed payload when no JSON block exists" {
  base_repo="$TEST_TMPDIR/repo"
  base_sha="$(setup_payload_repo "$base_repo")"
  echo "console.log('updated')" >>"$base_repo/src/app.ts"
  git -C "$base_repo" add src/app.ts
  git -C "$base_repo" commit -m "Update app" >/dev/null

  run "$AGENDEV_ROOT/scripts/state.sh" validate-payload "$base_repo" "$base_sha" "$(fixture_path "payloads/codex-output-no-json.txt")"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"status": "failed"'* ]]
  [[ "$output" == *'"payload_source": "synthetic"'* ]]
  [[ "$output" == *'Codex did not return a structured payload'* ]]
}

@test "validate-payload synthesizes from malformed JSON" {
  base_repo="$TEST_TMPDIR/repo"
  base_sha="$(setup_payload_repo "$base_repo")"
  echo "console.log('updated')" >>"$base_repo/src/app.ts"
  git -C "$base_repo" add src/app.ts
  git -C "$base_repo" commit -m "Update app" >/dev/null

  run "$AGENDEV_ROOT/scripts/state.sh" validate-payload "$base_repo" "$base_sha" "$(fixture_path "payloads/codex-return-malformed.txt")"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"payload_source": "synthetic"'* ]]
  [[ "$output" == *'"commits_pushed"'* ]]
}

@test "validate-payload patches missing required fields from ground truth" {
  base_repo="$TEST_TMPDIR/repo"
  base_sha="$(setup_payload_repo "$base_repo")"
  echo "console.log('updated')" >>"$base_repo/src/app.ts"
  git -C "$base_repo" add src/app.ts
  git -C "$base_repo" commit -m "Update app" >/dev/null
  payload_file="$TEST_TMPDIR/missing-fields-output.txt"
  wrap_payload_fixture "$(fixture_path "payloads/codex-return-missing-fields.json")" "$payload_file"

  run "$AGENDEV_ROOT/scripts/state.sh" validate-payload "$base_repo" "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"payload_source": "patched"'* ]]
  [[ "$output" == *'"files_changed": ['* ]]
  [[ "$output" == *'"patched_fields": ['* ]]
}

@test "validate-payload normalizes unknown status to failed" {
  base_repo="$TEST_TMPDIR/repo"
  base_sha="$(setup_payload_repo "$base_repo")"
  payload_file="$TEST_TMPDIR/unknown-status-output.txt"
  wrap_payload_fixture "$(fixture_path "payloads/codex-return-unknown-status.json")" "$payload_file"

  run "$AGENDEV_ROOT/scripts/state.sh" validate-payload "$base_repo" "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"status": "failed"'* ]]
  [[ "$output" == *'"patched_fields": ['* ]]
}

@test "validate-payload patches wrong types and ignores unknown fields" {
  base_repo="$TEST_TMPDIR/repo"
  base_sha="$(setup_payload_repo "$base_repo")"
  payload_file="$TEST_TMPDIR/wrong-types-output.txt"
  wrap_payload_fixture "$(fixture_path "payloads/codex-return-wrong-types.json")" "$payload_file"

  run "$AGENDEV_ROOT/scripts/state.sh" validate-payload "$base_repo" "$base_sha" "$payload_file"

  [ "$status" -eq 0 ]
  [[ "$output" == *'"status": "failed"'* ]]
  [[ "$output" != *'unknown_field'* ]]
  [[ "$output" == *'"tests_run": false'* ]]
}
