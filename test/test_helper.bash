setup() {
  export RUNOQ_ROOT
  RUNOQ_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  export RUNOQ_CONFIG="$RUNOQ_ROOT/config/runoq.json"
  export TEST_TMPDIR
  TEST_TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/runoq-test.XXXXXX")"
  export PATH="$RUNOQ_ROOT/test/helpers:$PATH"
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

make_remote_backed_repo() {
  local remote_dir="$1"
  local local_dir="$2"
  local seed_dir="$TEST_TMPDIR/seed-repo"

  mkdir -p "$seed_dir"
  git init -b main "$seed_dir" >/dev/null
  git -C "$seed_dir" config user.name "Test User"
  git -C "$seed_dir" config user.email "test@example.com"
  echo "seed" >"$seed_dir/README.md"
  git -C "$seed_dir" add README.md
  git -C "$seed_dir" commit -m "Initial commit" >/dev/null

  git clone --bare "$seed_dir" "$remote_dir" >/dev/null 2>&1
  git clone "$remote_dir" "$local_dir" >/dev/null 2>&1
  git -C "$local_dir" config user.name "Test User"
  git -C "$local_dir" config user.email "test@example.com"
}

run_bash() {
  run bash -lc "$1"
}

fixture_path() {
  printf '%s/test/fixtures/%s\n' "$RUNOQ_ROOT" "$1"
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
  export FAKE_GH_CAPTURE_DIR="${4:-$TEST_TMPDIR/fake-gh-capture}"
  export GH_BIN="$RUNOQ_ROOT/test/helpers/gh"
}
