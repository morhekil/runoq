# pr-lifecycle

Use this skill for deterministic PR operations. Delegate all GitHub mutations to `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh"`.

## Actions

- `create`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" create "$REPO" <branch> <issue-number> <title>`
- `comment`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" comment "$REPO" <pr-number> <body-file>`
- `update-summary`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" update-summary "$REPO" <pr-number> <summary-file>`
- `finalize`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" finalize "$REPO" <pr-number> <auto-merge|needs-review> [--reviewer username]`
- `line-comment`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" line-comment "$REPO" <pr-number> <file> <start-line> <end-line> <body>`
- `read-actionable`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" read-actionable "$REPO" <pr-number> "$(jq -r '.identity.handle' "$RUNOQ_CONFIG")"`
- `poll-mentions`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" poll-mentions "$REPO" "$(jq -r '.identity.handle' "$RUNOQ_CONFIG")" [--since timestamp]`
- `check-permission`: `"$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" check-permission "$REPO" <username> <read|write|admin>`

## Rules

- Preserve the audit markers `<!-- runoq:payload:* -->` and `<!-- runoq:event -->`.
- Update only marker-delimited sections in the PR body; never rewrite the whole template structure by hand.
- Treat review comments and `@runoq` issue comments as actionable input; audit comments are write-only.
