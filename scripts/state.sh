#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
export RUNOQ_ROOT

state_implementation="${RUNOQ_STATE_IMPLEMENTATION:-${RUNOQ_IMPLEMENTATION:-runtime}}"
case "$state_implementation" in
  shell|"")
    ;;
  runtime)
    runtime_bin="${RUNOQ_RUNTIME_BIN:-}"
    if [[ -n "$runtime_bin" ]]; then
      exec "$runtime_bin" "__state" "$@"
    fi
    go_bin="${RUNOQ_GO_BIN:-go}"
    command -v "$go_bin" >/dev/null 2>&1 || {
      echo "runoq: Go toolchain not found: $go_bin" >&2
      exit 1
    }
    cd "$RUNOQ_ROOT"
    exec "$go_bin" run "$RUNOQ_ROOT/cmd/runoq-runtime" "__state" "$@"
    ;;
  *)
    echo "runoq: Unknown RUNOQ_STATE_IMPLEMENTATION: $state_implementation (expected shell or runtime)" >&2
    exit 1
    ;;
esac

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  state.sh save <issue-number> [--state-dir DIR]
  state.sh load <issue-number> [--state-dir DIR]
  state.sh record-mention <comment-id> [--state-dir DIR]
  state.sh has-mention <comment-id> [--state-dir DIR]
  state.sh extract-payload <codex-output-file>
  state.sh validate-payload <worktree> <base-sha> <codex-output-file>
EOF
}

state_dir_arg=""

parse_state_dir_arg() {
  if [[ "${1:-}" == "--state-dir" ]]; then
    [[ $# -ge 2 ]] || runoq::die "--state-dir requires a value"
    state_dir_arg="$2"
    shift 2
  fi
  printf '%s\n' "$*"
}

state_dir_resolved() {
  if [[ -n "$state_dir_arg" ]]; then
    printf '%s\n' "$state_dir_arg"
  else
    runoq::state_dir
  fi
}

state_file() {
  local issue="$1"
  printf '%s/%s.json\n' "$(state_dir_resolved)" "$issue"
}

validate_phase_transition() {
  local from="$1"
  local to="$2"

  [[ "$from" == "$to" ]] && return 0

  case "$from:$to" in
    INIT:DEVELOP|INIT:CRITERIA|INIT:FINALIZE|INIT:FAILED|CRITERIA:DEVELOP|CRITERIA:FAILED|DEVELOP:REVIEW|DEVELOP:FAILED|REVIEW:DECIDE|REVIEW:FAILED|DECIDE:DEVELOP|DECIDE:FINALIZE|DECIDE:INTEGRATE|DECIDE:FAILED|FINALIZE:DONE|FINALIZE:FAILED|INTEGRATE:DONE|INTEGRATE:FAILED)
      return 0
      ;;
    DONE:*|FAILED:*)
      runoq::die "Invalid transition from terminal phase $from to $to"
      ;;
    *)
      runoq::die "Invalid phase transition: $from -> $to"
      ;;
  esac
}

load_state_raw() {
  local issue="$1"
  local file
  file="$(state_file "$issue")"
  [[ -f "$file" ]] || runoq::die "State file not found for issue $issue"
  jq -e '.' "$file" 2>/dev/null || runoq::die "State file is corrupted for issue $issue"
}

