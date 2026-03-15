# Approach C: Hybrid — Agent-Driven with GitHub Skills

## Problem Statement

Same as Approach B: the current local-file-based agentic workflow lacks visibility, auditability, and GitHub integration.

## Proposed Solution

Keep the orchestration as Claude Code agents — which already work well for the decision-making, error recovery, and nuanced judgment required — but build a structured **skill and utility layer** for GitHub operations. The agents gain GitHub capabilities without losing their flexibility.

The key insight: **the orchestrator agents are good at their job**. The gap is not in decision-making — it's in the I/O layer (local files vs. GitHub). Replace the I/O, keep the intelligence.

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
│  │  Dispatches to:      │     │  • perfect-review         │  │
│  │  orchestrator ───────┼────▶│                           │  │
│  └──────────────────────┘     │  Spawns:                  │  │
│                               │  • Claude Code (dev work) │  │
│                               └───────────────────────────┘  │
│                                                              │
│  ┌──────────────────────────────────────────────────────────┐│
│  │                    Skills Layer                           ││
│  │  ┌─────────────┐ ┌──────────────┐ ┌──────────────────┐  ││
│  │  │ issue-queue  │ │ pr-lifecycle │ │ diff-review      │  ││
│  │  │ (new)        │ │ (new)        │ │ (existing)       │  ││
│  │  └──────┬───────┘ └──────┬───────┘ └──────────────────┘  ││
│  │         │                │         ┌──────────────────┐  ││
│  │         │                │         │ perfect-review   │  ││
│  │         │                │         │ (existing)       │  ││
│  │         │                │         └──────────────────┘  ││
│  └─────────┼────────────────┼───────────────────────────────┘│
│            │                │                                │
│  ┌─────────▼────────────────▼───────────────────────────────┐│
│  │              GitHub Utility Scripts                       ││
│  │  gh-issue-queue.sh  │  gh-pr-lifecycle.sh                ││
│  └──────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
         │                    │
         ▼                    ▼
   GitHub API (via gh)   Target Project Codebase
```

### Key Difference from Approach B

| Aspect | Approach B | Approach C |
|--------|-----------|-----------|
| Orchestration logic | Deterministic TypeScript state machine | LLM agent with structured prompts |
| GitHub operations | Octokit library calls | `gh` CLI wrapped in skills/scripts |
| Decision-making | Coded rules (if/else) | Agent judgment with guardrails |
| Adding new steps | Code change to agendev | Edit agent prompt or add skill |
| Error recovery | Explicit error handling code | Agent reasons about the error |
| Testing | Unit tests on orchestration code | Integration tests on the full loop |
| Runtime dependency | Node.js + agendev binary | Claude Code + gh CLI |

## Components

### 1. GitHub Utility Scripts

Small, focused shell scripts that wrap `gh` CLI operations. These are the building blocks that skills invoke. Each script does one thing, takes arguments, returns structured output (JSON where possible).

**Location:** `agendev/scripts/`

#### `gh-issue-queue.sh`

```bash
# Subcommands:
gh-issue-queue.sh list <repo> <ready-label>
# Returns: JSON array of issues with parsed metadata (dependencies, priority)

gh-issue-queue.sh next <repo> <ready-label>
# Returns: JSON of the next actionable issue (dependencies satisfied)

gh-issue-queue.sh set-status <repo> <issue-number> <status>
# Removes old agendev:* label, adds new one

gh-issue-queue.sh create <repo> <title> <body> [--depends-on N,M]
# Creates issue with agendev:ready label and metadata block
```

**Dependency resolution approach:**
- Parse `<!-- agendev:meta ... -->` block from issue body.
- Check each dependency's current labels — if all are `agendev:done`, the issue is actionable.
- Return issues sorted by: explicit priority > issue number (FIFO).

#### `gh-pr-lifecycle.sh`

```bash
gh-pr-lifecycle.sh create <repo> <branch> <issue-number> <title>
# Creates draft PR linked to issue, returns PR number

gh-pr-lifecycle.sh push <branch>
# Pushes current branch to remote

