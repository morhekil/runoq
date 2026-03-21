#!/usr/bin/env bats

load test_helper

write_run_config() {
  local path="$1"
  cat >"$path" <<'EOF'
{
  "labels": {
    "ready": "runoq:ready",
    "inProgress": "runoq:in-progress",
    "done": "runoq:done",
    "needsReview": "runoq:needs-human-review",
    "blocked": "runoq:blocked",
    "maintenanceReview": "runoq:maintenance-review"
  },
  "identity": {
    "appSlug": "runoq",
    "handle": "runoq"
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
  "branchPrefix": "runoq/",
  "worktreePrefix": "runoq-wt-",
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
<!-- runoq:meta
depends_on: []
priority: 1
estimated_complexity: low
-->

## Acceptance Criteria

- [ ] Adds the queue implementation file.
EOF
}

queue_issue_body() {
  local depends_on="$1"
  local priority="$2"
  cat <<EOF
<!-- runoq:meta
depends_on: $depends_on
priority: $priority
estimated_complexity: low
-->

## Acceptance Criteria

- [ ] Queue task succeeds.
EOF
}

write_issue_view_scenario_rules() {
  local issue_body="$1"
  cat <<EOF
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  }
EOF
}

setup_run_fixture_env() {
  local local_dir="$1"

  export TARGET_ROOT="$local_dir"
  export RUNOQ_REPO="owner/repo"
  export REPO="owner/repo"
  export GH_TOKEN="existing-token"
  export RUNOQ_TEST_RUN_MODE="fixture"
  export RUNOQ_TEST_ORCHESTRATOR_RETURN_FILE=""
  export RUNOQ_TEST_CODEX_OUTPUT_FILE=""
  export RUNOQ_TEST_DEV_COMMAND=""
  export RUNOQ_CONFIG="$TEST_TMPDIR/config.json"
  write_run_config "$RUNOQ_CONFIG"
}

write_needs_review_scenario() {
  local scenario="$1"
  local issue_body="$2"
  local issue_comment="$3"

  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
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
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
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
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:needs-human-review"],
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
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  }
]
EOF
}

