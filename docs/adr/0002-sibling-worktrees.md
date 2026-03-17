# ADR 0002: Use Sibling Worktrees Instead Of Mutating The Target Checkout

## Status

Accepted

## Context

The runtime needs to create branches, run verification, and leave interrupted work resumable without trampling the operator’s main checkout.

## Decision

Create one sibling worktree per issue from `origin/main` and run development and verification there. Keep the target repository’s main working tree intact.

## Consequences

- Operators keep a clean primary checkout while queue work happens elsewhere.
- Interrupted work can be resumed by branch and worktree metadata.
- Queue execution is less likely to mix local operator changes with runtime work.
- The runtime must manage worktree creation, cleanup, and conflict detection explicitly.

## Rejected Alternatives

- Reuse the target repository’s main checkout for execution.
- Clone a fresh temp repository per issue and discard it immediately.
- Depend on agents to avoid local checkout corruption without structural isolation.
