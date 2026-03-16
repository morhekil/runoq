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
4. **Respect compound reliability.** Each agent handoff is a failure point. At 95% per-step reliability across 10 sequential steps, end-to-end reliability drops to ~60%. Keep the sequential chain short (≤5 agent steps per issue), define typed contracts at every agent boundary, insert verification checkpoints between phases, and treat budget exhaustion as a stop signal — not a retry trigger.
5. **Automate execution, not judgment.** Planning (Phase 1) stays human-driven because it requires judgment about priorities, scope, and context that agents lack. Execution (Phase 2) is agent-driven because it's structured, repeatable work where agents excel. This split is deliberate — the productivity gains come from autonomous execution of well-defined tasks, not from replacing human decision-making at the planning layer.

## Target Workflow

### Phase 1: Planning (human-driven)

The user does local planning and thinking — in conversation with Claude Desktop, Claude Code, or on paper. When ready, they instruct their LLM to slice the plan into GitHub Issues:

```
agendev plan docs/my-plan.md
```

The skill creates issues with structured metadata (dependencies, priority, acceptance criteria) and the `agendev:ready` label.

### Phase 2: Execution (agent-driven)

The user starts the github-orchestrator agent:

```
agendev run
```

For each issue in the queue:

1. **Pick**: Pull next actionable issue (dependencies satisfied, not blocked).
2. **Branch**: Create feature branch `agendev/<issue>-<slug>` from main.
3. **PR**: Create draft pull request linked to the issue.
4. **Develop**: Dispatch to orchestrator agent — the existing dev-review loop.
5. **Push & Report**: The implementing agent (Codex) pushes its commits when done with a dev round. After each review round, orchestrator posts review results as a PR comment (round number, scorecard, checklist).
6. **Iterate**: Orchestrator feeds review checklist back to dev agent. Repeat until PASS or max rounds.
7. **Finalize**: Update PR description with work summary and areas needing human attention. Mark PR ready for review.
8. **Merge or Assign**: If orchestrator has high confidence (diff-review passed cleanly, zero critical issues, verification step confirms tests and build pass) — merge the PR. Otherwise, assign human reviewer and leave PR open.
9. **Update Issue**: Label as `agendev:done` or `agendev:needs-human-review`.
10. **Next**: Return to step 1.

### Phase 3: Human Review (when needed)

PRs flagged `agendev:needs-human-review` have:
- Full review history in PR comments
- Summary of work done in PR description
- Specific areas flagged for attention
- A human reviewer assigned

### Phase 4: Maintenance Review (periodic, agent-driven)

A full PERFECT-D codebase review that runs independently of the per-issue loop. It catches cross-cutting concerns that diff-review structurally cannot see — problems that only emerge when looking at the codebase as a whole rather than one issue's changes at a time.

#### What it catches

- **Architectural drift** — modules violating their boundaries, dependency graph degradation across independently-merged issues.
- **Accumulated duplication** — two issues each add similar code; only a global view notices.
- **Convention drift** — naming, error handling, patterns that slowly diverge across issues.
- **Dead code / unused exports** — artifacts of iteration that no single diff-review flags.
- **Documentation staleness** — READMEs, architecture docs, AGENTS.md that haven't kept up with accumulated changes.
- **Integration-boundary test gaps** — individual issue tests cover their own paths but miss cross-module interactions.
- **Dependency health** — unused, duplicate, or outdated dependencies.

#### Trigger

- **On-demand**: Human invokes explicitly (e.g. `agendev maintenance-review`).
- **Queue-idle**: Optionally triggered when the issue queue has been empty for a configurable duration.
- **Precondition**: Runs against a clean `main` branch. All in-flight PRs must be merged or parked — no point reviewing code that's about to change.
- **Concurrency**: Mutex with the per-issue loop. Either pause the queue during maintenance review, or run read-only in a temporary worktree off `main`.

#### Scope derivation (zero-config)

Review scope and partitioning are derived automatically from existing project configuration — no maintenance-specific config needed.

1. **Exclusions** — `.gitignore` provides baseline exclusion (node_modules, dist, build artifacts, coverage output). TypeScript `tsconfig.json` `exclude` adds TS-specific exclusions (generated types, declaration output). The union of both defines "not reviewable code."
2. **Partitioning** — In monorepos with TypeScript project references (`tsconfig.json` `references`), each referenced sub-project is one review partition. In single-project repos, top-level source directories within `tsconfig.json` `include` paths are used as partitions. Each partition gets its own PERFECT-D scorecard.
3. **Spec source** — Per-issue review uses the issue body as its spec. Maintenance review uses project-level guidelines as its spec: AGENTS.md, README, architecture docs, and any documented conventions. The question being answered is "does this code match the project's stated intent?" rather than "does this diff satisfy this issue's acceptance criteria?"

#### Tracking issue & human triage

The maintenance review is **read-only** — it observes and proposes findings, never modifies code or files issues autonomously. All findings go through human triage before entering the queue.

Each maintenance review run creates a **tracking issue** labeled `agendev:maintenance-review`. The tracking issue body contains run metadata (timestamp, branch/commit reviewed, partitions identified) and a summary table of per-partition PERFECT-D scores.

**Findings are posted as individual comments on the tracking issue**, one comment per finding (or per grouped set of related findings). Each comment includes:

- PERFECT-D dimension and severity (bug / design / documentation / etc.)
- File locations and code references
- Concrete description of the problem
- Suggested fix

The agent then **waits for human triage**. The human responds to each finding comment by @-mentioning the agent (see Addressability — maintenance issue mentions below):

- **Approve** (`@agendev approve` or `@agendev file this`): The agent creates a new GitHub Issue from the finding, labeled `agendev:ready`, and links it from the tracking issue. The issue body includes the full finding detail and suggested fix.
- **Deny** (`@agendev skip` or `@agendev won't fix`): The agent marks the finding as declined (edits the comment or posts a reply) and moves on. No issue is created.
- **Question** (`@agendev why is this a problem?` or `@agendev what about X?`): The agent reads the question, the referenced code, and the review context, and posts a reply on the tracking issue. The finding stays in triage until the human follows up with approve or deny.
- **Modify** (`@agendev file this but lower priority` or `@agendev combine with the one above`): The agent adjusts the finding per the instruction before filing.

**Grouping**: Related findings (e.g. "extract duplicated validation logic in 3 places") are posted as a single comment, not three. The agent groups proactively during review; the human can also request combining findings during triage.

**Deduplication with in-flight work**: Findings that reference code currently being worked on in an open PR are flagged in the comment ("Note: this code is being modified in PR #N") so the human can decide whether to file or wait.

The tracking issue is updated with a summary once all findings are triaged: partitions reviewed, findings proposed, approved, declined, issues created.

#### Agent structure

A `maintenance-reviewer` agent — same dispatcher pattern as the existing `project-orchestrator`:

- **Role**: Derive partitions from project config, spawn `perfect-review` subagents per partition, collect results, deduplicate/group findings, post findings as comments on the tracking issue, then process human triage responses.
- **Does not fix anything** — observation, proposal, and issue-filing (after approval) only.
- **Context management**: Holds only partition names, scores, and finding summaries. Never holds full review content in the dispatcher context. Full review output is written to the tracking issue.
- **Triage loop**: After posting all findings, the agent polls for @-mentions on the tracking issue (same `poll-mentions` mechanism as the github-orchestrator). It processes each triage response, files approved issues, and waits until all findings are resolved or the human explicitly closes the tracking issue.

#### State & resumability

Same pattern as per-issue state: a local state file tracks which partitions have been reviewed, so a crashed maintenance review can resume from where it left off.

#### Feedback loop

If maintenance review repeatedly surfaces the same category of finding (e.g. missing error handling, inconsistent naming), that's a signal that the diff-review skill or AGENTS.md conventions need strengthening — not just that more issues need filing. The tracking issue summary should flag recurring patterns explicitly so the human can decide whether to improve the review criteria.

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

## Communication Diagram

Shows every actor, what they send/store, where it goes, and what's in it.

```
┌─────────┐
│  Human  │
└────┬────┘
     │ (1) "use plan-to-issues on docs/plan.md"
     │ (14) reviews PRs labeled needs-human-review
     ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        github-orchestrator                                  │
│                                                                             │
│  READS FROM:                                                                │
│  ├─ GitHub Issues ◄── issue queue (via issue-queue skill)                   │
│  ├─ config/agendev.json ◄── max rounds, token budget, labels               │
│  └─ .agendev/state/*.json ◄── orphaned breadcrumbs (startup reconciliation)│
│                                                                             │
│  WRITES TO:                                                                 │
│  ├─ GitHub Issues ──► labels (ready/in-progress/done/blocked/needs-review)  │
│  │                    comments (blocked reason, skip reason, stale reset)    │
│  ├─ GitHub PRs ────► create draft PR                                        │
│  │                    comment: dispatch payload (orchestrator contract)      │
│  │                    comment: return payload (orchestrator result)          │
│  │                    comment: finalize verdict + reason                     │
│  │                    update PR body: summary, review table, attention areas │
│  │                    set ready-for-review, enable auto-merge or assign      │
│  ├─ .agendev/state/<N>.json ──► breadcrumb (phase, round, tokens, SHAs)     │
│  ├─ git ──► create branch, create/remove worktree, checkout main            │
│  └─ local log ──► operational log (backup, not primary)                     │
│                                                                             │
│  DISPATCHES:                                                                │
│  │                                                                          │
│  │  (2) dispatch payload ──────────────────────────────────┐                │
│  │  { issue_number, issue_title, issue_body_summary,       │                │
│  │    branch, pr_number, repo, worktree,                   │                │
│  │    max_rounds, max_token_budget }                       │                │
│  │                                                         ▼                │
│  │  ┌──────────────────────────────────────────────────────────────────┐     │
│  │  │                       orchestrator                               │     │
│  │  │                                                                  │     │
│  │  │  READS FROM:                                                     │     │
│  │  │  ├─ dispatch payload (from github-orchestrator)                  │     │
│  │  │  ├─ AGENTS.md (from target project worktree)                     │     │
│  │  │  └─ Codex return payloads (after each dev round)                 │     │
│  │  │                                                                  │     │
│  │  │  WRITES TO:                                                      │     │
│  │  │  ├─ GitHub PRs ──► comment: dev round dispatch payload           │     │
│  │  │  │                 comment: Codex return payload (per round)      │     │
│  │  │  │                 comment: diff-review result (verdict, score,   │     │
│  │  │  │                          checklist)                            │     │
│  │  │  │                 comment: verification checkpoint failure       │     │
│  │  │  │                 comment: token budget exhausted                │     │
│  │  │  │                 update PR body: summary + attention areas      │     │
│  │  │  ├─ .agendev/state/<N>.json ──► breadcrumb updates               │     │
│  │  │  │   (phase, round, tokens_used, Codex return payload)           │     │
│  │  │  └─ local log ──► operational log                                │     │
│  │  │                                                                  │     │
│  │  │  DISPATCHES (per round):                                         │     │
│  │  │  │                                                               │     │
│  │  │  │  (3) dispatch payload ────────────────────┐                   │     │
│  │  │  │  { task, checklist, branch,               │                   │     │
│  │  │  │    worktree, round }                      │                   │     │
│  │  │  │                                           ▼                   │     │
│  │  │  │  ┌────────────────────────────────────────────────────┐       │     │
│  │  │  │  │                  Codex (dev agent)                  │       │     │
│  │  │  │  │                                                     │       │     │
│  │  │  │  │  READS FROM:                                        │       │     │
│  │  │  │  │  ├─ dispatch payload (from orchestrator)            │       │     │
│  │  │  │  │  └─ target project source code (in worktree)        │       │     │
│  │  │  │  │                                                     │       │     │
│  │  │  │  │  WRITES TO:                                         │       │     │
│  │  │  │  │  ├─ target project source code (in worktree)        │       │     │
│  │  │  │  │  ├─ git ──► commits + push to remote                │       │     │
│  │  │  │  │  └─ return payload (to orchestrator on exit)        │       │     │
│  │  │  │  │                                                     │       │     │
│  │  │  │  │  DOES NOT WRITE TO:                                 │       │     │
│  │  │  │  │  ├─ GitHub Issues (no direct access)                │       │     │
│  │  │  │  │  ├─ GitHub PRs (no direct access)                   │       │     │
│  │  │  │  │  └─ .agendev/state/ (orchestrator's responsibility) │       │     │
│  │  │  │  └────────────────────────────────────────────────────┘       │     │
│  │  │  │                         │                                     │     │
│  │  │  │  (4) return payload ◄───┘                                     │     │
│  │  │  │  { status, commits_pushed, commit_range,                      │     │
│  │  │  │    files_changed/added/deleted,                               │     │
│  │  │  │    tests_run, tests_passed, test_summary,                     │     │
│  │  │  │    build_passed, blockers, notes }                            │     │
│  │  │  │                                                               │     │
│  │  │  │  (5) orchestrator posts Codex payload as PR comment           │     │
│  │  │  │  (6) orchestrator runs verification checkpoint                │     │
│  │  │  │  (7) if verified: orchestrator runs diff-review               │     │
│  │  │  │  (8) orchestrator posts review result as PR comment           │     │
│  │  │  │  (9) DECIDE: iterate (goto 3) or finish                      │     │
│  │  │  │                                                               │     │
│  │  └──┼───────────────────────────────────────────────────────────────┘     │
│  │     │                                                                     │
│  │  (10) return payload ◄──┘                                                │
│  │  { verdict, rounds_used, final_score,                                    │
│  │    summary, caveats, tokens_used }                                       │
│  │                                                                          │
│  │  (11) github-orchestrator posts orchestrator payload as PR comment       │
│  │  (12) github-orchestrator finalizes PR (auto-merge or assign reviewer)   │
│  │  (13) github-orchestrator updates issue labels                           │
│  │                                                                          │
└─────────────────────────────────────────────────────────────────────────────┘

STORAGE LOCATIONS SUMMARY:

  GitHub Issues          GitHub PRs               Local State              Git
  ──────────────         ──────────────           ──────────────           ──────────────
  • labels (state)       • draft → ready          • .agendev/state/       • worktree per
  • metadata block       • body: summary,           <N>.json:               issue
    (dependencies,         review table,            phase, round,         • branch per
    priority)              attention areas           tokens, round           issue
  • comments:            • comments:                history, Codex        • commits from
    blocked reason,        ALL dispatch/return      payloads                Codex only
    skip reason,           payloads (full JSON    • local log             • push from
    stale reset            in collapsible block)    (backup)                worktree only
                           review verdicts
                           verification failures
                           stall/crash events
                           finalize decisions
                           worktree cleanup
```

