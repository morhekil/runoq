# Spec Issues

- **[2026-04-11 02:03:32]** Missing selection semantics: approved reviews can close successfully with zero items applied (Medium)
  - `comments/comments.go:43`-`75` can produce approve/reject selections that filter every proposed item out
  - `internal/orchestrator/tick.go:494`-`530` and `internal/orchestrator/tick.go:624`-`690` still treat that empty filtered selection as a successful apply path; top-level planning and adjustment reviews can close both the review and parent epic even when no milestones/tasks/adjustments were materialized
  - Observable result: a review can report `Applied approvals from #<n>, created issues` or `Applied adjustments from #<n>` even though human selection comments excluded every proposed change
  - The draft spec covers non-empty subset selection but does not say whether zero-selection approval is intended, forbidden, or should keep the review open
  - Classification: spec gap — add explicit zero-selection scenarios for top-level planning, milestone task-planning, and adjustment reviews
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:57:08]** Loop-driver wording is too narrow for targeted issues: it exits on any terminal `DONE` handoff, not only success completion (Medium)
  - `internal/cli/app.go:694`-`709` treats any `Issue #<n> — phase: DONE` line as loop-completion bookkeeping for the targeted issue
  - `internal/cli/app.go:770`-`778` then exits `runoq loop --issue N` as soon as that parsed `DONE` issue matches the target
  - `internal/orchestrator/phases.go:945`-`951` returns `phase:"DONE"` after deterministic needs-review handoff even though the issue remains open with `issue_status:"needs-review"`
  - Observable result: targeted loop mode stops after terminal human handoff as well as after successful merge/close completion
  - `docs/plans/smoke-testing-spec.md:741`-`750` currently says the loop exits once the targeted issue "reports complete", which is narrower and ambiguous relative to the implemented behavior
  - Classification: spec gap/inaccuracy — describe targeted loop exit in terms of terminal `DONE` state / terminal handoff, not just successful completion
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:48:45]** Missing precedence rule: approved planning/adjustment reviews are applied even when fresh human comments still exist (Medium)
  - Current implemented behavior at `internal/orchestrator/tick.go:160`-`193` and `comments/comments.go:43`-`77`
  - Once the plan-approved label is present, `findReviewIssue("pending")` no longer considers that review, and the tick proceeds straight to `handleApprovedPlanning()` / `handleApprovedAdjustment()` without any unanswered-comment pass
  - The apply path still parses raw human comments for approve/reject item selection, but non-selection comments are otherwise ignored while the review may be closed in that same tick
  - Observable result: an approved review with a fresh human question or change request can be materialized and closed without posting a responder reply first
  - `docs/plans/smoke-testing-spec.md:35`-`46` only defines unanswered-comment handling for pending reviews, and `docs/plans/smoke-testing-spec.md:81`-`145` only describe approved-review application after approval; the combined case is unspecified
  - Classification: spec gap/inconsistency — add an explicit rule that post-approval human comments do not block apply, get handled first, and are responded to
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:48:45]** Missing coverage: final-round verification failure goes directly to needs-review handoff without a REVIEW tick (Medium)
  - Current implemented behavior at `internal/orchestrator/phases.go:740`-`783`, especially `internal/orchestrator/phases.go:760`-`772`
  - `phaseDecide()` only chooses `iterate` for a failed `VERIFY` state when `round < maxRounds`; otherwise it keeps the default `finalize-needs-review` decision and sends the issue to `FINALIZE`
  - Observable result: if deterministic verification fails on the last allowed developer round, the runtime skips reviewer invocation and transitions from `VERIFY` to `DECIDE(finalize-needs-review)` and then human handoff
  - `docs/plans/smoke-testing-spec.md:531`-`538` only covers verification failure that later iterates successfully, and `docs/plans/smoke-testing-spec.md:431`-`443` only covers max-round exhaustion via repeated reviewer `ITERATE` verdicts
  - Classification: spec gap — add a max-round verification-failure scenario that asserts the direct needs-review handoff and the absence of an additional REVIEW tick
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:42:40]** Missing precedence rule: planning reviews with no proposal payload are redispatched before unanswered human comments are handled (Medium)
  - Current implemented behavior at `internal/orchestrator/tick.go:421`-`442`
  - `handlePendingReview()` checks for the `runoq:payload:plan-proposal` marker and returns `1` at `internal/orchestrator/tick.go:435`-`438` before it ever calls `comments.FindUnrespondedCommentIDs()`
  - Observable result: if a pending planning review has both no proposal payload in its body and fresh human comments, the tick treats it as "needs dispatch" and continues to proposal generation instead of responding to those comments first
  - `docs/plans/smoke-testing-spec.md:34`-`46` says unanswered comments are handled before other work, while `docs/plans/smoke-testing-spec.md:64`-`68` says a no-proposal planning review is redispatched; the combined case is currently unspecified
  - Classification: spec gap/inconsistency — add an explicit combined scenario or precedence rule so smoke tests lock down whether comment handling or redispatch wins when both conditions are true
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:42:40]** Missing coverage: tick-level PR conversation sweep preempts planning and milestone progression, not just implementation queue selection (Medium)
  - Current implemented behavior at `internal/orchestrator/tick.go:199`-`225`
  - The active-conversation sweep runs after pending/approved review handling but before planning-child dispatch, implementation selection, or milestone-complete review creation
  - Observable result: an unrelated in-progress task with an unprocessed PR comment can make the tick exit via `Responded to comments on PR #<n>` even when the current epic would otherwise have posted a planning proposal or created a milestone-adjustment review in that tick
  - `docs/plans/smoke-testing-spec.md:592`-`606` describes this sweep only as an implementation-flow pre-dispatch behavior and does not capture the broader tick-global preemption that the runtime actually implements
  - Classification: spec gap — expand the scenario wording or add a dedicated case covering conversation-sweep preemption of non-implementation work
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:36:50]** Missing coverage: malformed review output with no reviewer thread ID fails closed without any repair attempt (Medium)
  - Current implemented behavior at `internal/orchestrator/phases.go:536`-`591`
  - Reviewer contract repair is attempted only when contract errors exist and `review_thread_id` is non-empty; without a resumable thread, `REVIEW` immediately forces `FAIL` / `0` and posts contract errors with zero repair attempts
  - `docs/plans/smoke-testing-spec.md:678`-`695` covers repair success and repair failure after a same-thread retry, but omits the no-thread-id sub-path
  - Classification: spec gap — add a reviewer-contract scenario analogous to the codex no-`thread.started` case, asserting that no repair attempt is made when no resumable reviewer thread exists and the review still fails closed
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:30:05]** Missing coverage: next-tick recovery after a partial finalize failure (High)
  - Current implemented behavior at `internal/orchestrator/phases.go:839`-`868`, `internal/orchestrator/queue.go:236`-`239`, and `internal/orchestrator/tick.go:838`-`845` / `internal/orchestrator/tick.go:904`-`924`
  - If the finalize audit comment is posted but the later PR-body, merge, or issue-status step fails, the next tick recovers a persisted `FINALIZE` state and currently treats it as terminal rather than re-running finalization
  - `docs/plans/smoke-testing-spec.md:506`-`529` only specifies the failing tick, and `docs/plans/smoke-testing-spec.md:573`-`582` / `docs/plans/smoke-testing-spec.md:617`-`627` only cover crash recovery from earlier phases
  - Classification: spec gap — add a recovery scenario asserting that a stale `FINALIZE` state does not silently succeed on the next tick and instead either retries the remaining finalize work or fails closed without mutating issue status
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:21:09]** Missing coverage: RESPOND preemption also applies to PR-backed `INIT` state before `DEVELOP` begins (Medium)
  - Current implemented behavior at `internal/orchestrator/queue.go:255`-`256` and `internal/orchestrator/queue.go:353`-`357`, with regression coverage in `internal/orchestrator/app_test.go:1853`-`1937`
  - A recovered `INIT` state that already has `pr_number` can be interrupted by PR comment handling before any `DEVELOP` work runs
  - `docs/plans/smoke-testing-spec.md:584`-`591` describes RESPOND preemption generically, but `docs/plans/smoke-testing-spec.md:629`-`638` explicitly enumerates only `DEVELOP`, `VERIFY`, `REVIEW`, `DECIDE`, and `FINALIZE`
  - Classification: spec gap/inconsistency — include PR-backed `INIT` in the explicit RESPOND preemption contract
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:21:09]** Missing coverage: first-round transient develop failures also post a durable `develop-transient` PR comment (Medium)
  - Current implemented behavior at `internal/orchestrator/queue.go:418`-`428`, with regression coverage in `internal/orchestrator/app_test.go:2996`-`3063`
  - After the `INIT` tick, the first `DEVELOP` tick already has a PR, so transient capacity/rate-limit failures post the same diagnostic audit comment as later-round failures
  - `docs/plans/smoke-testing-spec.md:644`-`659` only requires the diagnostic PR comment for the "Later developer round" sub-path
  - Classification: spec gap — first-round transient failures should also assert the `develop-transient` audit comment
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:21:09]** Missing coverage: corrupt persisted implementation state fails closed on unsupported phase or RESPOND resume target (Medium)
  - Current implemented behavior at `internal/orchestrator/queue.go:240`-`295`, with regression coverage in `internal/orchestrator/app_test.go:1335`-`1399`
  - If the latest PR audit-state block contains an unknown `phase`, or a `RESPOND` state with an unsupported `resume_phase`, the tick errors immediately instead of guessing a recovery path
  - `docs/plans/smoke-testing-spec.md:573`-`582`, `docs/plans/smoke-testing-spec.md:617`-`627`, and `docs/plans/smoke-testing-spec.md:697`-`730` cover only supported recovery states and omit this failure path
  - Classification: spec gap — add a recovery-state corruption scenario that asserts a hard failure rather than silent replay
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:14:36]** Missing coverage: needs-review handoff can fail after marking the PR ready but before moving the issue to needs-review (Medium)
  - Current implemented behavior at `internal/orchestrator/phases.go:937` and `internal/orchestrator/phases.go:941`, with regression coverage in `internal/orchestrator/app_test.go:896`
  - If reviewer assignment fails during `phaseDevelopNeedsReview`, the tick errors after `pr ready` has already happened, leaving a partially completed observable handoff
  - `docs/plans/smoke-testing-spec.md:392`-`429` only describe successful needs-review and budget-exhaustion handoffs, and `docs/plans/smoke-testing-spec.md:724`-`730` only cover audit-comment persistence failure
  - Classification: spec gap — add a failure scenario for partial needs-review handoff after the handoff audit comment succeeds
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:08:03]** Missing coverage: milestone review can fail after creating the adjustment review issue but before assignment (Medium)
  - Current implemented behavior at `internal/orchestrator/tick.go:887` and `internal/orchestrator/tick.go:892`
  - On assignment failure, the tick errors after the adjustment review issue already exists, leaving a partially completed observable state
  - `docs/plans/smoke-testing-spec.md:138`-`145` and `docs/plans/smoke-testing-spec.md:777` only describe the happy path
  - Classification: spec gap — add a fixture-smoke failure scenario for create-then-assign partial success
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 00:57:09]** Spec contradicts itself on top-level planning seeding (Medium)
  - `docs/plans/smoke-testing-spec.md:81` says "first open milestone epic"
  - `docs/plans/smoke-testing-spec.md:766` says "first newly created milestone"
  - Implementation matches the latter: `internal/orchestrator/tick.go:500`
  - Classification: spec issue — reconcile to match implementation
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Missing coverage: INIT draft-PR creation failure rollback (Medium)
  - Current implemented behavior: INIT draft-PR creation failure rolls the issue back to ready after status/worktree setup
  - Implemented at `internal/orchestrator/phases.go:96` with coverage in `internal/orchestrator/app_test.go:487`
  - Classification: spec gap — behavior exists and is tested, but smoke spec does not describe it
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Missing coverage: bootstrap/planning-dispatch failure after proposal body update when assignment fails (Medium)
  - Current implemented behavior at `internal/orchestrator/tick.go:404` and `internal/orchestrator/tick.go:739`
  - Coverage in `internal/orchestrator/tick_test.go:1515`
  - Classification: spec gap — behavior exists and is tested, but smoke spec does not describe it
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Missing scenario: INIT fails on draft PR creation after the branch has already been pushed
  - Expected: issue returns to ready, no orphan worktree remains, no success is reported
  - Classification: new scenario to add to smoke spec
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Missing scenario: bootstrap/planning-dispatch posts proposal body successfully but then fails on assignment
  - Expected: tick errors and must not report proposal-post success
  - Classification: new scenario to add to smoke spec
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Missing scenario: approved planning apply fails after creating some tasks but before finishing dependency linking
  - Smoke should assert whether partial tasks are forbidden or intentionally tolerated
  - Related to system bug: approved milestone task-planning does not fail closed (`internal/orchestrator/tick.go:544`)
  - Classification: new scenario to add to smoke spec
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Missing scenario: approved adjustment apply receives a mixed batch where an early valid adjustment mutates state and a later invalid adjustment fails
  - Smoke should lock down atomic vs partial-apply behavior
  - Related to system bug: approved adjustment application is non-atomic (`internal/orchestrator/tick.go:638`)
  - Classification: new scenario to add to smoke spec
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

