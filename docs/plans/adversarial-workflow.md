# Adversarial Workflow Implementation Plan

## Overview

Introduce an adversarial-style development workflow where a **bar-setter** agent writes acceptance tests from the specification before implementation begins, and the implementer (codex) must satisfy those tests without modifying them. The workflow supports hierarchical planning with **epics** (milestones) containing **tasks**, with acceptance criteria gated on complexity.

Additionally, the orchestrator and issue-runner are demoted from LLM agents to deterministic shell scripts, and a mention-triage + response workflow is added for human interaction on PRs.

## Core Concepts

### Epic
A GitHub issue with `type: epic` in its metadata. Represents a feature or milestone. Has child tasks. Gets integration-level acceptance criteria from bar-setter. Has its own INTEGRATE verification after all children complete.

### Task
A GitHub issue with `type: task` and a `parent_epic` reference. Gets bar-setter criteria if `estimated_complexity` is medium or higher. Low-complexity tasks skip the CRITERIA phase entirely.

### Criteria Commit
A commit authored by bar-setter containing test files and/or acceptance specs. Its SHA is recorded in the dispatch payload. `verify.sh` enforces that files present in that commit are unchanged at HEAD. Codex is expected to add its own tests alongside — the criteria are the floor, not the ceiling.

### Tamper Check
For each file in the criteria commit's diff, assert it is byte-identical at HEAD. Purely commit-based, not path-based. If codex modifies any criteria file, `verify.sh` fails with `criteria tampered: <files>` and feeds it back as a normal verification failure.

## Components

| Component | Type | Model | Purpose |
|-----------|------|-------|---------|
| **orchestrator** | shell script + haiku call for mention triage | haiku (classification only) | Dispatch phases, mediate handoffs, apply decision table, detect and route PR mentions |
| **issue-runner** | shell script | none | Drive codex rounds, track token budget, call verify.sh, package payloads |
| **bar-setter** | agent | opus | Read spec, write acceptance tests, commit them. CRITERIA phase only. |
| **mention-responder** | agent | sonnet | Answer freeform human questions on PRs |
| **diff-reviewer** | agent | opus | PERFECT-D code quality review on diffs |
| **maintenance-reviewer** | agent | opus | Periodic code health review (unchanged) |
| **codex** | external | operator-configured | Implementation |
| **verify.sh round** | shell subcommand | none | Existing verification + new criteria tamper check |
| **verify.sh integrate** | shell subcommand (new) | none | Run epic-level criteria tests against integrated codebase |

## Phase Flow: Task Within an Epic

```
INIT
│  orchestrator creates worktree, draft PR, state breadcrumb
▼
CRITERIA  (skipped if estimated_complexity == low)
│  orchestrator spawns bar-setter with:
│    { issueNumber, specPath, epicCriteria (if child task),
│      worktree, branch, guidelines }
│
│  bar-setter:
│    1. Reads task spec (and parent epic criteria if applicable)
│    2. Writes test files and/or acceptance specs to worktree
│    3. Commits them
│    4. Returns: { criteria_commit: "<sha>", criteria_files: [...], summary: "..." }
│
│  orchestrator records criteria_commit in state and payload
│  posts criteria summary to PR as audit comment
▼
DEVELOP  (existing loop, one new input)
│  orchestrator invokes issue-runner script with existing payload +
│    { criteria_commit: "<sha>" }
│
│  issue-runner drives codex:
│    1. Record baseline git hash
│    2. Invoke codex with spec or previous checklist
│    3. Capture and validate codex payload via state.sh
│    4. Track cumulative tokens
│    5. Call verify.sh round (which now includes tamper check)
│    6. If verification fails: feed failures back to codex, next round
│    7. If verification passes: grep for related files, return review_ready payload
│
│  verify.sh round (extended with one new check):
│    - All existing checks
│    - NEW: for each file in criteria_commit's diff, assert unchanged at HEAD
│    - If criteria files modified: fail with "criteria tampered: <files>"
│
│  On pass: issue-runner returns review_ready payload
│  On max rounds exhausted: escalate with criteria_commit in payload
▼
REVIEW  (unchanged)
│  orchestrator spawns diff-reviewer
│  PERFECT-D review on the diff
│  Returns PASS or ITERATE with checklist
▼
DECIDE  (unchanged)
│  PASS → finalize
│  ITERATE → back to DEVELOP with checklist
│  Max rounds exhausted → needs-review, payload includes criteria_commit
```

When `estimated_complexity == low`, the flow is: INIT → DEVELOP → REVIEW → DECIDE (identical to today).

## Phase Flow: Epic Completion

```
All child tasks reach DONE
▼
INTEGRATE
│  orchestrator detects all children of epic are done
│  creates integration worktree from main (with all child PRs merged)
│  calls: verify.sh integrate <worktree> <criteria_commit>
│    1. Confirms criteria files from epic-level criteria_commit are unchanged
│    2. Runs the test suite
│    3. Returns: { ok: true/false, failures: [...] }
│
│  If ok: epic marked done
│  If not: create fix task under the epic, back to queue
```

## Mention Triage and Response Flow

