#!/usr/bin/env bats

load test_helper

write_identity_file() {
  local repo_dir="$1"
  mkdir -p "$repo_dir/.runoq"
  cat >"$repo_dir/.runoq/identity.json" <<'EOF'
{
  "appId": 123
}
EOF
}

make_remote_backed_repo_isolated() {
  rm -rf "$TEST_TMPDIR/seed-repo"
  make_remote_backed_repo "$1" "$2"
}

normalize_worktree_json() {
  printf '%s' "$1" | jq -S '.worktree = "__WORKTREE__"'
}

normalize_path_text() {
  printf '%s' "$1" | sed -E 's#/[[:graph:]]+#__PATH__#g'
}

write_fake_runtime_worktree_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'FAKE_RUNTIME:%s %s\n' "$1" "${*:2}"
EOF
  chmod +x "$path"
}

write_fake_go_bin() {
  local path="$1"
  cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'FAKE_GO_CWD:%s\n' "$PWD"
printf 'FAKE_GO_ARGS:%s\n' "$*"
EOF
  chmod +x "$path"
}

@test "worktree wrapper defaults to runtime and preserves explicit shell override" {
  project_dir="$TEST_TMPDIR/default-wrapper-project"
  make_git_repo "$project_dir"

  fake_runtime_bin="$TEST_TMPDIR/fake-runtime-worktree"
  write_fake_runtime_worktree_bin "$fake_runtime_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_WORKTREE_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/worktree.sh" inspect 42'
  [ "$status" -eq 0 ]
  [ "$output" = "FAKE_RUNTIME:__worktree inspect 42" ]

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_WORKTREE_IMPLEMENTATION="shell" RUNOQ_RUNTIME_BIN="'"$fake_runtime_bin"'" "'"$RUNOQ_ROOT"'/scripts/worktree.sh"'
  [ "$status" -ne 0 ]
  [[ "$output" != *"FAKE_RUNTIME:"* ]]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"worktree.sh create"* ]]
}

@test "worktree wrapper go fallback runs from RUNOQ_ROOT when runtime bin is unset" {
  project_dir="$TEST_TMPDIR/default-wrapper-go-cwd-project"
  make_git_repo "$project_dir"

  fake_go_bin="$TEST_TMPDIR/fake-go-worktree"
  write_fake_go_bin "$fake_go_bin"

  run bash -lc 'cd "'"$project_dir"'" && RUNOQ_IMPLEMENTATION="" RUNOQ_WORKTREE_IMPLEMENTATION="" RUNOQ_RUNTIME_BIN="" RUNOQ_GO_BIN="'"$fake_go_bin"'" "'"$RUNOQ_ROOT"'/scripts/worktree.sh" inspect 42'
  [ "$status" -eq 0 ]
  [[ "$output" == *"FAKE_GO_CWD:$RUNOQ_ROOT"* ]]
  [[ "$output" == *"FAKE_GO_ARGS:run $RUNOQ_ROOT/cmd/runoq-runtime __worktree inspect 42"* ]]
}

