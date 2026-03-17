# ADR 0001: Shell Scripts And JSON Contracts Define The Deterministic Boundary

## Status

Accepted

## Context

`agendev` coordinates GitHub state, local worktrees, verification, recovery, and maintenance review around LLM-driven workflows. Prompt text alone is too hard to test, recover, and review for these responsibilities.

## Decision

Put deterministic behavior in shell scripts and machine-readable JSON contracts. Agents and skills should call those scripts instead of reimplementing the same rules in prompt prose.

## Consequences

- Queue ordering, PR lifecycle, auth, verification, and state transitions can be tested with Bats and fixtures.
- Recovery behavior is reviewable in code instead of hidden in prompts.
- Prompt changes stay thinner and safer because they dispatch into stable contracts.
- Changing a contract now requires more discipline because other scripts and tests may depend on it.

## Rejected Alternatives

- Put most orchestration logic in prompts.
- Allow each agent to handcraft its own `gh` calls and output shapes.
- Use prose-only contracts between shell and prompt layers.
