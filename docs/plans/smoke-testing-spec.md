# Smoke Testing Specification

This is a forward-looking guidance document for the design and implementation of smoke tests for the runoq system. It defines the scope, philosophy, and specific scenarios that future smoke suites should cover to validate the externally observable behavior of the current system.

The runtime behavior described here is intended to come from the implemented system, even where the corresponding smoke harness, fixture lane, helper script, or test repo fixture has not yet been built. In other words: this document is a smoke-spec contract for future implementation, not a claim that every lane described below already exists as runnable smoke infrastructure in this repository.

## Philosophy

Smoke tests validate **externally observable behavior** — what the operator sees in the terminal and on GitHub. They don't test internal logic (unit tests do that). They answer: "if I run runoq against a real repo, does it produce the correct GitHub state and terminal output at each step?"

Two tiers exist because they optimize for different things:

- **Fixture smoke** — fast (~2 min), uses fixture agents, real GitHub API. Validates orchestration logic: correct labels, PR lifecycle, comment structure, tick-per-phase boundaries. Runs on every change.
- **Live smoke** — slow (~15-30 min), uses real codex/claude. Validates that agents actually produce working code, reviews are meaningful, and the full pipeline converges. Runs before releases or on schedule.

Both tiers use the same validation functions — the difference is only what drives the agents.

---

## Tier 1: Fixture Smoke — Planning Flow

Planning smoke must cover bootstrap, planning review, approved planning application, and approved adjustment application. The minimum observable planning scenarios are listed later in this document so the tier is no longer undocumented.

---

## Tier 2: Fixture Smoke — Implementation Flow

### Setup

Same repo and fixture infrastructure as the planning smoke. After tasks are materialized under a milestone, instead of manually closing them, run the implementation tick-per-phase flow with a **fixture codex** that:

1. Creates a real file in the worktree
2. Commits and pushes to origin
3. Writes valid JSONL events to stdout (with `thread.started` and token counts)
4. Writes a last-message file to the `-o` path. Happy-path fixtures should include a valid payload block; failure fixtures may intentionally omit or malform it so `state validate-payload` can normalize it.
5. Captures thread IDs so schema-retry scenarios can verify same-thread resume within a single develop round

And a **fixture diff-reviewer** response for the `claude stream-json --agent diff-reviewer` invocation that returns a PASS verdict with a score.

### Agent invocation policy

- `INIT` is its own tick. A fresh implementation dispatch should stop after opening the PR and posting initial audit state.
- Every implementation tick runs reconciliation before normal queue dispatch. Fixture smoke should exercise this pre-dispatch recovery behavior explicitly.
- Later `DEVELOP` rounds should start with a fresh codex invocation. Cross-round continuity must come from explicit carry-forward state such as `previous_checklist`, not hidden thread memory.
- Codex `resume` is allowed only for same-round schema-retry repair after an invalid payload block, and the current runtime performs at most one same-thread retry.
- Reviewer invocations should be fresh on every `REVIEW` tick. There is no cross-round reviewer resume contract.
- Reviewer contract repair is a separate same-thread behavior within a single `REVIEW` tick. It is not a cross-round resume contract.

### Scenario: Reconciliation before dispatch

Before normal queue selection, the tick reconciles stale implementation state against GitHub.

- [ ] A stale issue labeled `runoq:in-progress` with no linked PR is reset to `runoq:ready`
- [ ] The issue receives a comment explaining that it was returned to the queue because no linked PR exists
- [ ] The tick performs reconciliation before selecting the next implementation task

### Scenario: In-progress task is resumed before new dispatch

If an epic already contains an implementation task labeled `runoq:in-progress`, the tick resumes that task before considering any ready sibling.

- [ ] The tick resumes the existing in-progress task before selecting from the ready queue
- [ ] No new ready sibling is dispatched while an in-progress sibling exists
- [ ] Terminal output identifies the resumed task rather than a newly selected task

### Scenario: Targeted issue dispatch mode

Operator provides `TargetIssue`, so the tick dispatches that specific implementation task instead of walking the milestone queue.

- [ ] The tick prints `Dispatching target issue #<n>` before dispatch
- [ ] An open implementation task goes straight through the implementation phase machine for that task
- [ ] Terminal output reports `Issue #<n> — phase: <PHASE>` for the targeted task
- [ ] Queue walking is bypassed; the tick does not require epic or milestone selection to dispatch the targeted task
- [ ] A closed targeted task reports `Issue #<n> already complete` and exits without reopening or redispatching it
- [ ] A non-existent targeted issue reports `target issue #<n> not found` and exits with an error
- [ ] A targeted epic/planning/adjustment issue reports `target issue #<n> is not an implementation task` and exits with an error

