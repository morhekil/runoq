# ADR 0003: Use GitHub Issues And PR Comments As The Primary Audit And Control Surface

## Status

Accepted

## Context

Operators need a visible, collaborative place to inspect queue state, PR progress, escalations, and maintenance findings. Local files alone are too opaque and too easy to lose.

## Decision

Use GitHub issues, PRs, labels, and machine-marked comments as the primary operational audit trail and control surface.

## Consequences

- Operators can understand queue state and failures from GitHub without opening local files first.
- Audit markers make comments machine-recognizable and human-readable.
- Maintenance review and mention triage can happen where collaborators already work.
- The runtime becomes tightly coupled to GitHub issue/PR semantics and permissions.

## Rejected Alternatives

- Keep the operational history only in local JSON files.
- Use prompt memory as the primary execution history.
- Hide most runtime activity behind local logs with minimal GitHub visibility.