**Key invariants:**
- Codex writes only to the codebase (commits + push) and its return payload. It never touches GitHub Issues, PRs, or state files.
- The orchestrator is the only actor that posts per-round PR comments (Codex payloads, review results, verification failures).
- The github-orchestrator is the only actor that manages issue labels, creates/finalizes PRs, and posts dispatch/completion-level PR comments.
- Every payload that crosses an agent boundary appears as a PR comment. The PR thread is a complete replayable log.
- Local state (`.agendev/state/`) is a recovery mechanism, not the audit trail. GitHub is the audit trail.

## Inter-Agent Contracts

Every agent boundary has a typed contract defining exactly what is passed and what is returned. Ambiguous handoffs are the #1 failure mode in multi-agent systems (36.9% of all failures per the MAST study). These contracts prevent two agents from interpreting the same data differently.

**Logging rule:** Every dispatch and return payload at every agent boundary must be posted as a PR comment immediately when it is sent or received. This is a hard requirement — the PR comment thread is the single source of truth for what each agent was told and what it reported back. See Operational Audit Trail for the comment format.

**Comment read isolation:** Agents must not read the full PR comment history back as input — doing so pulls the entire audit trail (payload dumps, event markers) into the context window, wasting tokens on data the agent already has in memory. Agents selectively read only two kinds of comments:

1. **@-mentioned comments.** Comments containing `@agendev` (the GitHub App's handle, configurable via `identity.handle` in `agendev.json`) are actionable feedback directed at the agent. Human reviewers use this to flag concerns, request changes, or provide clarification. The agent only processes mentions from authorized users (see Agent Identity, Addressability & Authorization).
2. **Line-level review comments.** GitHub review comments attached to specific code lines are inherently actionable and always read.

All other comment types (`<!-- agendev:payload:* -->`, `<!-- agendev:event -->`) are write-only audit artifacts — posted for human debugging and post-hoc analysis, never consumed by agents. The `gh-pr-lifecycle.sh` script provides a `read-actionable` subcommand that filters comments by these criteria, so agents never see the raw comment stream.

### github-orchestrator → orchestrator

**Dispatch payload:**
```json
{
  "issue_number": 42,
  "issue_title": "Fix auth token refresh",
  "issue_body_summary": "≤500 token summary of issue body + acceptance criteria",
  "branch": "agendev/42-fix-auth-refresh",
  "pr_number": 87,
  "repo": "owner/repo",
  "worktree": "../agendev-wt-42",
  "max_rounds": 5,
  "max_token_budget": 500000
}
```

**Return payload:**
```json
{
  "verdict": "PASS | PASS_WITH_CAVEATS | FAIL",
  "rounds_used": 3,
  "final_score": 40,
  "summary": "What was done, key decisions made",
  "caveats": ["Optional list of areas needing human attention"],
  "tokens_used": 287000
}
```

`tokens_used` here is the cumulative total derived from Codex's actual JSON stream metrics (`turn.completed` → `usage`), not self-reported. The orchestrator sums `input_tokens` + `output_tokens` across all Codex turns per round, then accumulates across rounds.

### orchestrator → Codex (dev agent)

**Dispatch payload:**
```json
{
  "task": "Issue description + acceptance criteria (summarized)",
  "checklist": ["Review feedback items from previous round, if any"],
  "branch": "agendev/42-fix-auth-refresh",
  "worktree": "../agendev-wt-42",
  "round": 2
}
```

**Return payload:**
```json
{
  "status": "completed | failed | stuck",
  "commits_pushed": ["abc1234", "def5678"],
  "commit_range": "abc1234..def5678",
  "files_changed": ["src/auth/refresh.ts", "src/auth/refresh.test.ts"],
  "files_added": ["src/auth/token-store.ts"],
  "files_deleted": [],
  "tests_run": true,
  "tests_passed": true,
  "test_summary": "14 passed, 0 failed",
  "build_passed": true,
  "blockers": ["Optional: description of anything that prevented completion"],
  "notes": "Optional: key decisions, assumptions, or deviations from the task"
}
```

**Field semantics:**
- `status`: `completed` = all checklist items addressed; `failed` = hit an unrecoverable error (build broken in ways unrelated to the task, missing dependencies, etc.); `stuck` = made partial progress but couldn't finish (unclear requirements, needs human clarification).
- `commits_pushed`: SHA list of commits pushed this round. **Empty array is the critical signal** — it means no work was produced. The orchestrator must not proceed to review if this is empty.
- `files_changed` / `files_added` / `files_deleted`: lets the orchestrator scope the diff-review to only touched files, keeping review tokens low.
- `tests_run` / `tests_passed` / `test_summary`: if the agent ran tests, report results. Orchestrator can skip review entirely on test failure and feed errors directly back.
- `build_passed`: whether a build/compile step succeeded. Same skip-review logic as test failure.
- `blockers`: anything that prevented full completion. Surfaces to the orchestrator for inclusion in PR comments and human escalation decisions.
- `notes`: design decisions or deviations the orchestrator should know about when evaluating review results.

### Verification checkpoints

The orchestrator validates the Codex return payload before proceeding to review. Validation has two layers: **payload consistency** (does the payload make internal sense?) and **ground-truth verification** (do the claims match reality?).

**Payload consistency checks:**
1. **Commits claimed** — `commits_pushed` is non-empty. If empty and status is `completed`, treat as `failed` (contract violation).
2. **Tests/build green** — if `tests_passed` or `build_passed` is false, skip review. Feed the failure details directly back to the next dev round as a checklist item — don't waste review tokens on code that doesn't compile or pass tests.
3. **Status check** — if `stuck`, the orchestrator includes `blockers` in a PR comment and may escalate to `needs-human-review` rather than burning rounds on something the dev agent already flagged as unclear.

**Ground-truth verification** (run from the worktree — these are deterministic shell checks, not LLM judgment):
4. **Commits actually exist** — run `git log` in the worktree and confirm every SHA in `commits_pushed` is present on the branch. Agents can hallucinate SHAs.
5. **Files match** — run `git diff --stat` against the pre-round HEAD to get the actual list of changed/added/deleted files. Compare against `files_changed` / `files_added` / `files_deleted`. Log discrepancies (use the ground-truth list for review scoping, not the claimed list).
6. **Push verified** — confirm the branch tip on the remote matches the local branch tip (`git ls-remote origin <branch>` vs. `git rev-parse HEAD`). If the agent claims commits but didn't push, the review would be against stale code.
7. **Tests/build independently verified** — run the configured `verification.testCommand` and `verification.buildCommand` (see Configuration) from the worktree. This is the ground-truth check for Codex's `tests_passed` and `build_passed` claims. If either command fails, treat as a verification failure regardless of what Codex reported. If no commands are configured, treat as a setup error — refuse to proceed and log the misconfiguration.

If any ground-truth check fails, treat as a verification failure: post details to the PR, feed corrected information to the next dev round. Do not proceed to review with unverified claims.

This catches silent failures, partial completions, and hallucinated results before spending tokens on review.

## Components

### 1. GitHub Utility Scripts

Small, focused shell scripts wrapping `gh` CLI operations. Each script does one thing, takes arguments, returns structured JSON output.

**Location:** `scripts/`

**Why shell scripts:**
- `gh` CLI handles auth, pagination, rate limiting already.
- No build step, no dependencies beyond `gh` (≥2.88) and `jq` (≥1.8). Pin minimum versions — `jq` behavior varies across versions for null handling and edge cases.
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

gh-pr-lifecycle.sh read-actionable <repo> <pr-number> <agent-handle>
# Returns JSON array of actionable comments only:
# 1. PR-level comments containing @<agent-handle>
# 2. All line-level review comments (inherently actionable)
# Filters out agendev:payload and agendev:event audit comments

gh-pr-lifecycle.sh poll-mentions <repo> <agent-handle> [--since <timestamp>]
# Returns JSON array of unprocessed @-mentions across open PRs and issues:
# { comment_id, author, body, pr_number/issue_number, created_at, context_type }
# Used by github-orchestrator's mention polling cycle

gh-pr-lifecycle.sh check-permission <repo> <username> <required-level>
# Checks commenter's repository permission via GitHub API
# required-level: "read" | "write" | "admin"
# Returns exit 0 if user has >= required-level, exit 1 otherwise
# Used by authorization layer before processing any @-mention
```

### 2. Git Worktree Isolation

Each issue is developed in its own git worktree, ensuring agents never interfere with each other or with the user's main working tree. This is a hard safety invariant — not optional.

**Worktree lifecycle:**

```bash
# Fetch latest main without touching the main working tree
git fetch origin main

# Create worktree for issue 42 branching from origin/main
git worktree add ../agendev-wt-42 -b agendev/42-fix-auth-refresh origin/main

# Agent works entirely within ../agendev-wt-42/
# All commits, pushes happen from there

# On completion (success or failure), clean up
git worktree remove ../agendev-wt-42
```

**Why `origin/main`, not `main`:** Creating worktrees from the local `main` branch requires the main working tree to be up-to-date, which means running `git checkout main && git pull` — a race condition if multiple orchestrator instances run concurrently. Using `origin/main` after a `git fetch` avoids touching the main working tree entirely. The main tree stays clean and uncontested.

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
| Orchestrator dispatched | PR | Full dispatch payload JSON (github-orchestrator → orchestrator contract). |
| Dev round dispatched | PR | Codex dispatch payload JSON (orchestrator → Codex contract) for this round. |
| Dev round completed | PR | Full Codex return payload JSON. Human-readable summary line above the JSON block: "Development round N: status=completed, commits=2, tests=14 passed." |
| Diff-review result | PR | Round number, verdict, checklist (existing behavior). |
| Orchestrator completed | PR | Full orchestrator return payload JSON (orchestrator → github-orchestrator contract). |
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
| PR finalized (auto-merge) | PR | "Diff-review passed, zero critical issues, verification step confirms tests/build pass. Merging." |
| PR finalized (needs-review) | PR | "Assigned to @reviewer for human review. Reason: [caveats/failure/max rounds]." |
| Token budget exhausted | PR + Issue | "Token budget exhausted (N/M tokens used) during phase DEVELOP round 3. Escalating to human review." |
| Verification checkpoint failed | PR | "Post-dev verification failed: [no new commits / build failure / push missing]. Feeding errors to next dev round." |
| Ground-truth verification mismatch | PR | "Codex claimed [N] commits but [M] found on branch. Files mismatch: claimed [...], actual [...]." |
| Circuit breaker triggered | PR + Issue (latest) | "Queue halted after N consecutive failures. Failed issues: #X, #Y, #Z. Investigate before resuming." |
| Maintenance review started | Tracking Issue | Run metadata: commit reviewed, partitions derived, timestamp. |
| Maintenance partition reviewed | Tracking Issue | Partition name, PERFECT-D score, finding count. |
| Maintenance finding proposed | Tracking Issue | One comment per finding (or grouped findings): dimension, severity, files, description, suggested fix. |
| Maintenance finding triaged | Tracking Issue | Human triage result: approved (link to created issue), declined, or modified. |
| Maintenance review completed | Tracking Issue | Summary: partitions reviewed, findings proposed, approved, declined, issues created, recurring patterns flagged. |

**Format:** Comments are prefixed with `<!-- agendev:event -->` so they can be distinguished from human comments and parsed programmatically if needed. Payload comments use `<!-- agendev:payload:<type> -->` (e.g., `<!-- agendev:payload:codex-return -->`) so they can be extracted programmatically for analysis.

**Payload comment format:**

Every dispatch and return payload is logged as a PR comment with a human-readable summary line followed by the full JSON in a collapsible details block:

```markdown
<!-- agendev:payload:codex-return -->
**Dev round 2 complete:** status=completed, 2 commits, 3 files changed, tests 14/14 passed

<details>
<summary>Full payload</summary>

\```json
{ ... full return payload ... }
\```
</details>
```

This serves three purposes: (1) the PR comment thread is a complete replay of every agent interaction for debugging, (2) payloads are machine-parseable for future tooling that analyzes agent performance across issues, (3) human reviewers can skim the summary lines and expand details only when investigating problems.

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
    { "round": 1, "score": 28, "verdict": "ITERATE", "commit_range": "abc..def", "tokens": { "input": 118000, "cached_input": 94000, "output": 24000 } }
  ],
  "tokens_used": 287000,
  "started_at": "2026-03-15T10:00:00Z",
  "updated_at": "2026-03-15T10:45:00Z",
  "main_sha": "abc123"
}
```

**Phases:** `INIT`, `DEVELOP`, `REVIEW`, `DECIDE`, `FINALIZE`, `DONE`, `FAILED`

**DECIDE phase decision table:** The DECIDE phase is the branching point after each review round. The orchestrator evaluates the following conditions in order:

| Condition | Action | Next Phase |
|-----------|--------|------------|
| Token budget exceeded | Return FAIL with budget exhaustion | FINALIZE |
| Max rounds reached | Return current verdict (PASS/FAIL) | FINALIZE |
| Verification checkpoint failed (no commits, build broken, push missing) | Feed errors to dev agent as checklist | DEVELOP |
| Codex status = `stuck` | Evaluate blockers; if unclear requirements → escalate | FINALIZE (needs-review) |
| Diff-review verdict = PASS (score ≥ threshold) | Return PASS | FINALIZE |
| Diff-review verdict = ITERATE | Feed review checklist to dev agent | DEVELOP |
| Diff-review verdict = FAIL (critical issues found) | If rounds remaining, feed back; else return FAIL | DEVELOP or FINALIZE |

**State transition invariants:**
- Transitions follow the happy path: `INIT → DEVELOP → REVIEW → DECIDE → FINALIZE → DONE`.
- `FAILED` is a terminal state. No transition from `FAILED` back to `DEVELOP`, `REVIEW`, or any earlier phase. Resuming a failed issue requires human intervention (clearing the state file or starting a new run).
- `DONE` is terminal. A completed issue is never re-entered by the orchestrator.
- Backward transitions within the loop (`DECIDE → DEVELOP` for another round) are allowed only via `DECIDE` and only when the round counter has not hit `maxRounds`.
- These invariants are enforced by `state.sh save`, which rejects invalid transitions. This is a structural guarantee, not a prompt-level instruction — the script refuses to write a state file that violates the rules.

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
4. **Granularity validation.** Before presenting to the user, flag issues that may be mis-scoped:
   - **Too broad:** Issues with more than 5 acceptance criteria, or whose description implies touching more than 3-4 files across multiple subsystems. Suggest splitting.
   - **Too narrow:** Issues that describe a single-line change or trivial rename with no behavioral impact. Suggest merging with a related issue.
   - **Missing testability:** Issues with no clear way to verify completion (no acceptance criteria that can be checked by running tests or inspecting output). Flag for the user to add criteria.
5. Present proposed issues for user confirmation before creating, with any granularity warnings displayed inline.
6. Create issues via issue-queue skill.
7. Display the full queue with dependency graph.

### 8. GitHub Orchestrator Agent

**Location:** `.claude/agents/github-orchestrator.md` (in agendev repo)

Replaces `project-orchestrator.md`. Project-level dispatcher that pulls work from GitHub Issues instead of local spec files.

**Role:** Dispatch only. Never reads or modifies source code.

**Startup sequence:**
1. Read centralized agendev configuration.
2. Run startup reconciliation (check orphaned state, stale labels).
3. Fetch and display the full issue queue.

**Dispatch loop:**
1. Get next actionable issue (via issue-queue skill + dispatch eligibility checks). If `--issue N` was specified, target that issue directly instead of pulling from the queue — but still run eligibility checks (dependencies resolved, no existing in-progress PR, etc.). If eligibility fails, report why and stop (don't fall back to the queue). After completing the single issue, stop — don't continue to the next issue in the queue.
2. If no actionable issue: report status (empty vs. blocked) and stop.
3. Mark issue as `in-progress`.
4. Create feature branch from main.
5. Create draft PR (via pr-lifecycle skill).
6. Write initial state breadcrumb.
7. Dispatch to orchestrator agent (via Task) with dispatch payload (see Inter-Agent Contracts).
   The orchestrator reads AGENTS.md files itself from the project root — no need to pass them downstream.
8. Wait for completion. Parse return payload.
9. Based on result:
   - Clean PASS (diff-review passed, zero critical issues) **and** issue `estimated_complexity` is `low` → finalize with auto-merge.
   - Clean PASS but `estimated_complexity` is `medium` or `high` → finalize with needs-review (agent confidence is insufficient for complex changes).
   - PASS with caveats → finalize with needs-review.
   - FAIL → finalize with needs-review, comment explaining failure.
   - Token budget exhausted → finalize with needs-review, comment with usage and last phase reached.
10. Update issue status.
11. Update state breadcrumb to DONE.
12. `git fetch origin main` (update remote tracking branch — no checkout needed since worktrees branch from `origin/main`).
13. Continue to next issue.

**Decision-making guidelines (for the agent):**
- If an issue's dependencies have failed, mark as blocked, **comment on the issue** with the blocking dependency, and skip. Don't ask the user.
- If the orchestrator fails on an issue, don't retry. Mark as needs-review, **comment on the PR and issue** with failure details, and continue.
- If main has diverged and the branch needs updating, rebase onto `origin/main` before the next dev round. **Comment on the PR** with the rebase result. Only auto-rebase when `origin/main` has diverged by ≤10 commits **and** the divergent commits do not touch any files in the issue's `files_changed` set. If either threshold is exceeded, mark as needs-review with the divergence details rather than risking a bad merge resolution.
- If the queue is empty, check for blocked issues and report what's blocking them.
- **Circuit breaker:** Track consecutive failures across issues. If `consecutiveFailureLimit` (default: 3) issues in a row result in FAIL or token budget exhaustion, **stop the queue**, post a summary comment on the most recent PR listing all failed issues, and exit. Consecutive failures often signal a systemic problem (bad base state, missing dependency, flawed issue specs) that burning through more issues won't fix.
- **All operational decisions must be recorded** — see Operational Audit Trail (section 3).

### 9. Modified Orchestrator Agent

The existing `orchestrator.md` gains GitHub awareness. The core dev-review loop stays the same.

**Additions:**

After each development round:
- Codex returns a structured payload (see Inter-Agent Contracts: orchestrator → Codex return payload). The orchestrator **must parse this before doing anything else**.
- **Run verification checkpoint** using the Codex return payload (see Inter-Agent Contracts: Verification checkpoints). Key signals:
  - `commits_pushed` empty → no work produced, do not review.
  - `tests_passed: false` or `build_passed: false` → skip review, feed failures back as checklist items for next round.
  - `status: stuck` → evaluate `blockers`; may escalate to human review instead of burning another round.
- Scope the diff-review using ground-truth file lists (from verification, not Codex self-report). Additionally, expand the review scope to include **direct importers** of changed files — a simple `grep -rl` for changed module/file names across the codebase identifies files that may break due to signature changes, removed exports, or renamed symbols. This is a cheap deterministic check (not an LLM call) that catches the most common cross-file breakage that per-file review misses. The expanded file list is passed to diff-review as context, not as files-under-review — the review focuses on changed code but can flag caller-site issues.
- Include `notes` from the Codex payload in context for the review — design decisions and deviations inform whether the review should flag them or accept them.
- Update state breadcrumb with current phase, round, cumulative `tokens_used`, and the Codex return payload for auditability.
- **Check token budget.** If cumulative usage exceeds `maxTokenBudget`, stop immediately — return `FAIL` with budget exhaustion reason. Do not start another round. Token tracking uses **Codex's actual usage metrics** from its JSON output (`turn.completed` → `usage.input_tokens` + `usage.output_tokens`), not self-reported values from the return payload. The orchestrator parses Codex's JSON stream, sums token counts across turns, and records the actual total in the state file. The `tokens_used` field in the Codex return payload is removed — actual usage from the JSON stream is authoritative.

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

### 10. Cost & Performance Observability

State files contain per-round token data (actual Codex metrics) and outcome data (verdicts, scores, round counts). A reporting script aggregates this into actionable summaries.

**`scripts/report.sh`:**

```bash
report.sh summary [--last N]
# Aggregates completed state files. Output:
# - Total issues processed, pass/fail/caveats breakdown
# - Total tokens consumed (input, cached, output)
# - Average rounds per issue, average tokens per round
# - Consecutive failure streaks

