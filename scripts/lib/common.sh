#!/usr/bin/env bash

set -euo pipefail

runoq::die() {
  echo "runoq: $*" >&2
  exit 1
}

# Structured logging — only emits when RUNOQ_LOG is non-empty.
# Usage: runoq::log <prefix> <message>
runoq::log() {
  [[ -n "${RUNOQ_LOG:-}" ]] || return 0
  printf '[%s] %s\n' "$1" "$2" >&2
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

_RUNOQ_BOT_TOKEN_INIT=0

# Auto-mint a GitHub App installation token using JWT + curl.
# Uses curl (not gh) to avoid circular dependency with runoq::gh().
runoq::_mint_bot_token() {
  local identity_file
  identity_file="$(runoq::target_root 2>/dev/null)/.runoq/identity.json" || return 1
  [[ -f "$identity_file" ]] || return 1

  local app_id installation_id key_path
  app_id="$(jq -r '.appId' "$identity_file")"
  installation_id="$(jq -r '.installationId' "$identity_file")"
  key_path="${RUNOQ_APP_KEY:-$(jq -r '.privateKeyPath // empty' "$identity_file")}"
  key_path="${key_path/#\~/$HOME}"

  [[ -n "$app_id" && "$app_id" != "null" ]] || return 1
  [[ -n "$installation_id" && "$installation_id" != "null" ]] || return 1
  [[ -f "$key_path" ]] || return 1

  # Mint JWT
  local now exp header payload unsigned signature jwt
  now="$(date +%s)"
  exp="$((now + 540))"
  header="$(printf '{"alg":"RS256","typ":"JWT"}' | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
  payload="$(jq -cnj --argjson iat "$now" --argjson exp "$exp" --arg iss "$app_id" \
    '{iat:$iat,exp:$exp,iss:$iss}' | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
  unsigned="${header}.${payload}"
  signature="$(printf '%s' "$unsigned" | openssl dgst -binary -sha256 -sign "$key_path" \
    | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
  jwt="${unsigned}.${signature}"

  # Exchange JWT for installation token via curl (not gh, to avoid re-entry)
  local response token
  response="$(curl -sf -X POST \
    "https://api.github.com/app/installations/${installation_id}/access_tokens" \
    -H "Authorization: Bearer ${jwt}" \
    -H "Accept: application/vnd.github+json" 2>/dev/null)" || return 1
  token="$(printf '%s' "$response" | jq -r '.token // empty')"
  [[ -n "$token" ]] || return 1

  export GH_TOKEN="$token"
}

runoq::gh() {
  if [[ -z "${GH_TOKEN:-}" && -z "${RUNOQ_NO_AUTO_TOKEN:-}" && "$_RUNOQ_BOT_TOKEN_INIT" -eq 0 ]]; then
    _RUNOQ_BOT_TOKEN_INIT=1
    runoq::_mint_bot_token 2>/dev/null || true
  fi
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
  mktemp "${TMPDIR:-/tmp}/runoq.XXXXXX"
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

runoq::runtime_log_root() {
  if [[ -n "${RUNOQ_LOG_ROOT:-}" ]]; then
    printf '%s\n' "$RUNOQ_LOG_ROOT"
    return
  fi
  printf '%s/log\n' "$(runoq::target_root)"
}

runoq::_capture_dir_override() {
  local tool_kind="$1"
  case "$tool_kind" in
    claude)
      printf '%s\n' "${RUNOQ_CLAUDE_CAPTURE_DIR:-}"
      ;;
    codex)
      printf '%s\n' "${RUNOQ_CODEX_CAPTURE_DIR:-}"
      ;;
    *)
      printf '\n'
      ;;
  esac
}

