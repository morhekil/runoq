# Smoke Testing Specification

## Philosophy

Smoke tests validate **externally observable behavior** — what the operator sees in the terminal and on GitHub. They don't test internal logic (unit tests do that). They answer: "if I run runoq against a real repo, does it produce the correct GitHub state and terminal output at each step?"

Two tiers exist because they optimize for different things:

- **Fixture smoke** — fast (~2 min), uses fixture agents, real GitHub API. Validates orchestration logic: correct labels, PR lifecycle, comment structure, tick-per-phase boundaries. Runs on every change.
- **Live smoke** — slow (~15-30 min), uses real codex/claude. Validates that agents actually produce working code, reviews are meaningful, and the full pipeline converges. Runs before releases or on schedule.

Both tiers use the same validation functions — the difference is only what drives the agents.

---

## Tier 1: Fixture Smoke — Planning Flow

**What exists today** (`smoke-tick.sh`): validates the planning orchestration through milestone decomposition, human approval, task materialization, and adjustment reviews.

**No changes needed** — this passed with 21/21 checks. It already exercises:
- Bootstrap (runoq init → project planning issue)
- Proposal posting (agent writes proposal into issue body)
- Human comment handling (eyes reaction → response → +1 reaction)
- Awaiting-review state detection
- Milestone materialization (approved plan → epic issues created)
- Task decomposition (planning issue → task issues under epic)
- Milestone review and adjustment cycle
- All-milestones-complete terminal state

---

## Tier 2: Fixture Smoke — Implementation Flow

**Does not exist yet.** This is the critical gap.

### Setup

Same repo and fixture infrastructure as the planning smoke. After tasks are materialized under a milestone, instead of manually closing them, run the implementation tick-per-phase flow with a **fixture codex** that:

1. Creates a real file in the worktree
2. Commits and pushes to origin
3. Writes valid JSONL events to stdout (with `thread.started` and token counts)
4. Writes a valid last-message payload to the `-o` path (matching the required schema)

And a **fixture diff-reviewer** response for the `claude stream-json --agent diff-reviewer` invocation that returns a PASS verdict with a score.

### Scenario: Happy path (3 ticks)

**Tick 1 — Develop + Open PR:**
```
tick_once
```
Verify on GitHub:
- [ ] Issue has `runoq:in-progress` label (not `runoq:ready`)
- [ ] A draft PR for correct branch exists with `Closes #<issue>` in body
- [ ] PR audit comment contains `<!-- runoq:state:{...} -->` with `"phase":"OPEN-PR"` and implementor agent attribution
- [ ] Code commits are pushed to PR
- [ ] Verification result posted as PR comment with verifier attribution
- [ ] No review comments or other activity on PR yet

Verify correct output on terminal.

**Tick 2 — Review:**
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

**Tick 3 — Decide + Finalize (PASS path):**
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

**Tick 5** — Review (PASS this time)
- [ ] PR has review comment with `PASS` verdict and reviewer agent attribution

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

### Scenario: Verification comment during DEVELOP

After codex runs, the issue-runner runs verification against acceptance criteria.

- [ ] Verification result posted as a comment on PR with verifier agent attribution
- [ ] Verification checklist is carried forward to next round on iterate

### Scenario: Verification-driven iteration inside DEVELOP

Verification fails on the first internal developer round, then succeeds on a later internal round without leaving the DEVELOP tick.

- [ ] First failed verification posts a verifier PR comment with concrete failures
- [ ] The next internal developer round receives the carried-forward verification checklist
- [ ] A later internal round succeeds and the overall tick exits at `OPEN-PR`
- [ ] The PR shows both the failed verification artefact and the later successful develop/open-PR artefacts

### Scenario: Verification max-rounds exhaustion inside DEVELOP

Verification fails on every internal developer round until `maxRounds` is reached.

- [ ] Verifier PR comments are posted for each failed internal round
- [ ] The final tick escalates to `runoq:needs-human-review`
- [ ] PR is marked ready for review
- [ ] PR has durable handoff audit comment explaining max-rounds exhaustion from verification failures

### Scenario: Invalid payload schema recovery

Fixture codex writes an invalid final payload block but includes a valid `thread.started` event.

**Schema retry succeeds**
- [ ] `state validate-payload` fails for the first payload
- [ ] Same-thread schema retry is attempted
- [ ] The corrected payload is accepted without re-running development commands
- [ ] Tick continues through normal verification and PR flow

**Schema retry fails**
- [ ] `state validate-payload` remains invalid after bounded retries
- [ ] Issue escalates to `runoq:needs-human-review`
- [ ] PR has durable handoff audit comment explaining payload/schema failure
- [ ] Tick does not loop indefinitely

### Scenario: Resume from crash (idempotency)

Process crashes after tick 1 (DEVELOP + OPEN-PR complete, state persisted in PR comment). A new `tick_once` call starts fresh.

**Tick 2** — Derives state from PR comments, resumes correctly
- [ ] `deriveStateFromGitHub` finds existing PR and extracts state from audit comment
- [ ] Tick resumes from OPEN-PR phase (runs REVIEW), does NOT re-run INIT or DEVELOP
- [ ] No duplicate PR created
- [ ] Review proceeds normally

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

**Before PR exists** — transient failure on the first developer round
- [ ] Issue stays `runoq:in-progress` (not needs-review)
- [ ] No PR is created yet
- [ ] Tick exits in a waiting/retryable state rather than escalating
- [ ] Terminal output clearly reports a transient/retryable failure reason

**After PR exists** — transient failure on a later developer round
- [ ] Issue stays `runoq:in-progress` (not needs-review)
- [ ] Existing PR remains open
- [ ] PR has diagnostic comment about the transient failure
- [ ] Exit code indicates "waiting/retryable" (not error)

**Tick 2** — Retry succeeds
- [ ] Normal develop + PR creation flow

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

| Tier | Agents | GitHub | Runtime | Runs when | Validates |
|------|--------|--------|---------|-----------|-----------|
| Fixture — Planning | fixture-claude | real API | ~2 min | every change | orchestration logic, human interaction flow |
| Fixture — Implementation | fixture-codex + fixture-reviewer | real API | ~3 min | every change | tick-per-phase boundaries, PR lifecycle, label transitions, agent communication |
| Live — End-to-end | real codex + real claude | real API | ~15-30 min | pre-release / scheduled | actual code generation, review quality, full convergence |
