# Smoke Testing Specification

## Philosophy

Smoke tests validate **externally observable behavior** — what the operator sees in the terminal and on GitHub. They don't test internal logic (unit tests do that). They answer: "if I run runoq against a real repo, does it produce the correct GitHub state and terminal output at each step?"

Two tiers exist because they optimize for different things:

- **Fixture smoke** — fast (~2 min), uses fixture agents, real GitHub API. Validates orchestration logic: correct labels, PR lifecycle, comment structure, tick-per-phase boundaries. Runs on every change.
- **Live smoke** — slow (~15-30 min), uses real codex/claude. Validates that agents actually produce working code, reviews are meaningful, and the full pipeline converges. Runs before releases or on schedule.

Both tiers use the same validation functions — the difference is only what drives the agents.

---

## Tier 1: Fixture Smoke — Planning Flow

_TODO document_

---

## Tier 2: Fixture Smoke — Implementation Flow

### Setup

Same repo and fixture infrastructure as the planning smoke. After tasks are materialized under a milestone, instead of manually closing them, run the implementation tick-per-phase flow with a **fixture codex** that:

1. Creates a real file in the worktree
2. Commits and pushes to origin
3. Writes valid JSONL events to stdout (with `thread.started` and token counts)
4. Writes a valid last-message payload to the `-o` path (matching the required schema)
5. Validates that a consistent thread ID is used across all runs

And a **fixture diff-reviewer** response for the `claude stream-json --agent diff-reviewer` invocation that returns a PASS verdict with a score.
diff-reviewer fixture must also validate that it receives correct consistent resume thread ID throughout the whole PR review work.

### Scenario: Happy path (6 ticks)

**Tick 1 — Init:**

```
tick_once
```

Verify on GitHub:

- [ ] Issue has `runoq:in-progress` label (not `runoq:ready`)
- [ ] A draft PR for correct branch exists with `Closes #<issue>` in body
- [ ] PR audit comment contains correct state/phase and implementor agent attribution
- [ ] The issue branch has been pushed to origin with the initial empty bootstrap commit
- [ ] No implementation code commits are pushed yet

**Tick 2 — Develop:**

```
tick_once
```

Verify on GitHub:

- [ ] Code commits are pushed to PR
- [ ] PR has a new `DEVELOP` audit comment with implementor attribution
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

### Scenario: Iterate path (6 ticks)

Same as happy path but the fixture reviewer returns `ITERATE` on first review.

**Tick 1** — Develop + Open PR (same checks as happy path tick 1)

**Tick 2** — Review returns ITERATE verdict

- [ ] PR has review comment with `ITERATE` verdict and reviewer agent attribution
- [ ] Issue still `runoq:in-progress`

**Tick 3** — Decide: iterate

- [ ] PR has audit comment for decide phase with orchestrator agent attribution, containing "iterate" decision
- [ ] State includes review checklist carried forward as `previous_checklist`

**Tick 4** — Develop round 2

- [ ] New commits pushed to branch (from second codex round)
- [ ] PR has new develop audit comment for round 2 with implementor agent attribution
- [ ] Implementor codex has been launched via resume with its correct consistent thread id for this PR and a modified propmpt that includes review feedback and allows for different responses: address and modify code, or push back with an explanation

**Tick 5** — Review (PASS this time)

- [ ] PR has review comment with `PASS` verdict and reviewer agent attribution
- [ ] Reviewer claude has been launched with --resume with its correct consistent thread id for this PR

**Tick 6** — Decide + Finalize

- [ ] Same finalize checks as happy path tick 3

### Scenario: Needs-review path (1 tick)

Fixture codex produces no commits (simulates failure).

**Tick 1** — Develop fails verification, creates PR, sets needs-review

- [ ] PR exists (work is visible even on failure)
- [ ] PR is marked ready for review
- [ ] Issue has `runoq:needs-human-review` label
- [ ] Issue is NOT closed
- [ ] PR has durable handoff audit comment explaining the failure reason and needs-review routing

### Scenario: Budget exhaustion path (2 sub-paths)

Fixture codex returns `budget_exhausted` status (simulates token budget exceeded).

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

After final review tick (ITERATE at max round) — Decide + Finalize to needs-review:

- [ ] DECIDE falls through to `finalize-needs-review` (round >= maxRounds)
- [ ] Issue has `runoq:needs-human-review` label
- [ ] PR is marked ready for review, reviewer assigned
- [ ] PR body has `## Final Status` table with ITERATE verdict and max round count

### Scenario: INIT failure path (1 tick)

Fixture simulates worktree creation or branch push failure during INIT.

**Tick 1** — INIT fails, issue returned to queue

- [ ] Issue reverts to `runoq:ready` label (not `runoq:in-progress`)
- [ ] No PR created
- [ ] No worktree left behind

### Scenario: Finalize decision sub-paths

Fixture reviewer returns `PASS` but finalize routes to needs-review due to config constraints.

**PASS with caveats** — reviewer returns PASS with caveats:

- [ ] Issue has `runoq:needs-human-review` (not `runoq:done`)
- [ ] PR finalize comment shows reason: caveats present
- [ ] PR marked ready, reviewer assigned (not auto-merged)

**PASS with auto-merge disabled** — config has `auto_merge_enabled=false`:

- [ ] Issue has `runoq:needs-human-review`
- [ ] PR finalize comment shows reason: auto-merge disabled

### Scenario: Verification failure drives iteration across ticks

Verification fails on the first `VERIFY` tick. A later round succeeds after `DECIDE(iterate)` sends the issue back through `DEVELOP`.

- [ ] Failed `VERIFY` posts a verifier PR comment with concrete failures
- [ ] `DECIDE` records an `iterate` decision instead of finalizing
- [ ] The next `DEVELOP` tick carries the prior checklist into the codex prompt
- [ ] A later round succeeds through the normal `DEVELOP -> VERIFY -> REVIEW -> DECIDE` cadence

### Scenario: Invalid payload schema recovery

Fixture codex writes an invalid final payload block but includes a valid `thread.started` event.

**Schema retry succeeds**

- [ ] `state validate-payload` fails for the first payload
- [ ] Same-thread schema retry is attempted
- [ ] The corrected payload is accepted without re-running development commands
- [ ] Tick stops in `DEVELOP`; verification still happens later in the separate `VERIFY` tick

**Schema retry fails**

- [ ] `state validate-payload` remains invalid after bounded retries
- [ ] `DEVELOP` still completes and persists the invalid payload state
- [ ] The later `VERIFY` tick records the deterministic failure
- [ ] Tick does not loop indefinitely

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

### Scenario: Resume after DECIDE(iterate) crash

Process crashes after the DECIDE tick has posted an `iterate` audit comment, but before the next DEVELOP tick starts.

**Next tick** — Derives iterate state from PR comments and resumes correctly

- [ ] `deriveStateFromGitHub` extracts the latest DECIDE state from the PR audit comment
- [ ] `previous_checklist` is preserved into the resumed DEVELOP round
- [ ] Tick resumes directly into the next developer round
- [ ] No duplicate PR is created
- [ ] No duplicate review or finalize actions are posted

### Scenario: Conversation loop (RESPOND phase)

A human posts a comment on the PR while the issue is in-progress.

- [ ] Bot detects unprocessed comment (no +1 reaction, not a bot comment)
- [ ] Bot posts reply with agent attribution
- [ ] Bot adds +1 reaction to original comment (marks as processed)
- [ ] Bot-generated comments (with `runoq:bot` marker) are excluded from processing

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
