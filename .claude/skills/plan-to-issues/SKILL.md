---
name: plan-to-issues
description: Slice a local plan document into GitHub issues organized into an epic/task hierarchy. Superseded by scripts/plan.sh + milestone-decomposer/task-decomposer agents.
---

# plan-to-issues

> **Note:** This skill is superseded by `scripts/plan.sh` plus the `milestone-decomposer`
> and `task-decomposer` agents. Use `runoq plan <file>` instead, which first slices the
> plan into milestones, then expands each milestone into tasks while handling confirmation
> and issue creation deterministically in a shell script.
> This skill definition is retained for reference.

Use this skill to slice a local plan document into GitHub issues, organized into an epic/task hierarchy when appropriate.

## Process

1. Read the requested local plan file from disk before proposing anything.
2. Reuse `"$RUNOQ_ROOT/templates/issue-template.md"` as the issue-body shape. Do not invent a parallel template in prompt text.
3. **Detect epics.** Before proposing individual issues, identify epic-sized chunks in the plan:
   - Plan sections that span multiple subsystems.
   - Sections with 3 or more subtasks.
   - Sections that represent a cohesive feature or milestone.
   Flag these as epics rather than individual issues.
4. Propose a hierarchical issue structure:
   - **Epics**: scope summary and integration-level acceptance criteria (criteria that only pass when the children work together).
   - **Tasks**: small, independently testable issues nested under their parent epic. Each task carries:
     - `type: task`
     - `parent_epic: <epic issue number>` (filled in after epic creation)
     - `estimated_complexity: low | medium | high`
   - The complexity estimate determines whether bar-setter (acceptance test agent) runs:
     - `low` — bar-setter is skipped; the task is trivial enough that implementation-time checks suffice.
     - `medium` / `high` — bar-setter writes acceptance tests before implementation begins.
   - Standalone issues that do not belong to any epic are still allowed; treat them as tasks with no parent.
5. Flag bad granularity before creating issues:
   - Too broad: more than 5 acceptance criteria, multiple subsystems, or a plan that obviously needs further decomposition. **Suggest splitting into an epic with child tasks.**
   - Too narrow: trivial rename or formatting-only work with no behavioral impact. **Suggest as a `low`-complexity task (bar-setter will be skipped).**
   - Right-sized but part of a larger feature: **suggest as a `medium`-complexity task under an epic.**
   - Missing testability: no verifiable acceptance criteria or no observable outcome.
6. Present the proposed issue queue to the user for confirmation before creating anything. The confirmation step must clearly show:
   - The epic/task tree (epics first, child tasks indented beneath).
   - Complexity assignments for every task, each with a 1-2 sentence rationale explaining *why* (e.g., "medium — touches two modules and introduces a new public API surface").
   - Which tasks will trigger bar-setter and which will not.
   - Dependency graph and any granularity warnings.
   This must include explicit confirmation before creating GitHub issues.
   **Auto-confirm mode:** If the environment variable `RUNOQ_AUTO_CONFIRM=1` is set, skip the confirmation step and proceed directly to issue creation. This is used in CI and automated evaluation contexts.
7. Only after confirmation (or auto-confirm), create issues through `"$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create`:
   - **Create epics first** using `--type epic`.
   - **Then create child tasks** using `--type task --parent-epic <N>` where `<N>` is the epic issue number.
   - **Pass `--complexity-rationale "<rationale>"`** on every task to record *why* the complexity level was chosen. This rationale is stored in the issue metadata and displayed on PRs.
   - **After all children are created**, update each epic with `--children N,M,O` to record the full child list.
   Do not hand-write `gh issue create` calls.
8. After creation, summarize the created issue numbers, the epic→task hierarchy, and the dependency graph so the queue order is visible.

## Output contract

- Proposal phase:
  - Show the epic/task tree: epics listed with scope and integration-level acceptance criteria, child tasks indented beneath with title, acceptance criteria, dependencies, complexity estimate, and a short rationale.
  - Show complexity assignments and indicate which tasks will trigger bar-setter (medium/high) and which will not (low).
  - Call out whether the source plan is too broad, too narrow, or untestable before proceeding.
- Creation phase:
  - Use `"$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create` for each approved issue with the appropriate flags:
    - `--type epic|task`
    - `--parent-epic N` (for child tasks)
    - `--estimated-complexity low|medium|high`
    - `--complexity-rationale "<rationale>"`
    - `--children N,M,O` (for updating epics after children are created)
  - Reuse `"$RUNOQ_ROOT/templates/issue-template.md"` language and section structure.
  - Finish with the dependency graph including epic→task relationships and the created issue links or numbers.
  - Emit a final structured payload for automated consumption:

````markdown
<!-- runoq:payload:plan-to-issues -->
```json
{
  "issues": [
    {"number": 1, "type": "epic", "title": "...", "complexity": null, "children": [2, 3]},
    {"number": 2, "type": "task", "title": "...", "complexity": "medium", "complexity_rationale": "...", "parent_epic": 1, "depends_on": []},
    {"number": 3, "type": "task", "title": "...", "complexity": "low", "complexity_rationale": "...", "parent_epic": 1, "depends_on": [2]}
  ]
}
```
````