report.sh issue <issue-number>
# Detailed breakdown for a single issue:
# - Per-round token usage, verdicts, scores
# - Time from INIT to DONE/FAILED
# - Verification failures encountered

report.sh cost [--last N]
# Token usage aggregated with estimated cost
# (configurable $/token rate in agendev.json)
```

This data directly informs the "triggers for migration" thresholds in Future Work — compound reliability rate, cost trends, and failure patterns are all derivable from state files without any additional instrumentation.

### Review Strategy

**Per-issue (during development loop):** diff-review only. This is the lightweight gate that checks changed code against the issue's acceptance criteria. Fast, focused, sufficient for iterative development.

**Periodic (maintenance task):** Full PERFECT-D review of the entire codebase, run on-demand or when the queue is idle. Catches cross-cutting concerns that diff-review structurally cannot see (architectural drift, accumulated duplication, convention drift, dead code, documentation staleness). Findings are posted to a tracking issue for human triage; approved findings become new GitHub Issues for the queue to process. See **Phase 4: Maintenance Review** for full details.

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
  "identity": {
    "appSlug": "agendev",
    "handle": "agendev"
  },
  "authorization": {
    "minimumPermission": "write",
    "denyResponse": "comment"
  },
  "maxRounds": 5,
  "maxTokenBudget": 500000,
  "autoMerge": {
    "enabled": true,
    "requireVerification": true,
    "requireZeroCritical": true,
    "maxComplexity": "low"
  },
  "reviewers": ["username"],
  "branchPrefix": "agendev/",
  "worktreePrefix": "agendev-wt-",
  "consecutiveFailureLimit": 3,
  "verification": {
    "testCommand": "npm test",
    "buildCommand": "npm run build"
  },
  "stall": {
    "timeoutSeconds": 600
  }
}
```

