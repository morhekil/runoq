# pr-lifecycle

Use this skill for deterministic PR operations. Delegate all GitHub mutations to `gh-pr-lifecycle.sh`.

## Actions

- `create`: `gh-pr-lifecycle.sh create "$REPO" <branch> <issue-number> <title>`
- `comment`: `gh-pr-lifecycle.sh comment "$REPO" <pr-number> <body-file>`
- `update-summary`: `gh-pr-lifecycle.sh update-summary "$REPO" <pr-number> <summary-file>`
- `finalize`: `gh-pr-lifecycle.sh finalize "$REPO" <pr-number> <auto-merge|needs-review> [--reviewer username]`
- `line-comment`: `gh-pr-lifecycle.sh line-comment "$REPO" <pr-number> <file> <start-line> <end-line> <body>`
- `read-actionable`: `gh-pr-lifecycle.sh read-actionable "$REPO" <pr-number> "$(jq -r '.identity.handle' "$AGENDEV_CONFIG")"`
- `poll-mentions`: `gh-pr-lifecycle.sh poll-mentions "$REPO" "$(jq -r '.identity.handle' "$AGENDEV_CONFIG")" [--since timestamp]`
- `check-permission`: `gh-pr-lifecycle.sh check-permission "$REPO" <username> <read|write|admin>`

## Rules

- Preserve the audit markers `<!-- agendev:payload:* -->` and `<!-- agendev:event -->`.
- Update only marker-delimited sections in the PR body; never rewrite the whole template structure by hand.
- Treat review comments and `@agendev` issue comments as actionable input; audit comments are write-only.
