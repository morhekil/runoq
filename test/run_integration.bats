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

@test "agendev run --issue executes the single-issue happy path end to end" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
  export TARGET_ROOT="$local_dir"
  export AGENDEV_REPO="owner/repo"
  export REPO="owner/repo"
  export GH_TOKEN="existing-token"
  export AGENDEV_TEST_RUN_MODE="fixture"
  export AGENDEV_TEST_DEV_COMMAND='mkdir -p src && printf "export const queue = true;\n" > src/queue.ts && git add src/queue.ts && git commit -m "Add queue implementation" >/dev/null && git push -u origin HEAD >/dev/null 2>&1'
  export AGENDEV_TEST_ORCHESTRATOR_RETURN_FILE="$TEST_TMPDIR/orchestrator-return.json"
  export AGENDEV_CONFIG="$TEST_TMPDIR/config.json"
  write_run_config "$AGENDEV_CONFIG"
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