save_state() {
  local issue="$1"
  local file current_phase current_started payload tmp now
  runoq::ensure_state_dir
  file="$(state_file "$issue")"
  payload="$(cat)"
  now="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

  printf '%s' "$payload" | jq -e --argjson issue "$issue" '. + {issue:$issue}' >/dev/null || runoq::die "State payload must be valid JSON"

  if [[ -f "$file" ]]; then
    current_phase="$(jq -r '.phase' "$file")"
    current_started="$(jq -r '.started_at // empty' "$file")"
    validate_phase_transition "$current_phase" "$(printf '%s' "$payload" | jq -r '.phase')"
  else
    current_started="$now"
  fi

  tmp="$(mktemp "$(state_dir_resolved)/.${issue}.XXXXXX")"
  printf '%s' "$payload" | jq \
    --argjson issue "$issue" \
    --arg started_at "$current_started" \
    --arg updated_at "$now" '
      . + {
        issue: $issue,
        started_at: (.started_at // $started_at),
        updated_at: $updated_at
      }
    ' >"$tmp"
  mv "$tmp" "$file"
  cat "$file"
}

load_state() {
  local issue="$1"
  load_state_raw "$issue"
}

extract_payload_block() {
  local source_file="$1"
  local marked_block
  marked_block="$(
    awk '
      /^<!-- runoq:payload:codex-return -->$/ {
        saw_marker = 1
        next
      }
      saw_marker && /^```/ {
        if (!in_block) {
          in_block = 1
          block = ""
          next
        }
        printf "%s", block
        exit
      }
      saw_marker && in_block {
        block = block $0 ORS
      }
    ' "$source_file"
  )"
  if [[ -n "$marked_block" ]]; then
    printf '%s' "$marked_block"
    return 0
  fi

  awk '
    /^```/ {
      if (in_block) {
        last_block = block
        block = ""
        in_block = 0
      } else {
        in_block = 1
        block = ""
      }
      next
    }
    in_block {
      block = block $0 ORS
    }
    END {
      printf "%s", last_block
    }
  ' "$source_file"
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
        commit_range: (
          if ($commits | length) == 0 then ""
          else ($commits[0] + ".." + $commits[-1])
          end
        ),
        files_changed: $diff.files_changed,
        files_added: $diff.files_added,
        files_deleted: $diff.files_deleted
      }
    '
}

