#!/usr/bin/env bats

load test_helper

write_run_config() {
  local path="$1"
  cat >"$path" <<'EOF'
{
  "labels": {
    "ready": "agendev:ready",
    "inProgress": "agendev:in-progress",
    "done": "agendev:done",
    "needsReview": "agendev:needs-human-review",
    "blocked": "agendev:blocked",
    "maintenanceReview": "agendev:maintenance-review"
  },
  "identity": {
    "appSlug": "agendev",
    "handle": "agendev"
  },
  "authorization": {
    "minimumPermission": "write",
    "denyResponse": "comment"
  },
  "maxRounds": 5,
  "maxTokenBudget": 500000,
  "tokenCost": {
    "inputPerMillion": 0,
    "cachedInputPerMillion": 0,
    "outputPerMillion": 0
  },
  "autoMerge": {
    "enabled": true,
    "requireVerification": true,
    "requireZeroCritical": true,
    "maxComplexity": "low"
  },
  "reviewers": ["username"],
  "branchPrefix": "agendev/",
  "worktreePrefix": "agendev-wt-",
  "consecutiveFailureLimit": 3,
  "verification": {
    "testCommand": "true",
    "buildCommand": "true"
  },
  "stall": {
    "timeoutSeconds": 600
  }
}
EOF
}

happy_issue_body() {
  cat <<'EOF'
<!-- agendev:meta
depends_on: []
priority: 1
estimated_complexity: low
-->

## Acceptance Criteria

- [ ] Adds the queue implementation file.
EOF
}

write_issue_view_scenario_rules() {
  local issue_body="$1"
  cat <<EOF
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"agendev:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"agendev:ready"}],"url":"https://example.test/issues/42"}')
  }
EOF
}

setup_run_fixture_env() {
  local local_dir="$1"

  export TARGET_ROOT="$local_dir"
  export AGENDEV_REPO="owner/repo"
  export REPO="owner/repo"
  export GH_TOKEN="existing-token"
  export AGENDEV_TEST_RUN_MODE="fixture"
  export AGENDEV_TEST_ORCHESTRATOR_RETURN_FILE=""
  export AGENDEV_TEST_CODEX_OUTPUT_FILE=""
  export AGENDEV_TEST_DEV_COMMAND=""
  export AGENDEV_CONFIG="$TEST_TMPDIR/config.json"
  write_run_config "$AGENDEV_CONFIG"
}

write_needs_review_scenario() {
  local scenario="$1"
  local issue_body="$2"
  local issue_comment="$3"

  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:ready", "--add-label", "agendev:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "agendev/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "body"],
    "stdout_file": "$AGENDEV_ROOT/test/fixtures/comments/pr-view-body.json"
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "87", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--add-reviewer", "username", "--add-assignee", "username"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:in-progress", "--add-label", "agendev:needs-human-review"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "$issue_comment"],
    "stdout": ""
  }
]
EOF
}

write_interrupted_run_scenario() {
  local scenario="$1"

  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "number"],
    "stdout": "{\"number\":87}"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: DEVELOP round 1. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: DEVELOP round 1. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  }
]
EOF
}