@test "acceptance parity: worktree create/remove/inspect matches shell and runtime contracts" {
  prepare_runtime_bin

  shell_parent="$TEST_TMPDIR/shell-parent"
  runtime_parent="$TEST_TMPDIR/runtime-parent"
  shell_remote="$shell_parent/remote.git"
  shell_local="$shell_parent/local"
  runtime_remote="$runtime_parent/remote.git"
  runtime_local="$runtime_parent/local"

  mkdir -p "$shell_parent" "$runtime_parent"
  make_remote_backed_repo_isolated "$shell_remote" "$shell_local"
  make_remote_backed_repo_isolated "$runtime_remote" "$runtime_local"
  write_identity_file "$shell_local"
  write_identity_file "$runtime_local"

  echo "local only" >"$shell_local/local-only.txt"
  git -C "$shell_local" add local-only.txt
  git -C "$shell_local" commit -m "Local-only commit" >/dev/null

  echo "local only" >"$runtime_local/local-only.txt"
  git -C "$runtime_local" add local-only.txt
  git -C "$runtime_local" commit -m "Local-only commit" >/dev/null

  run bash -lc 'cd "'"$shell_local"'" && TARGET_ROOT="'"$shell_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/worktree.sh" create 42 "Implement queue" 2>"'"$TEST_TMPDIR"'/shell-create.err"'
  shell_create_status="$status"
  shell_create_output="$output"

  run bash -lc 'cd "'"$runtime_local"'" && TARGET_ROOT="'"$runtime_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/worktree.sh" create 42 "Implement queue" 2>"'"$TEST_TMPDIR"'/runtime-create.err"'
  runtime_create_status="$status"
  runtime_create_output="$output"

  run cat "$TEST_TMPDIR/shell-create.err"
  shell_create_err="$output"
  run cat "$TEST_TMPDIR/runtime-create.err"
  runtime_create_err="$output"

  [ "$shell_create_status" -eq "$runtime_create_status" ]
  [ "$shell_create_status" -eq 0 ]
  [ "$(normalize_worktree_json "$shell_create_output")" = "$(normalize_worktree_json "$runtime_create_output")" ]
  [ "$(normalize_path_text "$shell_create_err")" = "$(normalize_path_text "$runtime_create_err")" ]

  shell_worktree="$(printf '%s' "$shell_create_output" | jq -r '.worktree')"
  runtime_worktree="$(printf '%s' "$runtime_create_output" | jq -r '.worktree')"

  [ -d "$shell_worktree" ]
  [ -d "$runtime_worktree" ]
  [ ! -f "$shell_worktree/local-only.txt" ]
  [ ! -f "$runtime_worktree/local-only.txt" ]
  [ "$(git -C "$shell_worktree" config user.name)" = "$(git -C "$runtime_worktree" config user.name)" ]
  [ "$(git -C "$shell_worktree" config user.name)" = "runoq[bot]" ]
  [ "$(git -C "$shell_worktree" config user.email)" = "$(git -C "$runtime_worktree" config user.email)" ]
  [ "$(git -C "$shell_worktree" config user.email)" = "123+runoq[bot]@users.noreply.github.com" ]

  run bash -lc 'cd "'"$shell_local"'" && TARGET_ROOT="'"$shell_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/worktree.sh" inspect 42 2>"'"$TEST_TMPDIR"'/shell-inspect.err"'
  shell_inspect_status="$status"
  shell_inspect_output="$output"

  run bash -lc 'cd "'"$runtime_local"'" && TARGET_ROOT="'"$runtime_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/worktree.sh" inspect 42 2>"'"$TEST_TMPDIR"'/runtime-inspect.err"'
  runtime_inspect_status="$status"
  runtime_inspect_output="$output"

  run cat "$TEST_TMPDIR/shell-inspect.err"
  shell_inspect_err="$output"
  run cat "$TEST_TMPDIR/runtime-inspect.err"
  runtime_inspect_err="$output"

  [ "$shell_inspect_status" -eq "$runtime_inspect_status" ]
  [ "$shell_inspect_status" -eq 0 ]
  [ "$(normalize_worktree_json "$shell_inspect_output")" = "$(normalize_worktree_json "$runtime_inspect_output")" ]
  [ "$(normalize_path_text "$shell_inspect_err")" = "$(normalize_path_text "$runtime_inspect_err")" ]

  run bash -lc 'cd "'"$shell_local"'" && TARGET_ROOT="'"$shell_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/worktree.sh" remove 42 2>"'"$TEST_TMPDIR"'/shell-remove.err"'
  shell_remove_status="$status"
  shell_remove_output="$output"

  run bash -lc 'cd "'"$runtime_local"'" && TARGET_ROOT="'"$runtime_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/worktree.sh" remove 42 2>"'"$TEST_TMPDIR"'/runtime-remove.err"'
  runtime_remove_status="$status"
  runtime_remove_output="$output"

  run cat "$TEST_TMPDIR/shell-remove.err"
  shell_remove_err="$output"
  run cat "$TEST_TMPDIR/runtime-remove.err"
  runtime_remove_err="$output"

  [ "$shell_remove_status" -eq "$runtime_remove_status" ]
  [ "$shell_remove_status" -eq 0 ]
  [ "$(normalize_worktree_json "$shell_remove_output")" = "$(normalize_worktree_json "$runtime_remove_output")" ]
  [ "$(normalize_path_text "$shell_remove_err")" = "$(normalize_path_text "$runtime_remove_err")" ]
  [ ! -e "$shell_worktree" ]
  [ ! -e "$runtime_worktree" ]
}

@test "acceptance parity: worktree create path-exists failure matches shell and runtime contracts" {
  prepare_runtime_bin

  shell_parent="$TEST_TMPDIR/shell-parent"
  runtime_parent="$TEST_TMPDIR/runtime-parent"
  shell_remote="$shell_parent/remote.git"
  shell_local="$shell_parent/local"
  runtime_remote="$runtime_parent/remote.git"
  runtime_local="$runtime_parent/local"

  mkdir -p "$shell_parent" "$runtime_parent"
  make_remote_backed_repo_isolated "$shell_remote" "$shell_local"
  make_remote_backed_repo_isolated "$runtime_remote" "$runtime_local"

  mkdir -p "$shell_parent/runoq-wt-42" "$runtime_parent/runoq-wt-42"

  run bash -lc 'cd "'"$shell_local"'" && TARGET_ROOT="'"$shell_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=shell "'"$RUNOQ_ROOT"'/scripts/worktree.sh" create 42 "Implement queue" 2>"'"$TEST_TMPDIR"'/shell-fail.err"'
  shell_fail_status="$status"
  shell_fail_output="$output"

  run bash -lc 'cd "'"$runtime_local"'" && TARGET_ROOT="'"$runtime_local"'" RUNOQ_WORKTREE_IMPLEMENTATION=runtime "'"$RUNOQ_ROOT"'/scripts/worktree.sh" create 42 "Implement queue" 2>"'"$TEST_TMPDIR"'/runtime-fail.err"'
  runtime_fail_status="$status"
  runtime_fail_output="$output"

  run cat "$TEST_TMPDIR/shell-fail.err"
  shell_fail_err="$output"
  run cat "$TEST_TMPDIR/runtime-fail.err"
  runtime_fail_err="$output"

  [ "$shell_fail_status" -eq "$runtime_fail_status" ]
  [ "$shell_fail_status" -ne 0 ]
  [ "$shell_fail_output" = "$runtime_fail_output" ]
  [ "$(normalize_path_text "$shell_fail_err")" = "$(normalize_path_text "$runtime_fail_err")" ]
  [[ "$(normalize_path_text "$runtime_fail_err")" == *"Worktree already exists:"* ]]
}