# System Issues

- **[2026-04-11 02:11:06]** Planning comment handling collapses all outstanding comments into one action, so mixed intents cannot be handled correctly (Medium)
  - `comments/handler.go:50`-`59` collects every unresponded planning comment ID and concatenates their bodies into one `commentBody` payload
  - `comments/handler.go:94`-`145` then asks the responder agent for a single `action`/`reply`, applies at most one side effect (`approve` or `change-request`), posts one reply, and marks every collected comment with `THUMBS_UP`
  - Observable result: if one fresh comment asks a question while another requests approval or a proposal change, the tick can only choose one action, yet it will still mark all comments as responded; some human feedback is therefore never actually answered or applied even though the review thread looks fully handled
  - Classification: runtime bug; smoke coverage should include mixed outstanding planning comments that require different actions and assert that each intent is handled separately in its own tick
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 02:03:32]** Planning-comment handling and PR `RESPOND` retries are not idempotent after partial side effects (Medium)
  - `comments/handler.go:112`-`146` posts the planning reply comment, and may also add the approval label or rewrite the proposal body, before it adds the final `THUMBS_UP` processed marker
  - `internal/orchestrator/phases.go:988`-`1011` posts the PR acknowledgment reply before adding the original comment's `+1` processed marker
  - Observable result: if the late reaction step fails, or a later side effect fails after the reply was posted, the tick returns an error but leaves visible reply/mutation side effects behind while the original comment still looks unprocessed; the next tick can then post duplicate replies and re-apply approval or change-request handling
  - Classification: runtime bug; smoke coverage should include reply-succeeds / processed-marker-fails subpaths and assert either idempotent retry behavior or a durable processed marker before visible side effects
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:57:08]** Reconciliation failures are silently ignored before dispatch (High)
  - `internal/orchestrator/tick.go:98`-`111` builds the pre-dispatch `dispatchsafety.App`, but `internal/orchestrator/tick.go:111` discards the `Reconcile()` result
  - `internal/orchestrator/queue.go:79`-`80` does the same in the single-issue `orchestrator run` path
  - Observable result: if stale-state reconciliation fails while checking linked PRs or resetting an in-progress issue, the runtime still proceeds into issue fetching and dispatch on unreconciled GitHub state, and the reconciliation failure is not surfaced in normal tick output
  - Classification: runtime bug; smoke coverage should include reconcile failure and assert the tick aborts before queue or targeted dispatch
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:57:08]** PR RESPOND only sees issue-thread comments and ignores GitHub review comments (High)
  - `internal/orchestrator/conversation.go:28`-`33` always queries `repos/<repo>/issues/<pr>/comments`
  - `internal/orchestrator/queue.go:330`-`338` and `internal/orchestrator/tick.go:290`-`321` rely on that helper for both per-phase RESPOND preemption and the tick-global conversation sweep
  - Observable result: PR review summaries and inline code-review comments under GitHub's review APIs never trigger `RESPOND`, so human review feedback can remain unacknowledged while later ticks continue other work
  - Classification: runtime bug; smoke coverage should distinguish issue comments from PR review comments and assert both operator-visible feedback channels are handled
  - Source: manual smoke-spec audit 2026-04-11

