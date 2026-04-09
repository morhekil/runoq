# Agent And Skill Guidelines

This guide explains how prompts, skills, and shell scripts should interact in `runoq`.

The short version: prompts dispatch and summarize; scripts own deterministic behavior.

## Core Rule

If a behavior needs to be:

- testable
- recoverable
- stable across prompt changes
- machine-consumed by another script
- auditable after the fact

it belongs in a shell script or JSON contract, not in prompt text.

## What Belongs In Scripts

Scripts should own:

- queue ordering and dependency resolution
- label transitions
- worktree paths and branch naming
- PR creation and update mechanics
- auth and token minting
- local state transitions
- payload extraction and normalization
- verification checks
- mention polling and permission checks
- maintenance issue filing and finding state changes

If you are tempted to describe one of those rules in prose and have the model “remember” it, that is a sign the logic belongs in a script instead.

## What Belongs In Agents And Skills

Agents and skills should own:

- reading repository context and instructions
- choosing which documented script action to call next
- summarizing queue state or findings for a human
- bounded judgment calls that are not yet deterministic contracts
- deciding whether the situation is blocked, ambiguous, or needs escalation

They should not own the GitHub mutation mechanics or replay the same parsing logic already implemented in shell.

## Prompt Discipline

### Thin-prompt rules

Good prompts in this repo:

- name the script or skill to use
- define scenario coverage and stop conditions
- preserve audit-marker requirements
- avoid duplicating parsing or mutation logic already implemented elsewhere

Bad prompts in this repo:

- restate label-resolution rules in prose
- tell the model to handcraft `gh` commands when a repo script already exists
- hide state-transition logic in English instead of using `state.sh`
- rely on a model to remember queue ordering or dependency behavior without calling the queue script

## Scripts Before Direct `gh`

When a repository script exists for a GitHub operation, use it instead of direct `gh` commands.

Examples:

- queue operations: use `gh-issue-queue.sh`
- PR lifecycle and review comments: use `gh-pr-lifecycle.sh`
- auth: use `gh-auth.sh`
- maintenance triage and findings: use `maintenance.sh`

Direct `gh` calls are acceptable only when:

- no script contract exists yet, and
- the behavior genuinely belongs in the prompt layer, and
- you are prepared to codify it later if it becomes stable or important

## Audit Marker Requirements

The prompt layer must preserve the repo’s audit markers.

Required markers include:

- `<!-- runoq:bot -->`
- `<!-- runoq:payload:github-orchestrator-dispatch -->`
- `<!-- runoq:payload:codex-return -->`
- `<!-- runoq:payload:orchestrator-return -->`

Prompt authors must not:

- invent alternate markers
- remove existing markers from template-driven comments
- treat audit payload comments as actionable review input on rereads

Audit markers are part of the machine-readable contract.

## JSON Payload Discipline

If a prompt exchanges structured data with another layer:

- prefer JSON
- keep field names stable
- document required and optional fields
- let shell validation normalize or reject malformed payloads

The prompt should not compensate for missing validation by quietly changing the contract in prose.

This is why `state.sh validate-payload` exists: prompt output is allowed to be imperfect, but the repair rules belong in code.

## Good Vs Bad Boundaries

### Queue logic

Good:

- skill calls `gh-issue-queue.sh next`
- prompt surfaces `blocked_reasons` exactly as returned

Bad:

- prompt fetches all issues directly
- prompt reimplements dependency ordering in natural language

### PR lifecycle

Good:

- skill calls `gh-pr-lifecycle.sh create`, `comment`, `update-summary`, and `finalize`
- prompt respects summary and attention markers

Bad:

- prompt rewrites the full PR body manually
- prompt posts ad hoc comments without required markers for operational events

### Maintenance review

Good:

- agent keeps review read-only until a human triage comment approves filing
- script turns approved findings into queue issues

Bad:

- prompt creates follow-up issues immediately because a finding “looks important”
- prompt interprets collaborator permissions without calling the permission-check script

## When To Add A Script Instead Of Expanding A Prompt

Add or extend a script when any of these are true:

- the prompt would need the same rule in multiple places
- a test could reasonably prove the behavior
- the output will be consumed by another script or workflow
- recovery after interruption depends on the behavior
- reviewers would struggle to verify the behavior by reading prompt prose alone

## Skill Authoring Guidance

Skills in this repo should:

- expose a small set of deterministic actions
- point to the exact script command to run
- tell the model what not to reimplement
- surface script output as-is where possible

The existing skills are the pattern to copy:

- `issue-queue` wraps `gh-issue-queue.sh`
- `pr-lifecycle` wraps `gh-pr-lifecycle.sh`
- `plan-to-issues` requires confirmation before issue creation and routes creation through the queue script

## Agent Authoring Guidance

Agents should:

- read `AGENTS.md`, config, and exported repo context first
- call reconciliation or setup scripts before taking new action
- encode scenario coverage and stop conditions explicitly
- escalate rather than guess when deterministic scripts say the state is blocked or unsafe

Agents should not:

- mutate the target source tree directly if that responsibility belongs elsewhere
- bypass queue, state, verification, or PR scripts
- treat prompts as the source of truth for deterministic workflow rules

## Review Checklist For Prompt Changes

Before merging a prompt or skill change, ask:

- Did any deterministic rule move into prompt prose?
- Could this behavior be covered better by a script and Bats test?
- Are audit markers still preserved?
- Does the change create any new JSON or comment contract that should be codified elsewhere?
- Does the prompt now duplicate logic already implemented by a script?

If the answer to any of those is yes, raise the bar before merging.

## Related Docs

- [Architecture overview](../architecture/overview.md)
- [Execution and maintenance flows](../architecture/flows.md)
- [Script contract reference](../reference/script-contracts.md)
- [Contributor testing guide](./testing.md)