### Scenario: INIT dry-run mode

Operator enables dry-run implementation dispatch for a specific issue.

- [ ] `INIT` returns a state payload with `phase=INIT`, `issue=<n>`, `branch=<branch>`, and `dry_run=true`
- [ ] No worktree is created
- [ ] No draft PR is created
- [ ] No issue status transition to `runoq:in-progress` occurs
- [ ] No audit comment is posted because no PR exists yet

### Scenario: Happy path (6 ticks)

**Tick 1 — Init:**

```
tick_once
```

Verify on GitHub:

- [ ] Issue has `runoq:in-progress` label (not `runoq:ready`)
- [ ] A draft PR for correct branch exists with `Closes #<issue>` in body
- [ ] PR audit comment contains correct state/phase and orchestrator attribution
- [ ] The issue branch has been pushed to origin with the initial empty bootstrap commit
- [ ] No implementation code commits are pushed yet

**Tick 2 — Develop:**

```
tick_once
```

Verify on GitHub:

- [ ] Code commits are pushed to PR
- [ ] PR has a new `DEVELOP` audit comment with implementor attribution
- [ ] The `DEVELOP` comment is posted by `orchestrator` via `issue-runner`
- [ ] No `VERIFY` or `REVIEW` comments are posted yet

Verify correct output on terminal.

**Tick 3 — Verify:**

```
tick_once
```

Verify on GitHub:

- [ ] PR has a new `VERIFY` audit comment with verifier attribution
- [ ] Verification result is posted separately from `DEVELOP`
- [ ] Issue still has `runoq:in-progress` label

**Tick 4 — Review:**

```
tick_once
```

Verify on GitHub:

- [ ] PR has new audit comment with review state and reviewer agent attribution
- [ ] The review comment is posted by `orchestrator` via `diff-reviewer`
- [ ] Review comment contains verdict (`PASS`, `ITERATE`, or `FAIL`)
- [ ] Review comment contains score
- [ ] Review comment contains full scorecard with comments/explanations
- [ ] Issue still has `runoq:in-progress` label (not done yet)
- [ ] No responses from codex implementor yet

Verify correct output on terminal.

**Tick 5 — Decide:**

```
tick_once
```

Verify on GitHub:

- [ ] PR has a new `DECIDE` audit comment
- [ ] The `DECIDE` comment is an orchestrator audit comment, not an implementor or reviewer comment
- [ ] The decision comment does not also finalize in the same tick
- [ ] Issue still has `runoq:in-progress` label

**Tick 6 — Finalize (PASS path):**

```
tick_once
```

Verify on GitHub:

- [ ] Issue has `runoq:done` label (not `runoq:in-progress`)
- [ ] Issue is closed
- [ ] PR is merged (or has auto-merge enabled)
- [ ] PR body has `## Final Status` table with verdict, score, rounds

Verify correct output on terminal.

### Scenario: Iterate path (10 ticks)

Same as happy path but the fixture reviewer returns `ITERATE` on first review.

**Tick 1** — Init (same checks as happy path tick 1)

**Tick 2** — Develop round 1 (same checks as happy path tick 2)

**Tick 3** — Verify round 1 (same checks as happy path tick 3)

**Tick 4** — Review returns ITERATE verdict

- [ ] PR has review comment with `ITERATE` verdict and reviewer agent attribution
- [ ] Issue still `runoq:in-progress`

**Tick 5** — Decide: iterate

- [ ] PR has audit comment for decide phase with orchestrator attribution, containing "iterate" decision
- [ ] State includes review checklist carried forward as `previous_checklist`

**Tick 6** — Develop round 2

- [ ] New commits pushed to branch (from second codex round)
- [ ] PR has new develop audit comment for round 2 with implementor agent attribution
- [ ] Implementor codex has been launched as a fresh round-2 invocation, not a cross-round thread resume
- [ ] The round-2 codex prompt includes the prior review checklist explicitly

**Tick 7** — Verify round 2

- [ ] PR has a new `VERIFY` audit comment for round 2

**Tick 8** — Review (PASS this time)

