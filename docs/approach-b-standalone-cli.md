# Approach B: Standalone Orchestration CLI

## Problem Statement

The current agentic development workflow (proven in sandboxbouldering/accounts) uses local markdown spec files for work items and local log files for observability. This works for solo development but lacks:

- **Visibility**: No way for collaborators to see what's being worked on, what's been reviewed, or what needs human attention.
- **Auditability**: Logs live on-disk and are not tied to the code changes they describe.
- **Integration**: Work items are disconnected from the codebase's collaboration platform (GitHub).
- **Resumability**: If the process crashes, restarting requires manual intervention to figure out where things left off.

## Proposed Solution

Build `agendev` as a standalone TypeScript CLI tool that manages the full lifecycle of agentic development work — from pulling GitHub Issues to delivering reviewed Pull Requests — by orchestrating Claude Code agents as its development and review backends.

The key shift: **move orchestration logic from LLM agents into deterministic code**, keeping LLMs only for tasks that genuinely require intelligence (writing code, reviewing code).

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    agendev CLI                          │
│                                                         │
│  ┌───────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │  Issue     │  │  PR          │  │  Orchestration   │  │
│  │  Queue     │  │  Lifecycle   │  │  Loop            │  │
│  │  Manager   │  │  Manager     │  │  Controller      │  │
│  └─────┬─────┘  └──────┬───────┘  └────────┬─────────┘  │
│        │               │                   │             │
│        ▼               ▼                   ▼             │
│  ┌─────────────────────────────────────────────────┐     │
│  │              GitHub Integration (Octokit)       │     │
│  └─────────────────────────────────────────────────┘     │
│                         │                                │
│  ┌─────────────────────────────────────────────────┐     │
│  │          Agent Runner (Claude Code CLI)          │     │
│  └─────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────┘
         │                    │
         ▼                    ▼
   GitHub API           Claude Code
   (Issues, PRs)        (dev agent, review agent)
```

### Components

#### 1. Issue Queue Manager

Responsible for discovering, prioritizing, and tracking work items from GitHub Issues.

**Inputs:**
- Repository owner/name
- Label filter (e.g., `agendev:ready`)
- Dependency convention (parses `depends on #N` from issue body)

**Behaviors:**
- `queue list` — Fetch issues matching the filter, resolve dependency order (topological sort), display the queue.
- `queue next` — Return the next actionable issue (all dependencies satisfied, not blocked).
- `queue create <spec>` — Create one or more GitHub Issues from a local plan/spec document. Supports slicing a plan into multiple issues with dependency links.
- `queue status` — Show current state of all tracked issues (ready, in-progress, done, blocked).

**Issue conventions:**
- Labels encode state: `agendev:ready`, `agendev:in-progress`, `agendev:done`, `agendev:blocked`, `agendev:needs-human-review`
- Issue body contains a structured section (fenced with markers) for machine-readable metadata:
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

#### 2. PR Lifecycle Manager

Manages the lifecycle of a Pull Request tied to an issue.

**Behaviors:**
- `pr create <issue> <branch>` — Create a draft PR linked to the issue. PR body contains issue reference, empty summary section (to be filled), and a status table.
- `pr push <branch>` — Push current branch state to remote.
- `pr comment <pr> <review-round> <review-result>` — Post a formatted review comment. Includes round number, PERFECT-D scorecard, metrics, and actionable checklist.
- `pr update-summary <pr> <summary>` — Update PR description with final work summary and areas needing human attention.
- `pr finalize <pr> <verdict>` — Based on verdict:
  - **auto-merge**: Enable auto-merge (squash), add `agendev:auto-merge` label.
  - **needs-review**: Request review from configured human reviewer(s), add `agendev:needs-human-review` label, assign reviewer.
- `pr line-comment <pr> <file> <line> <comment>` — Post a line-level review comment (used sparingly, only on final review for unresolved issues).

**PR conventions:**
- Branch naming: `agendev/<issue-number>-<short-slug>` (e.g., `agendev/42-fix-auth-refresh`)
- Draft until orchestrator is satisfied
- PR body structure:
  ```markdown
  ## Summary
  <!-- agendev:summary — auto-updated -->
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
  <!-- agendev:attention — auto-updated -->
  [filled by orchestrator on completion]
  ```

#### 3. Orchestration Loop Controller

The central state machine that drives the development cycle for a single issue.

**States:**
```
INIT → DEVELOP → REVIEW → DECIDE → FINALIZE
                   ↑         │
                   └─────────┘ (ITERATE)
```

**State: INIT**
1. Pick next issue from queue (or accept explicit issue number).
2. Read issue body to extract requirements, acceptance criteria, dependencies.
3. Create feature branch from `main`.
4. Create draft PR.
5. Update issue label to `agendev:in-progress`.
6. Transition to DEVELOP.