normalize_payload() {
  local payload_file="$1"
  local ground_truth_file="$2"

  jq -n \
    --slurpfile payload "$payload_file" \
    --slurpfile truth "$ground_truth_file" '
      def truth: $truth[0];
      def p: $payload[0];
      def string_array_or($value; $fallback):
        if ($value | type) == "array" and all($value[]?; type == "string") then $value else $fallback end;
      def string_or($value; $fallback):
        if ($value | type) == "string" then $value else $fallback end;
      def bool_or($value; $fallback):
        if ($value | type) == "boolean" then $value else $fallback end;
      def valid_status($value):
        if ($value | type) == "string" and ($value == "completed" or $value == "failed" or $value == "stuck") then $value else "failed" end;
      def truth_backed_string_array($value; $fallback):
        if ($value | type) == "array" and all($value[]?; type == "string") and $value == $fallback then $value else $fallback end;
      def truth_backed_string($value; $fallback):
        if ($value | type) == "string" and $value == $fallback then $value else $fallback end;
      def truth_backed_mismatch($value; $fallback):
        ($value | type) != ($fallback | type) or $value != $fallback;
      def schema_errors:
        [
          if (p.status | type) == "string" and (p.status == "completed" or p.status == "failed" or p.status == "stuck") then empty else "status_missing_or_invalid" end,
          if (p.commits_pushed | type) == "array" and all(p.commits_pushed[]?; type == "string") then empty else "commits_pushed_missing_or_non_string_array" end,
          if (p.commit_range | type) == "string" then empty else "commit_range_missing_or_non_string" end,
          if (p.files_changed | type) == "array" and all(p.files_changed[]?; type == "string") then empty else "files_changed_missing_or_non_string_array" end,
          if (p.files_added | type) == "array" and all(p.files_added[]?; type == "string") then empty else "files_added_missing_or_non_string_array" end,
          if (p.files_deleted | type) == "array" and all(p.files_deleted[]?; type == "string") then empty else "files_deleted_missing_or_non_string_array" end,
          if (p.tests_run | type) == "boolean" then empty else "tests_run_missing_or_non_boolean" end,
          if (p.tests_passed | type) == "boolean" then empty else "tests_passed_missing_or_non_boolean" end,
          if (p.test_summary | type) == "string" then empty else "test_summary_missing_or_non_string" end,
          if (p.build_passed | type) == "boolean" then empty else "build_passed_missing_or_non_boolean" end,
          if (p.blockers | type) == "array" and all(p.blockers[]?; type == "string") then empty else "blockers_missing_or_non_string_array" end,
          if (p.notes | type) == "string" then empty else "notes_missing_or_non_string" end
        ] | unique | sort;

      {
        status: valid_status(p.status),
        commits_pushed: truth_backed_string_array(p.commits_pushed; truth.commits_pushed),
        commit_range: truth_backed_string(p.commit_range; truth.commit_range),
        files_changed: truth_backed_string_array(p.files_changed; truth.files_changed),
        files_added: truth_backed_string_array(p.files_added; truth.files_added),
        files_deleted: truth_backed_string_array(p.files_deleted; truth.files_deleted),
        tests_run: bool_or(p.tests_run; false),
        tests_passed: bool_or(p.tests_passed; false),
        test_summary: string_or(p.test_summary; ""),
        build_passed: bool_or(p.build_passed; false),
        blockers: string_array_or(p.blockers; []),
        notes: string_or(p.notes; ""),
        payload_schema_valid: ((schema_errors | length) == 0),
        payload_schema_errors: schema_errors,
        payload_source: "patched",
        patched_fields: [
          if (p.status | type != "string") or (p.status != "completed" and p.status != "failed" and p.status != "stuck") then "status" else empty end,
          if truth_backed_mismatch(p.commits_pushed; truth.commits_pushed) then "commits_pushed" else empty end,
          if truth_backed_mismatch(p.commit_range; truth.commit_range) then "commit_range" else empty end,
          if truth_backed_mismatch(p.files_changed; truth.files_changed) then "files_changed" else empty end,
          if truth_backed_mismatch(p.files_added; truth.files_added) then "files_added" else empty end,
          if truth_backed_mismatch(p.files_deleted; truth.files_deleted) then "files_deleted" else empty end,
          if (p.tests_run | type != "boolean") then "tests_run" else empty end,
          if (p.tests_passed | type != "boolean") then "tests_passed" else empty end,
          if (p.test_summary | type != "string") then "test_summary" else empty end,
          if (p.build_passed | type != "boolean") then "build_passed" else empty end,
          if ((p | has("blockers")) and (p.blockers | type != "array")) then "blockers" else empty end,
          if ((p | has("notes")) and (p.notes | type != "string")) then "notes" else empty end,
          if ((p | has("blockers") | not)) then "blockers" else empty end,
          if ((p | has("notes") | not)) then "notes" else empty end
        ] | unique | sort,
        discrepancies: [
          if truth_backed_mismatch(p.commits_pushed; truth.commits_pushed) then "commits_pushed_mismatch" else empty end,
          if truth_backed_mismatch(p.commit_range; truth.commit_range) then "commit_range_mismatch" else empty end,
          if truth_backed_mismatch(p.files_changed; truth.files_changed) then "files_changed_mismatch" else empty end,
          if truth_backed_mismatch(p.files_added; truth.files_added) then "files_added_mismatch" else empty end,
          if truth_backed_mismatch(p.files_deleted; truth.files_deleted) then "files_deleted_mismatch" else empty end
        ] | unique | sort
      }
    '
}

synthesize_payload() {
  local ground_truth_file="$1"
  jq -n --slurpfile truth "$ground_truth_file" '
    {
      status: "failed",
      commits_pushed: $truth[0].commits_pushed,
      commit_range: $truth[0].commit_range,
      files_changed: $truth[0].files_changed,
      files_added: $truth[0].files_added,
      files_deleted: $truth[0].files_deleted,
      tests_run: false,
      tests_passed: false,
      test_summary: "",
      build_passed: false,
      blockers: ["Codex did not return a structured payload"],
      notes: "",
      payload_schema_valid: false,
      payload_schema_errors: ["payload_missing_or_malformed"],
      payload_source: "synthetic",
      patched_fields: [
        "status",
        "commits_pushed",
        "commit_range",
        "files_changed",
        "files_added",
        "files_deleted",
        "tests_run",
        "tests_passed",
        "test_summary",
        "build_passed",
        "blockers",
        "notes"
      ],
      discrepancies: ["payload_missing_or_malformed"]
    }
  '
}

