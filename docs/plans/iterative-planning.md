# Iterative Milestone-Gated Planning

## Status

Status: proposed

## Purpose

Replace the current waterfall planning pipeline (full plan → full issue list) with an iterative, milestone-gated workflow. The current `plan.sh` calls `plan-decomposer` once, produces all epics and tasks in a single pass, and presents them for confirmation. This has two problems:

1. **No adversarial review** — the decomposer's output goes directly to the human with zero scrutiny. Compare this to the implementation side where every diff gets a PERFECT-D review.
2. **Waterfall decomposition** — all tasks are materialized upfront with no ability to course-correct based on what's learned during implementation.

The new system introduces:
- Two-perspective adversarial review of plans (technical + product)
- Milestone-gated iteration where only the next milestone is broken into tasks
- Milestone retrospectives that can propose plan adjustments
- Planning and review conversations via GitHub issues, not terminal prompts
- A unified `runoq tick` command that advances the project by one step regardless of current phase

## Design Principles

1. **GitHub issues are the source of truth** — no local plan state. Milestones are epics, tasks are sub-issues.
2. **Planning is a task** — initial plan decomposition and per-milestone task breakdowns are themselves GitHub issues dispatched through the same workflow.
3. **Two-reviewer adversarial gate** — technical (CTO role) and product (CPO role) reviewers with orthogonal concerns.
4. **Script orchestrates, agents reason** — a deterministic script moves data between agents. No agent invokes another agent.
5. **Communication via issues** — human-in-the-loop happens on GitHub. Review, questions, partial approvals, rejections — all as issue comments.
6. **Proposals are text, materialization requires approval** — decomposer output is posted as a comment on a planning issue. Actual GitHub issues are only created after human approval.
7. **One command, one step** — `runoq tick` reads all state from GitHub and the committed config, executes exactly one step, and exits.

## Prerequisites

### P1. `set-status done` must close the issue

`internal/issuequeue/app.go` `runSetStatus()` only swaps labels. When status is `done`, it must also close the issue via `gh issue close`. This is required for "milestone epic closed = milestone done" semantics and for the tick state machine to detect milestone completion by checking open/closed state.

Work:
- After adding the `runoq:done` label in `runSetStatus`, if the target status is `done`, run `gh issue close <number> --repo <repo>`
- Update tests in `app_test.go` to verify close is called
- Update `dispatch_safety.bats` and `issue_queue.bats` if they assert on set-status behavior

Acceptance criteria — all must pass before the task is done:

```bash
# AC-P1-1: Go unit test exists and passes proving close is called on done
go test ./internal/issuequeue/... -run TestSetStatusDone -v 2>&1 | grep -q "PASS"

# AC-P1-2: The close call appears in the fake gh command log when status is "done"
# (bats test: issue_queue.bats)
# After: gh-issue-queue.sh set-status owner/repo 42 done
# The FAKE_GH_LOG must contain an "issue close" call for issue 42
grep 'issue close.*42' "$FAKE_GH_LOG"

# AC-P1-3: Non-done statuses must NOT trigger a close call
# After: gh-issue-queue.sh set-status owner/repo 42 in-progress
# The FAKE_GH_LOG must NOT contain "issue close"
! grep 'issue close' "$FAKE_GH_LOG"

# AC-P1-4: Existing set-status tests still pass (no regressions)
bats test/issue_queue.bats
bats test/runtime_issue_queue_acceptance.bats

# AC-P1-5: The close uses --repo flag (not bare issue number)
grep -n 'issue.*close.*--repo' internal/issuequeue/app.go

# AC-P1-SMOKE: On a real GitHub repo, set-status done actually closes the issue
# (verified by smoke-tick.sh E2 step 6: after marking tasks done, the issue
# is closed on GitHub)
# gh issue view <number> --repo <repo> --json state | jq -e '.state == "CLOSED"'
```

### P2. Committed project config file

The plan file path must survive a fresh checkout. `.runoq/` is gitignored (ephemeral state + secrets), so it cannot live there.

Add `runoq.json` at the target project root:

```json
{
  "plan": "docs/prd.md"
}
```

Work:
- Add `--plan <path>` flag to `setup.sh` (init). When provided, write/update `runoq.json` at target root and stage it.
- Add `runoq::plan_file()` helper to `common.sh` that reads from `runoq.json`
- `runoq plan` CLI subcommand reads plan path from `runoq.json` instead of requiring a positional argument. Positional argument still works as override.
- Update `internal/cli/app.go` `plan` case to read from config when no file argument is provided.

Acceptance criteria:

```bash
# AC-P2-1: setup.sh --plan writes runoq.json at target root
# In a temp git repo:
TARGET_ROOT="$tmpdir" "$RUNOQ_ROOT/scripts/setup.sh" --plan docs/prd.md
jq -e '.plan == "docs/prd.md"' "$tmpdir/runoq.json"

# AC-P2-2: runoq.json is NOT inside .runoq/ (must be at project root)
! test -f "$tmpdir/.runoq/runoq.json"
test -f "$tmpdir/runoq.json"

# AC-P2-3: runoq::plan_file reads the path back
source "$RUNOQ_ROOT/scripts/lib/common.sh"
result="$(TARGET_ROOT="$tmpdir" runoq::plan_file)"
[ "$result" = "docs/prd.md" ]

# AC-P2-4: runoq::plan_file dies with clear error when runoq.json is missing
! (TARGET_ROOT="$empty_tmpdir" runoq::plan_file 2>/dev/null)

# AC-P2-5: runoq plan (no file arg) reads from runoq.json
# (cli test: verify the plan case resolves planFile from config)
# Must NOT require a positional argument when runoq.json exists
cd "$tmpdir" && "$RUNOQ_ROOT/bin/runoq" plan --dry-run 2>&1
# exit 0 (not "Usage: runoq plan <file>")

# AC-P2-6: runoq.json is NOT in .gitignore
! grep -q 'runoq\.json' "$tmpdir/.gitignore"

# AC-P2-7: Bats test exists and passes
bats test/foundation.bats  # must include runoq.json structure test
```

## A. Issue Type System

### A1. Add `planning` and `adjustment` issue types

The system already supports `type: task | epic` via labels and native APIs. Extend to support `type: planning | adjustment` via `runoq:planning` and `runoq:adjustment` labels.

These types use the same metadata structure but signal different dispatch behavior to the tick script:

| Type | Dispatched how | Produces | Closes when |
|------|---------------|----------|-------------|
| `milestone` (epic) | Container only | N/A | All children closed |
| `task` | issue-runner → diff-reviewer (existing) | Code on PR | PR merged or needs-review |
| `planning` | Decomposer + reviewers → proposal comment | GitHub issues | Human approves, issues created |
| `adjustment` | Posts adjustment proposal comment | Modified/new milestones | Human approves, changes applied |

Work:
- Update metadata parser in `internal/issuequeue/app.go` to accept `planning` and `adjustment` as valid types
- Update `gh-issue-queue.sh create` (and Go runtime equivalent) to accept `--type planning` and `--type adjustment`
- Add type to `listedIssue` and `queueIssue` filtering — planning/adjustment issues are actionable but dispatched differently
- Update dispatch-safety eligibility to allow planning/adjustment types

Acceptance criteria:

