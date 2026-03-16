# Live Smoke Tests

The live smoke suite is intentionally separate from normal local test runs. It only executes against a dedicated sandbox repository when you opt in explicitly.

## Required environment

Set all of the following before running the sandbox suite:

- `AGENDEV_SMOKE=1`
- `AGENDEV_SMOKE_REPO=<owner>/<sandbox-repo>`
- `AGENDEV_SMOKE_APP_ID=<github-app-id>`
- `AGENDEV_SMOKE_INSTALLATION_ID=<sandbox-installation-id>`
- `AGENDEV_SMOKE_APP_KEY=/absolute/path/to/app-key.pem`
- `AGENDEV_SMOKE_PERMISSION_USER=<repo-collaborator-to-check>`
- `AGENDEV_SMOKE_PERMISSION_LEVEL=write`

## What it verifies

- GitHub App authentication via `scripts/gh-auth.sh`
- Label provisioning via `scripts/setup.sh`
- Queue issue creation via `scripts/gh-issue-queue.sh`
- PR creation and commenting via `scripts/gh-pr-lifecycle.sh`
- Comment attribution as `agendev[bot]`
- Real collaborator permission checks

The suite creates one issue, one draft PR, and one temporary branch in the sandbox repo, then closes the issue and PR and deletes the branch during cleanup.

## Commands

Preflight only:

```bash
scripts/live-smoke.sh preflight
```

Run the deterministic guard tests plus the skipped-by-default live suite:

```bash
bats test/live_smoke.bats test/live_smoke_sandbox.bats
```

Run the actual sandbox smoke flow directly:

```bash
scripts/live-smoke.sh run
```