gh-pr-lifecycle.sh comment <repo> <pr-number> <comment-body-file>
# Posts comment from file contents

gh-pr-lifecycle.sh update-summary <repo> <pr-number> <summary-file>
# Updates PR body's summary section (between markers)

gh-pr-lifecycle.sh finalize <repo> <pr-number> <verdict> [--reviewer <username>]
# verdict=auto-merge: mark ready, enable auto-merge
# verdict=needs-review: mark ready, request review, assign reviewer

gh-pr-lifecycle.sh line-comment <repo> <pr-number> <file> <line> <body>
# Post a review comment on a specific line
```

**Why shell scripts over TypeScript:**
- `gh` CLI handles auth, pagination, rate limiting already.
- Scripts are portable — agents can invoke them from any project.
- No build step, no dependencies beyond `gh` and `jq`.
- Easy to test manually: `./gh-issue-queue.sh list owner/repo agendev:ready | jq .`

### 2. Issue Queue Skill

**Location:** `.claude/skills/issue-queue/SKILL.md` (in agendev, copied/linked to target projects)

**Purpose:** Provides the github-orchestrator agent with structured access to the issue queue. Translates between the agent's needs (what should I work on next? what's the status?) and the GitHub API.

**Skill definition:**

```markdown
# Issue Queue Skill

## When to use
When the orchestrator needs to interact with the GitHub issue queue:
reading available work items, determining the next actionable item,
updating issue status, or creating new issues.

## Inputs
- action: list | next | set-status | create
- repo: GitHub repository (owner/repo)
- For set-status: issue_number, new_status (ready|in-progress|done|blocked|needs-human-review)
- For create: title, body, depends_on (optional list of issue numbers)

## Process

### Action: list
1. Run: `agendev/scripts/gh-issue-queue.sh list <repo> agendev:ready`
2. Parse JSON output.
3. For each issue, extract: number, title, dependencies, priority, labels.
4. Build dependency graph. Identify which issues are actionable (all deps satisfied).
5. Return formatted queue with status indicators.

### Action: next
1. Run: `agendev/scripts/gh-issue-queue.sh next <repo> agendev:ready`
2. If no actionable issue: report queue empty or blocked (list blocking dependencies).
3. Return issue details: number, title, body, acceptance criteria, dependencies.

### Action: set-status
1. Run: `agendev/scripts/gh-issue-queue.sh set-status <repo> <number> <status>`
2. Confirm status change.

### Action: create
1. Format issue body with metadata block and human-readable content.
2. Run: `agendev/scripts/gh-issue-queue.sh create <repo> <title> <body> [--depends-on ...]`
3. Return created issue number and URL.

## Output
Structured summary of the action result.
```

### 3. PR Lifecycle Skill

**Location:** `.claude/skills/pr-lifecycle/SKILL.md`

**Purpose:** Gives the orchestrator agent structured PR management capabilities.

**Skill definition:**

```markdown
# PR Lifecycle Skill

## When to use
When the orchestrator needs to create, update, comment on, or finalize
a pull request as part of the development loop.

## Inputs
- action: create | push | post-review | update-summary | finalize
- repo: GitHub repository (owner/repo)
- For create: branch_name, issue_number, pr_title
- For push: branch_name
- For post-review: pr_number, round_number, review_result (score, verdict, checklist, issues)
- For update-summary: pr_number, summary_text, attention_areas
- For finalize: pr_number, verdict (auto-merge|needs-review), reviewer (optional)

## Process

### Action: create
1. Run: `agendev/scripts/gh-pr-lifecycle.sh create <repo> <branch> <issue> <title>`
2. Return PR number and URL.

### Action: push
1. Run: `agendev/scripts/gh-pr-lifecycle.sh push <branch>`
2. Verify push succeeded.
3. Return new HEAD SHA.

### Action: post-review
1. Format review comment as markdown:
   - Round header with number and timestamp.
   - Metrics table (if available).
   - PERFECT-D scorecard.
   - Issues categorized by severity.
   - Checklist for next iteration (if ITERATE).
