# runoq

This repository implements the deterministic runtime layer for GitHub-backed agentic development orchestration.

## Core principles

- Keep durable behavior in code and machine-readable contracts, not in prompts.
- Prefer structured outputs over prose at script and tool boundaries.
- Treat GitHub as the audit and control surface.
- Treat `.runoq/state/*.json` as local recovery breadcrumbs, not the system of record.
- Preserve the target repository's main checkout; do execution work in sibling worktrees or explicit sandboxes.

## Development guidelines

- Keep the CLI thin and push stable logic into reusable runtime components.
- Prefer small, composable commands with explicit inputs, outputs, and side effects.
- Preserve existing contracts unless there is an intentional, documented versioned change.
- Favor deterministic behavior over cleverness.
- Reuse existing helpers, fixtures, and workflows before adding new ones.
- Keep prompts and agents thin; they should call stable repository logic rather than reimplement it.
- Focus on modularity and testability. Avoid monolithic scripts or agents that do everything.

## Testing and validation

- Add or update deterministic tests whenever behavior changes.
- Use fake `gh` fixtures for integration-style coverage where possible.
- Use live smoke lanes for real GitHub and real LLM validation, but keep them opt-in and credential-gated.
- Validate both happy paths and recovery or failure paths.

## Documentation

- Update docs when changing operator-visible behavior, contracts, or architecture.
- Read `docs/development-guidelines.md` for the more detailed contributor guidance.
