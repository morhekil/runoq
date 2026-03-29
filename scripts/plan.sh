#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

SCRIPTS_DIR="$(cd "$(dirname "$0")" && pwd)"
RUNOQ_ROOT="${RUNOQ_ROOT:-$(runoq::root)}"

###############################################################################
# Usage
###############################################################################

usage() {
  cat <<'EOF'
Usage:
  plan.sh <repo> <plan-file> [--auto-confirm] [--dry-run]

Decomposes a plan document into GitHub issues using the plan-decomposer agent.

Options:
  --auto-confirm   Skip interactive confirmation and create issues immediately.
  --dry-run        Show the proposal without creating issues.
EOF
}

###############################################################################
# Helpers
###############################################################################

claude_exec() {
  local claude_bin="${RUNOQ_CLAUDE_BIN:-claude}"
  command -v "$claude_bin" >/dev/null 2>&1 || runoq::die "Claude CLI not found: $claude_bin"
  (
    cd "$(runoq::target_root)"
    "$claude_bin" "$@"
  )
}

extract_marked_block() {
  local source_file="$1"
  local marker="$2"
  awk -v marker="$marker" '
    $0 ~ marker {
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
    in_block {
      block = block $0 "\n"
    }
  ' "$source_file"
}

log_info() {
  printf '[plan] %s\n' "$*" >&2
}

log_error() {
  printf '[plan] ERROR: %s\n' "$*" >&2
}

###############################################################################
# Phase 1: Call Claude to decompose the plan
###############################################################################

decompose_plan() {
  local plan_file="$1"
  local output_file

  local payload
  payload="$(jq -n \
    --arg planPath "$plan_file" \
    --arg templatePath "$RUNOQ_ROOT/templates/issue-template.md" \
    '{
      planPath: $planPath,
      templatePath: $templatePath,
      examplePlans: {
        broad: ($planPath | split("/")[:-1] | join("/") + "/../broad-example.md"),
        narrow: ($planPath | split("/")[:-1] | join("/") + "/../narrow-example.md"),
        untestable: ($planPath | split("/")[:-1] | join("/") + "/../untestable-example.md")
      }
    }')"

  output_file="$(mktemp "${TMPDIR:-/tmp}/runoq-plan-decompose.XXXXXX")"
  log_info "calling plan-decomposer agent"
  claude_exec --print --permission-mode bypassPermissions \
    --agent plan-decomposer --add-dir "$RUNOQ_ROOT" \
    -- "$payload" 2>&1 | tee "$output_file" >&2 || true

  local decomposition
  decomposition="$(extract_marked_block "$output_file" 'runoq:payload:plan-decomposer' 2>/dev/null || printf '')"

  if [[ -z "$decomposition" ]] || ! printf '%s' "$decomposition" | jq -e '.items' >/dev/null 2>&1; then
    log_error "plan-decomposer did not return valid JSON"
    log_error "output file: $output_file"
    rm -f "$output_file"
    return 1
  fi

  rm -f "$output_file"
  printf '%s\n' "$decomposition"
}

###############################################################################
# Phase 2: Present proposal to user
###############################################################################