**Repository detection:** Scripts run `git remote get-url origin` and parse `owner/repo` from the SSH or HTTPS URL. This works for any GitHub-hosted repo without configuration.

## Agent Identity, Addressability & Authorization

Agents that post comments, manage labels, and create PRs must be clearly distinguishable from human contributors. Humans must be able to address agents directly in comments. And agents must only respond to authorized users.

### Identity: GitHub App

A custom GitHub App ("agendev") provides a distinct, unfakeable identity for all agent actions on GitHub.

When the app authenticates via an installation token, every API action is attributed to `agendev[bot]` — a separate actor with:
- A **`[bot]` badge** on all comments (cannot be faked by human users).
- Its own avatar, profile page, and description.
- A distinct actor in the GitHub timeline and audit log — no ambiguity with any human contributor.

**Why a GitHub App, not a machine user or comment markers:**
- Machine user accounts have no `[bot]` badge, consume org seats, require long-lived PATs, and sit in a gray area of GitHub ToS for automation.
- Comment markers (HTML comments, emoji prefixes) leave all actions attributed to the human whose `gh` token is used — fundamentally confusing in multi-person projects and impossible to distinguish in the GitHub audit log.
- GitHub Apps are the platform-intended mechanism for automation. They get rotating short-lived tokens (not long-lived PATs), native @-mentionability, and clean identity separation.

**App permissions required:**
- **Issues:** Read & Write (labels, comments, metadata)
- **Pull Requests:** Read & Write (create, comment, review, merge)
- **Contents:** Read (branch verification, file checks)

**App creation** is a one-time manual step (github.com → Settings → Developer Settings → GitHub Apps → New). The app is owned by the user or org. A single app serves all repos.

**App installation** onto each target repo is scripted as part of `agendev init` (see Setup below), with the exception of the initial authorization click — GitHub requires a human to approve app installation on their repo/org. This is one-time per repo (or once per org for org-wide installation).

### Credentials & Per-Project Binding

The GitHub App has two credential components:

1. **Private key** (global, shared across all repos): Downloaded once when the app is created. Standard path: `~/.agendev/app-key.pem`. The init script and `gh-auth.sh` auto-detect this path without prompting. Override via `AGENDEV_APP_KEY` env var for CI or non-standard setups. Never committed to any repo.

2. **Installation binding** (per-project): After the app is installed on a repo, the installation ID and app ID are stored in the target project's `.agendev/identity.json`:

```json
{
  "appId": 123456,
  "installationId": 789012,
  "privateKeyPath": "~/.agendev/app-key.pem"
}
```

`.agendev/` is already gitignored. The `privateKeyPath` supports `~` expansion and can be overridden via the `AGENDEV_APP_KEY` environment variable for CI or shared-machine scenarios.

**Token lifecycle:** At runtime, `scripts/gh-auth.sh` reads `identity.json`, signs a JWT with the private key, exchanges it for a short-lived installation token (expires in 1 hour), and exports `GH_TOKEN`. All existing `gh` commands in the scripts work unchanged — they pick up `GH_TOKEN` from the environment. The auth script is called once at the start of each `agendev run` or `agendev plan` invocation; the token is refreshed if it expires mid-run.

### Addressability

GitHub Apps are @-mentionable natively. When the app is installed on a repo, any user can write `@agendev` in a comment (PR or issue) and it functions as a real GitHub mention — the app receives a notification.

The `read-actionable` subcommand in `gh-pr-lifecycle.sh` already filters for `@<agent-handle>` mentions. With the GitHub App approach, this becomes a first-class GitHub mechanism rather than plain text matching.

**Inbound command processing:** The github-orchestrator gains a second event source alongside the issue queue. Between issue dispatches (and during idle periods), it polls for unprocessed `@agendev` mentions across open PRs and issues in the repo:

```bash
gh-pr-lifecycle.sh poll-mentions <repo> <agent-handle> [--since <timestamp>]
# Returns: JSON array of unprocessed @-mentions with:
#   comment_id, author, body, pr_number/issue_number, created_at, author_permission
```

Each mention is processed once (tracked by comment ID in `.agendev/state/processed-mentions.json` to avoid duplicates across polling cycles).

**Mention types and responses:**

