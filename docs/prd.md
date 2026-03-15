# agendev: GitHub-Integrated Agentic Development Orchestration

## Problem Statement

The current agentic development workflow (proven in sandboxbouldering/accounts) uses local markdown spec files (SPEC-WI-*) for work items and local log files for observability. This works for solo development but breaks down in several ways:

- **Visibility**: No way for collaborators to see what's being worked on, what's been reviewed, or what needs human attention.
- **Auditability**: Logs live on-disk and are not tied to the code changes they describe.
- **Integration**: Work items are disconnected from the codebase's collaboration platform (GitHub).
- **Resumability**: If the process crashes, restarting requires manual triage to figure out where things left off.

## Design Philosophy

The existing orchestrator and project-orchestrator agents are good at their job. The gap is not in decision-making — it's in the I/O layer (local files vs. GitHub). The strategy is:

1. **Replace the I/O, keep the intelligence.** Swap local spec files for GitHub Issues, swap local logs for PR comments, keep agent-driven orchestration.
2. **Add just enough deterministic scaffolding** — a state file for crash recovery, a watchdog for stall detection, a reconciliation step on startup — without replacing agent judgment with a rigid state machine.
3. **Validate the workflow before hardening it.** Get end-to-end working first, discover what the right workflow actually is, then decide what (if anything) needs to move into deterministic code.

## Target Workflow

### Phase 1: Planning (human-driven)

The user does local planning and thinking — in conversation with Claude Desktop, Claude Code, or on paper. When ready, they instruct their LLM to slice the plan into GitHub Issues:

```
"use plan-to-issues skill on docs/my-plan.md"
```

The skill creates issues with structured metadata (dependencies, priority, acceptance criteria) and the `agendev:ready` label.

### Phase 2: Execution (agent-driven)

The user starts the github-orchestrator agent:

```
claude --add-dir /path/to/agendev --agent github-orchestrator "run"
```

For each issue in the queue:

1. **Pick**: Pull next actionable issue (dependencies satisfied, not blocked).
2. **Branch**: Create feature branch `agendev/<issue>-<slug>` from main.
3. **PR**: Create draft pull request linked to the issue.
4. **Develop**: Dispatch to orchestrator agent — the existing dev-review loop.
5. **Push & Report**: The implementing agent (Codex) pushes its commits when done with a dev round. After each review round, orchestrator posts review results as a PR comment (round number, scorecard, checklist).
6. **Iterate**: Orchestrator feeds review checklist back to dev agent. Repeat until PASS or max rounds.
7. **Finalize**: Update PR description with work summary and areas needing human attention. Mark PR ready for review.
8. **Merge or Assign**: If orchestrator has high confidence (diff-review passed cleanly, zero critical issues, CI green) — enable auto-merge. Otherwise, assign human reviewer and leave PR open.
9. **Update Issue**: Label as `agendev:done` or `agendev:needs-human-review`.
10. **Next**: Return to step 1.

### Phase 3: Human Review (when needed)

PRs flagged `agendev:needs-human-review` have:
- Full review history in PR comments
- Summary of work done in PR description
- Specific areas flagged for attention
- A human reviewer assigned

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                     Claude Code                              │
│                                                              │
│  ┌──────────────────────┐     ┌───────────────────────────┐  │
│  │  github-orchestrator │     │  orchestrator             │  │
│  │  (agent)             │     │  (agent, modified)        │  │
│  │                      │     │                           │  │
│  │  Uses skills:        │     │  Uses skills:             │  │
│  │  • issue-queue       │     │  • pr-lifecycle           │  │
│  │                      │     │  • diff-review            │  │
│  │  Dispatches to:      │     │                           │  │
│  │  orchestrator ───────┼────▶│                           │  │
│  └──────────────────────┘     │  Spawns:                  │  │
│                               │  • Codex (dev work)       │  │
│                               └───────────────────────────┘  │
│                                                              │
│  ┌──────────────────────────────────────────────────────────┐│
│  │                    Skills Layer                           ││
│  │  ┌─────────────┐ ┌──────────────┐ ┌──────────────────┐  ││
│  │  │ issue-queue  │ │ pr-lifecycle │ │ diff-review      │  ││
│  │  │ (new)        │ │ (new)        │ │ (existing)       │  ││
│  │  └──────┬───────┘ └──────┬───────┘ └──────────────────┘  ││
│  └─────────┼────────────────┼───────────────────────────────┘│
│            │                │                                │
│  ┌─────────▼────────────────▼───────────────────────────────┐│
│  │              GitHub Utility Scripts                       ││
│  │  gh-issue-queue.sh  │  gh-pr-lifecycle.sh                ││
│  └──────────────────────────────────────────────────────────┘│
│                                                              │
│  ┌──────────────────────────────────────────────────────────┐│
│  │              State & Safety Layer                        ││
│  │  state.sh (breadcrumb file)  │  watchdog.sh (stall det) ││
│  └──────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
         │                    │
         ▼                    ▼
   GitHub API (via gh)   Target Project Codebase
