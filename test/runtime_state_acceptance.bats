#!/usr/bin/env bats

load test_helper

setup_state_acceptance_project() {
  local dir="$1"
  make_git_repo "$dir" "git@github.com:owner/example.git"
  prepare_runtime_bin
}

normalize_state_json() {
  printf '%s' "$1" | jq -S 'del(.started_at, .updated_at)'
}

normalize_payload_json() {
  printf '%s' "$1" | jq -S .
}

@test "acceptance parity: state save/load matches shell and runtime contracts" {
  shell_project="$TEST_TMPDIR/shell-project"
  runtime_project="$TEST_TMPDIR/runtime-project"
  setup_state_acceptance_project "$shell_project"
  setup_state_acceptance_project "$runtime_project"

  shell_state_dir="$shell_project/.runoq/state"
  runtime_state_dir="$runtime_project/.runoq/state"
  payload_file="$TEST_TMPDIR/save-payload.json"
  printf '%s\n' '{"phase":"INIT","branch":"runoq/42-test","round":0}' >"$payload_file"

  run bash -lc 'cd "'"$shell_project"'" && cat "'"$payload_file"'" | RUNOQ_STATE_DIR="'"$shell_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42'
  shell_save_status="$status"
  shell_save_output="$output"

  run bash -lc 'cd "'"$runtime_project"'" && cat "'"$payload_file"'" | RUNOQ_STATE_DIR="'"$runtime_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" save 42'
  runtime_save_status="$status"
  runtime_save_output="$output"

  [ "$shell_save_status" -eq "$runtime_save_status" ]
  [ "$(normalize_state_json "$shell_save_output")" = "$(normalize_state_json "$runtime_save_output")" ]

  run bash -lc 'cd "'"$shell_project"'" && RUNOQ_STATE_DIR="'"$shell_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" load 42'
  shell_load_status="$status"
  shell_load_output="$output"

  run bash -lc 'cd "'"$runtime_project"'" && RUNOQ_STATE_DIR="'"$runtime_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" load 42'
  runtime_load_status="$status"
  runtime_load_output="$output"

  [ "$shell_load_status" -eq "$runtime_load_status" ]
  [ "$(normalize_state_json "$shell_load_output")" = "$(normalize_state_json "$runtime_load_output")" ]
}

@test "acceptance parity: state validate-payload matches shell and runtime contracts" {
  project_dir="$TEST_TMPDIR/project"
  setup_state_acceptance_project "$project_dir"
  mkdir -p "$project_dir/src"
  echo "console.log('hello')" >"$project_dir/src/app.ts"
  git -C "$project_dir" add src/app.ts
  git -C "$project_dir" commit -m "Add app" >/dev/null
  base_sha="$(git -C "$project_dir" rev-parse HEAD)"

  echo "console.log('updated')" >>"$project_dir/src/app.ts"
  git -C "$project_dir" add src/app.ts
  git -C "$project_dir" commit -m "Update app" >/dev/null

  payload_file="$TEST_TMPDIR/payload.txt"
  cat >"$payload_file" <<'EOF'
<!-- runoq:payload:codex-return -->
```json
{
  "status": "completed",
  "commits_pushed": ["wrongsha"],
  "commit_range": "wrongsha..wrongsha",
  "files_changed": ["src/app.ts"],
  "files_added": [],
  "files_deleted": [],
  "tests_run": true,
  "tests_passed": true,
  "test_summary": "ok",
  "build_passed": true,
  "blockers": [],
  "notes": "ok"
}
```
EOF

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" validate-payload "'"$project_dir"'" "'"$base_sha"'" "'"$payload_file"'"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" validate-payload "'"$project_dir"'" "'"$base_sha"'" "'"$payload_file"'"'
  runtime_status="$status"
  runtime_output="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$(normalize_payload_json "$shell_output")" = "$(normalize_payload_json "$runtime_output")" ]
}

