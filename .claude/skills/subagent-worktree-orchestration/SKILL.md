---
name: subagent-worktree-orchestration
description: Create and manage coding or verification subagents with hard isolation boundaries. Use when delegating implementation, testing, or review work to subagents in this repo or similar repos where the main checkout must stay clean, sibling git worktrees are preferred, and prompt-only scope control is not sufficient.
---

# Subagent Worktree Orchestration

Use this skill when delegation is desirable, but only if the orchestration boundary is stricter than "please stay in scope."

## Goal

Keep delegation safe by making the control boundary explicit:

- one sibling git worktree per worker
- exact owned files or directories
- exact forbidden files or directories or "all other files are out of bounds"
- no nested delegation
- focused tests only
- early inspection and cleanup

In this mode, file ownership is not an implementation detail. It is the safety boundary.

## Model Selection

Default model split for this workflow:

- implementation workers: `gpt-5.3-codex`
- read-only explorer workers: `gpt-5.4-mini`
- verification workers: `gpt-5.4-mini`

Use the smaller/faster verification model by default because verification tasks should be read-only, tightly scoped, and prompt-constrained.

Escalate a verification worker to `gpt-5.4` only when the review question is unusually subtle, contract-heavy, or blocked on nuanced reasoning.

Do not silently downgrade implementation workers to an older or smaller model for contract-sensitive migration work.

## Hard Rules

1. Create a separate sibling git worktree before spawning a coding or verification worker.
2. Never let a worker write in the main checkout.
3. Spawn one worker at a time until the control loop has proven reliable.
4. Every worker prompt must explicitly forbid nested delegation:
   - Do NOT use `spawn_agent`
   - Do NOT use `send_input`
   - Do NOT use `wait_agent`
   - Do NOT use `close_agent`
5. If the repo has an `AGENTS.md` file or named skills relevant to the task, explicitly instruct the worker to read and follow them before implementation.
6. For Go tasks, explicitly require the worker to use the available Go skills and repo guidance rather than silently ignoring them.
7. For Go tasks, include lint setup and lint verification early unless the task is explicitly read-only and lint would add no value.
8. Give every worker an exact write set:
   - exact owned files or directories
   - exact forbidden files or directories or "all other files are out of bounds"
9. Give every worker an exact validation set:
   - exact tests to run
   - no unrelated exploration
10. Require the worker to stop and report if it needs anything outside its assigned files or tests.
11. Inspect the worker early. Do not wait for a long run before checking branch state.
12. Do not treat an empty early diff as a failure by itself. Discovery, contract reading, and behavior alignment are normal before the first edit.
13. Keep at most the 3 latest completed agents around for review. As new completed agents accumulate, close older completed agents promptly so dead threads do not pile up.
14. When you discover a durable orchestration-related lesson during the run, update and commit this skill promptly so the behavior change becomes part of the repository workflow instead of a one-off memory.
15. If a worker violates scope once, stop, clean up, and tighten the boundary before retrying.

## Recommended Flow

1. Confirm the main checkout is clean.
2. Create a sibling worktree and branch for the worker.
3. Spawn one `worker` only.
4. In the worker prompt, specify:
   - worktree path
   - branch name
   - required repo guidance to read first (`AGENTS.md`, relevant skill files)
   - exact owned files or directories
   - exact forbidden files or directories or "all other files are out of bounds"
   - exact task
   - exact tests
   - exact lint command when the task includes Go code
   - no nested subagents
   - stop if blocked
5. Check progress early with:
   - `git status --short`
   - `git diff --stat`
   - first worker output
     Use these to confirm scope and direction, not to require immediate file edits.
     A worker that is still reading the relevant contracts or comparing behavior may be making normal progress even if the diff is still empty.
6. Review the diff and test results before starting the next worker.
7. Only after the implementation slice is stable should a second worker handle parity/docs or verification.
8. Keep only the most recent few completed agents you still need for inspection; close older completed agents as part of the normal control loop.
9. Once a slice has passed its intended verification gate, integrate it back into the main checkout promptly instead of letting completed work sit in side branches.
10. Keep the orchestration loop moving without waiting for the user to ask for the next step. Stop only for a real blocker, a meaningful decision, or explicit user redirection.
11. Remove temporary worktrees and branches when the run is over or reset is needed.

## Worker Prompt Shape

Use constraints like these in the worker prompt:

- Work only in `<worktree-path>` on branch `<branch>`.
- Do not edit the main checkout.
- Do NOT spawn subagents.
- Use model `<model>` for this role (`gpt-5.3-codex` for implementation, `gpt-5.4-mini` for read-only explorer/verification unless explicitly escalated for verification).
- Assigned files: `<list>`
- Forbidden files: `<list>` or "all other files are out of bounds"
- Run only these tests: `<list>`
- Stop and report if you need anything outside this scope.

## Scope Discipline

What may remain implementation detail inside a worker:

- internal function design
- package shape inside the owned slice
- local code structure
- exact test order

What must not remain implementation detail:

- which checkout it may write in
- which files it owns
- which files are forbidden
- whether it may delegate further
- which contract boundary it may change

## Failure Handling

If the run goes wrong:

- close the worker
- inspect repo state immediately
- revert or discard only the worker's isolated changes
- remove temporary worktrees if resetting from scratch
- restart with a smaller scope, not a broader prompt

**If a worker stalls (output file stops growing), kill it and respawn a new worker.** Do not attempt to complete the worker's unfinished edits yourself — mixing orchestrator edits with worker edits breaks the isolation boundary. Two recovery options:
1. **Continue in place:** Spawn a new worker pointing at the same worktree. The new worker inherits the partial state and picks up where the stalled one left off. Preferred when the stalled worker made substantial correct progress.
2. **Clean restart:** Discard changes, recreate worktree from clean state, respawn with a tighter prompt. Preferred when the stalled worker's edits are unreliable or scope-violating.

Before concluding that the control loop is broken, distinguish between:

- normal early exploration: reading contracts, tracing entrypoints, comparing shell behavior, planning the first edits
- actual stall: no substantive worker output for an extended period, explicit blockage, or repeated aimless exploration without converging on owned files

Early `wait_agent` timeouts or an empty `git diff --stat` are not enough on their own to classify the run as broken.

Do not pile more delegation on top of a broken control loop.

## Worktree Creation

**Always create worktrees from the main checkout directory**, never from inside another worktree. Running `git worktree add` from within a worktree creates a nested worktree inside the first one, which confuses worker path resolution. The correct pattern:

```bash
# From main checkout (e.g. /repo):
git worktree add /repo/.claude/worktrees/my-worker my-branch

# WRONG — creates nested worktree:
cd /repo/.claude/worktrees/other-worker
git worktree add .claude/worktrees/my-worker my-branch
```

When a worker needs commits from a prior slice's branch, create the worktree from the main checkout and then merge the dependency branch into it.
