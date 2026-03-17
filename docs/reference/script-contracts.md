# Script Contract Reference

This document summarizes the public shell-script contracts under [`scripts/`](../../scripts). It focuses on entrypoints other scripts, agents, and tests can safely build against.

## Contract Stability

Treat these outputs as stability-sensitive:

- JSON returned by the subcommands documented below
- audit markers such as `<!-- agendev:event -->` and `<!-- agendev:payload:* -->`
- marker-delimited PR template sections used by `update-summary`

Treat these as implementation details unless separately documented:

- internal helper functions inside each script
- exact prose of human-facing comments that do not include a documented marker
- temp-file names and temp-path layouts

## `gh-issue-queue.sh`

Purpose: queue discovery, dependency ordering, status-label mutation, and issue creation.

Primary callers: `run.sh`, `maintenance.sh`, the `plan-to-issues` skill, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `list` | `<repo> <ready-label>` | JSON array of issues with `number`, `title`, `body`, `url`, `labels`, `depends_on`, `priority`, `estimated_complexity`, `metadata_present`, `metadata_valid` | reads GitHub issues |
| `next` | `<repo> <ready-label>` | JSON object `{ issue, skipped }`; `issue` is either the next actionable issue object or `null`, `skipped` contains blocked items with `blocked_reasons` | reads GitHub issues and dependency labels |
| `set-status` | `<repo> <issue-number> <status>` where status is `ready`, `in-progress`, `done`, `needs-review`, or `blocked` | JSON object `{ issue, status, label }` | removes existing `agendev:*` labels and applies exactly one new state label |
| `create` | `<repo> <title> <body> [--depends-on N,M] [--priority N] [--estimated-complexity value]` | JSON object `{ title, url }` | creates a GitHub issue labeled `agendev:ready` with an `agendev:meta` block |

Notes:

- `next` sorts actionable items by metadata priority, then issue number.
- dependency checks require upstream issues to carry the configured done label.

## `gh-pr-lifecycle.sh`

Purpose: PR creation, audit comments, summary mutation, mention polling, permission checks, and finalization actions.

Primary callers: `run.sh`, `mentions.sh`, skills, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `create` | `<repo> <branch> <issue-number> <title>` | `{ url, number }` | creates a draft PR from `templates/pr-template.md` |
| `comment` | `<repo> <pr-number> <comment-body-file>` | `{ commented: true, pr }` | posts a PR comment using the file body |
| `update-summary` | `<repo> <pr-number> <summary-file>` | `{ updated: true, pr }` | replaces only the `agendev:summary` and `agendev:attention` marker blocks in the PR body |
| `finalize` | `<repo> <pr-number> <verdict> [--reviewer username]` | `{ pr, verdict, reviewer }` | `auto-merge`: ready PR then enable squash auto-merge; `needs-review`: ready PR and optionally assign reviewer/assignee |
| `line-comment` | `<repo> <pr-number> <file> <start-line> <end-line> <body>` | `{ path, start_line, end_line }` | creates a GitHub review comment |
| `read-actionable` | `<repo> <pr-number> <agent-handle>` | JSON array of actionable comments; issue comments include `id`, `author`, `body`, `html_url`, `comment_type`, review comments include `path` too | reads PR issue comments and review comments |
| `poll-mentions` | `<repo> <agent-handle> [--since timestamp]` | JSON array of unprocessed mentions with `comment_id`, `author`, `body`, `created_at`, `context_type`, `pr_number`, `issue_number` | reads open issues and issue comments; skips already-recorded mention IDs |
| `check-permission` | `<repo> <username> <required-level>` | on success `{ allowed: true, username, permission }`; on failure `{ allowed: false, username, permission }` and exit non-zero | reads collaborator permission level |

Notes:

- `read-actionable` filters out audit payload comments and `agendev:event` comments from issue-comment results.
- `poll-mentions` determines PR vs issue context from the GitHub item type, not from comment text.

## `state.sh`

Purpose: atomic state persistence, phase-transition validation, payload extraction/normalization, and processed-mention tracking.

Primary callers: `run.sh`, `mentions.sh`, `gh-pr-lifecycle.sh`, `maintenance.sh`, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `save` | `<issue-number> [--state-dir DIR]` with JSON on stdin | echoes the saved state JSON with injected `issue`, `started_at`, and `updated_at` | atomically writes `.agendev/state/<issue>.json` |
| `load` | `<issue-number> [--state-dir DIR]` | echoes the stored JSON state | reads state file |
| `record-mention` | `<comment-id> [--state-dir DIR]` | echoes the full processed-mention JSON array | atomically writes `processed-mentions.json` |
| `has-mention` | `<comment-id> [--state-dir DIR]` | prints `true` and exits 0 when present; prints `false` and exits non-zero when absent | reads processed mention state |
| `extract-payload` | `<codex-output-file>` | prints the last fenced block from the source file | reads a payload file |
| `validate-payload` | `<worktree> <base-sha> <codex-output-file>` | prints normalized payload JSON with fields such as `status`, file lists, test/build booleans, `payload_source`, `patched_fields`, `discrepancies` | reads git state and may synthesize fallback payload data |

Allowed phase transitions:

- `INIT -> DEVELOP | FINALIZE | FAILED`
- `DEVELOP -> REVIEW | FAILED`
- `REVIEW -> DECIDE | FAILED`
- `DECIDE -> DEVELOP | FINALIZE | FAILED`
- `FINALIZE -> DONE | FAILED`

Terminal phases `DONE` and `FAILED` reject further transitions.

## `verify.sh`

Purpose: ground-truth verification of one development round.