```bash
# AC-A1-1: Create a planning issue via the CLI
run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create owner/repo "Plan milestone 1" "body" \
  --type planning --priority 1 --estimated-complexity low
[ "$status" -eq 0 ]
# The captured body must contain "type: planning" in the metadata block
grep 'type: planning' "$FAKE_GH_CAPTURE_DIR/0.body"

# AC-A1-2: Create an adjustment issue via the CLI
run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create owner/repo "Adjust milestones" "body" \
  --type adjustment --priority 1 --estimated-complexity low
[ "$status" -eq 0 ]
grep 'type: adjustment' "$FAKE_GH_CAPTURE_DIR/0.body"

# AC-A1-3: Label-based type detection returns planning type
# Given an issue with "runoq:planning" label,
# gh-issue-queue.sh list must return the issue with type="planning"
printf '%s' "$list_output" | jq -e '.[] | select(.number == 99) | .type == "planning"'

# AC-A1-4: Metadata parser round-trips adjustment type
printf '%s' "$list_output" | jq -e '.[] | select(.number == 100) | .type == "adjustment"'

# AC-A1-5: dispatch-safety eligibility accepts planning issues
run "$RUNOQ_ROOT/scripts/dispatch-safety.sh" eligibility owner/repo 99
[ "$status" -eq 0 ]

# AC-A1-6: Existing task and epic types still work (no regressions)
bats test/issue_queue.bats
bats test/runtime_issue_queue_acceptance.bats
bats test/dispatch_safety.bats

# AC-A1-7: Invalid types are still rejected
run "$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create owner/repo "Bad" "body" --type bogus
[ "$status" -ne 0 ]

# AC-A1-8: Go unit tests cover new types
go test ./internal/issuequeue/... -run TestCreatePlanning -v 2>&1 | grep -q "PASS"
go test ./internal/issuequeue/... -run TestCreateAdjustment -v 2>&1 | grep -q "PASS"
go test ./internal/dispatchsafety/... -run TestPlanningEligibility -v 2>&1 | grep -q "PASS"
```

### A2. Add `planApproved` label to config

Add to the existing labels block in `config/runoq.json`:

```json
{
  "labels": {
    "planApproved": "runoq:plan-approved"
  }
}
```

Work:
- Add the label to `config/runoq.json`
- `setup.sh` already iterates all labels to ensure they exist — verify this picks up the new one
- Add `runoq::config_get '.labels.planApproved'` usage in planning scripts

Acceptance criteria:

```bash
# AC-A2-1: Config has the new label
jq -e '.labels.planApproved == "runoq:plan-approved"' "$RUNOQ_ROOT/config/runoq.json"

# AC-A2-2: Foundation test validates the new key exists
bats test/foundation.bats
# The "config has required top-level keys" test must check .labels.planApproved

# AC-A2-3: runoq::all_state_labels includes the new label
source "$RUNOQ_ROOT/scripts/lib/common.sh"
runoq::all_state_labels | grep -q 'runoq:plan-approved'

# AC-A2-4: setup.sh ensure_labels creates it on a fresh repo
# (verified by live smoke — label appears in gh label list after runoq init)
```

## B. Adversarial Plan Review Agents

### B1. `plan-reviewer-technical` agent

Role: CTO / staff engineer. Reviews plan decompositions for technical soundness.

Create `.claude/agents/plan-reviewer-technical.md`. Receives JSON payload:
- `proposalPath`: path to file containing the decomposition JSON
- `planPath`: path to original plan document
- `reviewType`: `milestone` | `task` (same agent reviews both granularities)

Reviews for:
- **Feasibility** — hidden technical blockers, unrealistic scope
- **Scope** — right-sizing (too broad = unshippable, too narrow = churn)
- **Technical risk** — high-risk items identified and sequenced early
- **Dependency sanity** — acyclic deps, hidden coupling, integration risks
- **Complexity honesty** — are ratings accurate for a senior engineer?
- **KISS/YAGNI** — over-engineering, premature abstraction, unnecessary infrastructure
- **Tech debt awareness** — does the plan create unacknowledged debt?

Returns marked verdict block:
```
<!-- runoq:payload:plan-review-technical -->
REVIEW-TYPE: plan-technical
VERDICT: PASS | ITERATE
SCORE: NN/35
CHECKLIST:
- [ ] ...
```

Hard rules:
- Do NOT read source code — review from plan alone
- Do NOT create issues or modify GitHub state
- Output ONLY the verdict block

Acceptance criteria:

```bash
# AC-B1-1: Agent file exists with correct frontmatter
grep -n '^name: plan-reviewer-technical$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"

# AC-B1-2: Agent prompt contains all 7 review dimensions as literal strings
grep -n 'Feasibility' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'Scope' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'Technical risk' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'Dependency sanity' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'Complexity honesty' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'KISS/YAGNI' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'Tech debt' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"

# AC-B1-3: Agent specifies the exact payload marker
grep -n 'runoq:payload:plan-review-technical' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"

# AC-B1-4: Agent specifies the exact verdict format with REVIEW-TYPE, VERDICT, SCORE, CHECKLIST
grep -n 'REVIEW-TYPE: plan-technical' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'VERDICT: PASS | ITERATE' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'SCORE:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'CHECKLIST:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"

# AC-B1-5: Hard rules are present as literal strings
grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
grep -n 'Output ONLY the verdict block' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"

# AC-B1-6: Agent accepts reviewType field (milestone or task)
grep -n 'reviewType' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"

# AC-B1-7: Bats test validates all of the above
bats test/skills_and_prompts.bats
```

### B2. `plan-reviewer-product` agent

Role: CPO / product owner. Reviews decompositions for product alignment.

Create `.claude/agents/plan-reviewer-product.md`. Same input payload as B1.

Reviews for:
- **PRD alignment** — does decomposition cover all requirements? Does it invent scope?
- **MVP focus** — what's the minimum shippable increment? Is value front-loaded?
- **Feature scope** — bells-and-whistles detection. "Nice to have" disguised as "must have"
- **Milestone sequencing** — does order make sense for stakeholders? Can anything ship earlier?
- **Acceptance criteria quality** — written from user/product perspective, not just technical
- **Discovery awareness** — are uncertainties identified rather than assumed away?

Returns marked verdict block:
```
<!-- runoq:payload:plan-review-product -->
REVIEW-TYPE: plan-product
VERDICT: PASS | ITERATE
SCORE: NN/30
CHECKLIST:
- [ ] ...
```

Same hard rules as B1.

Acceptance criteria:

```bash
# AC-B2-1: Agent file exists with correct frontmatter
grep -n '^name: plan-reviewer-product$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"

# AC-B2-2: Agent prompt contains all 6 review dimensions
grep -n 'PRD alignment' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n 'MVP focus' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n 'Feature scope' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n 'Milestone sequencing' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n 'Acceptance criteria quality' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n 'Discovery awareness' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"

# AC-B2-3: Uses its own distinct payload marker (not technical's)
grep -n 'runoq:payload:plan-review-product' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
! grep -n 'runoq:payload:plan-review-technical' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"

# AC-B2-4: Verdict format matches (same structure, different REVIEW-TYPE)
grep -n 'REVIEW-TYPE: plan-product' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"

# AC-B2-5: Hard rules present
grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
grep -n 'Output ONLY the verdict block' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"

# AC-B2-6: Bats test validates all of the above
bats test/skills_and_prompts.bats
```

### B3. Review loop in planning script

The planning dispatch script (called by the tick state machine for planning issues) runs this loop:

1. Call decomposer agent (milestone-decomposer or task-decomposer depending on context)
2. Call `plan-reviewer-technical` and `plan-reviewer-product` in parallel
3. If both PASS → post proposal on the planning issue
4. If either returns ITERATE → merge both checklists, re-invoke decomposer with feedback
5. Max `planning.maxDecompositionRounds` iterations (default 3), then post best-effort with warnings

Work:
- Create `scripts/plan-dispatch.sh` — handles the decompose→review loop
- Called by the tick state machine when dispatching a planning or adjustment issue
- Writes proposal as issue comment with `<!-- runoq:payload:plan-proposal -->` marker
- Items in proposal are numbered for addressability in human responses
- Add `planning.maxDecompositionRounds` to config (default: 3)

Acceptance criteria:

```bash
# AC-B3-1: plan-dispatch.sh exists and is executable
test -x "$RUNOQ_ROOT/scripts/plan-dispatch.sh"

# AC-B3-2: Config has maxDecompositionRounds
jq -e '.planning.maxDecompositionRounds == 3' "$RUNOQ_ROOT/config/runoq.json"

# AC-B3-3: With both reviewers PASSing on round 1, the script invokes
# decomposer exactly once and both reviewers exactly once (3 claude calls total)
# (bats test with fake claude: count invocations in FAKE_CLAUDE_LOG)
grep -c 'milestone-decomposer\|task-decomposer' "$FAKE_CLAUDE_LOG" | grep -q '^1$'
grep -c 'plan-reviewer-technical' "$FAKE_CLAUDE_LOG" | grep -q '^1$'
grep -c 'plan-reviewer-product' "$FAKE_CLAUDE_LOG" | grep -q '^1$'

# AC-B3-4: With one reviewer ITERATEing, decomposer is called a second time
# and receives the merged checklist in its payload
grep -c 'milestone-decomposer\|task-decomposer' "$FAKE_CLAUDE_LOG" | grep -q '^2$'
# The second invocation's payload file must contain the checklist items
grep -q 'CHECKLIST' "$second_invocation_payload"

# AC-B3-5: After maxDecompositionRounds with continued ITERATE, the script
# still posts a proposal (best-effort) with a warning marker
grep -q 'runoq:payload:plan-proposal' "$FAKE_GH_CAPTURE_DIR"/*.body
grep -q 'max review rounds reached' "$FAKE_GH_CAPTURE_DIR"/*.body

# AC-B3-6: Proposal items are sequentially numbered starting from 1
grep -qE '^1\. ' "$FAKE_GH_CAPTURE_DIR"/*.body
grep -qE '^2\. ' "$FAKE_GH_CAPTURE_DIR"/*.body

# AC-B3-7: Invalid decomposer output (not JSON) does not post a proposal
# and exits non-zero
[ "$status" -ne 0 ]
! grep -q 'runoq:payload:plan-proposal' "$FAKE_GH_CAPTURE_DIR"/*.body 2>/dev/null

# AC-B3-8: Bats test covers all of the above
bats test/plan_dispatch.bats

# AC-B3-SMOKE: In the fixture-mode tick smoke (E2), the review loop runs
# as part of step 1 (bootstrap) and step 3 (task breakdown). Both steps
# must produce a valid proposal comment on the planning issue.
# Verified by: smoke-tick.sh run checking proposal comments exist on GitHub
# with the runoq:payload:plan-proposal marker.

# AC-B3-SMOKE-LIVE: In the live smoke (E3), real LLM reviewers produce
# parseable verdict blocks that the loop script can process without error.
# Verified by: smoke-planning.sh run step 1 and step 3 completing
# without parse failures in the artifacts.
```

## C. Decomposer Agent Evolution

### C1. Rename `plan-decomposer` to `milestone-decomposer`

The existing `plan-decomposer` agent becomes `milestone-decomposer`. Its output changes to produce only milestones (coarse), not tasks.

Each milestone has:
- `key`: unique slug
- `title`: short description
- `type`: `implementation` | `discovery` | `migration` | `cleanup`
- `goal`: what's true when this milestone is done
- `criteria`: integration-level success criteria
- `scope`: which subsystems/areas are touched
- `sequencing_rationale`: why this position in the order
- `priority`: numeric ordering

Does NOT produce tasks. The output is intentionally coarse.

**Discovery milestones** have criteria like "answer question X" or "determine feasibility of Y". They produce findings, not code.

Work:
- Rename `.claude/agents/plan-decomposer.md` to `milestone-decomposer.md`
- Update agent prompt to produce milestones only with the schema above
- Update payload marker to `runoq:payload:milestone-decomposer`
- Remove task-level output from the agent
- Update `plan.sh` references (though `plan.sh` will be superseded by the tick system)

Acceptance criteria:

```bash
# AC-C1-1: Old file gone, new file exists
! test -f "$RUNOQ_ROOT/.claude/agents/plan-decomposer.md"
test -f "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"

# AC-C1-2: Frontmatter is correct
grep -n '^name: milestone-decomposer$' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"

# AC-C1-3: Payload marker is updated
grep -n 'runoq:payload:milestone-decomposer' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
! grep -n 'runoq:payload:plan-decomposer' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"

# AC-C1-4: Output schema specifies ALL required milestone fields
grep -n '"key"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n '"type"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n '"goal"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n '"criteria"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n '"scope"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n '"sequencing_rationale"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n '"priority"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"

# AC-C1-5: Agent does NOT produce task-level fields
! grep -n '"estimated_complexity"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
! grep -n '"complexity_rationale"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
! grep -n '"parent_epic_key"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"

# AC-C1-6: Agent lists valid milestone types including discovery
grep -n 'implementation.*discovery.*migration.*cleanup' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"

# AC-C1-7: No stale references to plan-decomposer in any script
! grep -rn 'plan-decomposer' "$RUNOQ_ROOT/scripts/"
# (plan.sh may still exist during transition — check only new scripts)

# AC-C1-8: Hard rules preserved from original
grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"

# AC-C1-9: Bats test validates agent structure
bats test/skills_and_prompts.bats
```

### C2. Create `task-decomposer` agent

New agent: `.claude/agents/task-decomposer.md`. Scoped to a single milestone.

Receives JSON payload:
- `milestonePath`: path to file with milestone spec (goal, criteria, scope)
- `planPath`: path to original plan document
- `priorFindingsPath`: path to file with prior milestone review findings (empty on first milestone)
- `templatePath`: path to issue template

Produces the same task structure as the current `plan-decomposer` (key, type, title, body, complexity, rationale, dependencies) but only for one milestone.

Because it receives prior findings, it can incorporate concrete knowledge from completed work rather than guessing.

Work:
- Create `.claude/agents/task-decomposer.md`
- Reuse the task-level decomposition logic from current `plan-decomposer`
- Add `priorFindingsPath` to input payload
- Payload marker: `runoq:payload:task-decomposer`

Acceptance criteria:

```bash
# AC-C2-1: Agent file exists with correct frontmatter
grep -n '^name: task-decomposer$' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"

# AC-C2-2: Payload marker is distinct from milestone-decomposer
grep -n 'runoq:payload:task-decomposer' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
! grep -n 'runoq:payload:milestone-decomposer' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"

# AC-C2-3: Input payload specifies all required fields
grep -n 'milestonePath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
grep -n 'planPath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
grep -n 'priorFindingsPath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
grep -n 'templatePath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"

# AC-C2-4: Output schema specifies task-level fields
grep -n '"estimated_complexity"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
grep -n '"complexity_rationale"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
grep -n '"depends_on_keys"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"

# AC-C2-5: Agent does NOT produce milestone-level fields
! grep -n '"goal"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
! grep -n '"sequencing_rationale"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"

# AC-C2-6: Agent is scoped to a single milestone (explicit instruction)
grep -n 'single milestone' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"

# AC-C2-7: Hard rules match milestone-decomposer pattern
grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"

# AC-C2-8: Bats test validates agent structure
bats test/skills_and_prompts.bats
```

### C3. Create `milestone-reviewer` agent

New agent: `.claude/agents/milestone-reviewer.md`. Runs after all tasks in a milestone are completed.

Receives JSON payload:
- `milestonePath`: path to file with milestone spec
- `planPath`: path to original plan document
- `completedTasksPath`: path to JSON with completed task details (issue bodies, PR outcomes, review scores)
- `remainingMilestonesPath`: path to JSON with remaining milestone specs

Produces:
```json
{
  "milestone_number": 42,
  "status": "complete | partial | pivoted",
  "delivered_criteria": ["..."],
  "missed_criteria": ["..."],
  "learnings": ["..."],
  "proposed_adjustments": [
    {
      "type": "modify | new_milestone | discovery | remove",
      "target_milestone_number": 45,
      "title": "...",
      "description": "...",
      "suggested_position": "before #47",
      "reason": "..."
    }
  ]
}
```

When `proposed_adjustments` is non-empty, the tick creates an adjustment sub-issue under the current milestone epic. Because adjustment issues are children of the milestone, the milestone stays open (has incomplete children) until the human reviews and approves.

New milestones proposed by the reviewer are not created until the adjustment issue is approved.

Work:
- Create `.claude/agents/milestone-reviewer.md`
- Payload marker: `runoq:payload:milestone-reviewer`

Acceptance criteria:

```bash
# AC-C3-1: Agent file exists with correct frontmatter
grep -n '^name: milestone-reviewer$' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"

# AC-C3-2: Payload marker is correct
grep -n 'runoq:payload:milestone-reviewer' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"

# AC-C3-3: Input payload specifies all required fields
grep -n 'milestonePath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n 'planPath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n 'completedTasksPath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n 'remainingMilestonesPath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"

# AC-C3-4: Output schema documents ALL required fields
grep -n '"milestone_number"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n '"status"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n '"delivered_criteria"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n '"missed_criteria"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n '"learnings"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n '"proposed_adjustments"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"

# AC-C3-5: Adjustment types are documented
grep -n 'modify' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n 'new_milestone' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n 'discovery' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n 'remove' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"

# AC-C3-6: Hard rules — read-only, no issue creation
grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
grep -n 'Do NOT modify GitHub state' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"

# AC-C3-7: Agent explicitly states proposed_adjustments can be empty array
grep -n 'proposed_adjustments.*\[\]' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"

# AC-C3-8: Bats test validates agent structure
bats test/skills_and_prompts.bats
```

## D. Unified Tick Command

### D1. `runoq tick` CLI subcommand

Add `tick` to the CLI subcommand dispatch in `internal/cli/app.go`. Routes to a new `scripts/tick.sh`.

`runoq tick` takes no arguments. It reads all state from GitHub and the committed config, decides what to do, executes one step, and exits. Output is a single-line status message suitable for piping to notifications.

Acceptance criteria:

```bash
# AC-D1-1: CLI dispatches tick to tick.sh
grep -n '"tick"' "$RUNOQ_ROOT/internal/cli/app.go"
grep -n 'tick.sh' "$RUNOQ_ROOT/internal/cli/app.go"

# AC-D1-2: tick.sh exists and is executable
test -x "$RUNOQ_ROOT/scripts/tick.sh"

# AC-D1-3: runoq tick with no arguments does not error on usage
# (unlike runoq plan which requires a file arg)
cd "$tmpdir" && "$RUNOQ_ROOT/bin/runoq" tick --help 2>&1
# Must NOT say "requires argument"

# AC-D1-4: Go test covers the tick CLI dispatch path
go test ./internal/cli/... -run TestTick -v 2>&1 | grep -q "PASS"

# AC-D1-5: tick.sh sources common.sh and reads runoq.json
grep -n 'runoq::plan_file' "$RUNOQ_ROOT/scripts/tick.sh"
```

### D2. Tick state machine

`scripts/tick.sh` implements:

```
Read runoq.json → get planFile
Read GitHub → all issues with runoq: labels
Derive: current milestone (first open epic by priority)

State machine (checked in this order):

1. NO MILESTONE EPICS EXIST
   → Bootstrap: create "Project Planning" milestone epic
   → Create "Break plan into milestones" planning issue under it
   → Dispatch the planning issue immediately (run decompose→review loop)
   → Exit: "Created planning milestone. Proposal posted on #N"

2. PENDING REVIEW ISSUE EXISTS (planning or adjustment type,
   open, no planApproved label)
   a. If new human comments exist that haven't been responded to:
      → Run comment-responder (adapted mention-responder) to handle
        questions, partial approvals, change requests
      → Exit: "Responded to comments on #N"
   b. If no new comments:
      → Exit: "Awaiting human decision on #N"

3. APPROVED REVIEW ISSUE EXISTS (has planApproved label, still open)
   → Parse the approved proposal from issue comments
   → Create the actual GitHub issues (milestones or tasks)
   → For newly created milestones: create planning issue under the first one
   → Mark the review issue done (close it)
   → Exit: "Applied approvals from #N, created issues #M, #O, ..."

4. CURRENT MILESTONE HAS PLANNING ISSUE NOT YET DISPATCHED
   (planning type, open, no proposal comment yet)
   → Run decompose→review loop (plan-dispatch.sh)
   → Post proposal as comment on the planning issue
   → Exit: "Proposal posted on #N"

5. CURRENT MILESTONE HAS READY/IN-PROGRESS IMPLEMENTATION TASKS
   → Run dispatch-safety reconcile
   → Dispatch next implementation issue (existing orchestrator logic)
   → Exit: "Dispatched #N" or "N tasks in progress, none ready"

6. CURRENT MILESTONE ALL CHILDREN DONE (epic-status all_done)
   → Run milestone-reviewer
   → If adjustments proposed:
     Create adjustment sub-issue under the milestone
     → Exit: "Milestone #N review complete. Adjustments proposed on #M"
   → If clean:
     Close the milestone epic
     Find next milestone, create planning issue under it
     → Exit: "Milestone #N complete. Planning #M for next milestone"

7. ALL MILESTONES DONE
   → Exit: "Project complete"
```

Work:
- Create `scripts/tick.sh` implementing the state machine above
- Add `tick` case to `internal/cli/app.go` subcommand dispatch
- The tick script calls existing scripts for implementation dispatch (`run.sh` / `orchestrator.sh`) and new scripts for planning dispatch (`plan-dispatch.sh`)
- Each state check is a function that returns early, making the state machine a linear chain of checks

Acceptance criteria:

```bash
# AC-D2-1: State 1 — bootstrap from empty project
# Given: no issues exist on repo, runoq.json has valid plan path
# When: runoq tick
# Then: exactly one epic created (type: epic), exactly one planning issue
#        created (type: planning, parent_epic set), proposal comment posted
run "$RUNOQ_ROOT/scripts/tick.sh"
[ "$status" -eq 0 ]
# Output mentions issue numbers
[[ "$output" == *"Proposal posted on #"* ]]
# Fake gh log shows: issue create (epic), issue create (planning),
# issue comment (proposal)
grep -c 'issue create' "$FAKE_GH_LOG" | grep -q '^2$'
grep -q 'issue comment' "$FAKE_GH_LOG"

# AC-D2-2: State 2 — pending review, no new comments
# Given: planning issue exists, no planApproved label, no new comments
# When: runoq tick
# Then: exits 0 with "Awaiting" message, no GitHub mutations
run "$RUNOQ_ROOT/scripts/tick.sh"
[ "$status" -eq 0 ]
[[ "$output" == *"Awaiting human decision on #"* ]]
! grep -q 'issue create\|issue edit\|issue comment' "$FAKE_GH_LOG"

# AC-D2-3: State 2a — pending review with new human comment
# Given: planning issue exists with unresponded human comment
# When: runoq tick
# Then: comment-responder invoked, reply posted
[[ "$output" == *"Responded to comments on #"* ]]
grep -q 'plan-comment-responder' "$FAKE_CLAUDE_LOG"
grep -q 'issue comment' "$FAKE_GH_LOG"

# AC-D2-4: State 3 — approved review, materialize issues
# Given: planning issue has planApproved label and a proposal comment
# When: runoq tick
# Then: milestone epics or task issues created from proposal,
#        planning issue closed via set-status done
[[ "$output" == *"Applied approvals from #"* ]]
grep -q 'issue create' "$FAKE_GH_LOG"
grep -q 'set-status.*done' "$FAKE_GH_LOG" || grep -q 'issue close' "$FAKE_GH_LOG"

# AC-D2-5: State 5 — implementation dispatch delegates to orchestrator
# Given: current milestone has ready task issues
# When: runoq tick
# Then: orchestrator.sh or run.sh invoked (not plan-dispatch.sh)
[[ "$output" == *"Dispatched #"* ]]
# The orchestrator script was called
grep -q 'orchestrator\|run\.sh' "$COMMAND_LOG"

# AC-D2-6: State 6 — milestone complete, no adjustments
# Given: all children of current milestone are done, reviewer returns clean
# When: runoq tick
# Then: milestone epic closed, planning issue created under next milestone
[[ "$output" == *"Milestone #"*"complete"* ]]
grep -q 'set-status.*done' "$FAKE_GH_LOG" || grep -q 'issue close' "$FAKE_GH_LOG"
grep -q 'issue create' "$FAKE_GH_LOG"  # planning issue for next milestone

# AC-D2-7: State 6 — milestone complete with adjustments
# Given: all children done, reviewer proposes adjustments
# When: runoq tick
# Then: adjustment sub-issue created under current milestone (NOT closed)
[[ "$output" == *"Adjustments proposed on #"* ]]
# The adjustment issue has parent_epic set to current milestone
grep -q 'type: adjustment' "$FAKE_GH_CAPTURE_DIR"/*.body
grep -q 'parent_epic:' "$FAKE_GH_CAPTURE_DIR"/*.body

# AC-D2-8: State 7 — all milestones done
# Given: all milestone epics are closed/done
# When: runoq tick
# Then: exits 0 with "Project complete"
[[ "$output" == *"Project complete"* ]]
! grep -q 'issue create\|issue edit' "$FAKE_GH_LOG"

# AC-D2-9: Idempotency — running tick twice in same state = same output
output1="$("$RUNOQ_ROOT/scripts/tick.sh" 2>&1)"
output2="$("$RUNOQ_ROOT/scripts/tick.sh" 2>&1)"
[ "$output1" = "$output2" ]

# AC-D2-10: tick.sh fails with clear error when runoq.json missing
(unset TARGET_ROOT; cd "$empty_tmpdir" && run "$RUNOQ_ROOT/scripts/tick.sh")
[ "$status" -ne 0 ]
[[ "$output" == *"runoq.json"* ]]

# AC-D2-11: All state transitions covered by bats tests
bats test/tick.bats
# Must have at least 10 @test entries (one per state + idempotency + error)
test "$(grep -c '@test' "$RUNOQ_ROOT/test/tick.bats")" -ge 10

# AC-D2-SMOKE-FIXTURE: The full tick lifecycle passes as a fixture-mode
# smoke test against a real GitHub repo (E2). This is the primary
# acceptance gate — if this doesn't pass, the task is not done.
# The test exercises 10 steps including comments, partial approvals,
# rejections, adjustments, and discovery forced-pause.
"$RUNOQ_ROOT/scripts/smoke-tick.sh" run
# Must exit 0 with status: ok and zero failures

# AC-D2-SMOKE-LIVE: The tick workflow produces valid output with real
# LLM agents (E3). This is the eval gate — if agents produce outputs
# that the scripts can't parse, the integration is broken.
RUNOQ_SMOKE=1 "$RUNOQ_ROOT/scripts/smoke-planning.sh" run
# Must exit 0 with status: ok and zero failures
```