present_proposal() {
  local decomposition="$1"

  local warnings
  warnings="$(printf '%s' "$decomposition" | jq -r '.warnings // [] | .[]')"

  if [[ -n "$warnings" ]]; then
    printf '\n⚠ Warnings:\n' >&2
    printf '%s' "$decomposition" | jq -r '.warnings[] | "  - " + .' >&2
    printf '\n' >&2
  fi

  printf '\nProposed issue hierarchy:\n\n' >&2

  # Print epics first, then their children indented
  while IFS= read -r item; do
    [[ -n "$item" ]] || continue
    local item_type item_title item_key complexity rationale
    item_type="$(printf '%s' "$item" | jq -r '.type')"
    item_title="$(printf '%s' "$item" | jq -r '.title')"
    item_key="$(printf '%s' "$item" | jq -r '.key')"

    if [[ "$item_type" == "epic" ]]; then
      printf '  📦 [epic] %s\n' "$item_title" >&2
      local children_keys
      children_keys="$(printf '%s' "$item" | jq -r '.children_keys // [] | .[]')"
      while IFS= read -r child_key; do
        [[ -n "$child_key" ]] || continue
        local child
        child="$(printf '%s' "$decomposition" | jq -c --arg k "$child_key" '.items[] | select(.key == $k)')"
        [[ -n "$child" ]] || continue
        local child_title child_complexity child_rationale child_deps bar_setter_note
        child_title="$(printf '%s' "$child" | jq -r '.title')"
        child_complexity="$(printf '%s' "$child" | jq -r '.estimated_complexity // "medium"')"
        child_rationale="$(printf '%s' "$child" | jq -r '.complexity_rationale // ""')"
        child_deps="$(printf '%s' "$child" | jq -r '.depends_on_keys // [] | if length > 0 then " (depends on: " + join(", ") + ")" else "" end')"
        if [[ "$child_complexity" == "low" ]]; then
          bar_setter_note=" [bar-setter: skip]"
        else
          bar_setter_note=" [bar-setter: run]"
        fi
        printf '    ├─ [%s] %s%s%s\n' "$child_complexity" "$child_title" "$child_deps" "$bar_setter_note" >&2
        if [[ -n "$child_rationale" ]]; then
          printf '    │    └─ %s\n' "$child_rationale" >&2
        fi
      done <<< "$children_keys"
      printf '\n' >&2
    fi
  done < <(printf '%s' "$decomposition" | jq -c '.items[]')

  # Print standalone tasks (no parent_epic_key)
  while IFS= read -r item; do
    [[ -n "$item" ]] || continue
    local item_type parent_key
    item_type="$(printf '%s' "$item" | jq -r '.type')"
    parent_key="$(printf '%s' "$item" | jq -r '.parent_epic_key // empty')"
    [[ "$item_type" == "task" && -z "$parent_key" ]] || continue

    local item_title complexity rationale deps bar_setter_note
    item_title="$(printf '%s' "$item" | jq -r '.title')"
    complexity="$(printf '%s' "$item" | jq -r '.estimated_complexity // "medium"')"
    rationale="$(printf '%s' "$item" | jq -r '.complexity_rationale // ""')"
    deps="$(printf '%s' "$item" | jq -r '.depends_on_keys // [] | if length > 0 then " (depends on: " + join(", ") + ")" else "" end')"
    if [[ "$complexity" == "low" ]]; then
      bar_setter_note=" [bar-setter: skip]"
    else
      bar_setter_note=" [bar-setter: run]"
    fi
    printf '  [%s] %s%s%s\n' "$complexity" "$item_title" "$deps" "$bar_setter_note" >&2
    if [[ -n "$rationale" ]]; then
      printf '    └─ %s\n' "$rationale" >&2
    fi
  done < <(printf '%s' "$decomposition" | jq -c '.items[]')
}