- **[2026-04-11 01:36:50]** Human thumbs-up reactions can suppress planning-comment handling and PR RESPOND preemption (Medium)
  - `comments/comments.go:80`-`123` and `internal/orchestrator/conversation.go:25`-`78` both treat any existing `THUMBS_UP` / `+1` reaction count as proof the bot already handled the comment
  - Neither path verifies that the reaction came from the runoq bot identity, even though the surrounding code comments describe the processed marker as bot-authored
  - Observable result: a human or third-party thumbs-up on a planning review comment can stop pending-review comment handling, and a human or third-party `+1` on a PR comment can stop `RESPOND` preemption, leaving real human feedback unacknowledged
  - Classification: runtime bug; smoke coverage should include pre-existing human-authored thumbs-up / `+1` reactions and assert that only bot reactions mark comments processed
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:30:05]** Partial finalization is unrecoverable and can mis-mark unfinished work as complete (High)
  - `internal/orchestrator/phases.go:839`-`868` persists a `phase:"FINALIZE"` audit comment before the PR body update, PR finalization, and issue status update have all succeeded
  - `internal/orchestrator/queue.go:236`-`239` then treats recovered `FINALIZE` state as terminal on later ticks instead of re-entering `runFromFinalize()`
  - `internal/orchestrator/tick.go:838`-`845` and `internal/orchestrator/tick.go:904`-`924` still treat that stale `FINALIZE` snapshot as success-path terminal state, and will translate `issue_status:"done"` into a fresh `set-status done`
  - Observable result: after a PR-body-update, merge, or issue-status failure that happens after the finalize audit comment succeeds, the next tick can either mark the issue `runoq:done` even though the PR was never finalized, or loop reporting `Issue #<n> — phase: FINALIZE` without retrying the missing finalize step
  - Classification: runtime bug; persisted `FINALIZE` is currently an intermediate state that is being recovered as if it were safely terminal
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:21:09]** Queue mode misclassifies open non-ready tasks as dependency-blocked (Medium)
  - `internal/orchestrator/tick.go:227`-`244` sends any epic with open task children into implementation dispatch unless there are zero open children
  - `internal/orchestrator/depgraph.go:49`-`52` then drops every OPEN task that lacks the ready label from the dispatch graph
  - `internal/orchestrator/tick.go:764`-`778` prints `All tasks blocked` when that graph has no candidate, even if the remaining open task is merely `needs-human-review` or otherwise non-ready rather than dependency-blocked
  - Observable result: queue ticks can report dependency/cycle blocking with no blocked-reason details when the real externally visible state is only "open task exists but is not ready"
  - Classification: runtime bug; smoke coverage should distinguish non-ready open tasks from true dependency/cycle blocking
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:14:36]** Planning/bootstrap issue creation fails open when hierarchy or dependency mutations fail (High)
  - `internal/issuequeue/app.go:441` calls `postCreateMutations()` after `gh issue create` succeeds, but none of those mutation failures are surfaced to the caller
  - `internal/issuequeue/app.go:449` only logs sub-issue linking failure and `internal/issuequeue/app.go:451` still logs the issue as linked even after the POST failed
  - `internal/issuequeue/app.go:849` only logs blocked-by mutation failure, so dependency materialization can be dropped silently
  - `internal/orchestrator/tick.go:380`, `internal/orchestrator/tick.go:520`, `internal/orchestrator/tick.go:549`, and `internal/orchestrator/tick.go:687` treat the returned URL as successful materialization and continue/close reviews
  - Observable result: bootstrap and approved planning/adjustment ticks can report success even though planning/task issues are not actually linked under their epics or their dependency edges were not materialized on GitHub
  - Classification: runtime bug; smoke coverage should include post-create mutation/link failures after issue creation itself succeeds
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:14:36]** Approved `modify` adjustments silently no-op when the target issue is missing from the cached issue list (Medium)
  - `internal/orchestrator/tick.go:646` resolves the target through `findIssueByNumber()`
  - `internal/orchestrator/tick.go:647`-`652` only edits the body when that lookup succeeds; a missing target is treated as success instead of error
  - `internal/orchestrator/tick.go:665`-`670` then closes the adjustment review and parent milestone as though the requested change had been applied
  - Observable result: the tick can print `Applied adjustments from #<n>` even though the approved modification never changed any GitHub issue
  - Classification: runtime bug; smoke coverage should include `modify` adjustments that reference a non-existent or non-loaded target
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:08:03]** Approved top-level planning application is non-atomic (High)
  - `internal/orchestrator/tick.go:499` — milestone epics are created incrementally
  - `internal/orchestrator/tick.go:519` — the seeded planning issue is created only after milestone creation has already started
  - `internal/orchestrator/tick.go:524` — review/parent closing happens after the materialization steps
  - Any later failure can leave some new milestone epics behind while the review remains open, so a retry can duplicate externally visible planning state
  - Classification: runtime bug
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 01:08:03]** Approved adjustment apply can close the milestone before planning advancement succeeds (High)
  - `internal/orchestrator/tick.go:665` — the adjustment review and parent milestone are closed before the advancement step
  - `internal/orchestrator/tick.go:673` — issue refresh happens only after both closures
  - `internal/orchestrator/tick.go:687` — seeding the next planning issue happens last and can still fail
  - A refresh or seed failure leaves the adjustment review and parent epic closed even though planning did not actually advance
  - Classification: runtime bug
  - Source: smoke-spec audit 2026-04-11