@test "agendev run --issue executes the single-issue happy path end to end" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export AGENDEV_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export AGENDEV_TEST_ORCHESTRATOR_RETURN_FILE="$TEST_TMPDIR/orchestrator-return.json"
  cat >"$AGENDEV_TEST_ORCHESTRATOR_RETURN_FILE" <<'EOF'
{
  "verdict": "PASS",
  "rounds_used": 1,
  "final_score": 42,
  "summary": "Implemented the queue file and verified the branch.",
  "caveats": [],
  "tokens_used": 1234
}
EOF

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"agendev:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"agendev:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:ready", "--add-label", "agendev:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "agendev/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "body"],
    "stdout_file": "$AGENDEV_ROOT/test/fixtures/comments/pr-view-body.json"
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "87", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "merge", "87", "--repo", "owner/repo", "--auto", "--squash"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:in-progress", "--add-label", "agendev:done"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42'

  [ "$status" -eq 0 ]

  run jq -r '.phase' "$local_dir/.agendev/state/42.json"
  [ "$status" -eq 0 ]
  [ "$output" = "DONE" ]

  worktree_path="$(cd "$local_dir/.." && pwd)/agendev-wt-42"
  [ ! -e "$worktree_path" ]

  run cat "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"issue edit 42 --repo owner/repo --remove-label agendev:ready --add-label agendev:in-progress"* ]]
  [[ "$output" == *"pr create --repo owner/repo --draft --title Implement queue --head agendev/42-implement-queue"* ]]
  [[ "$output" == *"pr ready 87 --repo owner/repo"* ]]
  [[ "$output" == *"pr merge 87 --repo owner/repo --auto --squash"* ]]
  [[ "$output" == *"issue edit 42 --repo owner/repo --remove-label agendev:in-progress --add-label agendev:done"* ]]

  run rg -n "agendev:payload:github-orchestrator-dispatch" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "agendev:payload:codex-return" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "agendev:payload:orchestrator-return" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "Implemented the queue file and verified the branch" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "agendev run --issue escalates no-commit runs to needs-human-review with verification comments" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export AGENDEV_TEST_DEV_COMMAND='true'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: no new commits were created, branch tip is not pushed to origin."
  write_needs_review_scenario "$scenario" "$issue_body" "$issue_comment"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42'

  [ "$status" -eq 0 ]
  run jq -r '.phase' "$local_dir/.agendev/state/42.json"
  [ "$output" = "FAILED" ]

  run cat "$FAKE_GH_LOG"
  [[ "$output" == *"issue edit 42 --repo owner/repo --remove-label agendev:in-progress --add-label agendev:needs-human-review"* ]]
  [[ "$output" == *"pr edit 87 --repo owner/repo --add-reviewer username --add-assignee username"* ]]

  run rg -n "Post-dev verification failed: no new commits were created, branch tip is not pushed to origin" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "Assigned to @username for human review. Reason: post-dev verification failed: no new commits were created, branch tip is not pushed to origin." "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "agendev run --issue escalates failing test and build verification to needs-human-review" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export AGENDEV_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  jq '.verification.testCommand = "false" | .verification.buildCommand = "false"' "$AGENDEV_CONFIG" >"$TEST_TMPDIR/config-fail.json"
  export AGENDEV_CONFIG="$TEST_TMPDIR/config-fail.json"

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: test command failed, build command failed."
  write_needs_review_scenario "$scenario" "$issue_body" "$issue_comment"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42'

  [ "$status" -eq 0 ]
  run rg -n "Post-dev verification failed: test command failed, build command failed" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "agendev run --issue escalates missing push verification failures to needs-human-review" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export AGENDEV_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: branch tip is not pushed to origin."
  write_needs_review_scenario "$scenario" "$issue_body" "$issue_comment"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42'

  [ "$status" -eq 0 ]
  run rg -n "Post-dev verification failed: branch tip is not pushed to origin" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "agendev run --issue comments when payloads are synthesized from malformed Codex output" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export AGENDEV_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export AGENDEV_TEST_CODEX_OUTPUT_FILE="$AGENDEV_ROOT/test/fixtures/payloads/codex-return-malformed.txt"

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: payload reported failing tests, payload reported failing build."
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:ready", "--add-label", "agendev:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "agendev/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "body"],
    "stdout_file": "$AGENDEV_ROOT/test/fixtures/comments/pr-view-body.json"
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "87", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "edit", "87", "--repo", "owner/repo", "--add-reviewer", "username", "--add-assignee", "username"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:in-progress", "--add-label", "agendev:needs-human-review"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "$issue_comment"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42'

  [ "$status" -eq 0 ]
  run rg -n "Codex payload required reconstruction. Source=synthetic" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "payload_missing_or_malformed" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "agendev run --issue records watchdog stalls and preserves state for reconciliation" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  jq '.stall.timeoutSeconds = 1' "$AGENDEV_CONFIG" >"$TEST_TMPDIR/config-stall.json"
  export AGENDEV_CONFIG="$TEST_TMPDIR/config-stall.json"
  export AGENDEV_TEST_DEV_COMMAND='sleep 2'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:ready", "--add-label", "agendev:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "agendev/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Agent stalled after 1 seconds of inactivity. Process terminated. State preserved for resume."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42'

  [ "$status" -eq 124 ]
  run jq -r '.phase' "$local_dir/.agendev/state/42.json"
  [ "$output" = "DEVELOP" ]
  run jq -r '.stall.timed_out' "$local_dir/.agendev/state/42.json"
  [ "$output" = "true" ]
  run rg -n "Agent stalled after 1 seconds of inactivity. Process terminated. State preserved for resume." "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "agendev run --issue comments on agent crashes and leaves interrupted state for startup reconciliation" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export AGENDEV_TEST_DEV_COMMAND='exit 23'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "agendev:ready", "--add-label", "agendev:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "agendev/42-implement-queue"],
    "stdout": "https://example.test/pull/87"
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Agent exited unexpectedly (exit code 23). Last phase: DEVELOP, round 1."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --issue 42'

  [ "$status" -eq 23 ]
  run jq -r '.phase' "$local_dir/.agendev/state/42.json"
  [ "$output" = "DEVELOP" ]
  run rg -n "Agent exited unexpectedly \\(exit code 23\\). Last phase: DEVELOP, round 1." "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "agendev run --dry-run performs startup reconciliation before reporting queue state" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  mkdir -p "$local_dir/.agendev/state"
  git -C "$local_dir" checkout -b agendev/42-implement-queue >/dev/null 2>&1
  echo "work" >"$local_dir/work.txt"
  git -C "$local_dir" add work.txt
  git -C "$local_dir" commit -m "Work in progress" >/dev/null
  git -C "$local_dir" push -u origin agendev/42-implement-queue >/dev/null 2>&1
  cat >"$local_dir/.agendev/state/42.json" <<'EOF'
{
  "issue": 42,
  "phase": "DEVELOP",
  "round": 1,
  "branch": "agendev/42-implement-queue",
  "pr_number": 87,
  "updated_at": "2026-03-17T00:00:00Z"
}
EOF
  git -C "$local_dir" checkout main >/dev/null 2>&1

  scenario="$TEST_TMPDIR/scenario.json"
  write_interrupted_run_scenario "$scenario"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$AGENDEV_ROOT"'/bin/agendev" run --dry-run'

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.mode')" = "dry-run" ]
  [ "$(printf '%s' "$output" | jq -r '.reconciliation[0].action')" = "resume" ]
}
