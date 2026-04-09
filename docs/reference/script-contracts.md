# Script Contract Reference

This document summarizes the public shell-script contracts under [`scripts/`](../../scripts). It focuses on entrypoints other scripts, agents, and tests can safely build against.

## Contract Stability

Treat these outputs as stability-sensitive:

- JSON returned by the subcommands documented below
- audit markers such as `<!-- runoq:bot -->` and `<!-- runoq:payload:* -->`
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
| `list` | `<repo> <ready-label>` | JSON array of issues with `number`, `title`, `body`, `url`, `labels`, `depends_on`, `priority`, `estimated_complexity`, `complexity_rationale`, `type`, `parent_epic`, `metadata_present`, `metadata_valid` | reads GitHub issues |
| `next` | `<repo> <ready-label>` | JSON object `{ issue, skipped }`; `issue` is either the next actionable issue object or `null`, `skipped` contains blocked items with `blocked_reasons` | reads GitHub issues and dependency labels |
| `set-status` | `<repo> <issue-number> <status>` where status is `ready`, `in-progress`, `done`, `needs-review`, or `blocked` | JSON object `{ issue, status, label }` | removes existing `runoq:*` labels and applies exactly one new state label |
| `create` | `<repo> <title> <body> [--depends-on N,M] [--priority N] [--estimated-complexity value] [--type epic\|task] [--parent-epic N] [--complexity-rationale text]` | JSON object `{ title, url }` | creates a GitHub issue labeled `runoq:ready` with native metadata (issueType, labels, blockedBy) |
| `epic-status` | `<repo> <epic-number>` | JSON object `{ epic, children, all_done, pending }` where `children` lists child issue numbers with their labels, `all_done` is boolean, `pending` lists children not yet `runoq:done` | reads GitHub issue metadata and child labels |

Notes:

- `next` sorts actionable items by metadata priority, then issue number.
- `next` skips issues with `type: epic` — only tasks are dispatched directly.
- `create` accepts `--type`, `--parent-epic`, and `--complexity-rationale` flags for hierarchical issue structures and rationale tracking.
- `epic-status` checks whether all children of an epic have the `runoq:done` label, enabling INTEGRATE phase triggering.
- dependency checks require upstream issues to carry the configured done label.
- `gh-issue-queue.sh` remains the stable shell entrypoint and dispatches to the Go runtime queue engine (`RUNOQ_RUNTIME_BIN` first, then `go run` fallback from `RUNOQ_ROOT`).
- `assign` adds the operator as issue assignee (resolves via `RUNOQ_OPERATOR_LOGIN` or `gh api user`).

## `tick-fmt.sh`

Purpose: pure formatting and parsing subcommands for the tick pipeline. All string templating, markdown generation, JSON transformation, and text parsing that was previously done inline in shell via jq/awk/printf is now implemented in Go (`internal/tick/`).

Primary callers: `tick.sh`, `plan-dispatch.sh`, `lib/planning.sh`.

| Subcommand | stdin | stdout |
|---|---|---|
| `format-proposal` | Proposal JSON | Markdown with payload marker |
| `proposal-comment-body` | `{proposal, technical, product, warning}` | Full review comment markdown |
| `milestone-body` | ProposalItem JSON | Milestone issue body |
| `adjustment-review-body` | `{proposed_adjustments}` JSON | Adjustment review body |
| `parse-verdict` | Verdict text | ReviewScore JSON |
| `extract-json <marker>` | Text with marker block | Extracted JSON |
| `human-comment-selection` | Issue view JSON | `{approved, rejected}` JSON |
| `select-items --selection JSON` | Proposal JSON | Filtered Proposal JSON |
| `merge-checklists <left> <right>` | (none) | Merged text |

Notes:
- `tick-fmt.sh` dispatches to the Go runtime (`__tick_fmt`) using the same pattern as `gh-issue-queue.sh`.
- All subcommands are pure transformations: no GitHub API calls, no env dependencies.

## `plan.sh`

Purpose: legacy plan decomposition pipeline. Decomposes a plan document into epics and tasks, presents the proposal, and creates GitHub issues deterministically.