*Development PRs and issues:*
- **Question on a PR** (`@agendev why did you change X?`): Agent reads the PR context and referenced code, posts a reply explaining the rationale from the review/dev round that produced the change.
- **Action request on a PR** (`@agendev please also add tests for the edge case`): Agent treats this as an additional checklist item. If the PR is still in-progress, it's folded into the next dev round. If the PR is finalized, a comment is posted explaining that the PR is closed for agent work and suggesting a follow-up issue.
- **Action request on an issue** (`@agendev pick this up next`): Agent re-prioritizes or immediately dispatches the issue (subject to eligibility checks).

*Maintenance review tracking issues (labeled `agendev:maintenance-review`):*
- **Approve finding** (`@agendev approve`, `@agendev file this`): Agent creates a GitHub Issue from the finding, labeled `agendev:ready`, and links it from the tracking issue.
- **Deny finding** (`@agendev skip`, `@agendev won't fix`): Agent marks the finding as declined. No issue is created.
- **Question on finding** (`@agendev why is this a problem?`): Agent posts a reply with additional context from the review. Finding stays in triage.
- **Modify finding** (`@agendev file this but lower priority`, `@agendev combine with the one above`): Agent adjusts the finding per the instruction before filing.

The agent distinguishes context by the issue label: mentions on `agendev:maintenance-review` issues are triage commands; mentions on all other issues/PRs are development commands. Same authorization rules apply to both.

### Authorization

Agents must not act on commands from unauthorized users. A drive-by `@agendev merge this` from a random contributor must be ignored.

**Permission check:** Before processing any `@agendev` mention, the agent resolves the commenter's repository permission:

```bash
gh-pr-lifecycle.sh check-permission <repo> <username> <required-level>
# Calls: gh api repos/{owner}/{repo}/collaborators/{username}/permission
# Returns: exit 0 if authorized, exit 1 if not
```

**Authorization config** (in `agendev.json`):

```json
{
  "authorization": {
    "minimumPermission": "write",
    "denyResponse": "comment"
  }
}
```

- **`minimumPermission`**: The minimum repo permission level required to command the agent. Default `"write"` — any collaborator with `write` or `admin` permission can address the agent. Can be set to `"admin"` to restrict further.

**`denyResponse`** controls behavior when an unauthorized user @-mentions the agent:
- `"comment"`: Post a reply: "I can only respond to users with write access to this repository."
- `"silent"`: No response. The mention is logged locally but not acknowledged on GitHub.

**Authorization applies only to commands** (mentions that request action). Informational audit comments posted by the agent (payload dumps, event markers) are write-only and don't involve any inbound authorization.

### C3 Component Diagrams

#### Agent Identity & Auth Infrastructure

Shows how the GitHub App identity layer integrates with the existing agendev runtime.

```
┌─ GitHub App: agendev ─────────────────────────────────────────────────────┐
│  Registered once at github.com/settings/apps                              │
│  Permissions: Issues (RW), Pull Requests (RW), Contents (R)              │
│  Installed per-repo (or per-org)                                          │
│                                                                           │
│  Identity: "agendev[bot]" with [bot] badge                               │
│  @-mentionable as @agendev on any installed repo                         │
└───────────────────────────────────────────────────────────────────────────┘
         │                                          ▲
         │ installation token (1hr TTL)             │ @agendev mentions
         │                                          │ (comments on PRs/issues)
         ▼                                          │
┌─ agendev runtime ─────────────────────────────────┼───────────────────────┐
│                                                    │                       │
│  ┌──────────────┐    ┌────────────────────────────┐│                       │
│  │ gh-auth.sh   │    │ Target Project             ││                       │
│  │              │    │ .agendev/                   ││                       │
│  │ Reads:       │    │   identity.json ───────────┘│                       │
│  │  identity.json    │     appId: 123456           │                       │
│  │  app-key.pem │    │     installationId: 789012  │                       │
│  │              │    │     privateKeyPath: ~/.../   │                       │
│  │ Produces:    │    │   state/                    │                       │
│  │  GH_TOKEN    │    │     processed-mentions.json │                       │
│  │  (exported)  │    │                             │                       │
│  └──────┬───────┘    └─────────────────────────────┘                       │
│         │                                                                  │
│         │  GH_TOKEN env var picked up by all gh commands                  │
│         ▼                                                                  │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │                    GitHub Utility Scripts                             │  │
│  │                                                                      │  │
│  │  gh-issue-queue.sh          All API calls authenticated as           │  │
│  │  gh-pr-lifecycle.sh    ◄──  agendev[bot] via installation token      │  │
│  │  gh-auth.sh                                                          │  │
│  │                                                                      │  │
│  │  New subcommands:                                                    │  │
│  │  • poll-mentions ── find unprocessed @agendev mentions               │  │
│  │  • check-permission ── verify commenter authorization                │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                            │
└────────────────────────────────────────────────────────────────────────────┘
         │
         ▼
┌─ GitHub API ──────────────────────────────────────────────────────────────┐
│  All mutations (comments, labels, PRs) attributed to agendev[bot]        │
│  Permission checks: GET /repos/{owner}/{repo}/collaborators/{user}/perm  │
└───────────────────────────────────────────────────────────────────────────┘
```

#### Inbound Mention Processing Flow

Shows how an @agendev mention moves from a GitHub comment through authorization to agent action.

```
┌──────────┐
│  Human   │  writes "@agendev please add error handling"
│  (on PR) │  on PR #87, comment ID 1234
└────┬─────┘
     │
     ▼
┌─ github-orchestrator ──────────────────────────────────────────────────────┐
│                                                                            │
│  ┌─ Poll Cycle ─────────────────────────────────────────────────────────┐  │
│  │                                                                      │  │
│  │  (1) poll-mentions                                                   │  │
│  │      gh-pr-lifecycle.sh poll-mentions owner/repo agendev             │  │
│  │      Returns: [{ id: 1234, author: "Saruman", body: "...",          │  │
│  │                   pr_number: 87 }]                                   │  │
│  │                                                                      │  │
│  │  (2) Deduplicate                                                     │  │
│  │      Check comment ID against .agendev/state/processed-mentions.json │  │
│  │      Skip if already processed                                       │  │
│  │                                                                      │  │
│  │  (3) Authorize                                                       │  │
│  │      gh-pr-lifecycle.sh check-permission owner/repo Saruman write    │  │
│  │      ┌─────────┐                                                     │  │
│  │      │ Allowed? │──── NO ──► Post denial comment (if denyResponse    │  │
│  │      └────┬─────┘           = "comment") or silently skip            │  │
│  │           │ YES                                                      │  │
│  │           ▼                                                          │  │
│  │  (4) Classify mention                                                │  │
│  │      ┌─────────────┬────────────────┬──────────────────┐             │  │
│  │      │ Question    │ Action (PR)    │ Action (Issue)   │             │  │
│  │      │ on PR       │                │                  │             │  │
│  │      ▼             ▼                ▼                  │             │  │
│  │   Read PR context  If in-progress:  Re-prioritize or   │             │  │
│  │   + diff, post     fold into next   dispatch issue     │             │  │
│  │   explanatory      dev round.       (eligibility       │             │  │
│  │   reply            If finalized:    checks apply)      │             │  │
│  │                    suggest follow-                      │             │  │
│  │                    up issue                             │             │  │
│  │                                                                      │  │
│  │  (5) Record comment ID in processed-mentions.json                    │  │
│  │                                                                      │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                            │
│  ┌─ Issue Queue (existing) ─────────────────────────────────────────────┐  │
│  │  Continues as before — poll cycle runs between issue dispatches      │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                            │
└────────────────────────────────────────────────────────────────────────────┘
```

