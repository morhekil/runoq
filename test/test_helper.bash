setup() {
  export AGENDEV_ROOT="/Users/Saruman/Projects/agendev"
  export AGENDEV_CONFIG="$AGENDEV_ROOT/config/agendev.json"
  export TEST_TMPDIR
  TEST_TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/agendev-test.XXXXXX")"
}

teardown() {
  rm -rf "$TEST_TMPDIR"
}

make_git_repo() {
  local repo_dir="$1"
  local remote_url="${2:-git@github.com:owner/example.git}"

  mkdir -p "$repo_dir"
  git init -b main "$repo_dir" >/dev/null
  git -C "$repo_dir" config user.name "Test User"
  git -C "$repo_dir" config user.email "test@example.com"
  echo "seed" >"$repo_dir/README.md"
  git -C "$repo_dir" add README.md
  git -C "$repo_dir" commit -m "Initial commit" >/dev/null
  git -C "$repo_dir" remote add origin "$remote_url"
}

run_bash() {
  run bash -lc "$1"
}