confirm_proposal() {
  if [[ "${RUNOQ_AUTO_CONFIRM:-0}" == "1" ]]; then
    log_info "auto-confirm enabled, proceeding with issue creation"
    return 0
  fi

  printf '\nCreate these issues? [y/N] ' >&2
  local answer
  read -r answer
  case "$answer" in
    y|Y|yes|Yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

###############################################################################
# Phase 3: Create issues deterministically
###############################################################################

create_issues() {
  local repo="$1"
  local decomposition="$2"
  local issue_map='{}' created_issues='[]'

  # Pass 1: create epics
  while IFS= read -r item; do
    [[ -n "$item" ]] || continue
    local item_type
    item_type="$(printf '%s' "$item" | jq -r '.type')"
    [[ "$item_type" == "epic" ]] || continue

    local key title body priority complexity
    key="$(printf '%s' "$item" | jq -r '.key')"
    title="$(printf '%s' "$item" | jq -r '.title')"
    body="$(printf '%s' "$item" | jq -r '.body')"
    priority="$(printf '%s' "$item" | jq -r '.priority // 1')"
    complexity="$(printf '%s' "$item" | jq -r '.estimated_complexity // "high"')"

    log_info "creating epic: ${title}"
    local create_output issue_url issue_number
    create_output="$(runoq::retry 3 5 \
      "$SCRIPTS_DIR/gh-issue-queue.sh" create "$repo" "$title" "$body" \
        --priority "$priority" \
        --estimated-complexity "$complexity" \
        --type epic)" || runoq::die "Failed to create epic: ${title}"

    issue_url="$(printf '%s' "$create_output" | jq -r '.url')"
    issue_number="$(printf '%s' "$issue_url" | grep -oE '[0-9]+$')"
    [[ -n "$issue_number" ]] || runoq::die "Failed to parse issue number for epic: ${title}"

    issue_map="$(printf '%s' "$issue_map" | jq --arg key "$key" --argjson number "$issue_number" '. + {($key): $number}')"
    created_issues="$(printf '%s' "$created_issues" | jq --argjson item "$item" --argjson number "$issue_number" --arg url "$issue_url" \
      '. + [$item + {number: $number, url: $url}]')"
    log_info "created epic #${issue_number}: ${issue_url}"
  done < <(printf '%s' "$decomposition" | jq -c '.items[] | select(.type == "epic")')

  # Pass 2: create tasks (in order, respecting dependencies)
  while IFS= read -r item; do
    [[ -n "$item" ]] || continue
    local item_type
    item_type="$(printf '%s' "$item" | jq -r '.type')"
    [[ "$item_type" == "task" ]] || continue

    local key title body priority complexity complexity_rationale parent_epic_key depends_json
    key="$(printf '%s' "$item" | jq -r '.key')"
    title="$(printf '%s' "$item" | jq -r '.title')"
    body="$(printf '%s' "$item" | jq -r '.body')"
    priority="$(printf '%s' "$item" | jq -r '.priority // 3')"
    complexity="$(printf '%s' "$item" | jq -r '.estimated_complexity // "medium"')"
    complexity_rationale="$(printf '%s' "$item" | jq -r '.complexity_rationale // ""')"
    parent_epic_key="$(printf '%s' "$item" | jq -r '.parent_epic_key // empty')"

    # Resolve dependency keys to issue numbers
    depends_json="$(printf '%s' "$item" | jq --argjson issue_map "$issue_map" \
      '[(.depends_on_keys // [])[] | $issue_map[.] // empty]')"

    local args=(
      "$SCRIPTS_DIR/gh-issue-queue.sh"
      create "$repo" "$title" "$body"
      --priority "$priority"
      --estimated-complexity "$complexity"
      --type task
    )

    if [[ -n "$complexity_rationale" ]]; then
      args+=(--complexity-rationale "$complexity_rationale")
    fi

    if [[ "$(printf '%s' "$depends_json" | jq 'length')" -gt 0 ]]; then
      args+=(--depends-on "$(printf '%s' "$depends_json" | jq -r 'join(",")')")
    fi

    if [[ -n "$parent_epic_key" ]]; then
      local epic_number
      epic_number="$(printf '%s' "$issue_map" | jq -r --arg k "$parent_epic_key" '.[$k] // empty')"
      if [[ -n "$epic_number" ]]; then
        args+=(--parent-epic "$epic_number")
      fi
    fi

    log_info "creating task: ${title}"
    local create_output issue_url issue_number
    create_output="$(runoq::retry 3 5 "${args[@]}")" || runoq::die "Failed to create task: ${title}"

    issue_url="$(printf '%s' "$create_output" | jq -r '.url')"
    issue_number="$(printf '%s' "$issue_url" | grep -oE '[0-9]+$')"
    [[ -n "$issue_number" ]] || runoq::die "Failed to parse issue number for task: ${title}"

    issue_map="$(printf '%s' "$issue_map" | jq --arg key "$key" --argjson number "$issue_number" '. + {($key): $number}')"
    created_issues="$(printf '%s' "$created_issues" | jq --argjson item "$item" --argjson number "$issue_number" --arg url "$issue_url" \
      '. + [$item + {number: $number, url: $url}]')"
    log_info "created task #${issue_number}: ${issue_url}"
  done < <(printf '%s' "$decomposition" | jq -c '.items[] | select(.type == "task")')

  # Output structured result
  jq -n \
    --argjson issues "$created_issues" \
    --argjson issue_map "$issue_map" '{
    status: "ok",
    issues: $issues,
    issue_map: $issue_map
  }'
}

###############################################################################
# Main
###############################################################################

[[ $# -ge 2 ]] || { usage >&2; exit 1; }
repo="$1"
plan_file="$2"
shift 2

auto_confirm=false
dry_run=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --auto-confirm)
      auto_confirm=true
      export RUNOQ_AUTO_CONFIRM=1
      shift
      ;;
    --dry-run)
      dry_run=true
      shift
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
done

plan_file="$(runoq::absolute_path "$plan_file")"
[[ -f "$plan_file" ]] || runoq::die "Plan file not found: $plan_file"

runoq::require_cmd jq

# Phase 1: Decompose
log_info "decomposing plan: ${plan_file}"
decomposition="$(decompose_plan "$plan_file")" || runoq::die "Plan decomposition failed."

item_count="$(printf '%s' "$decomposition" | jq '.items | length')"
epic_count="$(printf '%s' "$decomposition" | jq '[.items[] | select(.type == "epic")] | length')"
task_count="$(printf '%s' "$decomposition" | jq '[.items[] | select(.type == "task")] | length')"
log_info "decomposition: ${item_count} items (${epic_count} epics, ${task_count} tasks)"

# Phase 2: Present and confirm
present_proposal "$decomposition"

if [[ "$dry_run" == "true" ]]; then
  log_info "dry run — skipping issue creation"
  printf '%s\n' "$decomposition"
  exit 0
fi

if ! confirm_proposal; then
  log_info "cancelled by user"
  exit 1
fi

# Phase 3: Create issues
result="$(create_issues "$repo" "$decomposition")"
printf '%s\n' "$result"
