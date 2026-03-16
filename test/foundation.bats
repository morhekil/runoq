#!/usr/bin/env bats

load test_helper

@test "config has required top-level keys" {
  run jq -e '
    .labels.ready and
    .labels.inProgress and
    .labels.done and
    .labels.needsReview and
    .labels.blocked and
    .identity.appSlug and
    .identity.handle and
    .authorization.minimumPermission and
    .maxRounds and
    .maxTokenBudget and
    .verification.testCommand and
    .verification.buildCommand and
    .stall.timeoutSeconds
  ' "$AGENDEV_CONFIG"

  [ "$status" -eq 0 ]
}

@test "issue template includes metadata block and acceptance criteria section" {
  run grep -n "agendev:meta" "$AGENDEV_ROOT/templates/issue-template.md"
  [ "$status" -eq 0 ]

  run grep -n "## Acceptance Criteria" "$AGENDEV_ROOT/templates/issue-template.md"
  [ "$status" -eq 0 ]
}

@test "pr template includes marker-delimited summary and attention sections" {
  run grep -n "agendev:summary:start" "$AGENDEV_ROOT/templates/pr-template.md"
  [ "$status" -eq 0 ]

  run grep -n "agendev:summary:end" "$AGENDEV_ROOT/templates/pr-template.md"
  [ "$status" -eq 0 ]

  run grep -n "agendev:attention:start" "$AGENDEV_ROOT/templates/pr-template.md"
  [ "$status" -eq 0 ]

  run grep -n "agendev:attention:end" "$AGENDEV_ROOT/templates/pr-template.md"
  [ "$status" -eq 0 ]
}

@test "repo resolution parses SSH GitHub remotes" {
  run_bash '
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::repo_from_remote "git@github.com:owner/example.git"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "owner/example" ]
}

@test "agendev root resolves to the project root when sourced directly" {
  run_bash '
    unset AGENDEV_ROOT
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::root
  '

  [ "$status" -eq 0 ]
  [ "$output" = "$AGENDEV_ROOT" ]
}

@test "repo resolution honors AGENDEV_REPO override" {
  run_bash '
    export AGENDEV_REPO="override/repo"
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::repo_from_remote "git@gitlab.com:owner/example.git"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "override/repo" ]
}

@test "repo resolution parses HTTPS GitHub remotes" {
  run_bash '
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::repo_from_remote "https://github.com/owner/example.git"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "owner/example" ]
}

@test "repo resolution fails for non-GitHub remotes" {
  run_bash '
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::repo_from_remote "git@gitlab.com:owner/example.git"
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Origin remote is not a GitHub URL"* ]]
}

@test "origin resolution fails cleanly when the repository has no origin remote" {
  repo_dir="$TEST_TMPDIR/repo"
  make_git_repo "$repo_dir"
  git -C "$repo_dir" remote remove origin

  run_bash '
    cd "'"$repo_dir"'"
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::origin_url
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"No 'origin' remote found. agendev requires a GitHub-hosted repo."* ]]
}

@test "target root resolution fails outside a git repository" {
  mkdir -p "$TEST_TMPDIR/outside"
  cd "$TEST_TMPDIR/outside"

  run_bash '
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::target_root
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Run agendev from inside a git repository."* ]]
}

@test "repo resolution from origin works inside a git repository" {
  make_git_repo "$TEST_TMPDIR/repo" "git@github.com:owner/example.git"
  cd "$TEST_TMPDIR/repo"

  run_bash '
    export AGENDEV_CONFIG="'"$AGENDEV_CONFIG"'"
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::repo
  '

  [ "$status" -eq 0 ]
  [ "$output" = "owner/example" ]
}

@test "target root resolution honors TARGET_ROOT override" {
  override_dir="$TEST_TMPDIR/override"
  mkdir -p "$override_dir"

  run_bash '
    export TARGET_ROOT="'"$override_dir"'"
    source "'"$AGENDEV_ROOT"'/scripts/lib/common.sh"
    agendev::target_root
  '

  [ "$status" -eq 0 ]
  [ "$output" = "$override_dir" ]
}
