#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

usage() {
  cat <<'EOF'
Usage:
  report.sh summary [--last N]
  report.sh issue <issue-number>
  report.sh cost [--last N]
EOF
}

state_files_query() {
  local last="${1:-}"
  local state_dir
  state_dir="$(runoq::state_dir)"
  mkdir -p "$state_dir"
  find "$state_dir" -maxdepth 1 -type f -name '*.json' | sort | {
    if [[ -n "$last" ]]; then
      tail -n "$last"
    else
      cat
    fi
  }
}

summary_report() {
  local last="${1:-}"
  local files=()
  mapfile -t files < <(state_files_query "$last")
  if [[ "${#files[@]}" -eq 0 ]]; then
    jq -n '{issues:0, pass:0, fail:0, caveats:0, tokens:{input:0,cached_input:0,output:0,total:0}, average_rounds:0}'
    return
  fi

  jq -s '
    def rounds_count:
      if (.rounds | type) == "array" then (.rounds | length)
      elif (.rounds | type) == "number" then .rounds
      elif (.outcome.rounds_used // null) != null then .outcome.rounds_used
      elif (.result.rounds_used // null) != null then .result.rounds_used
      else 0
      end;
    def phase_value:
      if (.phase // null) != null then .phase
      elif (.status // "") == "done" then "DONE"
      elif (.status // "") == "failed" then "FAILED"
      else null
      end;
    def verdict_value:
      .outcome.verdict // .result.verdict // .verdict // null;
    {
      issues: length,
      pass: map(select(verdict_value == "PASS")) | length,
      fail: map(select(phase_value == "FAILED" or verdict_value == "FAIL")) | length,
      caveats: map(select(verdict_value == "PASS_WITH_CAVEATS")) | length,
      tokens: {
        input: (map(.rounds[]?.tokens.input // 0) | add // 0),
        cached_input: (map(.rounds[]?.tokens.cached_input // 0) | add // 0),
        output: (map(.rounds[]?.tokens.output // 0) | add // 0),
        total: (map(.tokens_used // 0) | add // 0)
      },
      average_rounds: ((map(rounds_count) | add // 0) / length)
    }
  ' "${files[@]}"
}

issue_report() {
  local issue="$1"
  local file
  file="$(runoq::state_dir)/${issue}.json"
  [[ -f "$file" ]] || runoq::die "No state file found for issue $issue"
  jq '.' "$file"
}

cost_report() {
  local last="${1:-}"
  local files=() input cached output
  mapfile -t files < <(state_files_query "$last")
  if [[ "${#files[@]}" -eq 0 ]]; then
    jq -n '{issues:0, estimated_cost:0}'
    return
  fi

  input="$(runoq::config_get '.tokenCost.inputPerMillion')"
  cached="$(runoq::config_get '.tokenCost.cachedInputPerMillion')"
  output="$(runoq::config_get '.tokenCost.outputPerMillion')"

  jq -s --argjson in_rate "$input" --argjson cache_rate "$cached" --argjson out_rate "$output" '
    {
      issues: length,
      tokens: {
        input: (map(.rounds[]?.tokens.input // 0) | add // 0),
        cached_input: (map(.rounds[]?.tokens.cached_input // 0) | add // 0),
        output: (map(.rounds[]?.tokens.output // 0) | add // 0)
      }
    }
    | .estimated_cost =
      (((.tokens.input / 1000000) * $in_rate) +
       ((.tokens.cached_input / 1000000) * $cache_rate) +
       ((.tokens.output / 1000000) * $out_rate))
  ' "${files[@]}"
}

subcommand="${1:-}"
shift || true

case "$subcommand" in
  summary)
    if [[ "${1:-}" == "--last" ]]; then
      [[ $# -eq 2 ]] || { usage >&2; exit 1; }
      summary_report "$2"
    else
      [[ $# -eq 0 ]] || { usage >&2; exit 1; }
      summary_report
    fi
    ;;
  issue)
    [[ $# -eq 1 ]] || { usage >&2; exit 1; }
    issue_report "$1"
    ;;
  cost)
    if [[ "${1:-}" == "--last" ]]; then
      [[ $# -eq 2 ]] || { usage >&2; exit 1; }
      cost_report "$2"
    else
      [[ $# -eq 0 ]] || { usage >&2; exit 1; }
      cost_report
    fi
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
