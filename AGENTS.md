# agendev

This repository implements the deterministic shell/runtime layer for GitHub-backed agentic development orchestration.

## Priorities

- Keep GitHub operations in scripts, not prompts.
- Prefer structured JSON output over prose for script boundaries.
- Preserve the target project's main working tree; use sibling worktrees for execution.
- Treat `.agendev/state/*.json` as recovery breadcrumbs, not the audit trail.

## Development guidelines

Read `docs/development-guidelines.md` for detailed implementation and testing guidance based on the lessons learned during development.

## Testing

- Add or update Bats coverage when changing shell behavior.
- Use fake `gh` fixtures for deterministic integration tests.
- Keep prompts thin; most invariants belong in scripts.
