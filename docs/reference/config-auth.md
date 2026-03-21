# Configuration And Authentication Reference

This document explains how `runoq` resolves configuration, repository identity, GitHub authentication, and mention authorization.

## Resolution Order

At runtime, `runoq` resolves configuration and identity in this order:

1. `RUNOQ_ROOT` if already exported, otherwise from the CLI/script location
2. `RUNOQ_CONFIG` if set, otherwise `config/runoq.json` under `RUNOQ_ROOT`
3. `TARGET_ROOT` if set, otherwise the current git toplevel
4. `RUNOQ_REPO` if set, otherwise the `origin` GitHub remote converted to `owner/repo`
5. `GH_TOKEN` if already present and `RUNOQ_FORCE_REFRESH_TOKEN` is unset
6. `.runoq/identity.json` plus the GitHub App private key when a fresh installation token must be minted

## `config/runoq.json`

The default runtime config lives at [`config/runoq.json`](../../config/runoq.json).

### Keys And Consumers

| Key | Purpose | Primary consumers |
| --- | --- | --- |
| `labels.ready` | Queue label for dispatchable issues | `gh-issue-queue.sh`, `run.sh`, `setup.sh` |
| `labels.inProgress` | Queue label for active work | `gh-issue-queue.sh`, `dispatch-safety.sh`, `run.sh`, `setup.sh` |
| `labels.done` | Queue label for completed issues | `gh-issue-queue.sh`, `dispatch-safety.sh`, `run.sh`, `setup.sh` |
| `labels.needsReview` | Queue label for human escalation | `gh-issue-queue.sh`, `run.sh`, `setup.sh` |
| `labels.blocked` | Queue label for blocked work | `gh-issue-queue.sh`, `setup.sh` |
| `labels.maintenanceReview` | Tracking issue label for maintenance review | `maintenance.sh`, `setup.sh` |
| `identity.appSlug` | GitHub App slug expected on the repo installation and used for bot attribution | `setup.sh`, smoke scripts |
| `identity.handle` | Mention handle used by agents and scripts | agent prompts and mention flows |
| `authorization.minimumPermission` | Minimum collaborator permission required to act on mentions | `mentions.sh`, `gh-pr-lifecycle.sh` |
| `authorization.denyResponse` | Denial behavior for unauthorized mentions | `mentions.sh` |
| `maxRounds` | Maximum development/review loop rounds | `run.sh`, agent payloads |
| `maxTokenBudget` | Total token budget passed into orchestration | `run.sh`, agents |
| `tokenCost.*` | Per-million token pricing for reports | `report.sh` |
| `autoMerge.enabled` | Auto-merge policy setting | currently informational; finalization logic is enforced in `run.sh` |
| `autoMerge.requireVerification` | Auto-merge gating expectation | currently informational; verification is always run in `run.sh` |
| `autoMerge.requireZeroCritical` | Auto-merge review expectation | currently informational; not independently enforced in shell code |
| `autoMerge.maxComplexity` | Intended complexity threshold for auto-merge | current shell logic hard-codes low-complexity auto-merge in `run.sh` |
| `reviewers[]` | First reviewer/assignee for needs-review finalization | `run.sh` |
| `branchPrefix` | Prefix for per-issue branches | `common.sh`, `worktree.sh`, `dispatch-safety.sh` |
| `worktreePrefix` | Prefix for sibling worktree directories | `common.sh`, `worktree.sh` |
| `consecutiveFailureLimit` | Circuit-breaker threshold in queue mode | `run.sh` |
| `verification.testCommand` | Command run inside the worktree for test verification | `verify.sh` |
| `verification.buildCommand` | Command run inside the worktree for build verification | `verify.sh` |
| `stall.timeoutSeconds` | Watchdog inactivity timeout | `watchdog.sh`, `run.sh` |

### Notes On Config Drift

The `autoMerge.*` block expresses desired policy, but the current shell implementation only enforces a subset directly. Today:

- verification is always required before successful finalization
- low issue complexity is required for auto-merge
- caveats or non-`PASS` verdicts force `needs-human-review`

If you change config in this area, verify whether the shell code also needs to change.

## Identity Files

### `.runoq/identity.json`

`runoq init` creates [`TARGET_ROOT/.runoq/identity.json`](../../scripts/setup.sh) with this shape:

```json
{
  "appId": 123,
  "installationId": 789,
  "privateKeyPath": "/absolute/or/tilde/path/to/app-key.pem"
}
```

Field meaning:

- `appId`: numeric GitHub App ID supplied by `RUNOQ_APP_ID` or looked up from the public app slug
- `installationId`: numeric installation ID for the target repo
- `privateKeyPath`: path recorded during `init`; `gh-auth.sh` also allows `RUNOQ_APP_KEY` to override it later

If the file is missing, `gh-auth.sh` exits with `Run 'runoq init' first.` unless an existing `GH_TOKEN` is already available.

## Environment Variables

### Core runtime variables