@test "runoq run --issue executes the single-issue happy path end to end" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export RUNOQ_TEST_ORCHESTRATOR_RETURN_FILE="$TEST_TMPDIR/orchestrator-return.json"
  cat >"$RUNOQ_TEST_ORCHESTRATOR_RETURN_FILE" <<'EOF'
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
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
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
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
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
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:done"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'

  [ "$status" -eq 0 ]

  run jq -r '.phase' "$local_dir/.runoq/state/42.json"
  [ "$status" -eq 0 ]
  [ "$output" = "DONE" ]

  worktree_path="$(cd "$local_dir/.." && pwd)/runoq-wt-42"
  [ ! -e "$worktree_path" ]

  run cat "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"issue edit 42 --repo owner/repo --remove-label runoq:ready --add-label runoq:in-progress"* ]]
  [[ "$output" == *"pr create --repo owner/repo --draft --title Implement queue --head runoq/42-implement-queue"* ]]
  [[ "$output" == *"pr ready 87 --repo owner/repo"* ]]
  [[ "$output" == *"pr merge 87 --repo owner/repo --auto --squash"* ]]
  [[ "$output" == *"issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:done"* ]]

  run rg -n "runoq:payload:github-orchestrator-dispatch" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "runoq:payload:codex-return" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "runoq:payload:orchestrator-return" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "Implemented the queue file and verified the branch" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --issue escalates no-commit runs to needs-human-review with verification comments" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export RUNOQ_TEST_DEV_COMMAND='true'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: no new commits were created, branch tip is not pushed to origin."
  write_needs_review_scenario "$scenario" "$issue_body" "$issue_comment"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'

  [ "$status" -eq 0 ]
  run jq -r '.phase' "$local_dir/.runoq/state/42.json"
  [ "$output" = "FAILED" ]

  run cat "$FAKE_GH_LOG"
  [[ "$output" == *"issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review"* ]]
  [[ "$output" == *"pr edit 87 --repo owner/repo --add-reviewer username --add-assignee username"* ]]

  run rg -n "Post-dev verification failed: no new commits were created, branch tip is not pushed to origin" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "Assigned to @username for human review. Reason: post-dev verification failed: no new commits were created, branch tip is not pushed to origin." "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --issue escalates failing test and build verification to needs-human-review" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  jq '.verification.testCommand = "false" | .verification.buildCommand = "false"' "$RUNOQ_CONFIG" >"$TEST_TMPDIR/config-fail.json"
  export RUNOQ_CONFIG="$TEST_TMPDIR/config-fail.json"

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: test command failed, build command failed."
  write_needs_review_scenario "$scenario" "$issue_body" "$issue_comment"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'

  [ "$status" -eq 0 ]
  run rg -n "Post-dev verification failed: test command failed, build command failed" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --issue escalates missing push verification failures to needs-human-review" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: branch tip is not pushed to origin."
  write_needs_review_scenario "$scenario" "$issue_body" "$issue_comment"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'

  [ "$status" -eq 0 ]
  run rg -n "Post-dev verification failed: branch tip is not pushed to origin" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --issue comments when payloads are synthesized from malformed Codex output" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export RUNOQ_TEST_CODEX_OUTPUT_FILE="$RUNOQ_ROOT/test/fixtures/payloads/codex-return-malformed.txt"

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  issue_comment="Escalated to human review: post-dev verification failed: payload reported failing tests, payload reported failing build."
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
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
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
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
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:needs-human-review"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "$issue_comment"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'

  [ "$status" -eq 0 ]
  run rg -n "Codex payload required reconstruction. Source=synthetic" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
  run rg -n "payload_missing_or_malformed" "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --issue records watchdog stalls and preserves state for reconciliation" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  jq '.stall.timeoutSeconds = 1' "$RUNOQ_CONFIG" >"$TEST_TMPDIR/config-stall.json"
  export RUNOQ_CONFIG="$TEST_TMPDIR/config-stall.json"
  export RUNOQ_TEST_DEV_COMMAND='sleep 2'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
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

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'

  [ "$status" -eq 124 ]
  run jq -r '.phase' "$local_dir/.runoq/state/42.json"
  [ "$output" = "DEVELOP" ]
  run jq -r '.stall.timed_out' "$local_dir/.runoq/state/42.json"
  [ "$output" = "true" ]
  run rg -n "Agent stalled after 1 seconds of inactivity. Process terminated. State preserved for resume." "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --issue comments on agent crashes and leaves interrupted state for startup reconciliation" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export RUNOQ_TEST_DEV_COMMAND='exit 23'

  issue_body="$(happy_issue_body)"
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
$(write_issue_view_scenario_rules "$issue_body"),
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement queue", "--head", "runoq/42-implement-queue"],
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

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --issue 42'

  [ "$status" -eq 23 ]
  run jq -r '.phase' "$local_dir/.runoq/state/42.json"
  [ "$output" = "DEVELOP" ]
  run rg -n "Agent exited unexpectedly \\(exit code 23\\). Last phase: DEVELOP, round 1." "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --dry-run performs startup reconciliation before reporting queue state" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  mkdir -p "$local_dir/.runoq/state"
  git -C "$local_dir" checkout -b runoq/42-implement-queue >/dev/null 2>&1
  echo "work" >"$local_dir/work.txt"
  git -C "$local_dir" add work.txt
  git -C "$local_dir" commit -m "Work in progress" >/dev/null
  git -C "$local_dir" push -u origin runoq/42-implement-queue >/dev/null 2>&1
  cat >"$local_dir/.runoq/state/42.json" <<'EOF'
{
  "issue": 42,
  "phase": "DEVELOP",
  "round": 1,
  "branch": "runoq/42-implement-queue",
  "pr_number": 87,
  "updated_at": "2026-03-17T00:00:00Z"
}
EOF
  git -C "$local_dir" checkout main >/dev/null 2>&1

  scenario="$TEST_TMPDIR/scenario.json"
  write_interrupted_run_scenario "$scenario"
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --dry-run'

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.mode')" = "dry-run" ]
  [ "$(printf '%s' "$output" | jq -r '.reconciliation[0].action')" = "resume" ]
}

