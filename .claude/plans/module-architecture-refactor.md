# Module architecture refactor

## Context

Go code calls shell scripts that call back into Go (Go→shell→Go roundtrips). Shell scripts should be entry points only. The `internal/common/` package is a grab bag. The tick state machine lives in shell (tick.sh, 27KB). This refactor reorganizes modules around domain boundaries and eliminates internal shell roundtrips.

## Target architecture

```
cmd/runoq/main.go                  # entry point

# Domain packages (top-level) — "what runoq does"
orchestrator/                      — lifecycle state machine (tick + run + loop)
  ├──► planning/
  ├──► implementation/
  ├──► comments/
  ├──► issuequeue/
  └──► agents/

planning/                          — proposal decomposition, approval, materialization
  ├──► issuequeue/
  ├──► comments/
  └──► agents/

implementation/                    — issue execution (codex rounds, verification)
  ├──► issuequeue/
  ├──► comments/
  └──► agents/

comments/                          — comment processing, reactions, selection parsing
  └──► internal/gh

issuequeue/                        — issue CRUD, labels, status
  └──► internal/gh

agents/                            — claude/codex invocation, capture, streaming
  └──► internal/shell

# Infrastructure packages (internal/) — "how it does it"
internal/cli/                      — CLI routing, help text, arg parsing
internal/shell/                    — CommandExecutor, env helpers, FileExists
internal/gh/                       — GitHub API client, auth, git helpers
internal/state/                    — state file persistence
internal/verify/                   — test/build verification
internal/worktree/                 — git worktree management
```

No cycles. Domain packages depend on infrastructure, never the reverse.

## Milestones

### M1: Split internal/common → internal/shell + internal/cli

**Goal:** Eliminate the grab-bag common package. Establish infrastructure layer.

**Tasks:**
1. Create `internal/shell/` with CommandExecutor, RunCommand, CommandOutput, EnvLookup, EnvSet, FileExists
2. Move Fail, Failf, WriteJSON, ExitCodeFromError into `internal/cli/`
3. Move BranchSlug into `internal/gh/`
4. Update all imports across 10+ packages
5. Delete `internal/common/`

**Deliverable:** `internal/common/` no longer exists. All packages compile and tests pass.

**Acceptance:**
```
go test ./... -count=1
bats test/tick.bats
bats test/plan_dispatch.bats
bats test/issue_queue.bats
```

---

### M2: Create agents/

**Goal:** Top-level package for AI agent invocation (claude + codex), replacing captured_exec/claude_stream shell functions.

**Tasks:**
1. Create `agents/` with Backend type (Claude, Codex)
2. Implement Invoke(ctx, opts) → Response (captures stdout, stderr, logs)
3. Implement Stream(ctx, opts) → Response (live progress, used by planning)
4. Port `runoq::captured_exec` and `runoq::claude_stream` logic from common.sh
5. Wire into issuerunner (codex) and plan-comment-handler (claude)

**Deliverable:** Agent invocation goes through Go, not shell.

**Acceptance:**
```
go test ./agents/ -v
bats test/tick.bats
bats test/plan_dispatch.bats
source .env.smoke-sandbox && scripts/smoke-tick.sh run
```

---

### M3: Create comments/

**Goal:** Top-level package for comment processing — reactions, unresponded detection, agent response parsing, selection parsing.

**Tasks:**
1. Move from `internal/tick/`: FindUnrespondedCommentIDs, ParseHumanCommentSelection, ParseAgentResponse, AgentResponse types
2. Add reaction functions: AddReaction(ctx, commentID, content)
3. Wire into orchestrator and planning

**Deliverable:** All comment processing callable as Go functions.

**Acceptance:**
```
go test ./comments/ -v
bats test/tick.bats
```

---

### M4: Create planning/

**Goal:** Top-level planning domain package — proposal formatting, decomposition, approval, materialization.

