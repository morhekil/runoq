#!/usr/bin/env bats

load test_helper

@test "config has required top-level keys" {
  run jq -e '
    .labels.ready and
    .labels.inProgress and
    .labels.done and
    .labels.needsReview and
    .labels.blocked and
    .labels.planApproved and
    .identity.appSlug and
    .identity.handle and
    .authorization.minimumPermission and
    .maxRounds and
    .maxTokenBudget and
    .verification.testCommand and
    .verification.buildCommand and
    .stall.timeoutSeconds
  ' "$RUNOQ_CONFIG"

  [ "$status" -eq 0 ]
}

@test "issue template includes acceptance criteria section" {
  run grep -n "## Acceptance Criteria" "$RUNOQ_ROOT/templates/issue-template.md"
  [ "$status" -eq 0 ]
}

@test "pr template includes marker-delimited summary section" {
  run grep -n "runoq:summary:start" "$RUNOQ_ROOT/templates/pr-template.md"
  [ "$status" -eq 0 ]

  run grep -n "runoq:summary:end" "$RUNOQ_ROOT/templates/pr-template.md"
  [ "$status" -eq 0 ]
}

@test "repo resolution parses SSH GitHub remotes" {
  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::repo_from_remote "git@github.com:owner/example.git"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "owner/example" ]
}

@test "runoq root resolves to the project root when sourced directly" {
  run_bash '
    unset RUNOQ_ROOT
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::root
  '

  [ "$status" -eq 0 ]
  [ "$output" = "$RUNOQ_ROOT" ]
}

@test "repo resolution honors RUNOQ_REPO override" {
  run_bash '
    export RUNOQ_REPO="override/repo"
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::repo_from_remote "git@gitlab.com:owner/example.git"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "override/repo" ]
}

@test "repo resolution parses HTTPS GitHub remotes" {
  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::repo_from_remote "https://github.com/owner/example.git"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "owner/example" ]
}

@test "repo resolution parses authenticated HTTPS GitHub remotes" {
  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::repo_from_remote "https://x-access-token:ghs_example@github.com/owner/example.git"
  '

  [ "$status" -eq 0 ]
  [ "$output" = "owner/example" ]
}

@test "repo resolution fails for non-GitHub remotes" {
  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::repo_from_remote "git@gitlab.com:owner/example.git"
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
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::origin_url
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"No 'origin' remote found. runoq requires a GitHub-hosted repo."* ]]
}

@test "target root resolution fails outside a git repository" {
  mkdir -p "$TEST_TMPDIR/outside"
  cd "$TEST_TMPDIR/outside"

  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::target_root
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"Run runoq from inside a git repository."* ]]
}

@test "repo resolution from origin works inside a git repository" {
  make_git_repo "$TEST_TMPDIR/repo" "git@github.com:owner/example.git"
  cd "$TEST_TMPDIR/repo"

  run_bash '
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::repo
  '

  [ "$status" -eq 0 ]
  [ "$output" = "owner/example" ]
}

@test "target root resolution honors TARGET_ROOT override" {
  override_dir="$TEST_TMPDIR/override"
  mkdir -p "$override_dir"

  run_bash '
    export TARGET_ROOT="'"$override_dir"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::target_root
  '

  [ "$status" -eq 0 ]
  [ "$output" = "$override_dir" ]
}

@test "plan file resolution reads plan from target runoq.json" {
  project_dir="$TEST_TMPDIR/project"
  mkdir -p "$project_dir"
  cat >"$project_dir/runoq.json" <<'EOF'
{
  "plan": "docs/prd.md"
}
EOF

  run_bash '
    export TARGET_ROOT="'"$project_dir"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::plan_file
  '

  [ "$status" -eq 0 ]
  [ "$output" = "docs/prd.md" ]
}

@test "plan file resolution fails clearly when target runoq.json is missing" {
  project_dir="$TEST_TMPDIR/project-missing"
  mkdir -p "$project_dir"

  run_bash '
    export TARGET_ROOT="'"$project_dir"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::plan_file
  '

  [ "$status" -ne 0 ]
  [[ "$output" == *"runoq.json"* ]]
}

@test "all state labels include plan approval" {
  run_bash '
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::all_state_labels
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"runoq:plan-approved"* ]]
}
