# Post-Migration Architecture

This document describes the Go-primary runtime architecture after the shell-to-Go migration.

## Shell-to-Go Dispatch Pattern

Shell entrypoints in `scripts/` remain the stable public interface. Each script dispatches to the Go runtime binary via exec:

1. The script checks `RUNOQ_RUNTIME_BIN` for a prebuilt binary.
2. If not set, it falls back to `go run` from `RUNOQ_ROOT`.
3. The Go binary receives the subcommand and arguments, executes the logic, and returns JSON on stdout.

The shell entrypoint handles only argument forwarding and exit-code propagation. All business logic lives in Go.

## What Remains Shell-Owned

| Component | Reason |
| --- | --- |
| `issue-runner.sh` | Drives codex development rounds; tightly coupled to codex CLI invocation, streaming output capture, and per-round thread management |
| `scripts/lib/common.sh` (`runoq::gh()`) | Global bot auth: auto-mints app installation token on first `gh` call |
| `gh-auth.sh` | CLI bootstrap token export |
| `plan.sh` | Orchestrates agent invocation for plan decomposition |
| `bin/runoq` | Thin CLI router, repo context export, auth bootstrap |
| Smoke harness (`docs/live-smoke.md`) | Integration test driver |

Everything else dispatches to Go.

## Where To Make Changes

New runtime behavior should go in the `internal/runtime*` Go packages, not in shell scripts.

### Go Module Layout

| Package | Purpose |
| --- | --- |
| `internal/common` | Shared helpers (config loading, JSON utilities) |
| `internal/gh` | GitHub API client wrapper |
| `internal/runtimecli` | CLI dispatch and argument routing |
| `internal/runtimedispatchsafety` | Startup reconciliation and eligibility checks |
| `internal/runtimeissuequeue` | Queue discovery, ordering, label mutation |
| `internal/runtimeorchestrator` | Phase dispatch state machine |
| `internal/runtimereport` | Read-only reporting over state files |
| `internal/runtimestate` | Atomic state persistence and phase validation |
| `internal/runtimeverify` | Ground-truth verification of development rounds |
| `internal/runtimeworktree` | Sibling worktree creation and removal |

Each package has `app.go` (implementation) and `app_test.go` (unit tests). Run `go test ./internal/...` to validate.

## Testing

- **Go unit tests** (`go test ./...`): primary coverage for runtime logic.
- **Live smoke tests** (`docs/live-smoke.md`): opt-in GitHub integration validation.
