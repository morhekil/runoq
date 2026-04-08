---
name: issue-queue
description: Structured access to the GitHub issue queue — list, next, set-status, and create issues via gh-issue-queue.sh.
---

# issue-queue

Use this skill when you need structured access to the GitHub issue queue.

## Actions

- `list`: run `"$RUNOQ_ROOT/scripts/gh-issue-queue.sh" list "$REPO" "$(jq -r '.labels.ready' "$RUNOQ_CONFIG")"`
- `next`: run `"$RUNOQ_ROOT/scripts/gh-issue-queue.sh" next "$REPO" "$(jq -r '.labels.ready' "$RUNOQ_CONFIG")"`
- `set-status`: run `"$RUNOQ_ROOT/scripts/gh-issue-queue.sh" set-status "$REPO" <issue-number> <ready|in-progress|done|needs-review|blocked>`
- `create`: run `"$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create "$REPO" <title> <body> [--depends-on N,M] [--priority N] [--estimated-complexity value]`

## Rules

- Treat the script JSON as source of truth; do not reimplement dependency resolution or label logic in the prompt.
- Surface `blocked_reasons` exactly as returned by the script.
- Issue metadata is derived from labels and native GitHub APIs (issueType, sub-issues, blockedBy).
