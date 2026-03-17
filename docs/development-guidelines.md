# Development Guidelines

This document now serves as a short lessons-learned companion to the more durable contributor docs.

For day-to-day contributor guidance, start with:

- [Contributor testing guide](./contributing/testing.md)
- [Agent and skill guidelines](./contributing/agent-and-skill-guidelines.md)
- [Script contract reference](./reference/script-contracts.md)

## What This Project Learned

These points are worth preserving because they explain why the repo is structured the way it is.

- Keep orchestration rules in shell scripts and JSON contracts. The more behavior that lives in prompts, the harder it is to recover, test, and review.
- Resume behavior needs explicit terminal-state handling. Maintenance only became safe after treating `COMPLETED` as idempotent instead of replaying its final summary.
- Deterministic fake-`gh` integration coverage should carry almost all of the load. Real GitHub validation still matters, but only for auth, attribution, permission, and cleanup edges that fixtures cannot prove.
- Live smoke tests must stay opt-in and credential-gated. They should never affect normal local `bats` runs.
- Diff review after green tests still finds real issues. Bugs in duplicated maintenance summaries and auth-refresh behavior were both caught after otherwise successful validation.

## Ongoing Guardrails

- Preserve the target repo main checkout. Execution work belongs in sibling worktrees or explicit sandbox clones.
- Treat `.agendev/state/*.json` as resumability breadcrumbs, not the audit trail.
- Prefer machine-readable script boundaries over prose-only prompt contracts.
- Reuse existing scripts, helpers, fixtures, and skills before inventing new prompt behavior.
- Keep maintenance review read-only until a human explicitly approves filing work.

## How To Use This Document

Use this file when you need context on why a guideline exists.

Use the linked contributor docs when you need:

- testing instructions
- prompt and skill boundary rules
- shell entrypoint contracts
- operator-facing runtime behavior