## Integration with Target Projects

Agent and skill definitions live in the agendev repository and are loaded at runtime via `--add-dir`. Nothing is copied to target projects — agendev is the single source of truth.

**Setup:**

```bash
# First-time setup (includes GitHub App installation)
agendev init

# Re-run to auto-heal broken state (skips app installation if already bound)
agendev init
```

The init script is idempotent — safe to re-run at any time to auto-heal broken state without overwriting things that aren't broken. It performs:

1. **`.agendev/state/`** — create directory if missing.
2. **`.agendev/identity.json`** — bind the GitHub App to this repo:
   a. Check if `identity.json` already exists and is valid (app installed, token works). If so, skip.
   b. Locate the private key. Resolution order: `AGENDEV_APP_KEY` env var → `~/.agendev/app-key.pem` (standard path) → prompt. If found at the standard path, use it silently.
   c. Resolve the app ID automatically from the configured slug: `gh api /apps/<identity.appSlug> --jq '.id'`. No prompt or global storage needed.
   c. Check if the app is already installed on this repo (`gh api /repos/{owner}/{repo}/installation`).
   d. If not installed, print the installation URL and wait for the user to approve in-browser:
      ```
      GitHub App "agendev" is not installed on owner/repo.
      Install it here: https://github.com/apps/agendev/installations/new/permissions?target_id=<id>
      Press Enter when done...
      ```
   e. Fetch the installation ID, write `identity.json`.
   f. Mint a test token and validate access (list repo labels as a smoke test).
3. **GitHub labels** — ensure required `agendev:*` labels exist (skip any already present).
4. **`package.json`** — create with `test` and `build` scripts if missing. If present, leave untouched.
5. **CLI symlink** — ensure `/usr/local/bin/agendev` points to `bin/agendev` in this repo. If the symlink exists and already points to the right target, skip. If it points elsewhere or is broken, update it.

No agent/skill files are copied to the target project.

**Usage:**

```bash
# Plan and create issues
agendev plan docs/my-plan.md

# Run the queue
agendev run

# Run a specific issue (bypasses queue ordering, still runs reconciliation and eligibility checks)
agendev run --issue 42

# Dry run — show queue and planned execution order
agendev run --dry-run
```

The `bin/agendev` wrapper script resolves its own install location to find the agendev repo, passes `--add-dir` automatically, and routes subcommands: `run` dispatches to `--agent github-orchestrator`, `plan` dispatches to the plan-to-issues skill.

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
├── bin/
│   └── agendev                      # CLI wrapper (symlink to /usr/local/bin)
├── scripts/
│   ├── gh-issue-queue.sh          # Issue queue operations (gh wrapper)
│   ├── gh-pr-lifecycle.sh         # PR lifecycle + mention polling + permission checks
│   ├── gh-auth.sh                 # GitHub App JWT → installation token exchange
│   ├── state.sh                   # State breadcrumb read/write
│   ├── watchdog.sh                # Stall detection wrapper
│   ├── report.sh                  # Cost & performance reporting
│   └── setup.sh                   # One-time setup for target projects (agendev init)
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
| Compound reliability decay across agent chain | High | Verification checkpoints between dev and review phases; typed inter-agent contracts (see Inter-Agent Contracts); hard token budget per issue; max 5 sequential agent steps before a verification gate |
| Agent misinterprets GitHub state and makes wrong decision | High | Skills return structured JSON; agent prompts include explicit decision rules with examples |
| Agent forgets to push or post review (skips skill usage) | Medium | Agent prompt uses CRITICAL markers; review checklist includes "PR updated?" |
| Runaway token costs from retry loops or long issues | High | Per-issue `maxTokenBudget` enforced by orchestrator; budget exhaustion is a stop signal, not a retry trigger; cumulative usage tracked in state file |
| Coordination breakdown from ambiguous agent handoffs | High | Typed dispatch/return contracts at every agent boundary (see Inter-Agent Contracts); no implicit shared state between agents |
| Consecutive failures burn through the queue | High | Circuit breaker stops after `consecutiveFailureLimit` (default: 3) failures in a row; posts summary and exits |
| Agent self-reports inaccurate data (hallucinated SHAs, false test results) | High | Ground-truth verification checks (git log, git diff --stat, git ls-remote) validate Codex claims before proceeding to review |
| Stall during long dev round with no output | Medium | Watchdog kills after configurable timeout; state file enables resume |
| Crash mid-run leaves orphaned PR/branch/labels | Medium | Startup reconciliation checks for inconsistent state before dispatching |
| `gh` CLI output format changes between versions | Medium | Scripts parse JSON output (`--json` flag) not human-readable; pin minimum gh version |
| Shell scripts grow complex and hard to maintain | Medium | Keep scripts focused (one operation each); migrate to TypeScript if complexity demands |
| Context window pressure from issue bodies + review history | Medium | Skills summarize rather than passing raw content; only last review checklist goes to dev agent |
| Agent tries to read source code (violating role separation) | Low | Explicit NEVER rules in agent prompt (proven pattern from existing orchestrator) |
| Rate limiting from many `gh` calls | Low | `gh` handles retry; runs are sequential; batch where possible |
| Unauthorized user triggers agent action via @-mention | High | Authorization check before processing any mention; `check-permission` verifies repo-level write access before acting |
| Installation token expires mid-run (1hr TTL) | Low | `gh-auth.sh` checks token expiry and refreshes transparently; all scripts call through `gh-auth.sh` |
| Private key compromised | High | Key stored outside project repos; never committed; supports env var override for secret managers; rotating the key is a single re-download from GitHub App settings |
| GitHub App not installed on target repo | Low | `agendev init` validates installation; `gh-auth.sh` fails fast with actionable error message if token exchange fails |

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
- Compound reliability math shows end-to-end success rate below 70% (track via state file round/failure data).

The deterministic CLI would move orchestration logic from LLM agents into TypeScript code — keeping LLMs only for tasks that genuinely require intelligence (writing code, reviewing code). The state machine would formalize the phases (INIT → DEVELOP → REVIEW → DECIDE → FINALIZE) with typed state transitions, Octokit for GitHub operations, and full unit testability. This is a product-grade evolution, not a rewrite — every component maps 1:1 from the agent-driven version.
