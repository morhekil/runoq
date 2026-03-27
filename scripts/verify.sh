#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=./scripts/lib/common.sh
source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  verify.sh round <worktree> <branch> <base-sha> <payload-file>
  verify.sh integrate <worktree> <criteria-commit>
EOF
}

ground_truth_json() {
  local worktree="$1"
  local base_sha="$2"
  local commits_json diff_json
  commits_json="$(git -C "$worktree" rev-list --reverse "${base_sha}..HEAD" | jq -Rsc 'split("\n") | map(select(length > 0))')"
  diff_json="$(git -C "$worktree" diff --name-status "${base_sha}..HEAD" | jq -Rsc '
    split("\n")
    | map(select(length > 0))
    | map(split("\t"))
    | {
        files_changed: [ .[] | select(.[0] != "A" and .[0] != "D") | .[-1] ],
        files_added: [ .[] | select(.[0] == "A") | .[-1] ],
        files_deleted: [ .[] | select(.[0] == "D") | .[-1] ]
      }
  ')"

  jq -n \
    --argjson commits "$commits_json" \
    --argjson diff "$diff_json" '
      {
        commits_pushed: $commits,
        files_changed: $diff.files_changed,
        files_added: $diff.files_added,
        files_deleted: $diff.files_deleted
      }
    '
}

run_check_command() {
  local worktree="$1"
  local command="$2"
  bash -lc "cd \"$worktree\" && $command"
}

verify_round() {
  local worktree="$1"
  local branch="$2"
  local base_sha="$3"
  local payload_file="$4"
  local truth_file failures_file remote_sha local_sha test_command build_command
  truth_file="$(mktemp "${TMPDIR:-/tmp}/runoq-verify-truth.XXXXXX")"
  failures_file="$(mktemp "${TMPDIR:-/tmp}/runoq-verify-failures.XXXXXX")"
  printf '[]\n' >"$failures_file"

  ground_truth_json "$worktree" "$base_sha" >"$truth_file"

  if [[ "$(jq -r '.commits_pushed | length' "$truth_file")" -eq 0 ]]; then
    jq '. + ["no new commits were created"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  while IFS= read -r sha; do
    [[ -z "$sha" ]] && continue
    if ! git -C "$worktree" rev-parse --verify "$sha^{commit}" >/dev/null 2>&1; then
      jq --arg failure "missing commit $sha" '. + [$failure]' "$failures_file" >"${failures_file}.tmp"
      mv "${failures_file}.tmp" "$failures_file"
    fi
  done < <(jq -r '.commits_pushed[]?' "$payload_file")

  if ! jq -en --slurpfile payload "$payload_file" --slurpfile truth "$truth_file" '
    ($payload[0].files_changed == $truth[0].files_changed) and
    ($payload[0].files_added == $truth[0].files_added) and
    ($payload[0].files_deleted == $truth[0].files_deleted)
  ' >/dev/null; then
    jq '. + ["file lists do not match ground truth"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  local_sha="$(git -C "$worktree" rev-parse HEAD)"
  remote_sha="$(git -C "$worktree" ls-remote origin "$branch" | awk '{print $1}')"
  if [[ -z "$remote_sha" || "$remote_sha" != "$local_sha" ]]; then
    jq '. + ["branch tip is not pushed to origin"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  test_command="$(runoq::config_get '.verification.testCommand')"
  build_command="$(runoq::config_get '.verification.buildCommand')"
  [[ -n "$test_command" && "$test_command" != "null" ]] || runoq::die "verification.testCommand is not configured"
  [[ -n "$build_command" && "$build_command" != "null" ]] || runoq::die "verification.buildCommand is not configured"

  if ! run_check_command "$worktree" "$test_command" >/dev/null 2>&1; then
    jq '. + ["test command failed"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  if ! run_check_command "$worktree" "$build_command" >/dev/null 2>&1; then
    jq '. + ["build command failed"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  if [[ "$(jq -r '.tests_passed' "$payload_file")" != "true" ]]; then
    jq '. + ["payload reported failing tests"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  if [[ "$(jq -r '.build_passed' "$payload_file")" != "true" ]]; then
    jq '. + ["payload reported failing build"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  # Criteria tamper check (only if criteria_commit is present in payload)
  local criteria_commit
  criteria_commit="$(jq -r '.criteria_commit // empty' "$payload_file")"
  if [[ -n "$criteria_commit" ]]; then
    # Get the list of files from the criteria commit
    local criteria_files_list
    criteria_files_list="$(git -C "$worktree" diff-tree --no-commit-id --name-only -r "$criteria_commit" 2>/dev/null || true)"
    if [[ -n "$criteria_files_list" ]]; then
      local tampered_files=""
      while IFS= read -r cfile; do
        [[ -z "$cfile" ]] && continue
        # Compare the file at criteria_commit vs HEAD
        if ! git -C "$worktree" diff --quiet "$criteria_commit" HEAD -- "$cfile" 2>/dev/null; then
          tampered_files="${tampered_files:+$tampered_files, }$cfile"
        fi
      done <<< "$criteria_files_list"
      if [[ -n "$tampered_files" ]]; then
        jq --arg failure "criteria tampered: $tampered_files" '. + [$failure]' "$failures_file" >"${failures_file}.tmp"
        mv "${failures_file}.tmp" "$failures_file"
      fi
    fi
  fi

  jq -n \
    --slurpfile truth "$truth_file" \
    --slurpfile failures "$failures_file" '
    {
      ok: (($failures[0] | length) == 0),
      review_allowed: (($failures[0] | length) == 0),
      failures: $failures[0],
      actual: $truth[0]
    }
  '

  rm -f "$truth_file" "$failures_file"
}