@test "acceptance parity: state validate-payload missing source synthesizes identically" {
  project_dir="$TEST_TMPDIR/project-missing-source"
  setup_state_acceptance_project "$project_dir"
  mkdir -p "$project_dir/src"
  echo "console.log('hello')" >"$project_dir/src/app.ts"
  git -C "$project_dir" add src/app.ts
  git -C "$project_dir" commit -m "Add app" >/dev/null
  base_sha="$(git -C "$project_dir" rev-parse HEAD)"

  echo "console.log('updated')" >>"$project_dir/src/app.ts"
  git -C "$project_dir" add src/app.ts
  git -C "$project_dir" commit -m "Update app" >/dev/null

  missing_payload="$TEST_TMPDIR/does-not-exist.txt"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" validate-payload "'"$project_dir"'" "'"$base_sha"'" "'"$missing_payload"'" 2>"'"$TEST_TMPDIR"'/shell-missing-source.err"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" validate-payload "'"$project_dir"'" "'"$base_sha"'" "'"$missing_payload"'" 2>"'"$TEST_TMPDIR"'/runtime-missing-source.err"'
  runtime_status="$status"
  runtime_output="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$(normalize_payload_json "$shell_output")" = "$(normalize_payload_json "$runtime_output")" ]
}

@test "acceptance parity: state mention tracking matches shell and runtime contracts" {
  shell_project="$TEST_TMPDIR/shell-mentions-project"
  runtime_project="$TEST_TMPDIR/runtime-mentions-project"
  setup_state_acceptance_project "$shell_project"
  setup_state_acceptance_project "$runtime_project"

  shell_state_dir="$shell_project/.runoq/state"
  runtime_state_dir="$runtime_project/.runoq/state"

  run bash -lc 'cd "'"$shell_project"'" && RUNOQ_STATE_DIR="'"$shell_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" record-mention 101'
  shell_record_status="$status"
  shell_record_output="$output"

  run bash -lc 'cd "'"$runtime_project"'" && RUNOQ_STATE_DIR="'"$runtime_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" record-mention 101'
  runtime_record_status="$status"
  runtime_record_output="$output"

  [ "$shell_record_status" -eq "$runtime_record_status" ]
  [ "$shell_record_output" = "$runtime_record_output" ]

  run bash -lc 'cd "'"$shell_project"'" && RUNOQ_STATE_DIR="'"$shell_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" has-mention 101'
  shell_has_status="$status"
  shell_has_output="$output"

  run bash -lc 'cd "'"$runtime_project"'" && RUNOQ_STATE_DIR="'"$runtime_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" has-mention 101'
  runtime_has_status="$status"
  runtime_has_output="$output"

  [ "$shell_has_status" -eq "$runtime_has_status" ]
  [ "$shell_has_output" = "$runtime_has_output" ]

  run bash -lc 'cd "'"$shell_project"'" && RUNOQ_STATE_DIR="'"$shell_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" has-mention 999'
  shell_missing_status="$status"
  shell_missing_output="$output"

  run bash -lc 'cd "'"$runtime_project"'" && RUNOQ_STATE_DIR="'"$runtime_state_dir"'" RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" has-mention 999'
  runtime_missing_status="$status"
  runtime_missing_output="$output"

  [ "$shell_missing_status" -eq "$runtime_missing_status" ]
  [ "$shell_missing_output" = "$runtime_missing_output" ]
}

@test "acceptance parity: state extract-payload matches shell and runtime contracts" {
  project_dir="$TEST_TMPDIR/project-extract"
  setup_state_acceptance_project "$project_dir"
  source_file="$(fixture_path "payloads/codex-output-marked-block.txt")"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_STATE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/state.sh" extract-payload "'"$source_file"'"'
  shell_status="$status"
  shell_output="$output"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_STATE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/state.sh" extract-payload "'"$source_file"'"'
  runtime_status="$status"
  runtime_output="$output"

  [ "$shell_status" -eq "$runtime_status" ]
  [ "$shell_output" = "$runtime_output" ]
}