```
orchestrator polls for @runoq mentions (poll-mentions)
▼
For each unprocessed mention:
│  orchestrator calls haiku with structured-output prompt:
│    classify as: question | change-request | approval | irrelevant
▼
│  question → spawn mention-responder (sonnet) to answer
│  change-request → extract checklist, feed into DEVELOP loop
│  approval → script handles (label change, merge)
│  irrelevant → mark processed, no action
```

## Issue Metadata Extensions

```
<!-- runoq:meta
type: epic
parent_epic: null
children: [43, 44, 45]
depends_on: []
priority: 1
estimated_complexity: high
-->
```

New fields: `type` (epic|task, default task), `parent_epic` (issue number or null), `children` (array of issue numbers).

## Changes to Existing Components

### gh-issue-queue.sh
- Parse `type`, `parent_epic`, `children` from metadata
- Epic completion detection: check if all children have `runoq:done` label
- `next` skips epics (only tasks get dispatched directly)
- `create` accepts `--type`, `--parent-epic`, `--children` flags

### state.sh
- Support `criteria_commit` field in state files
- Add CRITERIA and INTEGRATE to valid phase transitions:
  - INIT → CRITERIA
  - CRITERIA → DEVELOP | FAILED
  - INTEGRATE → DONE | FAILED

### verify.sh
- `round` subcommand: new criteria tamper check (gated on criteria_commit presence in payload)
- New `integrate` subcommand: run criteria tests from epic-level criteria_commit

### plan-to-issues skill
- Identify epic-sized chunks vs. individual tasks
- Create epics first, then child tasks with parent_epic and type
- Set estimated_complexity on each task
- Show epic/task hierarchy in confirmation UX

### issue template
- Add type, parent_epic, children fields

### config/runoq.json
- No structural changes needed (complexity thresholds use existing estimated_complexity field)

## New Components

### bar-setter agent (.claude/agents/bar-setter.md)
- Model: opus
- Tools: Read (spec, guidelines, config), Write (test files), Bash (git commit)
- Forbidden: reading production source code
- Input: issue spec, epic criteria (for child tasks), worktree, branch
- Output: { criteria_commit, criteria_files, summary }

### mention-responder agent (.claude/agents/mention-responder.md)
- Model: sonnet
- Tools: Read (PR diff, issue spec, conversation), Bash (git), pr-lifecycle skill
- Forbidden: editing code, modifying PR state
- Posts reply with `<!-- runoq:event -->` audit marker

### orchestrator script (scripts/orchestrator.sh)
- Shell state machine replacing github-orchestrator agent
- Phases: INIT → CRITERIA → DEVELOP → REVIEW → DECIDE → FINALIZE
- Haiku classification call for mention triage
- Circuit breaker, decision table, audit comments

### issue-runner script (scripts/issue-runner.sh)
- Shell loop replacing issue-runner agent
- codex → validate → verify → iterate/return
- Passes criteria_commit to verify.sh

### verify.sh integrate subcommand
- Input: worktree, criteria_commit
- Confirms criteria files unchanged, runs test suite
- Returns { ok, failures }

## Smoke Lifecycle Test Updates

### New Fixture Design

Epic: "Progress tracking library with output formatters and CLI"

| Key | Title | Complexity | Bar-setter? | Depends on |
|-----|-------|-----------|-------------|------------|
| core-formatter | Add formatted progress output with multiple styles | medium | yes | — |
| clamp-overflow | Clamp overflowing progress values | low | skip | core-formatter |
| cli-wrapper | Add CLI wrapper with argument validation | medium | yes | clamp-overflow |

Plus the epic issue itself with `type: epic` and `children` list.

### Comment/Response Smoke Coverage

After first task's PR is created:
1. Post a question comment tagging @runoq
2. Post a change-request comment
3. Post an irrelevant comment (no tag)

Assert:
- mention-responder replied to question with audit marker
- change-request routed into develop loop
- irrelevant comment not processed

### New Summary Assertions

| Assertion | What it checks |
|-----------|---------------|
| criteria_phases_run == 2 | Bar-setter ran for medium tasks |
| criteria_phases_skipped == 1 | Skipped for low task |
| criteria_commits_recorded == 2 | State files have criteria_commit SHAs |
| criteria_tamper_violations == 0 | Tamper check passed |
| epic_count == 1 | One epic tracked |
| integration_gates_passed == 1 | verify.sh integrate succeeded |
| mentions.processed == 3 | All comments detected |
| mentions.questions_answered == 1 | Responder replied |
| mentions.change_requests_routed == 1 | Change request entered develop loop |
| mentions.irrelevant_skipped == 1 | Irrelevant comment ignored |

## Implementation Order

```
Phase 1 (parallel):
  1. gh-issue-queue.sh extensions
  2. state.sh extensions
  3. verify.sh extensions
  4. bar-setter agent definition
  5. mention-responder agent definition
  6. Issue template + config updates

Phase 2 (depends on Phase 1):
  7. orchestrator.sh (new script)
  8. issue-runner.sh (new script)
  9. plan-to-issues skill update

Phase 3 (depends on Phase 2):
  10. Smoke fixtures and assertions
  11. Documentation updates

Phase 4 (depends on Phase 3):
  12. Run smoke tests, iterate until passing
```
