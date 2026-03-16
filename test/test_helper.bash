setup() {
  export AGENDEV_ROOT="/Users/Saruman/Projects/agendev"
  export AGENDEV_CONFIG="$AGENDEV_ROOT/config/agendev.json"
  export TEST_TMPDIR
  TEST_TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/agendev-test.XXXXXX")"
  export PATH="$AGENDEV_ROOT/test/helpers:$PATH"
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

fixture_path() {
  printf '%s/test/fixtures/%s\n' "$AGENDEV_ROOT" "$1"
}

load_fixture() {
  cat "$(fixture_path "$1")"
}

write_fake_gh_scenario() {
  local path="$1"
  cat >"$path"
}

use_fake_gh() {
  export FAKE_GH_SCENARIO="$1"
  export FAKE_GH_STATE="${2:-$TEST_TMPDIR/fake-gh.state}"
  export FAKE_GH_LOG="${3:-$TEST_TMPDIR/fake-gh.log}"
  export GH_BIN="$AGENDEV_ROOT/test/helpers/gh"
}