2. Write to temp file.
3. Run: `agendev/scripts/gh-pr-lifecycle.sh comment <repo> <pr> <temp-file>`
4. Return comment URL.

### Action: update-summary
1. Format summary with:
   - Work summary (what was done, key decisions).
   - Areas for human attention (files, patterns, risks).
   - Final scorecard.
2. Write to temp file.
3. Run: `agendev/scripts/gh-pr-lifecycle.sh update-summary <repo> <pr> <temp-file>`

### Action: finalize
1. Run: `agendev/scripts/gh-pr-lifecycle.sh finalize <repo> <pr> <verdict> [--reviewer ...]`
2. Return final PR state.

## Output
Action result with relevant URLs and confirmation.
```

### 4. GitHub Orchestrator Agent

**Location:** `.claude/agents/github-orchestrator.md` (replaces `project-orchestrator.md`)

**Role:** Project-level dispatcher that pulls work from GitHub Issues instead of local spec files. Same philosophy as the existing project-orchestrator — it dispatches, it doesn't implement.

```markdown
# GitHub Orchestrator Agent

## Identity
You are a project-level dispatcher. You pull work items from GitHub Issues,
resolve dependencies, and dispatch each item to the orchestrator agent
for implementation. You NEVER read or modify source code.

## Allowed Tools
- Bash: only for git commands and agendev scripts
- Task: to spawn orchestrator agent
- Read: only for AGENTS.md files and config files
- AskUserQuestion: to resolve ambiguity
- Skills: issue-queue, pr-lifecycle

## Inputs
- `run` — process the full queue
- `run --issue <N>` — process a specific issue
- `run --dry-run` — show what would be done

## Configuration
Read `.agendev.json` from the project root for:
- repo (owner/repo)
- label conventions
- auto-merge policy
- reviewer assignments
- max rounds

## Process

### 1. Setup
1. Read AGENTS.md files (root + relevant subdirectories).
2. Read `.agendev.json` configuration.
3. Use issue-queue skill (action: list) to fetch and display the full queue.
4. If --dry-run: display queue and planned execution order, then stop.

### 2. Dispatch Loop
For each iteration:
1. Use issue-queue skill (action: next) to get the next actionable issue.
2. If no actionable issue: report status and stop.
3. Use issue-queue skill (action: set-status) to mark issue as in-progress.
4. Determine branch name: `agendev/<issue-number>-<slug>`.
5. Create branch from main: `git checkout -b <branch> main`.
6. Use pr-lifecycle skill (action: create) to create draft PR.
7. Spawn orchestrator agent via Task with:
   - Issue number, title, and full body (requirements + acceptance criteria).
   - Branch name.
   - PR number.
   - Project rules (AGENTS.md paths).
   - Configuration (max rounds, review thresholds).
8. Wait for orchestrator to complete. Parse result (PASS/FAIL, final score, summary).
9. Based on result:
   - PASS with high confidence → use pr-lifecycle skill (action: finalize, verdict: auto-merge).
   - PASS with caveats → use pr-lifecycle skill (action: finalize, verdict: needs-review).
   - FAIL → use pr-lifecycle skill (action: finalize, verdict: needs-review), add comment explaining failure.
10. Use issue-queue skill (action: set-status) to update issue (done or needs-human-review).
11. Switch back to main: `git checkout main && git pull`.
12. Continue to next issue.

### 3. Completion
Report summary:
- Issues processed: N
- Passed: N (auto-merged: N, needs-review: N)
- Failed: N
- Remaining in queue: N (blocked: N)

## Decision-Making Guidelines
- If an issue's dependencies have failed, mark the issue as blocked and skip it.
  Do NOT ask the user — just log it and move on.
- If the orchestrator fails on an issue, do NOT retry. Mark it as needs-review and continue.
- If main has diverged significantly since the branch was created,
  rebase the branch before the next dev round. If rebase has conflicts, mark
  the issue as needs-review with a note about conflicts.