Primary callers: `bin/runoq`, operators, tests.

| Invocation | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `plan.sh` | `<repo> <plan-file> [--auto-confirm] [--dry-run]` | JSON object `{ status, issues, issue_map }` on success; decomposition JSON on dry run | calls `milestone-decomposer` and `task-decomposer`, creates GitHub issues via `gh-issue-queue.sh create` |

Pipeline phases:

1. **Decompose**: calls `milestone-decomposer` to produce milestone items, then `task-decomposer` once per milestone to produce the final epic/task hierarchy with `key`, `type`, `title`, `body`, `priority`, `estimated_complexity`, `complexity_rationale`, dependency keys, and parent/children relationships.
2. **Present**: displays the proposed issue hierarchy to the operator with complexity, dependency, and bar-setter annotations. Shows warnings if the agent produced any.
3. **Create**: creates epics first, then tasks in order, resolving dependency keys to real issue numbers. Passes `--complexity-rationale` when present.

Notes:

- `plan.sh` is deprecated in favor of `tick.sh` plus `plan-dispatch.sh`, but remains available during the transition.
- `--auto-confirm` skips interactive confirmation.
- `--dry-run` outputs the decomposition JSON without creating issues.
- Epic issues are created with `--type epic`; child tasks reference their parent via `--parent-epic`.
- Agent invocation artifacts are persisted under `log/claude/milestone-decomposer-<timestamp>/` and `log/claude/task-decomposer-<timestamp>/`, including the request payload, live raw Claude stream, stderr, progress events, and extracted final response.

## `plan-dispatch.sh`

Purpose: deterministic planning review loop for planning issues. Produces proposal comments but does not directly create milestone or task issues.

Primary callers: `tick.sh`, tests.

| Invocation | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `plan-dispatch.sh` | `<repo> <issue-number> <milestone\|task> <plan-file> [milestone-file] [prior-findings-file]` | single-line status `Proposal posted on #<n>` on success | calls decomposer and both plan reviewers, posts proposal comment with `runoq:payload:plan-proposal` marker |

Pipeline phases:

1. **Decompose**: calls `milestone-decomposer` or `task-decomposer` depending on review mode.
2. **Review**: calls `plan-reviewer-technical` and `plan-reviewer-product`.
3. **Iterate or publish**: merges reviewer checklists when either reviewer returns `ITERATE`, reruns decomposition up to `planning.maxDecompositionRounds`, and posts the best proposal as an issue comment.

Notes:

- Proposal comments are numbered for human addressability.
- Reviewer outputs are parsed from their marker-delimited verdict blocks.
- When the maximum review rounds are reached, the proposal comment includes a warning but is still posted.

## `tick.sh`

Purpose: one-step project coordinator for iterative planning and milestone-gated execution.

Primary callers: `bin/runoq`, operators, smoke tests.

| Invocation | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `tick.sh` | none | one-line status such as `Proposal posted on #N`, `Responded to comments on #N`, `Applied approvals from #N`, `Dispatched #N`, or `Project complete` | reads GitHub issue state, may create planning/adjustment issues, may materialize approved milestones/tasks, may dispatch implementation |

State checks in order:

1. Bootstrap a planning epic and planning issue when no milestone epics exist.
2. Answer or await pending planning/adjustment review issues.
3. Materialize approved review issues into milestone/task issues.
4. Dispatch planning decomposition for the current milestone when needed.
5. Delegate implementation dispatch when the current milestone has open tasks.
6. Review completed milestones and either create adjustments or advance to the next milestone.
7. Report project completion when no open milestone epics remain.

Notes:

- `tick.sh` reads the committed plan path via `runoq::plan_file`, so `runoq.json` at the target root is required.
- Planning issues use proposal comments rather than immediate issue creation.
- Discovery milestones always pause for human review when `planning.discoveryMilestoneAutoAdvance` is `false`.

## `plan-comment-handler.sh`

Purpose: answer human comments on planning and adjustment review issues.

Primary callers: `tick.sh`, tests.