extract_thread_id_from_source() {
  local source_file="$1"
  [[ -f "$source_file" ]] || return 0
  jq -Rsr '
    split("\n")
    | map((try fromjson catch empty))
    | map(
        select((.type // .event // "") == "thread.started")
        | (.thread_id // .thread.id // empty)
      )
    | map(select(type == "string" and length > 0))
    | last // empty
  ' <"$source_file" 2>/dev/null || true
}

attach_thread_metadata() {
  local thread_id="$1"
  if [[ -z "$thread_id" ]]; then
    cat
    return
  fi
  jq --arg thread_id "$thread_id" '. + {thread_id: $thread_id}'
}

extract_payload() {
  local source_file="$1"
  local block
  block="$(extract_payload_block "$source_file")"
  [[ -n "$block" ]] || runoq::die "No fenced payload block found"
  printf '%s' "$block"
}

validate_payload() {
  local worktree="$1"
  local base_sha="$2"
  local source_file="$3"
  local extracted_file truth_file thread_id

  extracted_file="$(mktemp "${TMPDIR:-/tmp}/runoq-payload.XXXXXX")"
  truth_file="$(mktemp "${TMPDIR:-/tmp}/runoq-truth.XXXXXX")"
  ground_truth_json "$worktree" "$base_sha" >"$truth_file"
  thread_id="$(extract_thread_id_from_source "$source_file")"

  if ! extract_payload_block "$source_file" >"$extracted_file" || [[ ! -s "$extracted_file" ]]; then
    synthesize_payload "$truth_file" | attach_thread_metadata "$thread_id"
    rm -f "$extracted_file" "$truth_file"
    return
  fi

  if ! jq -e '.' "$extracted_file" >/dev/null 2>&1; then
    synthesize_payload "$truth_file" | attach_thread_metadata "$thread_id"
    rm -f "$extracted_file" "$truth_file"
    return
  fi

  normalize_payload "$extracted_file" "$truth_file" | attach_thread_metadata "$thread_id"
  rm -f "$extracted_file" "$truth_file"
}

mentions_file() {
  printf '%s/processed-mentions.json\n' "$(state_dir_resolved)"
}

read_mentions() {
  local file
  file="$(mentions_file)"
  if [[ ! -f "$file" ]]; then
    printf '[]\n'
    return
  fi
  jq -e '.' "$file" 2>/dev/null || runoq::die "Processed mentions file is corrupted"
}

record_mention() {
  local comment_id="$1"
  local file tmp
  runoq::ensure_state_dir
  file="$(mentions_file)"
  tmp="$(mktemp "$(state_dir_resolved)/.mentions.XXXXXX")"
  read_mentions | jq --argjson id "$comment_id" '
    if index($id) then . else . + [$id] end
  ' >"$tmp"
  mv "$tmp" "$file"
  cat "$file"
}

has_mention() {
  local comment_id="$1"
  if read_mentions | jq -e --argjson id "$comment_id" 'index($id) != null' >/dev/null; then
    printf 'true\n'
  else
    printf 'false\n'
    exit 1
  fi
}

case "${1:-}" in
  save)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    issue="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    save_state "$issue"
    ;;
  load)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    issue="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    load_state "$issue"
    ;;
  record-mention)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    comment_id="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    record_mention "$comment_id"
    ;;
  has-mention)
    shift
    [[ $# -ge 1 ]] || { usage >&2; exit 1; }
    comment_id="$1"
    shift
    remaining="$(parse_state_dir_arg "$@")"
    # shellcheck disable=SC2086
    set -- $remaining
    [[ $# -eq 0 ]] || { usage >&2; exit 1; }
    has_mention "$comment_id"
    ;;
  extract-payload)
    shift
    [[ $# -eq 1 ]] || { usage >&2; exit 1; }
    extract_payload "$1"
    ;;
  validate-payload)
    shift
    [[ $# -eq 3 ]] || { usage >&2; exit 1; }
    validate_payload "$1" "$2" "$3"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