@test "runoq run processes multiple queue issues in dependency order" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  export RUNOQ_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > "src/task-${RUNOQ_TEST_CURRENT_ISSUE}.ts" && git add src && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export RUNOQ_TEST_DEV_COMMAND_42='mkdir -p src && printf "export const alpha = true;\n" > src/task-42.ts && git add src/task-42.ts && git commit -m "Add task 42" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export RUNOQ_TEST_DEV_COMMAND_43='mkdir -p src && printf "export const beta = true;\n" > src/task-43.ts && git add src/task-43.ts && git commit -m "Add task 43" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export RUNOQ_TEST_ORCHESTRATOR_RETURN_FILE="$TEST_TMPDIR/orchestrator-return.json"
  cat >"$RUNOQ_TEST_ORCHESTRATOR_RETURN_FILE" <<'EOF'
{
  "verdict": "PASS",
  "rounds_used": 1,
  "final_score": 42,
  "summary": "Queue task completed cleanly.",
  "caveats": [],
  "tokens_used": 1234
}
EOF

  issue_42_body="$(queue_issue_body "[]" 1)"
  issue_43_body="$(queue_issue_body "[42]" 2)"
  ready_both="$(jq -n --arg body42 "$issue_42_body" --arg body43 "$issue_43_body" '[
    {number: 42, title: "Implement alpha", body: $body42, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"},
    {number: 43, title: "Implement beta", body: $body43, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/43"}
  ]')"
  ready_second="$(jq -n --arg body43 "$issue_43_body" '[
    {number: 43, title: "Implement beta", body: $body43, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/43"}
  ]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_both")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_42_body" '{"number":42,"title":"Implement alpha","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_42_body" '{"number":42,"title":"Implement alpha","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-alpha"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement alpha", "--head", "runoq/42-implement-alpha"],
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
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
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
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:done"],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_second")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,labels"],
    "stdout": "{\"number\":42,\"labels\":[{\"name\":\"runoq:done\"}]}"
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_43_body" '{"number":43,"title":"Implement beta","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/43"}')
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_43_body" '{"number":43,"title":"Implement beta","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/43"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"number\":42,\"labels\":[{\"name\":\"runoq:done\"}]}"
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/43-implement-beta"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "43", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Implement beta", "--head", "runoq/43-implement-beta"],
    "stdout": "https://example.test/pull/88"
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "view", "88", "--repo", "owner/repo", "--json", "body"],
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
  },
  {
    "contains": ["pr", "edit", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "88", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "merge", "88", "--repo", "owner/repo", "--auto", "--squash"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "43", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:done"],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": "[]"
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run'

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "completed" ]
  [ "$(printf '%s' "$output" | jq -r '.runs | length')" = "2" ]

  run cat "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" == *"issue view 42 --repo owner/repo --json number,labels"* ]]
  [[ "$output" == *"issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:done"* ]]
  [[ "$output" == *"issue edit 43 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:done"* ]]
}