- If the queue is empty, check if there are blocked issues and report what's blocking them.
```

### 5. Modified Orchestrator Agent

The existing `orchestrator.md` is modified to add GitHub awareness. Changes are minimal — the core dev-review loop stays the same.

**Key modifications to existing orchestrator.md:**

```markdown
## Additions to Orchestrator Agent

### GitHub-Aware Behaviors (additions to existing orchestrator)

#### After each development round:
1. Push commits to remote: use pr-lifecycle skill (action: push).
2. This provides real-time visibility — each push appears in the PR timeline.

#### After each review round:
1. Use pr-lifecycle skill (action: post-review) to post review results as PR comment.
2. Include: round number, score, verdict, checklist.
3. Continue to pass checklist to dev agent internally (don't rely on PR comments
   for agent-to-agent communication).

#### On completion:
1. Use pr-lifecycle skill (action: update-summary) with:
   - Summary of what was implemented and key design decisions.
   - Areas that need human attention (complex logic, security-sensitive code,
     architectural decisions).
   - Final PERFECT-D scorecard.
2. Return result to github-orchestrator: PASS/FAIL, score, summary.

### Unchanged Behaviors
- Still spawns codex/claude for development rounds.
- Still uses diff-review and perfect-review skills for code review.
- Still maintains round counter and max-rounds limit.
- Still captures structured review results internally.
- Local logging continues alongside PR comments.
```

### 6. Plan-to-Issues Skill

**Location:** `.claude/skills/plan-to-issues/SKILL.md`

**Purpose:** Helps the user slice a local plan into GitHub Issues. This is the entry point for new work — the user thinks and plans locally, then uses this skill to publish work items.

```markdown
# Plan to Issues Skill

## When to use
When the user has a plan (markdown document, conversation context, or verbal
description) and wants to create GitHub Issues for agentic development.

## Inputs
- plan: path to plan document OR inline description
- repo: target GitHub repository

## Process

### 1. Analyze the Plan
1. Read the plan document.
2. Identify discrete work items. Each work item should be:
   - Independently implementable (or with clear dependencies).
   - Small enough for a single PR (ideally < 500 lines of change).
   - Testable in isolation.

### 2. Structure Work Items
For each work item, draft:
- Title (concise, action-oriented: "Add X", "Fix Y", "Refactor Z").
- Description (what needs to be done, why, constraints).
- Acceptance criteria (testable conditions for "done").
- Dependencies (which other work items must complete first).
- Estimated complexity (small/medium/large — for human reference only).

### 3. Review with User
Present the proposed issues as a numbered list with:
- Title
- One-line summary
- Dependencies (by list number)
- Complexity

Ask user to confirm, modify, or reorder before creating.

### 4. Create Issues
For each confirmed work item:
1. Use issue-queue skill (action: create) to create the GitHub Issue.
2. Report created issue number and URL.

### 5. Summary
Display the full queue with dependency graph.