- [ ] PR has review comment with `PASS` verdict and reviewer agent attribution
- [ ] Reviewer claude has been launched as a fresh invocation for round 2, not a cross-round resume

**Tick 9** — Decide: finalize

- [ ] PR has a new `DECIDE` audit comment
- [ ] The decision comment does not also finalize in the same tick

**Tick 10** — Finalize

- [ ] Same finalize checks as happy path tick 6

### Scenario: Needs-review path

Two observable entry points exist:

- **Fresh issue path**: a fresh implementation dispatch still uses `INIT` first, so PR creation happens on the `INIT` tick and the deterministic needs-review handoff happens on the following `DEVELOP` tick.
- **Post-INIT path**: if `INIT` has already completed and a PR already exists, the needs-review handoff happens in a single `DEVELOP` tick.

Fixture codex produces no commits (simulates failure).

**DEVELOP tick** — Develop fails verification and routes to needs-review

- [ ] PR exists (work is visible even on failure)
- [ ] PR is marked ready for review
- [ ] Issue has `runoq:needs-human-review` label
- [ ] Issue is NOT closed
- [ ] PR has durable handoff audit comment explaining the failure reason and needs-review routing

### Scenario: Budget exhaustion path (2 sub-paths)

Fixture codex returns `budget_exhausted` status (simulates token budget exceeded).

As with other implementation failures, a fresh issue still reaches this path only after a prior `INIT` tick has already created the PR.

**Before-round exhaustion** — budget exhausted before starting a developer round

- [ ] PR exists so the branch is visible
- [ ] PR is marked ready for review
- [ ] Issue has `runoq:needs-human-review` label
- [ ] Issue is NOT closed
- [ ] PR has durable handoff audit comment indicating budget exhaustion before the round started

**After-round exhaustion** — budget exhausted after a developer round completes

- [ ] PR exists so the branch is visible
- [ ] PR is marked ready for review
- [ ] Issue has `runoq:needs-human-review` label
- [ ] Issue is NOT closed
- [ ] PR has durable handoff audit comment indicating budget exhaustion after the round completed

### Scenario: Max rounds exhausted path

Same as iterate path but the fixture reviewer returns `ITERATE` on every review until `maxRounds` is reached.

After final review tick (ITERATE at max round):

- [ ] The next tick is `DECIDE`, not `FINALIZE`
- [ ] `DECIDE` falls through to `finalize-needs-review` (round >= maxRounds)
- [ ] The following tick performs `FINALIZE`
- [ ] Issue has `runoq:needs-human-review` label
- [ ] PR is marked ready for review, reviewer assigned
- [ ] PR body has `## Final Status` table with ITERATE verdict and max round count

### Scenario: INIT failure path (1 tick)

Fixture simulates worktree creation or branch push failure during INIT.

**Tick 1** — INIT fails, issue returned to queue

- [ ] Issue reverts to `runoq:ready` label (not `runoq:in-progress`)
- [ ] No PR created
- [ ] No worktree left behind

### Scenario: INIT eligibility rejection paths

Eligibility rejection is observable even though no worktree or PR is created.

**Missing acceptance criteria**

- [ ] Issue remains `runoq:ready`
- [ ] No PR is created
- [ ] The issue receives a skip comment explaining that acceptance criteria are missing

**Blocked dependency**

- [ ] Issue remains `runoq:ready`
- [ ] No PR is created
- [ ] The issue receives a skip comment explaining which dependency is still incomplete

**Existing open PR for branch**

- [ ] A fresh dispatch does not create a duplicate PR or worktree
- [ ] The issue receives a skip comment explaining that an open PR already tracks the branch
- [ ] Queue-mode terminal output reports the issue as skipped instead of advancing the phase machine

**Branch conflict**

- [ ] Issue remains `runoq:ready`
- [ ] No PR is created
- [ ] The issue receives a skip comment explaining that the branch has unresolved conflicts

### Scenario: Finalize decision sub-paths

Fixture reviewer returns `PASS` but finalize routes to needs-review due to config constraints.

**PASS with caveats** — finalize sees persisted caveats despite a `PASS` review verdict:

- [ ] Issue has `runoq:needs-human-review` (not `runoq:done`)
- [ ] PR finalize comment shows reason: caveats present
- [ ] PR marked ready, reviewer assigned (not auto-merged)

**PASS with auto-merge disabled** — config has `auto_merge_enabled=false`:

- [ ] Issue has `runoq:needs-human-review`
- [ ] PR finalize comment shows reason: auto-merge disabled

**PR finalization fails** — `pr ready` or merge operation fails:

- [ ] Issue does not move to `runoq:done`
- [ ] Issue is not closed
- [ ] Terminal output reports a finalize failure
- [ ] The tick exits with an error instead of silently completing the issue

**Issue status update fails** — PR finalization succeeds, but the issue label/status transition fails:

- [ ] The tick exits with an error instead of returning `DONE`
- [ ] The issue does not silently appear complete in terminal output
- [ ] The failure is surfaced as an issue-status/finalize error

**PR body final status update fails** — final audit comment may exist, but the PR body cannot be updated:

- [ ] The tick exits with an error instead of returning `DONE`
- [ ] The PR body is not allowed to silently miss the `## Final Status` table while the issue is reported complete
- [ ] Terminal/log output surfaces the PR body update failure

### Scenario: Verification failure drives iteration across ticks

Verification fails on the first `VERIFY` tick. A later round succeeds after `DECIDE(iterate)` sends the issue back through `DEVELOP`.

- [ ] Failed `VERIFY` posts a verifier PR comment with concrete failures
- [ ] `DECIDE` records an `iterate` decision instead of finalizing
- [ ] The next `DEVELOP` tick carries the prior checklist into the codex prompt
- [ ] A later round succeeds through the normal `DEVELOP -> VERIFY -> REVIEW -> DECIDE` cadence

### Scenario: Invalid payload schema recovery

Fixture codex writes an invalid final payload block but includes a valid `thread.started` event.

**Schema retry succeeds**

- [ ] `state validate-payload` returns normalized JSON with `payload_schema_valid=false` for the first payload
- [ ] Same-thread schema retry is attempted
- [ ] Only one same-thread schema retry is attempted
- [ ] The corrected payload is accepted without re-running development commands
- [ ] Tick stops in `DEVELOP`; verification still happens later in the separate `VERIFY` tick

**Schema retry fails**

- [ ] `state validate-payload` continues to return normalized JSON with `payload_schema_valid=false` after the single allowed retry
- [ ] `DEVELOP` still completes and persists the invalid payload state
- [ ] Persisted state/comment includes schema metadata such as `payload_source`, `payload_schema_errors`, and caveat text indicating one failed resume attempt
- [ ] The later `VERIFY` tick records the deterministic failure
- [ ] Tick does not loop indefinitely

**No `thread.started` event**

- [ ] `state validate-payload` returns normalized JSON with `payload_schema_valid=false`
- [ ] No same-thread schema retry is attempted because no resumable thread ID is available
- [ ] `DEVELOP` still completes and persists caveats indicating the payload remained invalid and no thread ID was available
- [ ] The later `VERIFY` tick records the deterministic failure

**Synthetic or patched payload normalization**

- [ ] `state validate-payload` truth-backs commit and file facts from git ground truth
- [ ] The normalized payload exposes `payload_source` as `clean`, `patched`, or `synthetic`
- [ ] The normalized payload exposes `patched_fields` and `discrepancies` when the codex payload is missing, malformed, or inconsistent with git ground truth

### Scenario: Resume from crash (idempotency)

Process crashes after tick 2 (`DEVELOP` complete, state persisted in a PR comment). A new `tick_once` call starts fresh.

**Next tick** — Derives state from PR comments, resumes correctly

- [ ] `deriveStateFromGitHub` finds existing PR and extracts state from audit comment
- [ ] Tick resumes from `DEVELOP` state and runs `VERIFY`, not `INIT` or `DEVELOP`
- [ ] No duplicate PR created
- [ ] Later ticks continue through `REVIEW`, `DECIDE`, and `FINALIZE`

### Scenario: RESPOND preempts progress

An unprocessed PR comment exists before a normal implementation phase starts.

- [ ] The next tick runs `RESPOND` only
- [ ] The comment is acknowledged and marked processed
- [ ] The implementation phase does not advance in the same tick
- [ ] The following tick resumes the interrupted target phase

### Scenario: Tick-level conversation sweep

The tick scans active in-progress conversations before normal queue selection.

- [ ] An in-progress task with an unprocessed PR comment is handled before any new ready task is selected
- [ ] The tick emits `RESPOND`-only output for that task
- [ ] Normal implementation dispatch is deferred until a later tick

### Scenario: Resume after DECIDE(iterate) crash