**State: DEVELOP**
1. Spawn Claude Code agent in a git worktree for the feature branch.
2. Pass to agent: issue description, acceptance criteria, project AGENTS.md rules, previous review checklist (if iteration > 1).
3. Agent writes code, runs tests, makes commits.
4. Push commits to remote.
5. Transition to REVIEW.

**State: REVIEW**
1. If iteration == 1 or significant changes: run diff-review first (lightweight gate).
2. If diff-review passes: run full PERFECT-D review.
3. Capture structured review result: score, verdict, checklist, issues list.
4. Post review summary as PR comment.
5. Transition to DECIDE.

**State: DECIDE**
- If verdict == PASS → transition to FINALIZE.
- If verdict == ITERATE and round < MAX_ROUNDS → transition to DEVELOP with checklist.
- If round >= MAX_ROUNDS → transition to FINALIZE with `needs-review` verdict.

**State: FINALIZE**
1. Update PR description with summary and attention areas.
2. Mark PR as ready-for-review (remove draft status).
3. If auto-merge criteria met (score >= threshold, zero critical issues, CI green):
   - Enable auto-merge, add label.
4. Else:
   - Assign human reviewer, add `needs-human-review` label.
5. Update issue labels (`agendev:done` or `agendev:needs-human-review`).
6. Return control to caller (project-level loop or user).

**Resumability:**
The controller persists state to a local JSON file per issue:
```json
{
  "issue": 42,
  "pr": 87,
  "branch": "agendev/42-fix-auth-refresh",
  "state": "REVIEW",
  "round": 2,
  "rounds": [
    { "round": 1, "score": 28, "verdict": "ITERATE", "checklist": ["..."], "commit_range": "abc..def" }
  ],
  "started_at": "2026-03-15T10:00:00Z",
  "main_sha": "abc123"
}
```
On restart, the controller reads this file and resumes from the persisted state.

#### 4. Project-Level Runner

Drives the full project queue — the equivalent of today's project-orchestrator.

**Behavior:**
1. `agendev run` — Main entry point. Loops:
   a. Fetch queue, find next actionable issue.
   b. Check that `main` is up-to-date (pull).
   c. Dispatch to Orchestration Loop Controller.
   d. On completion, merge main into any remaining in-flight branches (conflict detection).
   e. Continue to next issue or stop if queue is empty / blocked.
2. `agendev run --issue 42` — Run a single specific issue.
3. `agendev run --dry-run` — Show what would be done without doing it.

**Configuration** (per-project, in `.agendev.json` or `agendev.config.ts`):
```typescript
{
  repo: "owner/repo",
  labels: {
    ready: "agendev:ready",
    inProgress: "agendev:in-progress",
    done: "agendev:done",
    needsReview: "agendev:needs-human-review",
    blocked: "agendev:blocked"
  },
  maxRounds: 5,
  autoMerge: {
    enabled: true,
    minScore: 38,
    requireCI: true,
    requireZeroCritical: true
  },
  reviewers: ["username"],
  branchPrefix: "agendev/",
  agentConfig: {
    // Path to AGENTS.md or equivalent rules for the dev agent
    rulesPath: "AGENTS.md",
    // Which Claude Code agent to use for development
    devAgent: "orchestrator",
    // Which skill to use for review
    reviewSkill: "perfect-review",
    diffReviewSkill: "diff-review"
  }
}
```

#### 5. GitHub Integration Layer

Thin wrapper around Octokit providing typed, testable GitHub operations.

**Modules:**
- `github/issues.ts` — CRUD for issues, label management, comment posting.
- `github/pulls.ts` — PR creation, update, review requests, merge, line comments.
- `github/auth.ts` — Token management (from `GITHUB_TOKEN` env var or `gh auth token`).

**Error handling:**
- Rate limit awareness: check `x-ratelimit-remaining`, back off when low.
- Retry with exponential backoff for 5xx errors.
- Conflict detection on PR updates (optimistic locking via ETags where available).

#### 6. Agent Runner

Manages spawning Claude Code for development and review work.

**For development rounds:**
```bash
claude --dangerously-skip-permissions \
  --agent orchestrator \
  --print \
  --input-file /tmp/agendev-prompt-42-round-2.md \
  --output-file /tmp/agendev-result-42-round-2.md
```

Or, if using Claude Code's SDK/API directly, spawn as a subprocess with structured I/O.

**For review rounds:**
Spawn a Claude Code instance with the review skill, passing the diff range and spec.

**Worktree management:**
- Create worktree: `git worktree add ../agendev-wt-42 agendev/42-slug`
- Remove on completion: `git worktree remove ../agendev-wt-42`
- Ensures the main working tree stays clean for the user.