| Invocation | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `plan-comment-handler.sh` | `<repo> <issue-number> <plan-file>` | single-line status `Responded to comments on #<n>` on success | reads issue comments, invokes `plan-comment-responder`, posts `runoq:bot` reply comment |

Notes:

- Only human comments without an existing `runoq:bot` reply are actionable.
- Replies are intentionally audit-marked so later ticks can detect whether a comment has already been handled.

## `orchestrator.sh`

Purpose: phase dispatch state machine for issue execution. Replaces the former `github-orchestrator` agent with a deterministic shell script.

Primary callers: `run.sh`, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `run` | `<repo> [--issue N] [--dry-run]` | JSON object with phase progression, outcome, and audit trail references | creates worktree, draft PR, spawns bar-setter/diff-reviewer agents, drives phase transitions, posts audit comments, finalizes PR |
| `mention-triage` | `<repo> <pr-number>` | JSON array of classified mentions with `comment_id`, `classification`, `action_taken` | polls mentions, classifies via haiku structured-output call, dispatches to mention-responder or feeds change-requests into DEVELOP |

Phase transitions driven by orchestrator:

- `INIT -> CRITERIA` (when estimated_complexity is medium or higher)
- `INIT -> DEVELOP` (when estimated_complexity is low, skipping CRITERIA)
- `CRITERIA -> REVIEW` (non-low complexity deterministic handoff path)
- `CRITERIA -> DEVELOP`
- `DEVELOP -> REVIEW`
- `REVIEW -> DECIDE`
- `DECIDE -> DEVELOP` (iterate)
- `DECIDE -> FINALIZE`
- `FINALIZE -> DONE`
- `INTEGRATE -> DONE` (epic completion)

Mention classification values: `question`, `change-request`, `approval`, `irrelevant`.

Notes:

- The orchestrator does not perform reasoning. It dispatches to agents for bounded tasks and applies a deterministic decision table for phase transitions.
- Mention triage uses a haiku structured-output call, not a full agent invocation.
- `orchestrator.sh` remains the stable shell entrypoint and dispatches to the Go runtime orchestrator (`RUNOQ_RUNTIME_BIN` first, then `go run` fallback from `RUNOQ_ROOT`).

## `issue-runner.sh`

Purpose: drive codex development rounds within the DEVELOP phase.

Primary callers: `orchestrator.sh`, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `run` | `<payload-json-file>` | JSON object with `status` (`review_ready`, `fail`, `budget_exhausted`), `issueNumber`, `prNumber`, `round`, `verificationPassed`, `verificationFailures`, `changedFiles`, `relatedFiles`, `cumulativeTokens`, `summary`, `caveats` | invokes codex in worktree, captures codex JSON events plus final assistant message separately, persists per-round `thread_id`, validates payload via `state.sh`, performs bounded same-thread schema retries via `codex exec resume <thread_id> ...` when payload schema is invalid, calls `verify.sh round` (including criteria tamper check when `criteria_commit` is present in payload), iterates on failure up to `maxRounds`, posts verification failure comments to PR |

The payload JSON file must include: `issueNumber`, `prNumber`, `worktree`, `branch`, `specPath`, `repo`, `maxRounds`, `maxTokenBudget`. Optional fields: `round`, `logDir`, `previousChecklist`, `cumulativeTokens`, `guidelines`, `criteria_commit`.

Notes:

- `issue-runner.sh` is the one remaining shell-owned component; it does not dispatch through the Go runtime.
- `RUNOQ_ISSUE_RUNNER_IMPLEMENTATION` defaults to `shell`; `runtime` is accepted only as a compatibility alias and runs the same shell path. All other subcommands (state, verify, worktree, dispatch-safety, issue-queue, orchestrator) are Go-owned via exec dispatch.
- Invokes codex with `--json -o <last-message-file>` and keeps separate event/message artifacts.
- Persists round `thread_id` from `thread.started` events and includes it in normalized payload artifacts.
- Performs bounded schema retries in the same codex session (`codex exec resume <thread_id> ...`) when `payload_schema_valid` is false.
- Injects `criteria_commit` into the payload file before verification when present, enabling the tamper check.
- Tracks cumulative token usage across rounds and checks budget before and after each round.
- Expands review scope by finding files that import changed files.
- Returns `review_ready` on verification success, `fail` on max-rounds exhaustion, or `budget_exhausted` when the token budget is exceeded.
- Each Codex invocation is captured under the issue log directory as `codex-round-<n>/` with `argv.txt`, `context.log`, `request.txt`, `stdout.log`, `stderr.log`, and `response.txt`.