### D3. Comment handling on review issues

When the tick detects a pending review issue with new human comments, it must handle them. Reuse the `mention-responder` pattern adapted for plan context.

Behavior by comment type:
- **Question** ("why is milestone 2 before 3?") → agent answers with context from the plan, posts reply
- **Partial approval** ("approve items 1 and 2, drop 3") → agent acknowledges, updates proposal, waits for final approval label
- **Change request** ("make milestone 2 depend on 1") → re-run decomposer with feedback, update issue body with revised proposal, post diff summary as comment
- **Approval label added** → next tick materializes issues

Work:
- Create `scripts/plan-comment-handler.sh` — reads issue comments, dispatches to a comment-responder agent, posts replies
- Create `.claude/agents/plan-comment-responder.md` — adapted from `mention-responder.md` but for plan review context
- Items in proposals are numbered so humans can reference them ("approve 1, reject 3, modify 2")

Acceptance criteria:

```bash
# AC-D3-1: Script and agent files exist
test -x "$RUNOQ_ROOT/scripts/plan-comment-handler.sh"
test -f "$RUNOQ_ROOT/.claude/agents/plan-comment-responder.md"

# AC-D3-2: Agent frontmatter
grep -n '^name: plan-comment-responder$' \
  "$RUNOQ_ROOT/.claude/agents/plan-comment-responder.md"

# AC-D3-3: Agent has hard rules matching mention-responder pattern
grep -n 'NEVER.*edit.*code' "$RUNOQ_ROOT/.claude/agents/plan-comment-responder.md"
grep -n 'NEVER.*modify.*state\|NEVER.*create issues' \
  "$RUNOQ_ROOT/.claude/agents/plan-comment-responder.md"
grep -n 'runoq:event' "$RUNOQ_ROOT/.claude/agents/plan-comment-responder.md"

# AC-D3-4: plan-comment-handler.sh reads comments from the issue
# (bats test: given a fake gh scenario with comments, handler invokes agent)
grep -q 'plan-comment-responder' "$FAKE_CLAUDE_LOG"

# AC-D3-5: Handler posts reply as issue comment with audit marker
grep -q 'issue comment' "$FAKE_GH_LOG"
grep -q 'runoq:event' "$FAKE_GH_CAPTURE_DIR"/*.body

# AC-D3-6: Handler does NOT post if no new human comments exist
# (bats test: given no new comments since last runoq reply, handler is a no-op)
! grep -q 'plan-comment-responder' "$FAKE_CLAUDE_LOG"

# AC-D3-7: Bats test exists
bats test/tick.bats  # comment handling tests integrated here
```

### D4. Discovery milestone handling

Milestones with type `discovery` have special behavior:

- Their tasks produce findings (documented in issue comments or committed docs), not production code
- The milestone-reviewer pays special attention to what was learned
- After a discovery milestone completes, the system always creates an adjustment issue (even if the reviewer thinks no changes are needed) because discovery milestones exist precisely because there's uncertainty that humans should evaluate

Work:
- `milestone-decomposer` can output milestones with `type: discovery`
- `tick.sh` state 6 checks milestone type — if discovery, always create adjustment issue
- Add `planning.discoveryMilestoneAutoAdvance` to config (default: `false`)
- When `false`, discovery milestones always pause for human review via adjustment issue

Acceptance criteria:

```bash
# AC-D4-1: Config has discoveryMilestoneAutoAdvance
jq -e '.planning.discoveryMilestoneAutoAdvance == false' "$RUNOQ_ROOT/config/runoq.json"

# AC-D4-2: tick.sh discovery path — always creates adjustment issue
# Given: discovery milestone with all children done,
#        milestone-reviewer returns EMPTY proposed_adjustments
# When: runoq tick
# Then: adjustment issue STILL created (not skipped)
grep -q 'type: adjustment' "$FAKE_GH_CAPTURE_DIR"/*.body
[[ "$output" == *"Adjustments proposed on #"* ]]

# AC-D4-3: Non-discovery milestone with empty adjustments does NOT create
# adjustment issue (contrast with D4-2)
# Given: implementation milestone, all children done, reviewer returns clean
# When: runoq tick
# Then: milestone closed, no adjustment issue
[[ "$output" == *"Milestone #"*"complete"* ]]
! grep -q 'type: adjustment' "$FAKE_GH_CAPTURE_DIR"/*.body 2>/dev/null

# AC-D4-4: tick.sh reads milestone type from issue metadata to decide
grep -n 'discovery' "$RUNOQ_ROOT/scripts/tick.sh"

# AC-D4-5: Bats test covers both discovery and non-discovery paths
bats test/tick.bats
```

## E. Smoke Testing

The existing smoke testing infrastructure has three tiers:
1. **Unit/acceptance tests** (bats) — fake GitHub API via scenario files, mocked agents
2. **Fixture-mode tests** — real scripts, shell commands replacing agents, real GitHub API
3. **Live smoke tests** — real agents, real GitHub API, capture wrappers for analysis

The new planning workflow needs coverage at all three tiers. **Every milestone-level state transition must be verifiable at the fixture-mode (E2) or live smoke (E3) level.** Unit tests (E1) cover helper functions and edge cases, but the primary acceptance gate for the tick system is end-to-end smoke.

### E1. Bats tests for new script helpers and state detection

Unit tests for the new deterministic functions that don't require GitHub.

Test cases:
- `runoq::plan_file()` reads from `runoq.json`, dies when missing
- Proposal comment formatting (numbered items, markers)
- Approval parsing (extract approved items from issue comments)
- Checklist merging (combine technical + product reviewer checklists)
- Verdict block parsing (extract REVIEW-TYPE, VERDICT, SCORE, CHECKLIST from agent output)

Work:
- Add to `test/foundation.bats` and/or create `test/tick_helpers.bats`
- These are pure function tests with no GitHub API calls

Acceptance criteria:

```bash
# AC-E1-1: Tests exist and pass
bats test/tick_helpers.bats

# AC-E1-2: Checklist merge handles both empty and populated checklists
# (specific @test names to verify coverage)
grep -q 'merge.*empty' test/tick_helpers.bats
grep -q 'merge.*both' test/tick_helpers.bats

# AC-E1-3: Verdict parsing handles malformed input without crashing
grep -q 'malformed\|invalid' test/tick_helpers.bats
```