## CLI Interface

```
agendev <command> [options]

Commands:
  run [--issue <N>] [--dry-run]     Run the orchestration loop
  queue list                         Show the issue queue
  queue next                         Show the next actionable issue
  queue create <plan-file>           Create issues from a plan document
  queue status                       Show status of all tracked issues
  pr status <N>                      Show PR status for an issue
  init                               Initialize agendev config in current project
  resume                             Resume from last saved state

Options:
  --repo <owner/repo>                Override repository
  --config <path>                    Config file path (default: .agendev.json)
  --verbose                          Verbose output
  --max-rounds <N>                   Override max iteration rounds
```

## Tech Stack

- **Runtime**: Node.js 22+
- **Language**: TypeScript 5.9 (strict mode, same conventions as target projects)
- **GitHub**: Octokit (`@octokit/rest`)
- **CLI framework**: commander
- **Schema validation**: zod (for config, GitHub payloads, state files)
- **Testing**: node:test (native test runner)
- **Process management**: Node.js child_process for spawning Claude Code

## Directory Structure

```
agendev/
├── src/
│   ├── main.ts                    # CLI entry point
│   ├── commands/
│   │   ├── run.ts                 # Orchestration loop command
│   │   ├── queue.ts               # Queue management commands
│   │   ├── pr.ts                  # PR status/management commands
│   │   ├── init.ts                # Project initialization
│   │   └── resume.ts              # Resume from saved state
│   ├── orchestrator/
│   │   ├── loop.ts                # State machine for single-issue orchestration
│   │   ├── state.ts               # State persistence (JSON files)
│   │   └── project-runner.ts      # Multi-issue queue runner
│   ├── github/
│   │   ├── client.ts              # Octokit wrapper with retry/rate-limit
│   │   ├── issues.ts              # Issue operations
│   │   ├── pulls.ts               # PR operations
│   │   └── auth.ts                # Token management
│   ├── agents/
│   │   ├── runner.ts              # Claude Code subprocess management
│   │   ├── prompt-builder.ts      # Build prompts for dev/review rounds
│   │   └── result-parser.ts       # Parse agent output into structured results
│   ├── queue/
│   │   ├── dependency-graph.ts    # Topological sort, cycle detection
│   │   ├── issue-parser.ts        # Extract metadata from issue bodies
│   │   └── priority.ts            # Queue ordering logic
│   ├── config.ts                  # Configuration schema and loading
│   └── types.ts                   # Shared type definitions
├── tests/
│   ├── orchestrator/
│   ├── github/
│   ├── queue/
│   └── agents/
├── package.json
├── tsconfig.json
├── CLAUDE.md
├── AGENTS.md
└── .agendev.json                  # Self-referential config for dogfooding
```

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Claude Code CLI interface changes break agent runner | High | Pin Claude Code version; abstract CLI interaction behind an adapter |
| GitHub API rate limiting during intensive runs | Medium | Rate-limit-aware client with backoff; batch operations where possible |
| Agent produces broken commits that fail CI | Medium | Run pre-commit checks (typecheck, lint, test) before pushing; don't push if checks fail |
| State file corruption on crash | Medium | Write state atomically (write-to-temp + rename); validate on load |
| Worktree cleanup failure leaves orphan directories | Low | Cleanup check on startup; `git worktree prune` |
| LLM costs for multi-round iterations | Medium | Configurable max rounds; diff-review as cheap gate before full review |
| Dependency cycles in issue graph | Low | Cycle detection in topological sort; fail with clear error |

## What This Approach Optimizes For

- **Testability**: Orchestration logic is deterministic TypeScript, fully unit-testable without LLM calls. GitHub integration testable with mocked Octokit.
- **Reusability**: `agendev` is project-agnostic. Point it at any repo with an `.agendev.json` config and project-specific AGENTS.md.
- **Debuggability**: Every state transition is logged. State files provide full replay capability. PR comments provide human-readable audit trail.
- **Reliability**: Deterministic state machine means no LLM hallucination in the orchestration layer. LLMs do what they're good at (code, reviews), code does what it's good at (state management, API calls, sequencing).

## What This Approach Trades Away

- **Flexibility**: The orchestration loop is fixed in code. Adding a new step (e.g., "run security scan after review") requires a code change to agendev itself, not just editing an agent prompt.
- **Development speed**: More upfront work than modifying existing agent prompts. Estimated 3-5 days to reach feature parity with the current local workflow plus GitHub integration.
- **LLM judgment in orchestration**: The current project-orchestrator agent can make nuanced decisions (e.g., "this WI looks risky, let me ask the user"). A deterministic loop either handles the case or doesn't. Edge cases require explicit code.
