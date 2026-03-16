# plan-to-issues

Use this skill to slice a local plan document into GitHub issues.

## Process

1. Read the requested local plan file from disk before proposing anything.
2. Reuse [templates/issue-template.md](../../../templates/issue-template.md) as the issue-body shape. Do not invent a parallel template in prompt text.
3. Propose small, independently testable issues with explicit acceptance criteria and dependency ordering.
4. Flag bad granularity before creating issues:
   - Too broad: more than 5 acceptance criteria, multiple subsystems, or a plan that obviously needs further decomposition. See `test/fixtures/plans/broad-example.md`.
   - Too narrow: trivial rename or formatting-only work with no behavioral impact. See `test/fixtures/plans/narrow-example.md`.
   - Missing testability: no verifiable acceptance criteria or no observable outcome. See `test/fixtures/plans/untestable-example.md`.
5. Present the proposed issue queue, dependency graph, and any granularity warnings to the user for confirmation before creating anything. This must include explicit confirmation before creating GitHub issues.
6. Only after confirmation, create issues through `scripts/gh-issue-queue.sh create`. Do not hand-write `gh issue create` calls.
7. After creation, summarize the created issue numbers and dependency graph so the queue order is visible.

## Output contract

- Proposal phase:
  - List each proposed issue with title, acceptance criteria, dependencies, and a short rationale.
  - Call out whether the source plan is too broad, too narrow, or untestable before proceeding.
- Creation phase:
  - Use `scripts/gh-issue-queue.sh create` for each approved issue.
  - Reuse `templates/issue-template.md` language and section structure.
  - Finish with the dependency graph and the created issue links or numbers.