### E2. Fixture-mode tick smoke test (primary acceptance gate)

End-to-end test of the complete tick lifecycle using real GitHub API but shell commands replacing LLM agents. **This is the primary acceptance gate for the tick system.** It exercises every state transition — including comments, partial approvals, rejections, adjustments, and discovery — against a real GitHub repo with deterministic agent outputs.

The test runs fully autonomously (no human interaction). Auto-approve is always on (`RUNOQ_SMOKE_TICK=1`), but the test also injects simulated human comments and partial approvals between ticks to verify the conversation workflow.

The plan fixture (`test/fixtures/plans/progress-library-discovery.md`) includes both implementation milestones and a discovery milestone so the happy path exercises all paths.

```
Fixture sequence with assertions:

STEP 1: Bootstrap — create project planning milestone + decompose
  runoq tick (on empty project with runoq.json)
  Agent fixture: milestone-decomposer returns 3 milestones:
    1. "Core formatter" (implementation)
    2. "Caching strategy" (discovery)
    3. "CLI wrapper" (implementation)
  Agent fixture: both reviewers return PASS
  ASSERT: 1 epic on GitHub ("Project Planning", type: epic)
  ASSERT: 1 planning issue (type: planning, parent_epic = planning epic)
  ASSERT: planning issue has comment with runoq:payload:plan-proposal
  ASSERT: proposal has 3 numbered items with reviewer scores

STEP 2: Human comments on milestone plan — question + partial rejection
  Post comment on planning issue: "Why is caching before CLI? Also drop item 3,
    CLI wrapper is out of scope — we'll add it later"
  runoq tick
  Agent fixture: plan-comment-responder answers the question and acknowledges
    the rejection
  ASSERT: reply comment posted on planning issue with runoq:event marker
  ASSERT: reply references items by number (item 2, item 3)
  runoq tick → no new comments, awaiting
  ASSERT: output contains "Awaiting human decision on #N"
  Post revised comment: "OK, approved with item 3 removed"
  gh label add runoq:plan-approved on planning issue
  runoq tick
  ASSERT: only 2 milestone epics created (not 3 — item 3 was rejected)
  ASSERT: milestone 1 = "Core formatter" (implementation)
  ASSERT: milestone 2 = "Caching strategy" (discovery)
  ASSERT: planning issue under milestone 1 exists
  ASSERT: original planning epic closed

STEP 3: Milestone 1 task breakdown
  runoq tick
  Agent fixture: task-decomposer returns 2 tasks
  Agent fixture: both reviewers return PASS
  ASSERT: planning issue has proposal comment with numbered tasks

STEP 4: Human approves tasks (no modifications)
  gh label add runoq:plan-approved on planning issue
  runoq tick
  ASSERT: 2 task issues under milestone 1 with valid metadata
  ASSERT: each has type: task, parent_epic, estimated_complexity,
          complexity_rationale, runoq:ready label
  ASSERT: planning issue closed

STEP 5: Implementation dispatch
  runoq tick
  ASSERT: orchestrator/run.sh invoked (command log)
  ASSERT: output contains "Dispatched #N"
  Simulate completion: set-status done on both tasks

STEP 6: Milestone 1 complete — reviewer proposes adjustments
  runoq tick
  Agent fixture: milestone-reviewer returns proposed_adjustments:
    - modify: add "input validation" to milestone 2 scope (type: modify)
    - new: "Address tech debt from shortcuts in formatter" (type: new_milestone)
  ASSERT: adjustment sub-issue created under milestone 1 (type: adjustment)
  ASSERT: adjustment body has numbered items (1. modify ..., 2. new ...)
  ASSERT: milestone 1 epic is NOT closed (open adjustment child)

STEP 7: Human partially approves adjustments
  Post comment: "Approve item 1 (add validation scope), reject item 2
    (tech debt is fine, no new milestone needed)"
  gh label add runoq:plan-approved on adjustment issue
  runoq tick
  ASSERT: milestone 2 epic body/metadata updated with validation scope
  ASSERT: NO new milestone epic created (item 2 was rejected)
  ASSERT: adjustment issue closed
  ASSERT: milestone 1 epic now closed (all children done)
  ASSERT: planning issue created under milestone 2 (discovery)

STEP 8: Discovery milestone — task breakdown + execution
  runoq tick → task breakdown for discovery milestone
  Agent fixture: task-decomposer returns 1 task ("benchmark caching options")
  approve → runoq tick → create task
  Simulate completion: set-status done on task

STEP 9: Discovery milestone complete — forced adjustment
  runoq tick
  Agent fixture: milestone-reviewer returns EMPTY proposed_adjustments
  ASSERT: adjustment issue STILL created (discovery always pauses)
  ASSERT: adjustment body explains this is a discovery review
  gh label add runoq:plan-approved
  runoq tick
  ASSERT: milestone 2 (discovery) closed
  ASSERT: no more milestones → project complete

STEP 10: Project complete
  runoq tick
  ASSERT: output contains "Project complete"
  ASSERT: all issues on repo are closed
  ASSERT: no runoq:ready or runoq:in-progress labels remain
```

Work:
- Create `scripts/smoke-tick.sh` with `preflight`, `run`, `cleanup` subcommands
- Create `test/fixtures/tick/` directory with all required fixture files (see below)
- Create `test/fixtures/plans/progress-library-discovery.md` — extended plan with discovery milestone
- Create agent wrapper scripts that return fixture outputs based on agent name and invocation count (read from `RUNOQ_TEST_AGENT_FIXTURE_DIR`)
- The fixture sequence injects comments via `gh issue comment` between ticks
- Reuse managed-repos.json manifest and cleanup infrastructure from `smoke-common.sh`

Required fixture files:

```
test/fixtures/tick/
  milestone-decomposer-output.json          — 3 milestones (2 impl + 1 discovery)
  milestone-decomposer-revised-output.json  — 2 milestones (after item 3 rejected)
  task-decomposer-milestone-1-output.json   — 2 tasks for core formatter
  task-decomposer-milestone-2-output.json   — 1 task for discovery
  reviewer-technical-pass.txt               — PASS verdict block
  reviewer-product-pass.txt                 — PASS verdict block
  milestone-reviewer-adjustment.json        — proposes modify + new_milestone
  milestone-reviewer-clean.json             — empty adjustments (for discovery)
  comment-response-question.md              — reply to "why caching before CLI?"
  comment-response-partial-approve.md       — acknowledges rejection of item 3
  comment-response-adjustment-partial.md    — acknowledges partial adjustment approval

test/fixtures/plans/
  progress-library-discovery.md             — plan with discovery milestone
```

Acceptance criteria:

```bash
# AC-E2-1: smoke-tick.sh preflight exits 0 when prerequisites met
run "$RUNOQ_ROOT/scripts/smoke-tick.sh" preflight
[ "$status" -eq 0 ]
printf '%s' "$output" | jq -e '.ready == true'

# AC-E2-2: smoke-tick.sh run completes all 10 steps and exits 0
run "$RUNOQ_ROOT/scripts/smoke-tick.sh" run
[ "$status" -eq 0 ]
printf '%s' "$output" | jq -e '.status == "ok"'

# AC-E2-3: Summary records all 10 state transitions
printf '%s' "$output" | jq -e '.steps | length >= 10'

# AC-E2-4: Zero failures
printf '%s' "$output" | jq -e '.failures | length == 0'

# AC-E2-5: Summary records comment interactions
printf '%s' "$output" | jq -e '.comment_interactions >= 3'

# AC-E2-6: Summary records partial approvals (items rejected)
printf '%s' "$output" | jq -e '.items_rejected >= 2'

# AC-E2-7: Summary records discovery milestone forced adjustment
printf '%s' "$output" | jq -e '.discovery_forced_adjustment == true'

# AC-E2-8: All fixture files exist and are valid
for f in milestone-decomposer-output.json milestone-decomposer-revised-output.json \
         task-decomposer-milestone-1-output.json task-decomposer-milestone-2-output.json \
         milestone-reviewer-adjustment.json milestone-reviewer-clean.json; do
  jq -e . "$RUNOQ_ROOT/test/fixtures/tick/$f" >/dev/null
done
for f in reviewer-technical-pass.txt reviewer-product-pass.txt; do
  grep -q 'VERDICT: PASS' "$RUNOQ_ROOT/test/fixtures/tick/$f"
done

# AC-E2-9: Plan fixture includes discovery language
grep -qi 'discovery\|feasib\|uncertain\|determine whether' \
  "$RUNOQ_ROOT/test/fixtures/plans/progress-library-discovery.md"

# AC-E2-10: Cleanup works
run "$RUNOQ_ROOT/scripts/smoke-tick.sh" cleanup --all
[ "$status" -eq 0 ]
```

