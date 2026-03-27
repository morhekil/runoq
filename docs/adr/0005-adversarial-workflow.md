# ADR 0005: Adversarial Acceptance Testing Workflow

## Status

Accepted

## Context

The existing workflow has a natural separation between implementation (codex) and review (diff-reviewer), but acceptance criteria are defined in the issue body, not by a dedicated agent. The reviewer scores diffs against general quality (PERFECT-D), not against task-specific acceptance tests. This means functional correctness against the specification is only checked indirectly.

## Decision

Introduce a bar-setter agent that writes acceptance tests from the specification before implementation begins. The implementer (codex) must satisfy these tests without modifying them. This creates an adversarial dynamic where the test author and implementer have information asymmetry — bar-setter doesn't know how codex will implement, codex doesn't know what bar-setter will test.

Key decisions:

- **Bar-setter reads specs only, never source code** — preserves information asymmetry
- **Criteria protection is commit-based, not path-based** — files from bar-setter's commit must be unchanged at HEAD
- **Complexity gates the criteria phase** — low complexity tasks skip bar-setter entirely
- **Epic/task hierarchy** — epics get integration-level criteria, tasks get unit-level criteria
- **Orchestrator and issue-runner demoted to shell scripts** — their work is deterministic dispatch, not reasoning
- **Mention triage uses haiku for classification** — cheap structured-output call, not a full agent

## Consequences

- Additional opus invocation per medium+ task for bar-setter
- Richer smoke test fixtures needed (epic + mixed complexity)
- verify.sh extended with tamper check and integrate subcommand
- Issue metadata extended with type, parent_epic, children fields
- plan-to-issues skill updated to produce hierarchical issue structures

## Rejected Alternatives

- Keep acceptance criteria as free-form text in issue bodies without executable tests
- Use the same agent for both criteria authoring and implementation
- Protect criteria files by path convention instead of commit identity
- Run bar-setter on all tasks regardless of complexity
