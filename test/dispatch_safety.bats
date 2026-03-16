#!/usr/bin/env bats

load test_helper

prepare_dispatch_repo() {
  local remote_dir="$1"
  local local_dir="$2"
  make_remote_backed_repo "$remote_dir" "$local_dir"
  git -C "$local_dir" remote set-url origin "$remote_dir"
}

write_issue_state_file() {
  local path="$1"
  local issue="$2"
  local phase="$3"
  local round="$4"
  local branch="$5"
  local pr_number="$6"

  cat >"$path" <<EOF
{
  "issue": $issue,
  "phase": "$phase",
  "round": $round,
  "branch": "$branch",
  "pr_number": $pr_number,
  "updated_at": "2026-03-17T00:00:00Z"
}
EOF
}

issue_body_with_meta() {
  local depends_on="${1:-[]}"
  cat <<EOF
<!-- agendev:meta
depends_on: $depends_on
priority: 2
estimated_complexity: low
-->

## Acceptance Criteria

- [ ] Works.
EOF
}

prepare_conflicting_branch() {
  local remote_dir="$1"
  local local_dir="$2"
  prepare_dispatch_repo "$remote_dir" "$local_dir"

  echo "base" >"$local_dir/conflict.txt"
  git -C "$local_dir" add conflict.txt
  git -C "$local_dir" commit -m "Add conflict file" >/dev/null
  git -C "$local_dir" push origin main >/dev/null 2>&1

  git -C "$local_dir" checkout -b agendev/42-implement-queue >/dev/null 2>&1
  echo "branch change" >"$local_dir/conflict.txt"
  git -C "$local_dir" add conflict.txt
  git -C "$local_dir" commit -m "Branch change" >/dev/null
  git -C "$local_dir" push -u origin agendev/42-implement-queue >/dev/null 2>&1

  git -C "$local_dir" checkout main >/dev/null 2>&1
  echo "main change" >"$local_dir/conflict.txt"
  git -C "$local_dir" add conflict.txt
  git -C "$local_dir" commit -m "Main change" >/dev/null
  git -C "$local_dir" push origin main >/dev/null 2>&1
}

@test "dispatch safety reconcile resumes recoverable orphaned runs" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  prepare_dispatch_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  export AGENDEV_STATE_DIR="$local_dir/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  git -C "$local_dir" checkout -b agendev/42-implement-queue >/dev/null 2>&1
  echo "work" >"$local_dir/work.txt"
  git -C "$local_dir" add work.txt
  git -C "$local_dir" commit -m "Work in progress" >/dev/null
  git -C "$local_dir" push -u origin agendev/42-implement-queue >/dev/null 2>&1
  write_issue_state_file "$AGENDEV_STATE_DIR/42.json" 42 REVIEW 2 agendev/42-implement-queue 87

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "number"],
    "stdout": "{\"number\":87}"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: REVIEW round 2. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: REVIEW round 2. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/dispatch-safety.sh" reconcile owner/repo

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.[0].action')" = "resume" ]
  [ "$(printf '%s' "$output" | jq -r '.[0].issue')" = "42" ]
}

@test "dispatch safety reconcile marks unrecoverable orphaned runs for human review" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  prepare_dispatch_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  export AGENDEV_STATE_DIR="$local_dir/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"
  write_issue_state_file "$AGENDEV_STATE_DIR/42.json" 42 DEVELOP 1 agendev/42-implement-queue 87

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "number"],
    "exit_code": 1,
    "stderr": "not found"
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
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: DEVELOP round 1. Marking for human review."],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[]"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/dispatch-safety.sh" reconcile owner/repo

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.[0].action')" = "needs-review" ]
}

@test "dispatch safety reconcile resets stale in-progress labels when no active state exists" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  prepare_dispatch_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  export AGENDEV_STATE_DIR="$local_dir/.agendev/state"
  mkdir -p "$AGENDEV_STATE_DIR"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "agendev:in-progress"],
    "stdout": "[{\"number\":43,\"title\":\"Implement queue\",\"labels\":[{\"name\":\"agendev:in-progress\"}]}]"
  },
  {
    "contains": ["issue", "view", "43", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"agendev:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "43", "--repo", "owner/repo", "--remove-label", "agendev:in-progress", "--add-label", "agendev:ready"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "43", "--repo", "owner/repo", "--body", "Found stale agendev:in-progress label with no active run. Reset to agendev:ready."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/dispatch-safety.sh" reconcile owner/repo

  [ "$status" -eq 0 ]
  [ "$(printf '%s' "$output" | jq -r '.[0].action')" = "reset-ready" ]
}

@test "dispatch safety eligibility skips issues missing acceptance criteria" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  prepare_dispatch_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<'EOF'
[
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": "{\"number\":42,\"title\":\"Implement queue\",\"body\":\"No acceptance criteria here.\",\"labels\":[{\"name\":\"agendev:ready\"}],\"url\":\"https://example.test/issues/42\"}"
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Skipped: missing acceptance criteria."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/dispatch-safety.sh" eligibility owner/repo 42

  [ "$status" -eq 1 ]
  [ "$(printf '%s' "$output" | jq -r '.allowed')" = "false" ]
  [[ "$output" == *"missing acceptance criteria"* ]]
}

@test "dispatch safety eligibility rejects incomplete dependencies" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  prepare_dispatch_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  body="$(issue_body_with_meta "[12]")"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"agendev:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["issue", "view", "12", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"number\":12,\"labels\":[{\"name\":\"agendev:ready\"}]}"
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Skipped: dependency #12 is not agendev:done."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/dispatch-safety.sh" eligibility owner/repo 42

  [ "$status" -eq 1 ]
  [[ "$output" == *"dependency #12 is not agendev:done"* ]]
}

@test "dispatch safety eligibility rejects issues with an existing open PR" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  prepare_dispatch_repo "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  body="$(issue_body_with_meta "[]")"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"agendev:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[{\"number\":88,\"url\":\"https://example.test/pull/88\"}]"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Skipped: existing open PR #88 already tracks this issue."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/dispatch-safety.sh" eligibility owner/repo 42

  [ "$status" -eq 1 ]
  [[ "$output" == *"existing open PR #88 already tracks this issue"* ]]
}

@test "dispatch safety eligibility rejects remote branches with unresolved conflicts" {
  remote_dir="$TEST_TMPDIR/remote.git"
  local_dir="$TEST_TMPDIR/local"
  prepare_conflicting_branch "$remote_dir" "$local_dir"
  export TARGET_ROOT="$local_dir"
  body="$(issue_body_with_meta "[]")"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": $(jq -Rn --arg body "$body" '{"number":42,"title":"Implement queue","body":$body,"labels":[{"name":"agendev:ready"}],"url":"https://example.test/issues/42"}')
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "agendev/42-implement-queue"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Skipped: branch agendev/42-implement-queue has unresolved conflicts with origin/main."],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$AGENDEV_ROOT/scripts/dispatch-safety.sh" eligibility owner/repo 42

  [ "$status" -eq 1 ]
  [[ "$output" == *"has unresolved conflicts with origin/main"* ]]
}