runoq::_capture_name_from_args() {
  local tool_kind="$1"
  shift

  local name="$tool_kind"
  local i=1
  while [[ $i -le $# ]]; do
    if [[ "$tool_kind" == "claude" && "${!i}" == "--agent" ]]; then
      local next_index=$((i + 1))
      if [[ $next_index -le $# ]]; then
        printf '%s\n' "${!next_index}"
        return
      fi
    fi
    if [[ "$tool_kind" == "claude" && "${!i}" == "--model" ]]; then
      local next_index=$((i + 1))
      if [[ $next_index -le $# ]]; then
        name="model-${!next_index}"
      fi
    fi
    if [[ "$tool_kind" == "codex" && "${!i}" == "exec" ]]; then
      name="exec"
    fi
    i=$((i + 1))
  done

  printf '%s\n' "$name"
}

runoq::_capture_request_arg() {
  local tool_kind="$1"
  shift

  local saw_payload_delimiter=0
  local arg
  for arg in "$@"; do
    if [[ "$arg" == "--" ]]; then
      saw_payload_delimiter=1
      continue
    fi
    if [[ "$saw_payload_delimiter" -eq 1 ]]; then
      printf '%s\n' "$arg"
      return
    fi
  done

  if [[ "$tool_kind" == "codex" && $# -gt 0 ]]; then
    local last_arg="${!#}"
    if [[ "$last_arg" != -* ]]; then
      printf '%s\n' "$last_arg"
      return
    fi
  fi

  printf '\n'
}

runoq::_capture_dir() {
  local tool_kind="$1"
  local tool_name="$2"

  local override_dir
  override_dir="$(runoq::_capture_dir_override "$tool_kind")"
  if [[ -n "$override_dir" ]]; then
    printf '%s\n' "$override_dir"
    return
  fi

  printf '%s/%s/%s-%s-%s\n' \
    "$(runoq::runtime_log_root)" \
    "$tool_kind" \
    "$tool_name" \
    "$(date -u +%Y-%m-%d-%H%M%S)" \
    "$$"
}

runoq::_write_capture_context() {
  local capture_dir="$1"
  local real_bin="$2"
  local tool_name="$3"
  shift 3

  {
    printf 'cwd=%s\n' "$PWD"
    printf 'TARGET_ROOT=%s\n' "${TARGET_ROOT:-}"
    printf 'REPO=%s\n' "${REPO:-}"
    printf 'RUNOQ_ROOT=%s\n' "${RUNOQ_ROOT:-}"
    printf 'REAL_BIN=%s\n' "$real_bin"
    printf 'TOOL=%s\n' "$tool_name"
  } >"$capture_dir/context.log"
  printf '%s\n' "$@" >"$capture_dir/argv.txt"
}

runoq::captured_exec() {
  local tool_kind="$1"
  local cwd="$2"
  local real_bin="$3"
  shift 3

  command -v "$real_bin" >/dev/null 2>&1 || runoq::die "${tool_kind^} CLI not found: $real_bin"

  local tool_name capture_dir request_arg
  tool_name="$(runoq::_capture_name_from_args "$tool_kind" "$@")"
  capture_dir="$(runoq::_capture_dir "$tool_kind" "$tool_name")"
  request_arg="$(runoq::_capture_request_arg "$tool_kind" "$@")"

  mkdir -p "$capture_dir"
  case "$tool_kind" in
    claude)
      RUNOQ_LAST_CLAUDE_CAPTURE_DIR="$capture_dir"
      export RUNOQ_LAST_CLAUDE_CAPTURE_DIR
      ;;
    codex)
      RUNOQ_LAST_CODEX_CAPTURE_DIR="$capture_dir"
      export RUNOQ_LAST_CODEX_CAPTURE_DIR
      ;;
  esac

  runoq::_write_capture_context "$capture_dir" "$real_bin" "$tool_name" "$@"
  if [[ -n "$request_arg" ]]; then
    printf '%s\n' "$request_arg" >"$capture_dir/request.txt"
  else
    : >"$capture_dir/request.txt"
  fi

  printf '[%s] logs: %s\n' "$tool_kind" "$capture_dir" >&2

  local stdout_pipe stderr_pipe
  stdout_pipe="$(mktemp "${TMPDIR:-/tmp}/runoq-capture-stdout.XXXXXX")"
  stderr_pipe="$(mktemp "${TMPDIR:-/tmp}/runoq-capture-stderr.XXXXXX")"
  rm -f "$stdout_pipe" "$stderr_pipe"
  mkfifo "$stdout_pipe" "$stderr_pipe"

  tee "$capture_dir/stdout.log" <"$stdout_pipe" &
  local stdout_tee_pid=$!
  tee "$capture_dir/stderr.log" <"$stderr_pipe" >&2 &
  local stderr_tee_pid=$!

  local status=0
  set +e
  (
    cd "$cwd"
    "$real_bin" "$@"
  ) >"$stdout_pipe" 2>"$stderr_pipe"
  status=$?
  set -e

  wait "$stdout_tee_pid" 2>/dev/null || true
  wait "$stderr_tee_pid" 2>/dev/null || true
  rm -f "$stdout_pipe" "$stderr_pipe"

  cp "$capture_dir/stdout.log" "$capture_dir/response.txt"
  return "$status"
}

# ---------------------------------------------------------------------------
# Retry helper for eventual consistency
# ---------------------------------------------------------------------------

# Retry a command up to N times with a pause between attempts.
# Usage: runoq::retry <max_attempts> <pause_seconds> <command...>
# Returns the exit code of the last attempt.
runoq::retry() {
  local max_attempts="$1" pause="$2"
  shift 2
  local attempt=1
  while true; do
    if "$@"; then
      return 0
    fi
    if (( attempt >= max_attempts )); then
      runoq::log "retry" "all ${max_attempts} attempts failed for: $*"
      return 1
    fi
    runoq::log "retry" "attempt ${attempt}/${max_attempts} failed, retrying in ${pause}s: $*"
    sleep "$pause"
    attempt=$((attempt + 1))
  done
}

# ---------------------------------------------------------------------------
# Bot identity for git operations
# ---------------------------------------------------------------------------

# Returns the GitHub App ID from the identity file or RUNOQ_APP_ID env var.
runoq::app_id() {
  if [[ -n "${RUNOQ_APP_ID:-}" ]]; then
    printf '%s\n' "$RUNOQ_APP_ID"
    return
  fi
  local identity_file
  identity_file="$(runoq::target_root)/.runoq/identity.json"
  if [[ -f "$identity_file" ]]; then
    jq -r '.appId' "$identity_file"
  fi
}

# Returns the app slug from config (e.g. "runoq").
runoq::app_slug() {
  runoq::config_get '.identity.appSlug'
}

# Configure git user identity in a directory to match the GitHub App bot.
# Usage: runoq::configure_git_bot_identity <dir>
runoq::configure_git_bot_identity() {
  local dir="$1"
  local app_id slug
  slug="$(runoq::app_slug)"
  app_id="$(runoq::app_id)"
  [[ -n "$slug" ]] || return 0
  git -C "$dir" config user.name "${slug}[bot]"
  if [[ -n "$app_id" ]]; then
    git -C "$dir" config user.email "${app_id}+${slug}[bot]@users.noreply.github.com"
  fi
}

# Rewrite a remote to use HTTPS with the current GH_TOKEN so pushes
# are authenticated as the bot. No-op if GH_TOKEN is unset.
# Usage: runoq::configure_git_bot_remote <dir> <repo> [remote]
runoq::configure_git_bot_remote() {
  local dir="$1" repo="$2" remote="${3:-origin}"
  [[ -n "${GH_TOKEN:-}" ]] || return 0
  git -C "$dir" remote set-url "$remote" "https://x-access-token:${GH_TOKEN}@github.com/${repo}.git"
}

# Run claude --print with live streaming progress to stderr.
# Outputs the final text result to stdout (same as --print would).
# Usage: runoq::claude_stream <output_file> [claude args...]
runoq::claude_stream() {
  local output_file="$1"
  shift
  local claude_bin="${RUNOQ_CLAUDE_BIN:-claude}"
  command -v "$claude_bin" >/dev/null 2>&1 || runoq::die "Claude CLI not found: $claude_bin"

  local agent_name payload_arg capture_dir
  agent_name="$(runoq::_capture_name_from_args claude "$@")"
  payload_arg="$(runoq::_capture_request_arg claude "$@")"
  capture_dir="$(runoq::_capture_dir claude "$agent_name")"
  mkdir -p "$capture_dir"
  RUNOQ_LAST_CLAUDE_CAPTURE_DIR="$capture_dir"
  export RUNOQ_LAST_CLAUDE_CAPTURE_DIR

  local stderr_file raw_stream_file response_file request_file progress_log
  stderr_file="$capture_dir/stderr.log"
  raw_stream_file="$capture_dir/stdout.log"
  response_file="$capture_dir/response.txt"
  request_file="$capture_dir/request.txt"
  progress_log="$capture_dir/progress.log"

  runoq::_write_capture_context "$capture_dir" "$claude_bin" "$agent_name" "$@"
  if [[ -n "$payload_arg" ]]; then
    printf '%s\n' "$payload_arg" >"$request_file"
  else
    : >"$request_file"
  fi
  : >"$progress_log"
  printf '[agent] logs: %s\n' "$capture_dir" >&2

  local stream_file
  stream_file="$raw_stream_file"
  : >"$stream_file"
  local progress_pipe
  progress_pipe="$(mktemp "${TMPDIR:-/tmp}/runoq-stream-progress.XXXXXX")"
  rm -f "$progress_pipe"
  mkfifo "$progress_pipe"

  emit_progress() {
    local message="$1"
    printf '%s\n' "$message" >&2
    printf '%s\n' "$message" >>"$progress_log"
  }

  # Run with stream-json so output arrives incrementally
  (
    cd "$(runoq::target_root)"
    "$claude_bin" --print --verbose --output-format stream-json "$@" < /dev/null
  ) >"$stream_file" 2>"$stderr_file" &
  local claude_pid=$!

  # Stream progress: show tool use and thinking indicators as they arrive.
  # Track tail and reader separately so they do not keep command-substitution
  # pipes open after the Claude process exits.
  tail -n +1 -f "$stream_file" 2>/dev/null >"$progress_pipe" &
  local tail_pid=$!
  (
    while IFS= read -r line; do
      local type
      type="$(printf '%s' "$line" | jq -r '.type // empty' 2>/dev/null)" || continue
      case "$type" in
        assistant)
          local tool_names thinking_count
          tool_names="$(printf '%s' "$line" | jq -r '[.message.content[]? | select(.type == "tool_use") | .name] | .[]' 2>/dev/null)" || true
          if [[ -n "$tool_names" ]]; then
            while IFS= read -r name; do
              [[ -n "$name" ]] && emit_progress "[agent] tool: $name"
            done <<< "$tool_names"
          fi
          thinking_count="$(printf '%s' "$line" | jq '[.message.content[]? | select(.type == "thinking")] | length' 2>/dev/null)" || true
          if [[ "${thinking_count:-0}" -gt 0 ]]; then
            emit_progress "[agent] thinking..."
          fi
          ;;
        result)
          emit_progress "[agent] done"
          ;;
      esac
    done <"$progress_pipe"
  ) &
  local reader_pid=$!

  local claude_status=0
  wait "$claude_pid" || claude_status=$?
  sleep 0.5
  kill "$tail_pid" "$reader_pid" 2>/dev/null || true
  wait "$tail_pid" 2>/dev/null || true
  wait "$reader_pid" 2>/dev/null || true

  # Extract final text from the stream and prefer the content that actually
  # contains the agent payload over status-only result summaries.
  local result_text assistant_text final_text normalized_result
  result_text="$(jq -r 'select(.type == "result") | .result // empty' "$stream_file" 2>/dev/null || printf '')"
  assistant_text="$(jq -r 'select(.type == "assistant") | .message.content[]? | select(.type == "text") | .text // empty' "$stream_file" 2>/dev/null || printf '')"
  normalized_result="$(printf '%s' "$result_text" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')"

  if [[ -n "$result_text" && "$result_text" == *'<!-- runoq:payload:'* ]]; then
    final_text="$result_text"
  elif [[ -n "$assistant_text" && "$assistant_text" == *'<!-- runoq:payload:'* ]]; then
    final_text="$assistant_text"
  elif [[ -n "$result_text" && "$normalized_result" != "done" ]]; then
    final_text="$result_text"
  elif [[ -n "$assistant_text" ]]; then
    final_text="$assistant_text"
  elif [[ -n "$result_text" ]]; then
    final_text="$result_text"
  fi

  if [[ -n "${final_text:-}" ]]; then
    printf '%s\n' "$final_text" >"$output_file"
    printf '%s\n' "$final_text" >"$response_file"
  else
    : >"$output_file"
    : >"$response_file"
  fi

  rm -f "$progress_pipe"
  return "$claude_status"
}