Primary callers: `run.sh`, agents, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `round` | `<worktree> <branch> <base-sha> <payload-file>` | `{ ok, review_allowed, failures, actual }` where `actual` contains ground-truth commit and file lists | reads git state, runs configured test/build commands in the worktree |

Verification checks:

- at least one new commit exists
- every payload commit exists locally
- payload file lists match git ground truth
- branch tip is pushed to `origin`
- configured test command succeeds
- configured build command succeeds
- payload reports tests/build as passed

`review_allowed` currently mirrors `ok`.

## `worktree.sh`

Purpose: isolate execution in sibling worktrees instead of mutating the target checkout.

Primary callers: `run.sh`, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `create` | `<issue-number> <title>` | `{ branch, worktree, base_ref }` | fetches `origin/main`, creates a new worktree and branch from `AGENDEV_BASE_REF` or `origin/main` |
| `remove` | `<issue-number>` | `{ removed, worktree }` | force-removes the sibling worktree if present |
| `inspect` | `<issue-number>` | `{ worktree, exists }` | no mutation |
| `branch-name` | `<issue-number> <title>` | prints the derived branch name | no mutation |

Notes:

- worktree paths are derived from `worktreePrefix` and live next to the target repo.
- `create` fails if the target path already exists.

## `dispatch-safety.sh`

Purpose: startup reconciliation and pre-dispatch eligibility checks.

Primary callers: `run.sh`, agent prompts, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `reconcile` | `<repo>` | JSON array of action objects such as `{ issue, pr_number, action, phase, round }` or `{ issue, action: "reset-ready" }` | may post issue/PR comments and may relabel stale or unrecoverable issues |
| `eligibility` | `<repo> <issue-number>` | on success `{ allowed: true, issue, branch, reasons: [] }`; on failure `{ allowed: false, issue, branch, reasons }` and exit non-zero | may post a skip comment to the issue |

Eligibility failure reasons currently include:

- missing acceptance criteria
- incomplete dependencies
- existing open PR for the derived branch
- unresolved conflicts between the existing branch and `origin/main`

## `report.sh`

Purpose: read-only reporting over local state files.

Primary callers: `bin/agendev`, operators, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `summary` | `[--last N]` | `{ issues, pass, fail, caveats, tokens, average_rounds }` | reads state files only |
| `issue` | `<issue-number>` | prints the stored issue JSON verbatim | reads one state file |
| `cost` | `[--last N]` | `{ issues, tokens, estimated_cost }` | reads state files and config only |

Notes:

- `summary` and `cost` return zeroed JSON when no state files exist.
- `issue` exits non-zero if the requested file does not exist.

## `mentions.sh`

Purpose: process bot mentions with permission checks and deduplication.

Primary callers: maintenance and mention-oriented workflows, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `process` | `<repo> <agent-handle> [--since <timestamp>]` | JSON array of processed mention records with `comment_id`, `author`, `action`, `permission`, `context_type`, `pr_number`, `issue_number` | reads mentions, checks permissions, records processed IDs, may post denial comments |

Action values:

- `process`: authorized, ready for caller-specific handling
- `deny`: unauthorized and `denyResponse` is `comment`
- `ignore`: unauthorized and `denyResponse` is not `comment`

## `maintenance.sh`

Purpose: partitioned maintenance review tracking, findings publication, triage, and summary generation.

Primary callers: the `maintenance-reviewer` agent, operators, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `derive-partitions` | `<root>` | `{ root, mode, exclusions, partitions }` | reads `.gitignore` and `tsconfig.json` |
| `start` | `<repo>` | `{ tracking_issue, partitions }` | creates a maintenance tracking issue, posts per-partition progress comments, writes `maintenance.json` |
| `post-findings` | `<repo> <tracking-issue> <findings-file>` | `{ tracking_issue, findings }` | posts finding comments, updates `maintenance.json` to `FINDINGS_POSTED` |
| `triage` | `<repo> <tracking-issue>` | `{ processed }` where `processed` is an array of handled comment IDs | reads tracking-issue comments, checks permissions, files queue issues, updates finding state, writes processed mentions |
| `run` | `<repo> <findings-file>` | terminal result such as `{ phase: "COMPLETED", tracking_issue, summary }` | starts or resumes the full workflow and may create follow-up queue issues |
| `report-partition` | `<repo> <tracking-issue> <partition-name> <score> <finding-count>` | no structured stdout beyond command success | posts a progress comment to the tracking issue |

Maintenance phases:

- `STARTED`
- `FINDINGS_POSTED`
- `COMPLETED`

Notes:

- maintenance is intended to stay read-only until triage comments approve filing.
- `run` is resumable by phase and does not repost the final summary once already completed.

## `watchdog.sh`

Purpose: terminate silent child commands after an inactivity timeout and leave a stall marker in state.

Primary callers: `run.sh`, tests.

| Invocation | Arguments | Contract | Side effects |
| --- | --- | --- | --- |
| `watchdog.sh` | `[--timeout SECONDS] [--issue N] [--state-dir DIR] -- command [args...]` | streams child stdout/stderr, exits with the child exit code on success/failure, exits `124` on inactivity timeout | on timeout, writes `.stall` data into the issue state file |

Timeout behavior:

- inactivity is based on lack of output, not wall-clock runtime
- on timeout, the child is terminated and the state file gains a `stall` object with `timed_out`, `timeout_seconds`, `detected_at`, `exit_code`, `command`, and, when available, `last_phase` and `last_round`

## Related Docs

- [CLI reference](./cli.md)
- [Configuration and auth reference](./config-auth.md)
- [State and audit model](./state-model.md)
- [Architecture overview](../architecture/overview.md)
- [Execution and maintenance flows](../architecture/flows.md)
