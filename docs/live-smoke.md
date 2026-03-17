# Live Smoke Tests

The live smoke suite validates the runtime against a real sandbox GitHub repository. It is intentionally separate from normal local test runs and should be run only when you mean to exercise real credentials, real API calls, and real cleanup.

## Scope And Risk Envelope

The smoke runner proves a narrow set of real-world behaviors:

- GitHub App auth via `scripts/gh-auth.sh`
- label provisioning via `scripts/setup.sh`
- queue issue creation via `scripts/gh-issue-queue.sh`
- draft PR creation via `scripts/gh-pr-lifecycle.sh`
- issue and PR comment attribution as `agendev[bot]`
- collaborator permission checks against the sandbox repo

It does not run the full `agendev run` queue workflow.

## Sandbox Ownership Expectations

Use a dedicated sandbox repo that you control.

Expectations:

- the repo is safe to create and close test issues and PRs in
- the repo can tolerate a temporary branch deletion during cleanup
- the GitHub App installation ID points at this sandbox repo
- the private key belongs to the same app installation you intend to validate
- `AGENDEV_SMOKE_PERMISSION_USER` names a collaborator whose permission level you are willing to check

Do not point the smoke suite at a production repo or a repo with active human work that could be confused by synthetic issues or PRs.

## Required Environment

Set all of the following:

- `AGENDEV_SMOKE=1`
- `AGENDEV_SMOKE_REPO=<owner>/<sandbox-repo>`
- `AGENDEV_SMOKE_APP_ID=<github-app-id>`
- `AGENDEV_SMOKE_INSTALLATION_ID=<sandbox-installation-id>`
- `AGENDEV_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`
- `AGENDEV_SMOKE_PERMISSION_USER=<repo-collaborator-to-check>`
- `AGENDEV_SMOKE_PERMISSION_LEVEL=write`

Optional:

- `AGENDEV_SMOKE_RUN_ID=<stable-id>` if you want a predictable run identifier instead of a UTC timestamp

## What The Runner Creates

During `scripts/live-smoke.sh run`, the suite creates:

- a temporary auth root with `.agendev/identity.json`
- a temporary clone of the sandbox repo
- a temporary `AGENDEV_SYMLINK_DIR`
- one queue issue
- one issue comment
- one temporary branch
- one draft PR
- one PR comment

During cleanup it attempts to:

- close the PR with a cleanup comment
- delete the temporary branch from `origin`
- close the issue with a cleanup comment
- remove the temp directories

Cleanup runs from a shell `trap`, so partial cleanup is still possible if a later step fails.

## Command Modes

### `scripts/live-smoke.sh preflight`

Use this first.

```bash
scripts/live-smoke.sh preflight
```

Preflight:

- checks whether `AGENDEV_SMOKE=1` is set
- validates required environment variables
- checks that the private key file exists
- returns JSON describing readiness

Typical output fields:

- `enabled`
- `repo`
- `permission_user`
- `permission_level`
- `key_path`
- `missing`
- `ready`

If `ready` is `false`, do not run the sandbox flow yet.

### Deterministic guard tests

These are still local and fixture-driven:

```bash
bats test/live_smoke.bats
```

They verify:

- preflight JSON shape
- required env handling
- key-path validation behavior

Run these when changing smoke-runner logic even if you are not going to hit real GitHub.

### Sandbox suite wrapper

```bash
bats test/live_smoke.bats test/live_smoke_sandbox.bats
```

`test/live_smoke_sandbox.bats` is skipped unless:

- `AGENDEV_SMOKE=1`
- preflight returns `ready: true`

This is the safest high-level command because it runs the deterministic guard tests first, then the real sandbox check only if you explicitly enabled it.

### Direct sandbox run

```bash
scripts/live-smoke.sh run
```

Use this when you want the raw runner output directly. On success it returns JSON with:

- `status: "ok"`
- `repo`
- `run_id`
- `issue_number`
- `pr_number`
- `bot_login`
- `permission_check`
- `checks`

## Execution Sequence

The smoke runner:

1. performs preflight and aborts if not ready
2. writes a temporary `.agendev/identity.json`
3. forces fresh token minting with `AGENDEV_FORCE_REFRESH_TOKEN=1`
4. clones the sandbox repo using the minted token
5. runs `scripts/setup.sh` against the clone
6. verifies all expected labels exist
7. creates a queue issue
8. posts an issue comment and checks that the author is `agendev[bot]`
9. runs the collaborator permission check
10. creates and pushes a temporary branch
11. creates a draft PR
12. posts a PR comment and checks that the author is `agendev[bot]`
13. returns the success summary JSON

## Why The Suite Stays Opt-In

The smoke suite must remain opt-in because it:

- uses real app credentials
- creates real GitHub resources
- depends on a repo-specific installation ID
- can leave behind branch, issue, or PR debris if cleanup is blocked externally

It should never be part of routine `bats test/*.bats` runs.

## Troubleshooting

### Preflight not ready

Symptoms:

- `ready: false`
- `missing` contains one or more messages

Fix:

- set `AGENDEV_SMOKE=1`
- supply the missing `AGENDEV_SMOKE_*` values
- correct the key path if the file does not exist

### Auth failures

Symptoms:

- `scripts/live-smoke.sh run` exits before clone or setup
- token minting fails
- the clone step cannot authenticate

Checks:

- confirm `AGENDEV_SMOKE_APP_ID` and `AGENDEV_SMOKE_INSTALLATION_ID`
- confirm `AGENDEV_SMOKE_APP_KEY` points at the right private key
- verify the app is installed on `AGENDEV_SMOKE_REPO`
- remember the runner forces `AGENDEV_FORCE_REFRESH_TOKEN=1`, so an existing `GH_TOKEN` will not mask app-auth problems

### Permission check failures

Symptoms:

- the final JSON does not return `status: "ok"`
- permission-check execution fails against `AGENDEV_SMOKE_PERMISSION_USER`

Checks:

- confirm the user is a collaborator on the sandbox repo
- confirm `AGENDEV_SMOKE_PERMISSION_LEVEL` matches the level you expect to validate
- inspect the repo’s actual collaborator permission state in GitHub

### Bot attribution mismatches

Symptoms:

- issue comment author or PR comment author is not `agendev[bot]`

Checks:

- confirm the app slug in config matches the installed app
- confirm the token was minted from the app installation, not inherited from another auth context
- check whether the app installation has permission to comment in the repo

### Cleanup issues

Symptoms:

- temporary issue or PR remains open
- temporary branch remains in the sandbox repo

Checks:

- search by the run ID in issue titles, PR titles, issue comments, and PR comments
- inspect whether branch protection or permissions blocked branch deletion
- check whether the trap ran after an earlier failure

Manual cleanup targets:

- issue title `agendev live smoke <run_id>`
- PR title `agendev live smoke <run_id>`
- branch `agendev-smoke-<run_id>`

## Recommended Usage Pattern

1. Run `scripts/live-smoke.sh preflight`.
2. Fix anything reported in `missing`.
3. Run `bats test/live_smoke.bats test/live_smoke_sandbox.bats` when you want the guard tests plus the real sandbox check.
4. Use `scripts/live-smoke.sh run` directly only when you want the raw runner output or are iterating on the script itself.

## Related Docs

- [Contributor testing guide](./contributing/testing.md)
- [Configuration and auth reference](./reference/config-auth.md)