Process crashes after the DECIDE tick has posted an `iterate` audit comment, but before the next DEVELOP tick starts.

**Next tick** — Derives iterate state from PR comments and resumes correctly

- [ ] `deriveStateFromGitHub` extracts the latest DECIDE state from the PR audit comment
- [ ] `previous_checklist` is preserved into the resumed DEVELOP round
- [ ] Tick resumes directly into the next developer round
- [ ] No duplicate PR is created
- [ ] No duplicate review or finalize actions are posted

### Scenario: Conversation loop (RESPOND phase)

An unprocessed non-audit PR comment exists while the issue is in-progress.

- [ ] Bot detects unprocessed comment (no +1 reaction, not a bot comment)
- [ ] Bot posts reply with agent attribution
- [ ] Bot adds +1 reaction to original comment (marks as processed)
- [ ] Bot-generated comments (with `runoq:bot` marker) are excluded from processing
- [ ] `RESPOND` may preempt any PR-backed implementation tick (`DEVELOP`, `VERIFY`, `REVIEW`, `DECIDE`, `FINALIZE`) and the interrupted phase does not advance in that tick

### Scenario: Transient error path

Fixture codex returns a `turn.failed` event with capacity error.

**First developer round** — transient failure on the first developer round

- [ ] Issue stays `runoq:in-progress` (not needs-review)
- [ ] A PR already exists for the issue
- [ ] The PR remains open
- [ ] Tick exits in a waiting/retryable state rather than escalating
- [ ] Retry/backoff state is persisted for the next tick
- [ ] Terminal output clearly reports a transient/retryable failure reason

**Later developer round** — transient failure on a later developer round

- [ ] Issue stays `runoq:in-progress` (not needs-review)
- [ ] Existing PR remains open
- [ ] PR has diagnostic comment about the transient failure
- [ ] Retry/backoff state is persisted for the next tick
- [ ] Exit code indicates "waiting/retryable" (not error)

**Tick 2** — Retry succeeds

- [ ] Normal develop retry flow resumes against the existing PR
- [ ] `transient_retries` and `transient_retry_after` are cleared after a successful develop round

**Backoff still active**

- [ ] A tick that lands before `transient_retry_after` does not invoke codex again
- [ ] Tick returns the persisted `DEVELOP` waiting state with `waiting=true` and `waiting_reason=transient_backoff`

**Retry budget exhausted**

- [ ] The 5th consecutive transient failure escalates to deterministic needs-review handoff
- [ ] PR is marked ready for review
- [ ] Issue moves to `runoq:needs-human-review`
- [ ] A durable finalize audit comment explains the transient-failure handoff

### Scenario: Reviewer contract repair

Fixture reviewer returns malformed review output on the initial `REVIEW` invocation.

**Repair succeeds**

- [ ] `REVIEW` captures the reviewer thread ID
- [ ] A same-thread repair attempt is issued within the same `REVIEW` tick
- [ ] The posted review comment records `Repair attempted = yes`
- [ ] The repaired review comment contains a valid scorecard, verdict, and score
- [ ] Tick still stops at `REVIEW`; `DECIDE` happens later in its own tick

**Repair fails**

- [ ] Only one same-thread repair attempt is issued
- [ ] The posted review comment records contract errors
- [ ] Review state is forced to `FAIL` with score `0`
- [ ] The next tick goes through `DECIDE` and routes to needs-review finalization unless another iteration path is available

### Scenario: PR exists but no recoverable state comment

An issue already has a linked PR, but the PR comments do not contain a structured `<!-- runoq:state:... -->` block.

- [ ] `deriveStateFromGitHub` finds the linked PR but returns `found=false`
- [ ] A fresh dispatch does not silently resume from non-structured prose comments
- [ ] A fresh dispatch does not create a duplicate PR or worktree
- [ ] The issue receives a skip comment explaining that an open PR already tracks the branch
- [ ] Queue-mode terminal output reports the issue as skipped

### Scenario: Recoverable work exists but PR is missing

Recovered or legacy state reaches `DEVELOP` with a branch but no PR number.

- [ ] The tick creates a draft PR for the existing branch instead of restarting from `INIT`
- [ ] The PR receives a state-bearing audit comment using the current phase marker, not a legacy pseudo-phase
- [ ] The resulting state includes the new `pr_number`
- [ ] If the audit comment cannot be posted, the tick exits with an error instead of silently proceeding with unpersisted recovery state