## Output
List of created issues with numbers, URLs, and dependency relationships.
```

## Project Structure

```
agendev/
├── scripts/
│   ├── gh-issue-queue.sh          # Issue queue operations (gh wrapper)
│   ├── gh-pr-lifecycle.sh         # PR lifecycle operations (gh wrapper)
│   └── install.sh                 # Symlink scripts + copy skills to target project
├── skills/
│   ├── issue-queue/
│   │   └── SKILL.md               # Issue queue skill definition
│   ├── pr-lifecycle/
│   │   └── SKILL.md               # PR lifecycle skill definition
│   └── plan-to-issues/
│       └── SKILL.md               # Plan slicing skill definition
├── agents/
│   ├── github-orchestrator.md     # Project-level dispatcher (replaces project-orchestrator)
│   └── orchestrator-github.md     # Patch/overlay for existing orchestrator
├── templates/
│   ├── agendev.config.json        # Template project config
│   ├── issue-template.md          # GitHub issue template with metadata block
│   └── pr-template.md             # PR body template with marker sections
├── docs/
│   ├── approach-b-standalone-cli.md
│   └── approach-c-hybrid-agent-skills.md
├── CLAUDE.md
├── AGENTS.md                      # Rules for developing agendev itself
└── .agendev.json                  # Self-referential config (dogfooding)
```

## Integration with Target Projects

To use agendev in a project (e.g., sandboxbouldering/accounts):

1. **Install:** Run `agendev/scripts/install.sh <target-project-path>` which:
   - Symlinks scripts to a known location (or adds agendev to PATH).
   - Copies skill definitions to `<target>/.claude/skills/`.
   - Copies agent definitions to `<target>/.claude/agents/`.
   - Creates `.agendev.json` from template.

2. **Configure:** Edit `.agendev.json` with repo details, reviewer names, thresholds.

3. **Create labels:** Run `gh label create agendev:ready --repo owner/repo` (and other labels). Could be scripted in install.sh.

4. **Use:**
   ```bash
   # Plan and create issues
   claude --agent github-orchestrator "create issues from docs/my-plan.md"

   # Or use plan-to-issues skill directly
   claude "use plan-to-issues skill on docs/my-plan.md for owner/repo"

   # Run the queue
   claude --agent github-orchestrator "run"

   # Run a specific issue
   claude --agent github-orchestrator "run --issue 42"
   ```

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Agent misinterprets GitHub state and makes wrong decision | High | Skills return structured data; agent prompts include explicit decision rules with examples |
| `gh` CLI output format changes between versions | Medium | Scripts parse JSON output (`--json` flag) not human-readable output; pin minimum gh version |
| Agent forgets to push or post review (skips skill usage) | Medium | Agent prompt uses CRITICAL markers for GitHub steps; review checklist includes "PR updated?" |
| Shell scripts become complex and hard to maintain | Medium | Keep scripts focused (one operation each); if complexity grows, migrate to Approach B |
| Context window pressure from long issue bodies + review history | Medium | Skills summarize rather than passing raw content; only last review checklist passed to dev agent |
| Agent tries to read code (violating role separation) | Low | Explicit NEVER rules in agent prompt (proven pattern from existing orchestrator) |
| Rate limiting from many `gh` calls | Low | `gh` handles auth/retry; batch operations where possible; runs are sequential not concurrent |

## What This Approach Optimizes For

- **Speed to working prototype**: Modifies existing working agents rather than rewriting from scratch. New code is ~200 lines of shell scripts + ~4 skill definitions + 2 agent definitions.
- **Flexibility**: Adding a new step (e.g., "run security scan") means adding a skill and a few lines to the agent prompt. No code compilation needed.
- **Consistency**: Same agent framework, same PERFECT-D review process, same AGENTS.md conventions. The GitHub integration is additive.
- **Low dependency footprint**: Requires only `gh` CLI and `jq` beyond what's already installed. No new npm packages, no build step for agendev itself.

## What This Approach Trades Away

- **Testability of orchestration logic**: Agent decision-making is tested by running the agent, not by unit tests. If the agent makes a wrong decision, you debug by reading logs rather than stepping through code.
- **Determinism**: Two runs with the same input may produce different orchestration paths (LLM non-determinism). The *outcomes* should be equivalent, but the path may vary.
- **Error handling robustness**: Shell scripts + agent judgment is less rigorous than typed error handling in TypeScript. Edge cases may surface as confusing agent behavior rather than clear error messages.
- **Resumability**: No structured state file. If the process crashes mid-run, the agent must inspect GitHub state (PR exists? What round? What labels?) to figure out where to resume. This is possible but less reliable than reading a state file.

## Migration Path to Approach B

If Approach C proves the concept but hits scaling or reliability limits, the migration path to Approach B is straightforward:

1. The GitHub utility scripts become the integration test suite for the Octokit-based TypeScript layer.
2. The skill definitions serve as functional specs for the corresponding TypeScript modules.
3. The agent definitions document the orchestration logic that needs to be encoded as a state machine.
4. The `.agendev.json` config schema carries over directly.

This makes Approach C a low-risk starting point — it validates the workflow design before committing to a full TypeScript implementation.