**Tasks:**
1. Move from `internal/tick/`: all format/parse/filter functions and types
2. Add proposal dispatch logic (from plan-dispatch.sh)
3. Add comment handler logic (from plan-comment-handler.sh)
4. Add materialization logic (from tick.sh handle_approved_planning/adjustment)
5. Delete `internal/tick/`

**Deliverable:** Planning lifecycle callable as Go functions.

**Acceptance:**
```
go test ./planning/ -v
bats test/tick.bats
bats test/plan_dispatch.bats
bats test/tick_helpers.bats
source .env.smoke-sandbox && scripts/smoke-tick.sh run
```

---

### M5: Move tick state machine to orchestrator/

**Goal:** Tick runs in Go. tick.sh deleted.

**Tasks:**
1. Move `internal/orchestrator/` → `orchestrator/` (top-level)
2. Implement tick state machine (from tick.sh main + handlers)
3. Loop command calls orchestrator.RunTick() directly
4. Delete tick.sh, tick-fmt.sh
5. Move `internal/issuequeue/` → `issuequeue/` (top-level, used by orchestrator + planning)

**Deliverable:** `runoq tick` and `runoq loop` run entirely in Go.

**Acceptance:**
```
go test ./orchestrator/ -v
go test ./... -count=1
source .env.smoke-sandbox && scripts/smoke-tick.sh run
```

---

### M6: Create implementation/ and eliminate shell roundtrips

**Goal:** Implementation domain as top-level package. No Go→shell→Go roundtrips remain.

**Tasks:**
1. Move `internal/issuerunner/` → `implementation/` (top-level)
2. orchestrator: call issuequeue, dispatchsafety, state, verify, worktree as Go packages directly
3. implementation: call state, verify as Go packages directly
4. Add PR lifecycle Go functions (from gh-pr-lifecycle.sh)
5. Shell scripts remain as CLI entry points only

**Deliverable:** No Go→shell→Go roundtrips. Shell scripts are thin wrappers.

**Acceptance:**
```
go test ./... -count=1
source .env.smoke-sandbox && scripts/smoke-tick.sh run
source .env.smoke-sandbox && bats test/live_smoke.bats
```

---

## Migration strategy

Each milestone is independently shippable. The system works after each one — shell wrappers continue to function as entry points even as internals move to Go. Tests run after every task.

Order is strict: M1 → M2 → M3 → M4 → M5 → M6. Each builds on the previous.

### Test strategy

Two layers of coverage:

1. **Go unit tests** — test domain logic directly (planning, orchestrator, comments, agents, etc.)
2. **Bats integration tests** — test shell wrapper contracts (correct CLI args → correct output through the full wrapper → Go → output path)

Bats tests stay but evolve: as internal logic moves to Go, the bats tests simplify from testing shell business logic to testing the wrapper integration. This ensures 100% coverage of both the Go core and the shell entry points.

Live smoke tests stay unchanged — they test the full end-to-end flow against real GitHub.

## Verification (full suite, run after each milestone)

```bash
# Unit tests
go test ./... -count=1 -v

# Integration tests (shell wrappers)
bats test/tick.bats
bats test/tick_helpers.bats
bats test/tick_fixtures.bats
bats test/plan_dispatch.bats
bats test/setup.bats
bats test/issue_queue.bats

# Live smoke (full end-to-end against real GitHub)
source .env.smoke-sandbox && scripts/smoke-tick.sh run
source .env.smoke-sandbox && bats test/live_smoke_tick.bats
source .env.smoke-sandbox && bats test/live_smoke_planning.bats
source .env.smoke-sandbox && bats test/live_smoke.bats
```

### Per-milestone smoke test requirements

| Milestone | Smoke tests required |
|---|---|
| M1 (common split) | smoke-tick (regression) |
| M2 (agents) | smoke-tick + smoke-lifecycle (agent invocation) |
| M3 (comments) | smoke-tick (comment reactions + processing) |
| M4 (planning) | smoke-tick + smoke-planning (proposal lifecycle) |
| M5 (orchestrator) | smoke-tick + smoke-planning + smoke-lifecycle |
| M6 (roundtrip elimination) | all smoke tests |