### Scenario: Audit-state persistence failures

GitHub audit comments are the recoverable state carrier for implementation ticks.

- [ ] If an `INIT`, `DEVELOP`, `VERIFY`, `REVIEW`, `DECIDE`, or recovery/backfill audit comment cannot be posted, the tick exits with an error
- [ ] The runtime does not report success for a phase whose state could not be durably persisted to GitHub
- [ ] Durable needs-review handoff comments are treated the same way: a failed handoff comment is an error, not a warning

### Scenario: All tasks blocked

An epic has open child tasks, but none are dispatchable because dependencies or cycles block every candidate.

- [ ] The tick reports blocked reasons for non-dispatchable tasks
- [ ] A dependency cycle is reported when one exists
- [ ] Terminal output reports `All tasks blocked`
- [ ] The tick returns a waiting result rather than dispatching work

### Scenario: Loop driver behavior

`runoq loop` is operator-visible behavior and should be validated separately from single-tick semantics.

- [ ] Exit code `0` from `tick` causes the next loop iteration to start immediately without sleeping
- [ ] Exit code `2` from `tick` increments the wait-cycle counter and sleeps for the configured backoff duration
- [ ] `--max-wait-cycles <n>` stops the loop after `n` consecutive waiting ticks with terminal output explaining why it stopped
- [ ] Exit code `3` from `tick` stops the loop cleanly because all work is complete
- [ ] In targeted-issue mode, the loop exits once that issue reports complete
- [ ] Invalid `--issue`, `--backoff`, and `--max-wait-cycles` values are rejected before the loop starts

### Scenario: Fixture Smoke — Planning Flow

Planning smoke is part of the currently implemented externally observable behavior and should not remain undocumented.

Minimum observable scenarios:

- [ ] Bootstrap path with no existing epics creates the planning epic and planning issue, then posts a proposal
- [ ] Pending planning review with unresolved human comments responds to those comments and does not advance in the same tick
- [ ] Planning review can remain in an awaiting-human-decision state across ticks without materializing milestones early
- [ ] Approved planning review materializes milestone or task issues, closes the review issue, and closes its parent `Project Planning` issue
- [ ] Approved top-level planning review seeds a planning issue only for the first newly created milestone
- [ ] Proposal posting for that seeded task-planning issue happens on a later planning-dispatch tick, not during the approval-application tick
- [ ] Approved adjustment review applies accepted adjustments, closes the review issue, and closes its parent planning issue
- [ ] Approved adjustment review seeds the next planning issue only when the next open milestone does not already have a planning child
- [ ] Approved adjustment terminal output reports that adjustments were applied, rather than always claiming new issues were created
- [ ] When a milestone's child tasks drain, the tick runs milestone review and creates an adjustment review issue
- [ ] When all milestones are complete, the terminal output reports that all milestones are complete

---

## Tier 3: Live Smoke — Full End-to-End

**Purpose:** Validate that real agents produce working code against a real codebase, reviews are meaningful, and the full pipeline converges from plan to merged PRs.

### Setup

- Fresh GitHub repo from `test/fixtures/live_smoke_lifecycle_target`
- Real codex for code generation
- Real claude for reviews
- `runoq loop --backoff 5 --max-wait-cycles 3` drives the system

Validations and verifications are to be derived from fixture smokes above, with the understanding that we don't have deterministic control over the outcome due to LLMs doing the implementation. We should review the final state of the project and all artefacts (logs in the terminal, comments and other events on PRs/issues) and confirm that they match expected behaviour and formats, and are consistent with desired project-completed state.

Lifesmoke target project needs to be set up in a way that would maximise verification pathways, at the same time keeping it practical and feasible in terms of implementation time required.

## Tier Structure Summary

| Tier                     | Agents                           | GitHub   | Runtime    | Runs when               | Validates                                                                       |
| ------------------------ | -------------------------------- | -------- | ---------- | ----------------------- | ------------------------------------------------------------------------------- |
| Fixture — Planning       | fixture-claude                   | real API | ~2 min     | every change            | orchestration logic, human interaction flow                                     |
| Fixture — Implementation | fixture-codex + fixture-reviewer | real API | ~3 min     | every change            | tick-per-phase boundaries, PR lifecycle, label transitions, agent communication |
| Live — End-to-end        | real codex + real claude         | real API | ~15-30 min | pre-release / scheduled | actual code generation, review quality, full convergence                        |
