# plan-to-issues

Use this skill to slice a local plan document into GitHub issues.

## Process

1. Read the requested plan file from disk.
2. Propose small, independently testable issues.
3. Flag bad granularity:
   - Too broad: more than 5 acceptance criteria or multiple subsystems.
   - Too narrow: trivial rename or one-line change without behavioral impact.
   - Missing testability: no verifiable acceptance criteria.
4. Present the proposed issue queue to the user for confirmation before creating anything.
5. When confirmed, call `gh-issue-queue.sh create` and reuse `templates/issue-template.md`.
6. Show the resulting dependency graph after creation.