@test "runoq run halts the queue when the consecutive failure limit is reached" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"
  jq '.consecutiveFailureLimit = 2' "$RUNOQ_CONFIG" >"$TEST_TMPDIR/config-circuit.json"
  export RUNOQ_CONFIG="$TEST_TMPDIR/config-circuit.json"
  export RUNOQ_TEST_DEV_COMMAND='true'

  issue_42_body="$(queue_issue_body "[]" 1)"
  issue_43_body="$(queue_issue_body "[]" 2)"
  issue_44_body="$(queue_issue_body "[]" 3)"
  ready_first="$(jq -n --arg body42 "$issue_42_body" --arg body43 "$issue_43_body" --arg body44 "$issue_44_body" '[
    {number: 42, title: "Fail alpha", body: $body42, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/42"},
    {number: 43, title: "Fail beta", body: $body43, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/43"},
    {number: 44, title: "Fail gamma", body: $body44, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/44"}
  ]')"
  ready_second="$(jq -n --arg body43 "$issue_43_body" --arg body44 "$issue_44_body" '[
    {number: 43, title: "Fail beta", body: $body43, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/43"},
    {number: 44, title: "Fail gamma", body: $body44, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/44"}
  ]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_first")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_42_body" '{"number":42,"title":"Fail alpha","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_42_body" '{"number":42,"title":"Fail alpha","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-fail-alpha"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Fail alpha", "--head", "runoq/42-fail-alpha"],
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
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
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
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:needs-human-review"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Escalated to human review: post-dev verification failed: no new commits were created, branch tip is not pushed to origin."],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_second")
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_43_body" '{"number":43,"title":"Fail beta","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/43"}')
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$issue_43_body" '{"number":43,"title":"Fail beta","body":$body,"labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/43"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/43-fail-beta"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:ready\"}]}"
  },
  {
    "contains": ["issue", "edit", "43", "--repo", "owner/repo", "--remove-label", "runoq:ready", "--add-label", "runoq:in-progress"],
    "stdout": ""
  },
  {
    "contains": ["pr", "create", "--repo", "owner/repo", "--draft", "--title", "Fail beta", "--head", "runoq/43-fail-beta"],
    "stdout": "https://example.test/pull/88"
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "view", "88", "--repo", "owner/repo", "--json", "body"],
    "stdout_file": "$RUNOQ_ROOT/test/fixtures/comments/pr-view-body.json"
  },
  {
    "contains": ["pr", "edit", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["pr", "ready", "88", "--repo", "owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["pr", "edit", "88", "--repo", "owner/repo", "--add-reviewer", "username", "--add-assignee", "username"],
    "stdout": ""
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "43", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:needs-human-review"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "43", "--repo", "owner/repo", "--body", "Escalated to human review: post-dev verification failed: no new commits were created, branch tip is not pushed to origin."],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "88", "--repo", "owner/repo", "--body-file"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "43", "--repo", "owner/repo", "--body", "Queue halted after 2 consecutive failures. Failed issues: #42, #43. Investigate before resuming."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run'

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.status')" = "halted" ]
  [ "$(printf '%s' "$output" | jq -r '.failed_issues | join(",")')" = "42,43" ]

  run cat "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" != *"issue view 44 --repo owner/repo --json number,title,body,labels,url"* ]]
  run rg -n "Queue halted after 2 consecutive failures. Failed issues: #42, #43. Investigate before resuming." "$FAKE_GH_CAPTURE_DIR"
  [ "$status" -eq 0 ]
}

@test "runoq run --dry-run reports queue selection and blocked reasons without dispatch mutation" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  setup_run_fixture_env "$local_dir"

  blocked_body="$(queue_issue_body "[42]" 1)"
  ready_body="$(queue_issue_body "[]" 2)"
  ready_queue="$(jq -n --arg blocked "$blocked_body" --arg ready "$ready_body" '[
    {number: 43, title: "Blocked beta", body: $blocked, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/43"},
    {number: 44, title: "Ready gamma", body: $ready, labels: [{name:"runoq:ready"}], url: "https://example.test/issues/44"}
  ]')"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:ready"],
    "stdout": $(printf '%s' "$ready_queue")
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,labels"],
    "stdout": "{\"number\":42,\"labels\":[{\"name\":\"runoq:ready\"}]}"
  }
]
EOF
  use_fake_gh "$scenario"

  run bash -lc 'cd "'"$local_dir"'" && "'"$RUNOQ_ROOT"'/bin/runoq" run --dry-run'

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.mode')" = "dry-run" ]
  [ "$(printf '%s' "$output" | jq -r '.selection.issue.number')" = "44" ]
  [ "$(printf '%s' "$output" | jq -r '.selection.skipped[0].blocked_reasons[0]')" = "dependency #42 is not runoq:done" ]

  run cat "$FAKE_GH_LOG"
  [ "$status" -eq 0 ]
  [[ "$output" != *"issue edit"* ]]
  [[ "$output" != *"pr create"* ]]
}
