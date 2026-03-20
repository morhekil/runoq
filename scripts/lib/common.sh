#!/usr/bin/env bash

set -euo pipefail

agendev::die() {
  echo "agendev: $*" >&2
  exit 1
}

agendev::script_dir() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

agendev::root() {
  if [[ -n "${AGENDEV_ROOT:-}" ]]; then
    printf '%s\n' "$AGENDEV_ROOT"
    return
  fi
  agendev::script_dir
}

agendev::config_path() {
  if [[ -n "${AGENDEV_CONFIG:-}" ]]; then
    printf '%s\n' "$AGENDEV_CONFIG"
    return
  fi
  printf '%s/config/agendev.json\n' "$(agendev::root)"
}

agendev::require_cmd() {
  local cmd
  for cmd in "$@"; do
    command -v "$cmd" >/dev/null 2>&1 || agendev::die "Missing required command: $cmd"
  done
}

agendev::config_get() {
  local filter="$1"
  jq -er "$filter" "$(agendev::config_path)"
}

agendev::target_root() {
  if [[ -n "${TARGET_ROOT:-}" ]]; then
    printf '%s\n' "$TARGET_ROOT"
    return
  fi
  git rev-parse --show-toplevel 2>/dev/null || agendev::die "Run agendev from inside a git repository."
}

agendev::origin_url() {
  git -C "$(agendev::target_root)" remote get-url origin 2>/dev/null || agendev::die "No 'origin' remote found. agendev requires a GitHub-hosted repo."
}

agendev::repo_from_remote() {
  local remote="${1:-}"
  if [[ -n "${AGENDEV_REPO:-}" ]]; then
    printf '%s\n' "$AGENDEV_REPO"
    return
  fi

  if [[ -z "$remote" ]]; then
    remote="$(agendev::origin_url)"
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
      agendev::die "Origin remote is not a GitHub URL: $remote"
      ;;
  esac
}

agendev::repo() {
  agendev::repo_from_remote "$(agendev::origin_url)"
}

agendev::state_dir() {
  if [[ -n "${AGENDEV_STATE_DIR:-}" ]]; then
    printf '%s\n' "$AGENDEV_STATE_DIR"
    return
  fi
  printf '%s/.agendev/state\n' "$(agendev::target_root)"
}

agendev::ensure_state_dir() {
  mkdir -p "$(agendev::state_dir)"
}

agendev::gh() {
  local gh_bin="${GH_BIN:-gh}"
  "$gh_bin" "$@"
}

agendev::branch_slug() {
  local raw="$1"
  raw="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
  raw="$(printf '%s' "$raw" | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g')"
  printf '%s\n' "${raw:-issue}"
}

agendev::branch_name() {
  local issue="$1"
  local title="$2"
  local prefix
  prefix="$(agendev::config_get '.branchPrefix')"
  printf '%s%s-%s\n' "$prefix" "$issue" "$(agendev::branch_slug "$title")"
}

agendev::worktree_path() {
  local issue="$1"
  local prefix target_root parent
  prefix="$(agendev::config_get '.worktreePrefix')"
  target_root="$(agendev::target_root)"
  parent="$(cd "$target_root/.." && pwd)"
  printf '%s/%s%s\n' "$parent" "$prefix" "$issue"
}

agendev::json_tmp() {
  mktemp "${TMPDIR:-/tmp}/agendev.XXXXXX.json"
}

agendev::write_json_file() {
  local path="$1"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/agendev-write.XXXXXX")"
  cat >"$tmp"
  mv "$tmp" "$path"
}

agendev::label_keys_json() {
  jq '.labels' "$(agendev::config_path)"
}

agendev::all_state_labels() {
  agendev::label_keys_json | jq -r '.[]'
}

agendev::label_for_status() {
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
  ' "$(agendev::config_path)" 2>/dev/null || agendev::die "Unknown status: $status"
}

agendev::default_branch_ref() {
  printf '%s\n' "${AGENDEV_BASE_REF:-origin/main}"
}

agendev::absolute_path() {
  local path="$1"
  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
  else
    printf '%s/%s\n' "$(pwd)" "$path"
  fi
}
