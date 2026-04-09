---
name: tdd
description: Code writing skill enforcing red/green/refactor TDD workflow with continuous docs, tests, and small meaningful commits.
---

# TDD Code Writing Workflow

When writing code, follow the red/green/refactor cycle strictly. Every feature or change begins with a failing test.
A TDD cycle is not complete until refactoring has been done and green state is committed.

This skill's commit gate overrides any default habit of deferring commits unless the user explicitly says not to commit.
Requests like "continue implementation" do not permit batching multiple TDD cycles into one uncommitted patch.

## Workflow

### 1. Red — write a failing test first

- Write the smallest test that expresses the next requirement.
- Run the test suite — confirm the new test fails and all existing tests still pass.
- Do NOT write implementation code yet.

### 2. Green — make it pass

- Write the minimum implementation to make the failing test pass.
- Run the test suite — confirm all tests pass (new and existing).
- Do NOT refactor yet.

### 3. Refactor — clean up

- Improve the implementation: remove duplication, clarify naming, simplify structure.
- Run the test suite — confirm all tests still pass.
- If the refactor is non-trivial, commit it separately from the green step.

### 4. Commit Gate

After green and refactor, you must stop and do all of the following before any further analysis, edits, or new tests:

1. Run the relevant tests and confirm they are green.
2. Inspect `git status --short`.
3. Commit the completed TDD cycle.

Do not continue to the next TDD cycle until the green state is committed.
The only exception is when the user explicitly instructs you not to commit.

- Each commit must have tests green before and after.
- Commit messages should describe the _what_ and _why_, not the mechanics of TDD.
- Prefer many small, meaningful commits over large batches.

### 5. Repeat

Go back to step 1 for the next requirement.

## Docs and coverage

- Update documentation as you go, not as a separate phase at the end.
- Document architectural changes, operational flows, and non-obvious decisions in the appropriate place (doc comments, README, design docs — whatever the project uses).
- Aim for as close to 100% test coverage as practical. When you add a code path, add a test for it. When you change behavior, update the corresponding test.
- If you skip an edge case, you must provide an explanation that would satisfy a senior engineer reviewing the code later.

## Rules

- Never commit with failing tests.
- Never write implementation before the test that requires it.
- Never batch multiple unrelated changes into one commit.
- Never batch multiple completed TDD cycles into one uncommitted change set.
- Keep each TDD cycle small — one behavior per cycle.
- When fixing a bug, first write a test that reproduces it (red), then fix (green).
- A completed red/green/refactor cycle MUST end with a commit.
- Do not start the next cycle while the previous green state is uncommitted.
- If unrelated local changes exist, commit only the files for the completed cycle and leave unrelated changes unstaged.
- If code was changed under this skill and the user did not explicitly forbid commits, the final response must include the commit SHA(s) or a concrete blocker that prevented committing.
