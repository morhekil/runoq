# issue-queue

Use this skill when you need structured access to the GitHub issue queue.

## Actions

- `list`: run `"$AGENDEV_ROOT/scripts/gh-issue-queue.sh" list "$REPO" "$(jq -r '.labels.ready' "$AGENDEV_CONFIG")"`
- `next`: run `"$AGENDEV_ROOT/scripts/gh-issue-queue.sh" next "$REPO" "$(jq -r '.labels.ready' "$AGENDEV_CONFIG")"`
- `set-status`: run `"$AGENDEV_ROOT/scripts/gh-issue-queue.sh" set-status "$REPO" <issue-number> <ready|in-progress|done|needs-review|blocked>`
- `create`: run `"$AGENDEV_ROOT/scripts/gh-issue-queue.sh" create "$REPO" <title> <body> [--depends-on N,M] [--priority N] [--estimated-complexity value]`

## Rules

- Treat the script JSON as source of truth; do not reimplement dependency resolution or label logic in the prompt.
- Surface `blocked_reasons` exactly as returned by the script.
- Issue metadata lives in the `<!-- agendev:meta -->` block; the script parses it for you.