## `gh-pr-lifecycle.sh`

Purpose: PR creation, audit comments, summary mutation, mention polling, permission checks, and finalization actions.

Primary callers: `run.sh`, `mentions.sh`, skills, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `create` | `<repo> <branch> <issue-number> <title>` | `{ url, number }` | creates a draft PR from `templates/pr-template.md` |
| `comment` | `<repo> <pr-number> <comment-body-file>` | `{ commented: true, pr }` | posts a PR comment using the file body |
| `update-summary` | `<repo> <pr-number> <summary-file>` | `{ updated: true, pr }` | replaces only the `runoq:summary` and `runoq:attention` marker blocks in the PR body |
| `finalize` | `<repo> <pr-number> <verdict> [--reviewer username]` | `{ pr, verdict, reviewer }` | `auto-merge`: ready PR then enable squash auto-merge; `needs-review`: ready PR and optionally assign reviewer/assignee |
| `line-comment` | `<repo> <pr-number> <file> <start-line> <end-line> <body>` | `{ path, start_line, end_line }` | creates a GitHub review comment |
| `read-actionable` | `<repo> <pr-number> <agent-handle>` | JSON array of actionable comments; issue comments include `id`, `author`, `body`, `html_url`, `comment_type`, review comments include `path` too | reads PR issue comments and review comments |
| `poll-mentions` | `<repo> <agent-handle> [--since timestamp]` | JSON array of unprocessed mentions with `comment_id`, `author`, `body`, `created_at`, `context_type`, `pr_number`, `issue_number` | reads open issues and issue comments; skips already-recorded mention IDs |
| `check-permission` | `<repo> <username> <required-level>` | on success `{ allowed: true, username, permission }`; on failure `{ allowed: false, username, permission }` and exit non-zero | reads collaborator permission level |

Notes:

- `read-actionable` filters out audit payload comments and `runoq:bot` comments from issue-comment results.
- `poll-mentions` determines PR vs issue context from the GitHub item type, not from comment text.

## `state.sh`

Purpose: atomic state persistence, phase-transition validation, payload extraction/normalization, and processed-mention tracking.

Primary callers: `run.sh`, `mentions.sh`, `gh-pr-lifecycle.sh`, `maintenance.sh`, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `save` | `<issue-number> [--state-dir DIR]` with JSON on stdin | echoes the saved state JSON with injected `issue`, `started_at`, and `updated_at` | atomically writes `.runoq/state/<issue>.json` |
| `load` | `<issue-number> [--state-dir DIR]` | echoes the stored JSON state | reads state file |
| `record-mention` | `<comment-id> [--state-dir DIR]` | echoes the full processed-mention JSON array | atomically writes `processed-mentions.json` |
| `has-mention` | `<comment-id> [--state-dir DIR]` | prints `true` and exits 0 when present; prints `false` and exits non-zero when absent | reads processed mention state |
| `extract-payload` | `<codex-output-file>` | prints the last fenced block from the source file | reads a payload file |
| `validate-payload` | `<worktree> <base-sha> <codex-output-file>` | prints normalized payload JSON with fields such as `status`, file lists, test/build booleans, `payload_schema_valid`, `payload_schema_errors`, `payload_source`, `patched_fields`, `discrepancies`, and optional `thread_id` | reads git state, validates payload schema shape/types, and may synthesize fallback payload data |

Allowed phase transitions:

- `INIT -> CRITERIA | DEVELOP | FINALIZE | FAILED`
- `CRITERIA -> DEVELOP | REVIEW | FAILED`
- `DEVELOP -> REVIEW | FAILED`
- `REVIEW -> DECIDE | FAILED`
- `DECIDE -> DEVELOP | FINALIZE | INTEGRATE | FAILED`
- `FINALIZE -> DONE | FAILED`
- `INTEGRATE -> DONE | FAILED`

Terminal phases `DONE` and `FAILED` reject further transitions.

Notes:

- `state.sh` remains the stable shell entrypoint and dispatches to the Go runtime state engine (`RUNOQ_RUNTIME_BIN` first, then `go run` fallback from `RUNOQ_ROOT`).

## `verify.sh`

Purpose: ground-truth verification of one development round.

Primary callers: `run.sh`, agents, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `round` | `<worktree> <branch> <base-sha> <payload-file>` | `{ ok, review_allowed, failures, actual }` where `actual` contains ground-truth commit and file lists | reads git state, runs configured test/build commands in the worktree |
| `integrate` | `<worktree> <criteria-commit>` | `{ ok, failures }` | confirms epic-level criteria files from `criteria_commit` are unchanged at HEAD, runs test suite in integrated worktree |

Verification checks for `round`:

- at least one new commit exists
- every payload commit exists locally
- payload file lists match git ground truth
- branch tip is pushed to `origin`
- configured test command succeeds
- configured build command succeeds
- payload reports tests/build as passed
- when `criteria_commit` is present in the payload file: each file in the criteria commit's diff is byte-identical at HEAD (criteria tamper check); failure reports `criteria tampered: <files>`

Verification checks for `integrate`:

- criteria files from the epic-level `criteria_commit` are unchanged at HEAD
- configured test command succeeds in the integrated worktree

`review_allowed` currently mirrors `ok`.

Notes:

- `verify.sh` remains the stable shell entrypoint and dispatches to the Go runtime verify engine (`RUNOQ_RUNTIME_BIN` first, then `go run` fallback from `RUNOQ_ROOT`).

## `worktree.sh`

Purpose: isolate execution in sibling worktrees instead of mutating the target checkout.

Primary callers: `run.sh`, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `create` | `<issue-number> <title>` | `{ branch, worktree, base_ref }` | fetches `origin/main`, creates a new worktree and branch from `RUNOQ_BASE_REF` or `origin/main` |
| `remove` | `<issue-number>` | `{ removed, worktree }` | force-removes the sibling worktree if present |
| `inspect` | `<issue-number>` | `{ worktree, exists }` | no mutation |
| `branch-name` | `<issue-number> <title>` | prints the derived branch name | no mutation |

Notes:

- worktree paths are derived from `worktreePrefix` and live next to the target repo.
- `create` fails if the target path already exists.
- `worktree.sh` remains the stable shell entrypoint and dispatches to the Go runtime worktree engine (`RUNOQ_RUNTIME_BIN` first, then `go run` fallback from `RUNOQ_ROOT`).

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

Notes:

- `dispatch-safety.sh` remains the stable shell entrypoint and dispatches to the Go runtime dispatch-safety engine (`RUNOQ_RUNTIME_BIN` first, then `go run` fallback from `RUNOQ_ROOT`).

## `report.sh`

Purpose: read-only reporting over local state files.

Primary callers: `bin/runoq`, operators, tests.

| Subcommand | Arguments | JSON/stdout contract | Side effects |
| --- | --- | --- | --- |
| `summary` | `[--last N]` | `{ issues, pass, fail, caveats, tokens, average_rounds }` | reads state files only |
| `issue` | `<issue-number>` | prints the stored issue JSON as formatted JSON (shell behavior matches `jq '.'`) | reads one state file |
| `cost` | `[--last N]` | `{ issues, tokens, estimated_cost }` | reads state files and config only |

Notes:

- `summary` and `cost` return zeroed JSON when no state files exist.
- `issue` exits non-zero if the requested file does not exist.
- `scripts/report.sh` remains the stable shell entrypoint; when `RUNOQ_IMPLEMENTATION=runtime` is used through `bin/runoq report ...`, runtime dispatch uses the Go report engine while preserving this contract.

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