- **[2026-04-11 00:57:09]** Targeted-issue mode can dispatch an epic as if it were an implementation task (High)
  - `internal/orchestrator/tick.go:123` — the TargetIssue branch checks `issueTypeOf(*target)` before `fetchDependencies()` runs
  - `internal/orchestrator/depgraph.go:457` — defaults an unknown type to `"task"`
  - Spec explicitly says targeted epic/planning/adjustment issues must error out (`docs/plans/smoke-testing-spec.md:213`)
  - Classification: runtime bug
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Approved milestone task-planning does not actually fail closed (High)
  - `internal/orchestrator/tick.go:544` — tasks are created incrementally
  - `internal/orchestrator/tick.go:572` — dependency linking happens later
  - Any later failure leaves already-created tasks behind while the review stays open
  - Conflicts with the "fails closed" intent in `docs/plans/smoke-testing-spec.md:101`
  - Classification: system-side issue; smoke spec should also assert absence of partial materialization
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`

- **[2026-04-11 00:57:09]** Approved adjustment application is non-atomic (High)
  - `internal/orchestrator/tick.go:638` — adjustments are applied one by one
  - A later unsupported or malformed entry fails only after earlier edits or milestone creation may already have happened
  - Overstates the rejection behavior described in `docs/plans/smoke-testing-spec.md:121`
  - Classification: runtime bug; smoke coverage should include mixed valid/invalid adjustment batches
  - Source: `docs/ralph/20260411-002944-smoke-spec-audit.md`
