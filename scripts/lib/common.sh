#!/usr/bin/env bash

set -euo pipefail

runoq::die() {
  echo "runoq: $*" >&2
  exit 1
}

runoq::script_dir() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

runoq::root() {
  if [[ -n "${RUNOQ_ROOT:-}" ]]; then
    printf '%s\n' "$RUNOQ_ROOT"
    return
  fi
  runoq::script_dir
}

runoq::config_path() {
  if [[ -n "${RUNOQ_CONFIG:-}" ]]; then
    printf '%s\n' "$RUNOQ_CONFIG"
    return
  fi
  printf '%s/config/runoq.json\n' "$(runoq::root)"
}

runoq::require_cmd() {
  local cmd
  for cmd in "$@"; do
    command -v "$cmd" >/dev/null 2>&1 || runoq::die "Missing required command: $cmd"
  done
}

runoq::config_get() {
  local filter="$1"
  jq -er "$filter" "$(runoq::config_path)"
}

runoq::target_root() {
  if [[ -n "${TARGET_ROOT:-}" ]]; then
    printf '%s\n' "$TARGET_ROOT"
    return
  fi
  git rev-parse --show-toplevel 2>/dev/null || runoq::die "Run runoq from inside a git repository."
}

runoq::origin_url() {
  git -C "$(runoq::target_root)" remote get-url origin 2>/dev/null || runoq::die "No 'origin' remote found. runoq requires a GitHub-hosted repo."
}

runoq::repo_from_remote() {
  local remote="${1:-}"
  if [[ -n "${RUNOQ_REPO:-}" ]]; then
    printf '%s\n' "$RUNOQ_REPO"
    return
  fi

  if [[ -z "$remote" ]]; then
    remote="$(runoq::origin_url)"
  fi

  case "$remote" in
    git@github.com:*)
      remote="${remote#git@github.com:}"
      remote="${remote%.git}"
      printf '%s\n' "$remote"
      ;;
    https://github.com/*|https://*@github.com/*)
      remote="${remote#https://}"
      remote="${remote#*@}"
      remote="${remote#github.com/}"
      remote="${remote%.git}"
      printf '%s\n' "$remote"
      ;;
    ssh://git@github.com/*)
      remote="${remote#ssh://git@github.com/}"
      remote="${remote%.git}"
      printf '%s\n' "$remote"
      ;;
    *)
      runoq::die "Origin remote is not a GitHub URL: $remote"
      ;;
  esac
}

runoq::repo() {
  runoq::repo_from_remote "$(runoq::origin_url)"
}

runoq::state_dir() {
  if [[ -n "${RUNOQ_STATE_DIR:-}" ]]; then
    printf '%s\n' "$RUNOQ_STATE_DIR"
    return
  fi
  printf '%s/.runoq/state\n' "$(runoq::target_root)"
}

runoq::ensure_state_dir() {
  mkdir -p "$(runoq::state_dir)"
}

runoq::gh() {
  local gh_bin="${GH_BIN:-gh}"
  "$gh_bin" "$@"
}

runoq::branch_slug() {
  local raw="$1"
  raw="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
  raw="$(printf '%s' "$raw" | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g')"
  printf '%s\n' "${raw:-issue}"
}

runoq::branch_name() {
  local issue="$1"
  local title="$2"
  local prefix
  prefix="$(runoq::config_get '.branchPrefix')"
  printf '%s%s-%s\n' "$prefix" "$issue" "$(runoq::branch_slug "$title")"
}

runoq::worktree_path() {
  local issue="$1"
  local prefix target_root parent
  prefix="$(runoq::config_get '.worktreePrefix')"
  target_root="$(runoq::target_root)"
  parent="$(cd "$target_root/.." && pwd)"
  printf '%s/%s%s\n' "$parent" "$prefix" "$issue"
}

runoq::json_tmp() {
  mktemp "${TMPDIR:-/tmp}/runoq.XXXXXX.json"
}

runoq::write_json_file() {
  local path="$1"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/runoq-write.XXXXXX")"
  cat >"$tmp"
  mv "$tmp" "$path"
}

runoq::label_keys_json() {
  jq '.labels' "$(runoq::config_path)"
}

runoq::all_state_labels() {
  runoq::label_keys_json | jq -r '.[]'
}

runoq::label_for_status() {
  local status="$1"
  jq -er --arg status "$status" '
    .labels[
      if $status == "ready" then "ready"
      elif $status == "in-progress" then "inProgress"
      elif $status == "done" then "done"
      elif $status == "needs-review" then "needsReview"
      elif $status == "blocked" then "blocked"
      else error("unknown status")
      end
    ]
  ' "$(runoq::config_path)" 2>/dev/null || runoq::die "Unknown status: $status"
}

runoq::default_branch_ref() {
  printf '%s\n' "${RUNOQ_BASE_REF:-origin/main}"
}

runoq::absolute_path() {
  local path="$1"
  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
  else
    printf '%s/%s\n' "$(pwd)" "$path"
  fi
}
