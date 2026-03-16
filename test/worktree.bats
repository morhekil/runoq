#!/usr/bin/env bats

load test_helper

@test "worktree create branches from origin/main rather than local main" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  export AGENDEV_REPO="owner/repo"

  echo "local only" >"$local_dir/local-only.txt"
  git -C "$local_dir" add local-only.txt
  git -C "$local_dir" commit -m "Local-only commit" >/dev/null

  run "$AGENDEV_ROOT/scripts/worktree.sh" create 42 "Implement queue"

  [ "$status" -eq 0 ]
  worktree_path="$(printf '%s' "$output" | jq -r '.worktree')"
  [ -d "$worktree_path" ]
  [ ! -f "$worktree_path/local-only.txt" ]
  [ "$(printf '%s' "$output" | jq -r '.base_ref')" = "origin/main" ]
}

@test "worktree remove cleans up the sibling worktree" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  export AGENDEV_REPO="owner/repo"

  run "$AGENDEV_ROOT/scripts/worktree.sh" create 42 "Implement queue"
  [ "$status" -eq 0 ]
  worktree_path="$(printf '%s' "$output" | jq -r '.worktree')"
  [ -d "$worktree_path" ]

  run "$AGENDEV_ROOT/scripts/worktree.sh" remove 42
  [ "$status" -eq 0 ]
  [ ! -e "$worktree_path" ]
}

@test "worktree create fails cleanly when the worktree path already exists" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  export AGENDEV_REPO="owner/repo"

  mkdir -p "$TEST_TMPDIR/agendev-wt-42"
  export AGENDEV_CONFIG="$AGENDEV_ROOT/config/agendev.json"

  run "$AGENDEV_ROOT/scripts/worktree.sh" create 42 "Implement queue"

  [ "$status" -ne 0 ]
  [[ "$output" == *"Worktree already exists"* ]]
}