```

## Components

### 1. GitHub Utility Scripts

Small, focused shell scripts wrapping `gh` CLI operations. Each script does one thing, takes arguments, returns structured JSON output.

**Location:** `scripts/`

**Why shell scripts:**
- `gh` CLI handles auth, pagination, rate limiting already.
- No build step, no dependencies beyond `gh` and `jq`.
- Easy to test manually: `./scripts/gh-issue-queue.sh list owner/repo agendev:ready | jq .`
- Portable — agents invoke them from any project.

#### `gh-issue-queue.sh`

```bash
# Subcommands:
gh-issue-queue.sh list <repo> <ready-label>
# Returns: JSON array of issues with parsed metadata (dependencies, priority)

gh-issue-queue.sh next <repo> <ready-label>
# Returns: JSON of the next actionable issue (dependencies satisfied)

gh-issue-queue.sh set-status <repo> <issue-number> <status>
# Removes old agendev:* labels, adds new one

gh-issue-queue.sh create <repo> <title> <body> [--depends-on N,M]
# Creates issue with agendev:ready label and metadata block
```

**Dependency resolution:**
- Parse `<!-- agendev:meta ... -->` block from issue body.
- Check each dependency's current labels — if all are `agendev:done`, the issue is actionable.
- Return issues sorted by: explicit priority > issue number (FIFO).

#### `gh-pr-lifecycle.sh`

```bash
gh-pr-lifecycle.sh create <repo> <branch> <issue-number> <title>
# Creates draft PR linked to issue, returns PR number and URL

gh-pr-lifecycle.sh comment <repo> <pr-number> <comment-body-file>
# Posts comment from file contents

gh-pr-lifecycle.sh update-summary <repo> <pr-number> <summary-file>
# Updates PR body's summary section (between markers)

gh-pr-lifecycle.sh finalize <repo> <pr-number> <verdict> [--reviewer <username>]
# verdict=auto-merge: mark ready, enable auto-merge
# verdict=needs-review: mark ready, request review, assign reviewer

gh-pr-lifecycle.sh line-comment <repo> <pr-number> <file> <start-line> <end-line> <body>
# Post a review comment on a line or line range (start-line == end-line for single line)
# Uses GitHub API's start_line + line parameters for multi-line comments
```

### 2. Git Worktree Isolation

Each issue is developed in its own git worktree, ensuring agents never interfere with each other or with the user's main working tree. This is a hard safety invariant — not optional.

**Worktree lifecycle:**

```bash
# Create worktree for issue 42 on its feature branch
git worktree add ../agendev-wt-42 -b agendev/42-fix-auth-refresh main

# Agent works entirely within ../agendev-wt-42/
# All commits, pushes happen from there