| Variable | Purpose | Notes |
| --- | --- | --- |
| `RUNOQ_ROOT` | Runtime repo root | Usually exported by `bin/runoq` automatically |
| `RUNOQ_CONFIG` | Override config path | Useful for alternate config files and tests |
| `TARGET_ROOT` | Override target repo root | Skips git toplevel detection when set |
| `RUNOQ_REPO` | Override `owner/repo` | Bypasses remote parsing |
| `RUNOQ_STATE_DIR` | Override local state directory | Primarily useful for tests and specialized workflows |
| `RUNOQ_BASE_REF` | Override the worktree base ref | Defaults to `origin/main` |
| `RUNOQ_SYMLINK_DIR` | Override where `runoq init` writes the symlink | Defaults to `/usr/local/bin` |
| `RUNOQ_CLAUDE_BIN` | Override the Claude executable name/path | Defaults to `claude` |

### Authentication variables

| Variable | Purpose | Notes |
| --- | --- | --- |
| `GH_TOKEN` | Reused GitHub token | Preferred by `gh-auth.sh` unless refresh is forced |
| `RUNOQ_APP_ID` | Bootstrap GitHub App ID for `runoq init` | Required for private GitHub Apps; optional for public apps if slug lookup works |
| `RUNOQ_APP_KEY` | Override GitHub App private key path | Used by both `setup.sh` and `gh-auth.sh` |
| `RUNOQ_FORCE_REFRESH_TOKEN` | Force minting a fresh installation token | Ignores an existing `GH_TOKEN` when set |

### Auth precedence

`gh-auth.sh export-token` behaves like this:

1. If `GH_TOKEN` is set and `RUNOQ_FORCE_REFRESH_TOKEN` is unset, return it unchanged.
2. Otherwise, if `RUNOQ_TEST_GH_TOKEN` is set, return that test token.
3. Otherwise, load `.runoq/identity.json`, resolve the private key path, mint a JWT, and call `POST /app/installations/<installationId>/access_tokens`.

This means a shell session with an exported `GH_TOKEN` will bypass GitHub App minting unless you explicitly force refresh.

## GitHub App Authentication Flow

### During `runoq init`

`runoq init` is the bootstrap phase:

- It does not call `gh-auth.sh`.
- It resolves the app ID from `RUNOQ_APP_ID` when present. If `RUNOQ_APP_ID` is unset, it falls back to looking up the public app by slug.
- It mints a short-lived app JWT from the app ID and private key, then resolves the repo installation ID from the GitHub App installation API.
- It uses operator `gh` auth only for repository label listing and creation.
- Private GitHub Apps should set `RUNOQ_APP_ID` explicitly before running `runoq init`.

If that bootstrap succeeds, later commands can mint installation tokens from the saved identity.

### During `plan`, `run`, and `maintenance`

These commands call `gh-auth.sh export-token` before invoking the runtime flow or Claude.

- If `GH_TOKEN` is already present, it is reused by default.
- If not, `gh-auth.sh` signs a JWT with the private key and exchanges it for an installation token.
- The resulting token is injected into the current `runoq` process environment via `eval`.

### Common auth failures

- `.runoq/identity.json` missing: run `runoq init`
- GitHub App private key path missing or unreadable
- Token mint response missing a `token` field
- `origin` remote missing or not hosted on `github.com`
- Installation lookup failures during `init`

## Mention Authorization

Mention processing combines configuration, collaborator permission checks, and local deduplication.

### Permission model

`gh-pr-lifecycle.sh check-permission` maps GitHub collaborator permissions to ranks:

- `admin` = 3
- `maintain` or `write` = 2
- `triage` or `read` = 1
- anything else = 0

`authorization.minimumPermission` accepts:

- `read`
- `write`
- `admin`

A mention is authorized when the collaborator rank is greater than or equal to the configured minimum.

### Denial behavior

`mentions.sh process` handles unauthorized mentions based on `authorization.denyResponse`:

- `comment`: post an `<!-- runoq:event -->` denial comment on the PR or issue
- any other value: ignore silently

The current default config is:

```json
{
  "authorization": {
    "minimumPermission": "write",
    "denyResponse": "comment"
  }
}
```

### Deduplication

Every processed mention ID is appended to `.runoq/state/processed-mentions.json` through `state.sh record-mention`.

- Authorized mentions are recorded once and returned with `action: "process"`.
- Unauthorized mentions are still recorded once, then returned with `action: "deny"` or `action: "ignore"`.
- Future polling skips already recorded comment IDs.

## Troubleshooting Checklist

- Confirm the runtime config path: `echo "$RUNOQ_CONFIG"`
- Inspect saved identity: `scripts/gh-auth.sh print-identity`
- Check whether `GH_TOKEN` is masking installation-token minting: `echo "${GH_TOKEN:+set}"`
- Force a fresh token when debugging app auth: `export RUNOQ_FORCE_REFRESH_TOKEN=1`
- Verify the private key path recorded in `.runoq/identity.json`
- Verify repo resolution from `origin` or `RUNOQ_REPO`
- Check `authorization.minimumPermission` and `authorization.denyResponse` when mentions are denied or ignored unexpectedly

## Related Docs

- [CLI reference](./cli.md)
- [Architecture overview](../architecture/overview.md)
- [Operator workflow](../operations/operator-workflow.md)
