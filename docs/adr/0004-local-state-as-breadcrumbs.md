# ADR 0004: Use Local State Files As Resumability Breadcrumbs, Not The Audit Trail

## Status

Accepted

## Context

The runtime needs local data to resume interrupted runs, deduplicate mentions, and detect stale queue state. At the same time, the long-lived audit trail belongs somewhere shared and visible.

## Decision

Use `.runoq/state/*.json` only as local resumability breadcrumbs and short-term execution state, not as the system audit trail.

## Consequences

- Reconciliation can reason about interrupted runs from local state.
- Mention polling can deduplicate by comment ID safely.
- Operators still need GitHub comments and labels to reconstruct the authoritative history.
- Corrupted or missing local state becomes a recovery problem, not a history loss problem, because GitHub remains the durable audit surface.

## Rejected Alternatives

- Treat local state files as the primary history of record.
- Store no local state and rely only on GitHub for resumability.
- Blend audit history and recovery breadcrumbs into one mutable local file.