# On completion (success or failure), clean up
git worktree remove ../agendev-wt-42
```

**Rules:**
- **One worktree per issue.** Worktree path: `../<worktree-prefix><issue-number>` (e.g., `../agendev-wt-42`). The prefix is configurable.
- **Agent CWD must be the worktree path.** The implementing agent (Codex) and the orchestrator both operate from within the worktree. They must never `cd` out of it.
- **Worktree path must be outside the main repo.** This prevents any accidental cross-contamination. Sibling directory to the project root.
- **The main working tree stays clean.** The github-orchestrator creates and removes worktrees but does not modify files in the main tree (except `.agendev/state/` breadcrumbs).
- **Worktree cleanup on failure.** If an agent crashes, the worktree persists for debugging. Startup reconciliation checks for orphaned worktrees and either resumes or cleans them up.
- **Parallel execution.** Because each issue gets its own worktree, multiple orchestrator instances can run different issues concurrently without conflicts. Each worktree has its own branch, index, and working directory. The only shared resource is the `.agendev/state/` directory (keyed by issue number, no conflicts).

**Worktree state is tracked in the breadcrumb file:**
```json
{
  "worktree": "../agendev-wt-42",
  ...
}
```

### 3. Operational Audit Trail

**Every operational event that affects an issue or PR must be recorded as a comment on the relevant PR (or on the issue if no PR exists yet).** This is a hard rule — local-only logging is insufficient. The GitHub comment history is the audit trail.

Events that must be commented:

| Event | Comment on | Content |
|-------|-----------|---------|
| Issue picked up for development | PR (after creation) | "Started development. Branch: `agendev/42-...`, Round 1." |
| Dev round completed | PR | "Development round N complete. Commits: `abc..def`." |
| Diff-review result | PR | Round number, verdict, checklist (existing behavior). |
| Agent stalled (watchdog killed) | PR + Issue | "Agent stalled after N minutes of inactivity. Process terminated. State preserved for resume." |
| Agent crashed / non-zero exit | PR + Issue | "Agent exited unexpectedly (exit code N). Last phase: DEVELOP, round 2." |
| Max rounds exhausted | PR | "Reached maximum iteration rounds (N). Final verdict: ITERATE. Escalating to human review." |
| Rebase attempted | PR | "Rebased onto main (`sha`). Result: success/conflict." |
| Rebase conflict → needs-review | PR + Issue | "Rebase conflict detected. Files: [...]. Marking for human review." |
| Startup reconciliation: orphan found | PR + Issue | "Detected interrupted run from [timestamp]. Previous phase: REVIEW round 2. Resuming / marking for review." |
| Startup reconciliation: stale label fixed | Issue | "Found stale `agendev:in-progress` label with no active run. Reset to `agendev:ready`." |
| Issue marked as blocked | Issue | "Marked as blocked. Dependency #N has status: needs-human-review." |
| Dispatch eligibility skip | Issue | "Skipped: [reason — e.g., 'missing acceptance criteria', 'branch has unresolved conflicts']." |
| Worktree cleanup | PR | "Worktree `../agendev-wt-42` removed." |
| PR finalized (auto-merge) | PR | "All checks passed. Auto-merge enabled." |
| PR finalized (needs-review) | PR | "Assigned to @reviewer for human review. Reason: [caveats/failure/max rounds]." |

**Format:** Comments are prefixed with `<!-- agendev:event -->` so they can be distinguished from human comments and parsed programmatically if needed.

**Failure to comment is not fatal.** If the `gh` call to post a comment fails (rate limit, network), log locally and continue. The comment is best-effort — the operation itself should not be blocked by a failed audit write.

### 4. State & Safety Layer

#### State File (breadcrumb)

Before each phase transition, the orchestrator writes a JSON breadcrumb to `.agendev/state/<issue-number>.json`:

```json
{
  "issue": 42,
  "pr": 87,
  "branch": "agendev/42-fix-auth-refresh",
  "phase": "REVIEW",
  "round": 2,
  "rounds": [
    { "round": 1, "score": 28, "verdict": "ITERATE", "commit_range": "abc..def" }
  ],
  "started_at": "2026-03-15T10:00:00Z",
  "updated_at": "2026-03-15T10:45:00Z",
  "main_sha": "abc123"
}
```

**Phases:** `INIT`, `DEVELOP`, `REVIEW`, `DECIDE`, `FINALIZE`, `DONE`, `FAILED`

On crash/restart, the agent reads the state file *first*, then cross-references GitHub state (PR exists? labels correct? last comment?) to determine where to resume. The state file is the primary recovery signal; GitHub state is the validation.

Implementation: a small `state.sh` script with `save` and `load` subcommands, writing atomically (write to temp + rename).

#### Stall Detection (watchdog)

A wrapper that monitors agent subprocess activity and kills runs that go silent.

```bash
# Wrap an agent invocation with a stall timeout
watchdog.sh --timeout 600 -- claude --agent orchestrator ...
```

**Behavior:**
- Monitors stdout/stderr of the wrapped process.
- If no output for `--timeout` seconds (default: 600 = 10 minutes), kill the process.
- Write a stall marker to the state file so the orchestrator knows this was a stall (not a clean failure).
- Exit with a distinct exit code (e.g., 124, matching `timeout(1)` convention).
- The github-orchestrator posts a comment on the PR and issue when it detects a stall (see Operational Audit Trail).

#### Startup Reconciliation

When the github-orchestrator starts (`run`), before pulling the queue:

1. **Check for orphaned state files**: Any `.agendev/state/*.json` with phase != DONE/FAILED? These are interrupted runs.
2. **Cross-reference GitHub**: For each orphaned state file, check: Does the PR exist? What labels are on the issue? Is the branch pushed?
3. **Decide**: Resume (if state is recoverable), or mark as `needs-human-review`. **Post a comment** on the PR and issue explaining what was found and what action was taken.
4. **Check for stale in-progress issues**: Any issues labeled `agendev:in-progress` with no corresponding state file? Reset to `agendev:ready` and **post a comment** on the issue explaining the reset.

#### Dispatch Eligibility

Before picking up an issue, the orchestrator runs through explicit checks:

1. Issue has required fields (title, body with acceptance criteria).
2. All dependencies (from `<!-- agendev:meta -->`) are resolved (`agendev:done`).
3. No existing `agendev:in-progress` PR for this issue.
4. Feature branch doesn't already exist with unresolved conflicts.
5. Global concurrency limit not exceeded (if running multiple orchestrators — future consideration).

If any check fails, skip the issue with a **comment on the issue** explaining why it was skipped, and move to the next.

### 5. Issue Queue Skill

**Location:** `.claude/skills/issue-queue/SKILL.md` (in agendev repo, loaded via `--add-dir`)

Provides the github-orchestrator agent with structured access to the issue queue. Translates between the agent's needs and the `gh-issue-queue.sh` script.

**Actions:** `list`, `next`, `set-status`, `create`

**Issue conventions:**
- Labels encode state: `agendev:ready`, `agendev:in-progress`, `agendev:done`, `agendev:blocked`, `agendev:needs-human-review`
- Issue body contains a structured metadata section:
  ```markdown
  <!-- agendev:meta
  depends_on: [12, 14]
  priority: 1
  estimated_complexity: medium
  -->
  ```
- Human-readable description, acceptance criteria, and context occupy the rest of the body.

**State transitions:**
```
ready → in-progress → done
                    → needs-human-review
                    → blocked (dependency failed)
```

### 6. PR Lifecycle Skill

**Location:** `.claude/skills/pr-lifecycle/SKILL.md` (in agendev repo)

Gives the orchestrator agent structured PR management capabilities via `gh-pr-lifecycle.sh`.

**Actions:** `create`, `post-review`, `update-summary`, `finalize`

**PR conventions:**
- Branch naming: `agendev/<issue-number>-<short-slug>`
- Draft until orchestrator is satisfied.
- PR body structure:
  ```markdown
  ## Summary
  <!-- agendev:summary -->
  [filled by orchestrator on completion]

  ## Linked Issue
  Closes #42

  ## Review Rounds
  | Round | Score | Verdict | Link |
  |-------|-------|---------|------|
  | 1     | 28/40 | ITERATE | [comment](#) |
  | 2     | 36/40 | ITERATE | [comment](#) |
  | 3     | 40/40 | PASS    | [comment](#) |

  ## Areas for Human Attention
  <!-- agendev:attention -->
  [filled by orchestrator on completion]
  ```

### 7. Plan-to-Issues Skill

**Location:** `.claude/skills/plan-to-issues/SKILL.md` (in agendev repo)

Helps the user slice a local plan into GitHub Issues. Entry point for new work.

**Process:**
1. Read the plan document.
2. Identify discrete work items — each independently implementable, small enough for a single PR (< 500 lines ideal), testable in isolation.
3. Draft: title, description, acceptance criteria, dependencies, estimated complexity.
4. Present proposed issues for user confirmation before creating.
5. Create issues via issue-queue skill.
6. Display the full queue with dependency graph.

### 8. GitHub Orchestrator Agent

**Location:** `.claude/agents/github-orchestrator.md` (in agendev repo)

Replaces `project-orchestrator.md`. Project-level dispatcher that pulls work from GitHub Issues instead of local spec files.

**Role:** Dispatch only. Never reads or modifies source code.

**Startup sequence:**
1. Read centralized agendev configuration.
2. Run startup reconciliation (check orphaned state, stale labels).
3. Fetch and display the full issue queue.

**Dispatch loop:**
1. Get next actionable issue (via issue-queue skill + dispatch eligibility checks).
2. If no actionable issue: report status (empty vs. blocked) and stop.
3. Mark issue as `in-progress`.
4. Create feature branch from main.
5. Create draft PR (via pr-lifecycle skill).
6. Write initial state breadcrumb.
7. Dispatch to orchestrator agent (via Task) with: issue details, branch, PR number, config.
   The orchestrator reads AGENTS.md files itself from the project root — no need to pass them downstream.
8. Wait for completion. Parse result.
9. Based on result:
   - Clean PASS (diff-review passed, zero critical issues) → finalize with auto-merge.
   - PASS with caveats → finalize with needs-review.
   - FAIL → finalize with needs-review, comment explaining failure.
10. Update issue status.
11. Update state breadcrumb to DONE.
12. `git checkout main && git pull`.
13. Continue to next issue.

**Decision-making guidelines (for the agent):**
- If an issue's dependencies have failed, mark as blocked, **comment on the issue** with the blocking dependency, and skip. Don't ask the user.
- If the orchestrator fails on an issue, don't retry. Mark as needs-review, **comment on the PR and issue** with failure details, and continue.
- If main has diverged, rebase the branch before the next dev round. **Comment on the PR** with the rebase result. If rebase conflicts, mark as needs-review with the conflict details.
- If the queue is empty, check for blocked issues and report what's blocking them.
- **All operational decisions must be recorded** — see Operational Audit Trail (section 3).

### 9. Modified Orchestrator Agent

The existing `orchestrator.md` gains GitHub awareness. The core dev-review loop stays the same.

**Additions:**

After each development round:
- The implementing agent (Codex) pushes its own commits to remote as part of its dev work. The orchestrator does not push on its behalf.
- Update state breadcrumb with current phase and round.

After each review round:
- Post diff-review results as PR comment via pr-lifecycle skill (round number, verdict, checklist).
- Continue passing checklist to dev agent internally — don't rely on PR comments for agent-to-agent communication.

On completion:
- Update PR description with summary (what was done, key decisions, areas for attention).
- Return structured result to github-orchestrator: PASS/FAIL, summary.

**Unchanged behaviors:**
- Spawns Codex for development rounds (Codex is the implementing agent, not Claude Code).
- Uses diff-review skill for code review between iterations. Full perfect-review is **not** run per-issue — see "Periodic Full Review" below.
- Maintains round counter and max-rounds limit.
- Local logging continues alongside PR comments.

### Review Strategy

**Per-issue (during development loop):** diff-review only. This is the lightweight gate that checks changed code against the issue's acceptance criteria. Fast, focused, sufficient for iterative development.

**Periodic (maintenance task):** Full PERFECT-D review runs on a schedule (or on-demand) across the entire project codebase. This catches cross-cutting concerns, architectural drift, and accumulated tech debt that diff-review can't see. Findings are filed as new GitHub Issues for the queue to process. This keeps the per-issue loop fast while still getting full-codebase review coverage.

## Configuration

Configuration lives centrally in the agendev project at `config/agendev.json`. All target projects use the same config — there is no per-project config file to drift out of sync.

The only truly project-specific value is the repository (`owner/repo`), which is **derived automatically** from `git remote get-url origin` at runtime — no manual configuration needed.

```json
{
  "labels": {
    "ready": "agendev:ready",
    "inProgress": "agendev:in-progress",
    "done": "agendev:done",
    "needsReview": "agendev:needs-human-review",
    "blocked": "agendev:blocked"
  },
  "maxRounds": 5,
  "autoMerge": {
    "enabled": true,
    "requireCI": true,
    "requireZeroCritical": true
  },
  "reviewers": ["username"],
  "branchPrefix": "agendev/",
  "worktreePrefix": "agendev-wt-",
  "stall": {
    "timeoutSeconds": 600
  }
}
```

**Repository detection:** Scripts run `git remote get-url origin` and parse `owner/repo` from the SSH or HTTPS URL. This works for any GitHub-hosted repo without configuration.

## Integration with Target Projects

Agent and skill definitions live in the agendev repository and are loaded at runtime via `--add-dir`. Nothing is copied to target projects — agendev is the single source of truth.

**Setup (one-time per project):**

```bash
# From the target project directory
/path/to/agendev/scripts/setup.sh
```

This creates `.agendev/state/` for breadcrumb files and ensures required GitHub labels exist. No agent/skill files are copied.

**Usage:**

All invocations use `--add-dir` to load agendev's agents and skills from the agendev repo:

```bash
# Plan and create issues
claude --add-dir /path/to/agendev "use plan-to-issues skill on docs/my-plan.md"

# Run the queue
claude --add-dir /path/to/agendev --agent github-orchestrator "run"

# Run a specific issue
claude --add-dir /path/to/agendev --agent github-orchestrator "run --issue 42"

# Dry run — show queue and planned execution order
claude --add-dir /path/to/agendev --agent github-orchestrator "run --dry-run"
```

To avoid typing `--add-dir` every time, set a shell alias:

```bash
alias agendev='claude --add-dir /path/to/agendev'

# Then:
agendev --agent github-orchestrator "run"
```

No per-project configuration needed — repo is detected from `git remote`, agents and skills are loaded from the agendev repo, everything else uses centralized defaults.

## Project Structure

```
agendev/
├── .claude/
│   ├── agents/
│   │   ├── github-orchestrator.md # Project-level dispatcher
│   │   └── orchestrator-github.md # GitHub additions for existing orchestrator
│   └── skills/
│       ├── issue-queue/
│       │   └── SKILL.md
│       ├── pr-lifecycle/
│       │   └── SKILL.md
│       └── plan-to-issues/
│           └── SKILL.md
├── scripts/
│   ├── gh-issue-queue.sh          # Issue queue operations (gh wrapper)
│   ├── gh-pr-lifecycle.sh         # PR lifecycle operations (gh wrapper)
│   ├── state.sh                   # State breadcrumb read/write
│   ├── watchdog.sh                # Stall detection wrapper
│   └── setup.sh                   # One-time setup for target projects
├── config/
│   └── agendev.json               # Centralized configuration (all projects)
├── templates/
│   ├── issue-template.md          # GitHub issue template with metadata block
│   └── pr-template.md             # PR body template with marker sections
├── docs/
│   └── prd.md                     # This document
├── CLAUDE.md
└── AGENTS.md
```

When Claude Code is invoked with `--add-dir /path/to/agendev`, it discovers agents from `agendev/.claude/agents/` and skills from `agendev/.claude/skills/` automatically. Target projects never contain copies of these files.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Agent misinterprets GitHub state and makes wrong decision | High | Skills return structured JSON; agent prompts include explicit decision rules with examples |
| Agent forgets to push or post review (skips skill usage) | Medium | Agent prompt uses CRITICAL markers; review checklist includes "PR updated?" |
| Stall during long dev round with no output | Medium | Watchdog kills after configurable timeout; state file enables resume |
| Crash mid-run leaves orphaned PR/branch/labels | Medium | Startup reconciliation checks for inconsistent state before dispatching |
| `gh` CLI output format changes between versions | Medium | Scripts parse JSON output (`--json` flag) not human-readable; pin minimum gh version |
| Shell scripts grow complex and hard to maintain | Medium | Keep scripts focused (one operation each); migrate to TypeScript if complexity demands |
| Context window pressure from issue bodies + review history | Medium | Skills summarize rather than passing raw content; only last review checklist goes to dev agent |
| Agent tries to read source code (violating role separation) | Low | Explicit NEVER rules in agent prompt (proven pattern from existing orchestrator) |
| Rate limiting from many `gh` calls | Low | `gh` handles retry; runs are sequential; batch where possible |

## Future Work: Deterministic CLI

If this workflow proves the concept but hits scaling or reliability limits, the migration path to a deterministic TypeScript CLI is straightforward:

1. The shell scripts become the integration test suite for an Octokit-based TypeScript layer.
2. The skill definitions serve as functional specs for corresponding TypeScript modules.
3. The agent definitions document the orchestration logic that gets encoded as a state machine.
4. The centralized config schema carries over directly.
5. The state file format becomes the persistence layer for a proper state machine.

**Triggers for migration:**
- 3+ incidents caused by agent non-determinism in orchestration decisions.
- Need for concurrent issue processing (multiple issues in parallel).
- Shell script complexity exceeds maintainability threshold.
- Need for proper unit testing of orchestration logic.

The deterministic CLI would move orchestration logic from LLM agents into TypeScript code — keeping LLMs only for tasks that genuinely require intelligence (writing code, reviewing code). The state machine would formalize the phases (INIT → DEVELOP → REVIEW → DECIDE → FINALIZE) with typed state transitions, Octokit for GitHub operations, and full unit testability. This is a product-grade evolution, not a rewrite — every component maps 1:1 from the agent-driven version.