verify_integrate() {
  local worktree="$1"
  local criteria_commit="$2"
  local failures_file test_command
  failures_file="$(mktemp "${TMPDIR:-/tmp}/runoq-verify-integrate.XXXXXX")"
  printf '[]\n' >"$failures_file"

  # Check criteria files are unchanged
  local criteria_files_list
  criteria_files_list="$(git -C "$worktree" diff-tree --no-commit-id --name-only -r "$criteria_commit" 2>/dev/null || true)"
  if [[ -n "$criteria_files_list" ]]; then
    while IFS= read -r cfile; do
      [[ -z "$cfile" ]] && continue
      if [[ ! -f "$worktree/$cfile" ]]; then
        jq --arg failure "criteria file missing: $cfile" '. + [$failure]' "$failures_file" >"${failures_file}.tmp"
        mv "${failures_file}.tmp" "$failures_file"
      elif ! git -C "$worktree" diff --quiet "$criteria_commit" HEAD -- "$cfile" 2>/dev/null; then
        jq --arg failure "criteria tampered: $cfile" '. + [$failure]' "$failures_file" >"${failures_file}.tmp"
        mv "${failures_file}.tmp" "$failures_file"
      fi
    done <<< "$criteria_files_list"
  else
    jq '. + ["no criteria files found in criteria commit"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  # Run test suite
  test_command="$(runoq::config_get '.verification.testCommand')"
  [[ -n "$test_command" && "$test_command" != "null" ]] || runoq::die "verification.testCommand is not configured"
  if ! run_check_command "$worktree" "$test_command" >/dev/null 2>&1; then
    jq '. + ["test command failed"]' "$failures_file" >"${failures_file}.tmp"
    mv "${failures_file}.tmp" "$failures_file"
  fi

  jq -n \
    --slurpfile failures "$failures_file" '
    {
      ok: (($failures[0] | length) == 0),
      failures: $failures[0]
    }
  '

  rm -f "$failures_file"
}

case "${1:-}" in
  round)
    [[ $# -eq 5 ]] || { usage >&2; exit 1; }
    verify_round "$2" "$3" "$4" "$5"
    ;;
  integrate)
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    verify_integrate "$2" "$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