### E3. Live smoke test with real agents

Extend `smoke-planning.sh` to test the tick workflow with real LLM agents. This is the eval tier — validates that real LLM outputs are structurally valid and the deterministic scripts can process them. Uses the same progress-library-discovery plan as E2.

Runs fully autonomously with auto-approve. The script auto-approves between ticks (`RUNOQ_SMOKE_TICK=1` implies auto-approve) but also injects one simulated human comment per proposal to verify the comment-response flow with real LLM agents.

```
STEP 1: runoq tick → bootstrap + milestone decomposition (real LLM)
  EVAL: proposal comment exists on the planning issue
  EVAL: proposal parses as JSON with ≥1 milestone
  EVAL: each milestone has key, title, type, goal, criteria, scope, priority
  EVAL: at least one milestone has type "implementation"
  EVAL: at least one milestone has type "discovery"
  EVAL: reviewer scores present in proposal

STEP 2: Inject comment: "Why this milestone order?" → runoq tick
  EVAL: reply posted with runoq:event marker
  EVAL: reply is non-empty and references the plan
  Auto-approve → runoq tick → materialize milestones
  EVAL: milestone epics exist with valid metadata
  EVAL: planning issue under first milestone exists

STEP 3: runoq tick → task decomposition (real LLM)
  EVAL: proposal has ≥1 task with key, title, body, complexity, rationale
  EVAL: task bodies contain "## Acceptance Criteria"
  EVAL: complexity_rationale ≥10 chars

STEP 4: Auto-approve → runoq tick → create task issues
  EVAL: task issues exist with valid metadata, parent_epic, runoq:ready
  EVAL: dependency references resolve to real issue numbers

(Stop here — implementation dispatch is the lifecycle smoke's job)
```

Work:
- Modify `scripts/smoke-planning.sh` `run` to use tick-based workflow
- Uses `RUNOQ_SMOKE_TICK=1` (auto-approve + comment injection)
- Add eval assertion functions for new payload formats
- Add claude capture wrappers for all new agents
- Record agent invocation count and order in artifacts

Acceptance criteria:

```bash
# AC-E3-1: smoke-planning.sh run completes and exits 0
RUNOQ_SMOKE=1 RUNOQ_SMOKE_TICK=1 run "$RUNOQ_ROOT/scripts/smoke-planning.sh" run
[ "$status" -eq 0 ]
printf '%s' "$output" | jq -e '.status == "ok"'

# AC-E3-2: Summary records milestone and task counts
printf '%s' "$output" | jq -e '.planning.milestones >= 1'
printf '%s' "$output" | jq -e '.planning.tasks >= 1'

# AC-E3-3: Discovery milestone detected
printf '%s' "$output" | jq -e '.planning.has_discovery_milestone == true'

# AC-E3-4: Comment interaction recorded
printf '%s' "$output" | jq -e '.comment_interactions >= 1'

# AC-E3-5: Zero failures
printf '%s' "$output" | jq -e '.failures | length == 0'

# AC-E3-6: Agent capture artifacts exist (≥4 agent invocations)
test -d "$artifacts_dir/claude"
ls "$artifacts_dir/claude"/*/argv.txt | wc -l | grep -qE '[4-9]|[1-9][0-9]'

# AC-E3-7: All created issues have valid metadata
printf '%s' "$output" | jq -e '.checks | index("all_metadata_valid")'
```

### E4. Smoke test fixtures and plan file

Adjustment, discovery, comment handling, and partial approval are all exercised in the E2 happy path (not separate scenarios). This task covers creating the fixtures and plan file they depend on.

Work:
- Create `test/fixtures/plans/progress-library-discovery.md` — extended version of the progress-library plan with an explicit discovery milestone (e.g., "determine whether caching is needed")
- Create all fixture files listed in E2
- Create agent wrapper scripts in `test/helpers/` that read `RUNOQ_TEST_AGENT_FIXTURE_DIR` and return the right fixture based on agent name + invocation count

Acceptance criteria:

```bash
# AC-E4-1: Plan fixture includes both implementation and discovery milestones
test -f "$RUNOQ_ROOT/test/fixtures/plans/progress-library-discovery.md"
grep -qi 'discovery\|feasib\|uncertain\|determine whether' \
  "$RUNOQ_ROOT/test/fixtures/plans/progress-library-discovery.md"

# AC-E4-2: All fixture files exist and parse correctly
for f in milestone-decomposer-output.json milestone-decomposer-revised-output.json \
         task-decomposer-milestone-1-output.json task-decomposer-milestone-2-output.json \
         milestone-reviewer-adjustment.json milestone-reviewer-clean.json; do
  jq -e . "$RUNOQ_ROOT/test/fixtures/tick/$f" >/dev/null
done
for f in reviewer-technical-pass.txt reviewer-product-pass.txt; do
  grep -q 'VERDICT: PASS' "$RUNOQ_ROOT/test/fixtures/tick/$f"
done

# AC-E4-3: milestone-decomposer fixture has 3 milestones (2 impl + 1 discovery)
jq -e '.items | length == 3' "$RUNOQ_ROOT/test/fixtures/tick/milestone-decomposer-output.json"
jq -e '[.items[] | select(.type == "discovery")] | length == 1' \
  "$RUNOQ_ROOT/test/fixtures/tick/milestone-decomposer-output.json"

# AC-E4-4: Revised output has 2 milestones (item 3 removed)
jq -e '.items | length == 2' \
  "$RUNOQ_ROOT/test/fixtures/tick/milestone-decomposer-revised-output.json"

# AC-E4-5: Adjustment fixture proposes both modify and new_milestone types
jq -e '.proposed_adjustments | length >= 2' \
  "$RUNOQ_ROOT/test/fixtures/tick/milestone-reviewer-adjustment.json"
jq -e '[.proposed_adjustments[] | select(.type == "modify")] | length >= 1' \
  "$RUNOQ_ROOT/test/fixtures/tick/milestone-reviewer-adjustment.json"
jq -e '[.proposed_adjustments[] | select(.type == "new_milestone")] | length >= 1' \
  "$RUNOQ_ROOT/test/fixtures/tick/milestone-reviewer-adjustment.json"

# AC-E4-6: Clean milestone-reviewer fixture has empty adjustments
jq -e '.proposed_adjustments | length == 0' \
  "$RUNOQ_ROOT/test/fixtures/tick/milestone-reviewer-clean.json"

# AC-E4-7: Comment response fixtures exist
test -f "$RUNOQ_ROOT/test/fixtures/tick/comment-response-question.md"
test -f "$RUNOQ_ROOT/test/fixtures/tick/comment-response-partial-approve.md"
test -f "$RUNOQ_ROOT/test/fixtures/tick/comment-response-adjustment-partial.md"

# AC-E4-8: Agent fixture wrapper script exists and is executable
test -x "$RUNOQ_ROOT/test/helpers/fixture-claude"
```

### E5. Bats tests for plan-dispatch review loop

Unit tests for the decompose→review loop using fake gh and fake claude.

Test cases:
- Both reviewers PASS on round 1 → proposal posted, 3 total agent calls
- One reviewer ITERATEs → decomposer re-invoked with merged checklist, proposal posted on round 2
- Both ITERATE → merged checklist, re-invoke
- Max rounds reached → best-effort proposal with warning
- Decomposer returns invalid JSON → error, no proposal posted

Acceptance criteria:

```bash
# AC-E5-1: Bats test file exists with ≥5 test cases
test -f "$RUNOQ_ROOT/test/plan_dispatch.bats"
test "$(grep -c '@test' "$RUNOQ_ROOT/test/plan_dispatch.bats")" -ge 5

# AC-E5-2: All tests pass
bats test/plan_dispatch.bats

# AC-E5-3: Tests verify agent invocation counts
grep -q 'FAKE_CLAUDE_LOG' "$RUNOQ_ROOT/test/plan_dispatch.bats"
```

### E6. Bats tests for tick state machine transitions

Unit tests for each tick state using fake gh scenarios and fake claude.

Test cases (one @test per state):
- Bootstrap (state 1)
- Pending review, no comments (state 2b)
- Pending review with comment (state 2a)
- Approved review (state 3)
- Planning issue dispatch (state 4)
- Implementation dispatch (state 5)
- Milestone complete, clean (state 6a)
- Milestone complete, adjustments (state 6b)
- Discovery milestone complete (state 6c — D4 path)
- All milestones done (state 7)
- Idempotency (repeated tick = same output)
- Missing runoq.json (error case)

Acceptance criteria:

```bash
# AC-E6-1: Bats test file exists with ≥10 test cases
test -f "$RUNOQ_ROOT/test/tick.bats"
test "$(grep -c '@test' "$RUNOQ_ROOT/test/tick.bats")" -ge 10

# AC-E6-2: All tests pass
bats test/tick.bats

# AC-E6-3: Each state in the state machine has a corresponding test
grep -q 'bootstrap\|no.*milestone.*epic' "$RUNOQ_ROOT/test/tick.bats"
grep -q 'pending.*review\|awaiting' "$RUNOQ_ROOT/test/tick.bats"
grep -q 'approved' "$RUNOQ_ROOT/test/tick.bats"
grep -q 'implementation.*dispatch\|Dispatched' "$RUNOQ_ROOT/test/tick.bats"
grep -q 'milestone.*complete\|all.*done' "$RUNOQ_ROOT/test/tick.bats"
grep -q 'discovery' "$RUNOQ_ROOT/test/tick.bats"
grep -q 'project.*complete\|all.*milestone' "$RUNOQ_ROOT/test/tick.bats"
grep -q 'idempoten' "$RUNOQ_ROOT/test/tick.bats"
```

## F. Migration and Cleanup

### F1. Deprecate `plan.sh` direct invocation

Once the tick workflow is operational, `plan.sh` becomes an internal implementation detail called by `plan-dispatch.sh`. The `runoq plan <file>` CLI subcommand should still work for backwards compatibility but should print a deprecation notice pointing to `runoq tick`.

Work:
- Add deprecation message to `runoq plan` CLI path
- Keep `plan.sh` functional for the transition period
- Update `smoke-planning.sh` to use tick-based workflow

Acceptance criteria:

```bash
# AC-F1-1: runoq plan prints deprecation notice to stderr
cd "$tmpdir" && "$RUNOQ_ROOT/bin/runoq" plan docs/prd.md --dry-run 2>&1 | grep -qi 'deprecat'

# AC-F1-2: runoq plan still works (exits 0 with valid output)
cd "$tmpdir" && "$RUNOQ_ROOT/bin/runoq" plan docs/prd.md --dry-run
[ "$status" -eq 0 ]

# AC-F1-3: smoke-planning.sh uses tick-based workflow (not plan.sh directly)
grep -q 'tick\|runoq tick' "$RUNOQ_ROOT/scripts/smoke-planning.sh"
! grep -q 'plan\.sh.*--auto-confirm' "$RUNOQ_ROOT/scripts/smoke-planning.sh"
```

### F2. Update documentation

- Update `docs/architecture/flows.md` with the tick state machine diagram
- Update `docs/operations/operator-workflow.md` with the new workflow
- Update `docs/reference/script-contracts.md` with new scripts and contracts
- Add ADR for the iterative planning decision

Acceptance criteria:

```bash
# AC-F2-1: flows.md documents the tick state machine
grep -q 'tick' "$RUNOQ_ROOT/docs/architecture/flows.md"
grep -q 'milestone' "$RUNOQ_ROOT/docs/architecture/flows.md"

# AC-F2-2: script-contracts.md documents all new scripts
grep -q 'tick.sh' "$RUNOQ_ROOT/docs/reference/script-contracts.md"
grep -q 'plan-dispatch.sh' "$RUNOQ_ROOT/docs/reference/script-contracts.md"
grep -q 'plan-comment-handler.sh' "$RUNOQ_ROOT/docs/reference/script-contracts.md"

# AC-F2-3: operator-workflow.md documents the tick workflow
grep -q 'runoq tick' "$RUNOQ_ROOT/docs/operations/operator-workflow.md"

# AC-F2-4: ADR exists
ls "$RUNOQ_ROOT/docs/adr/"*iterative-planning* 2>/dev/null | grep -q .
```

## Implementation Order

The sections are ordered for incremental delivery. **Smoke tests are written alongside features, not after.** Each milestone must have its smoke coverage before the milestone is considered done.

1. **P1, P2** — prerequisites. Can be done immediately, independently useful. Verified by existing bats tests + the fixture-mode smoke will exercise P1 (issue close on done) end-to-end later.
2. **A1, A2** — type system extensions. Small, foundational. Verified by bats tests.
3. **B1, B2** — reviewer agents. Verified by `skills_and_prompts.bats` structural tests.
4. **C1, C2, C3** — decomposer split + milestone reviewer. Verified by `skills_and_prompts.bats`.
5. **B3** — review loop script. Verified by `plan_dispatch.bats`.
6. **D1, D2** — tick command and state machine. Verified by `tick.bats` unit tests.
7. **E1, E4, E5, E6** — helper unit tests, fixtures, and tick state bats tests. Must be green before proceeding.
8. **E2** — fixture-mode tick smoke test. **This is the primary integration gate.** Must pass the full 10-step lifecycle (including comments, partial approvals, adjustments, discovery) against a real GitHub repo before D is considered done.
9. **D3** — comment handling. Already exercised by E2 steps 2 and 7 — bats unit tests in `tick.bats` complement the smoke.
10. **D4** — discovery milestones. Already exercised by E2 step 9 — bats unit tests complement.
11. **E3** — live smoke with real agents. Written after E2 is green. Validates agent outputs are structurally correct.
13. **F1, F2** — cleanup and docs. After all smokes pass.

## Config Changes Summary

Additions to `config/runoq.json`:

```json
{
  "labels": {
    "planApproved": "runoq:plan-approved"
  },
  "planning": {
    "maxDecompositionRounds": 3,
    "discoveryMilestoneAutoAdvance": false
  }
}
```

New committed file at target project root:

```json
// runoq.json
{
  "plan": "docs/prd.md"
}
```

## New Files Summary

| File | Type | Purpose |
|------|------|---------|
| `.claude/agents/milestone-decomposer.md` | agent | Breaks plan into milestones (renamed from plan-decomposer) |
| `.claude/agents/task-decomposer.md` | agent | Breaks one milestone into tasks |
| `.claude/agents/plan-reviewer-technical.md` | agent | CTO-role technical review of plans |
| `.claude/agents/plan-reviewer-product.md` | agent | CPO-role product review of plans |
| `.claude/agents/milestone-reviewer.md` | agent | Retrospective after milestone completion |
| `.claude/agents/plan-comment-responder.md` | agent | Handles human comments on review issues |
| `scripts/tick.sh` | script | Tick state machine |
| `scripts/plan-dispatch.sh` | script | Decompose→review loop |
| `scripts/plan-comment-handler.sh` | script | Comment handling on review issues |
| `scripts/smoke-tick.sh` | script | Fixture-mode tick smoke test |
| `test/tick.bats` | test | Unit tests for tick state machine |
| `test/plan_dispatch.bats` | test | Unit tests for review loop |
| `test/tick_helpers.bats` | test | Unit tests for helper functions |
| `test/fixtures/tick/` | fixtures | Agent output fixtures for tick tests |
| `test/fixtures/plans/discovery-plan.md` | fixture | Plan with explicit discovery requirement |
