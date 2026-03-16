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
