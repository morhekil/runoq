# Setup From Scratch

This guide takes you from a brand-new checkout of the `agendev` repository to successfully running both smoke test lanes (sandbox and lifecycle). It covers every prerequisite, the GitHub App creation, and the environment configuration needed before anything will work.

## 1. Install System Dependencies

Install all of the following. The smoke tests and runtime scripts will fail if any are missing.

| Tool | Purpose | Install (macOS) |
| --- | --- | --- |
| `bash` | Shell runtime | Preinstalled |
| `git` | Version control | `xcode-select --install` |
| `gh` | GitHub CLI | `brew install gh` |
| `jq` | JSON processing | `brew install jq` |
| `openssl` | JWT signing for GitHub App auth | `brew install openssl` |
| `bats` | Shell test framework | `brew install bats-core` |
| `shellcheck` | Shell linting | `brew install shellcheck` |
| `node` / `npm` | Lifecycle eval target repos use `npm test` and `npm run build` | `brew install node` |
| `claude` | Claude CLI, used by `agendev plan`, `run`, and `maintenance` | [Install instructions](https://docs.anthropic.com/en/docs/claude-code) |
| `codex` | Required by lifecycle eval alongside `claude` | See OpenAI Codex docs |

If `claude` or `codex` are installed under different names or paths, override with:

```bash
export AGENDEV_CLAUDE_BIN=/path/to/claude
export AGENDEV_SMOKE_CODEX_BIN=/path/to/codex
```

## 2. Clone The Runtime Repository

```bash
git clone <agendev-repo-url>
cd agendev
```

All smoke test commands run from inside this checkout. Set a convenience variable:

```bash
export AGENDEV_RUNTIME="$(pwd)"
```

## 3. Authenticate The GitHub CLI

Log in with `gh` so the operator shell can create repos, manage issues/PRs, and delete repos during cleanup:

```bash
gh auth login
```

When prompted, choose `GitHub.com`, HTTPS, and authenticate via browser. After login, verify:

```bash
gh auth status
```

For lifecycle eval cleanup, your token needs the `delete_repo` scope. Add it if missing:

```bash
gh auth refresh -s delete_repo
```

## 4. Create The GitHub App

`agendev` authenticates as a GitHub App to manage issues, PRs, and labels on target repositories.

### 4a. Register the app

Go to **GitHub > Settings > Developer settings > GitHub Apps > New GitHub App**.

| Field | Value |
| --- | --- |
| **GitHub App name** | `agendevapp` (must match the `identity.appSlug` value in `config/agendev.json`) |
| **Homepage URL** | Any valid URL |
| **Webhook** | Uncheck **Active** — agendev does not use webhook delivery |

For **visibility**, choose:

- **Private / only on this account** if all repos that `agendev` will touch live under the same user or organization that owns the app. This is the preferred setup for a single-owner sandbox plus lifecycle eval.
- **Public / any account** only if you intentionally need to install the app on other GitHub accounts or organizations. Public apps can be installed by other GitHub accounts; private apps cannot.

### 4b. Set repository permissions

Under **Permissions & events > Repository permissions**, grant:

| Permission | Access | Used for |
| --- | --- | --- |
| **Contents** | Read & write | Reading repo contents and pushing branches |
| **Issues** | Read & write | Creating, labeling, and commenting on queue issues |
| **Pull requests** | Read & write | Creating draft PRs, posting audit comments, managing reviewers |
| **Metadata** | Read-only | Required by GitHub for all apps |

No organization permissions, account permissions, or webhook event subscriptions are needed.

### 4c. Note the App ID

After creating the app you land on the app's **General** settings page. The numeric **App ID** is shown near the top. You will need this for the sandbox smoke configuration.

### 4d. Generate and install the private key

Scroll to **Private keys** on the same page and click **Generate a private key**. GitHub downloads a `.pem` file.

Move it to the default location:

```bash
mkdir -p "$HOME/.agendev"
mv ~/Downloads/<app-name>.*.private-key.pem "$HOME/.agendev/app-key.pem"
chmod 600 "$HOME/.agendev/app-key.pem"
```

Or place it anywhere and export the path:

```bash
export AGENDEV_APP_KEY="/path/to/your-key.pem"
```

### 4e. Install the app

From the app settings page, click **Install App** in the left sidebar. Select the owner where your target and sandbox repos live.

- If the app is **private**, the install target must be the same user or organization that owns the app.
- If the app is **public**, you can install it on other accounts or organizations that you control.

Then choose repository access:

- **Only select repositories** is fine for sandbox smoke or for a fixed set of existing repos.
- **All repositories** is the safest choice if you plan to run lifecycle eval, because lifecycle eval creates disposable repos under the selected owner and the app needs access to those new repos immediately.

After installation, note the **Installation ID** from the URL. The URL will look like:

```
https://github.com/settings/installations/<installation-id>
```

For organization installs the URL is:

```
https://github.com/organizations/<org>/settings/installations/<installation-id>
```

### 4f. Verify the installation is reachable

```bash
gh api "/repos/<owner>/<repo>/installation"
```

Run that against a repository that should be covered by the installation. Confirm:

- `app_slug` matches `identity.appSlug` in `config/agendev.json`
- `app_id` matches the App ID from step 4c

Do not use `gh api "/apps/<slug>"` as the primary check here. Private apps may return `404` on that endpoint even when the installation is correct, so `agendev init` resolves the App ID from the repository installation instead.

## 5. Prepare A Sandbox Repository

The sandbox smoke lane runs against an existing repo that you control. It does not create or delete repos — it only creates and cleans up test issues, PRs, and labels.

Create a throwaway repo or use an existing one:

```bash
gh repo create <owner>/agendev-sandbox --private --clone
cd agendev-sandbox
git commit --allow-empty -m "init" && git push
cd "$AGENDEV_RUNTIME"
```

Make sure the GitHub App is installed on this repo (step 4e).

You also need a GitHub user who is a collaborator on the sandbox repo, for the permission check. This can be your own username:

```bash
gh api "repos/<owner>/agendev-sandbox/collaborators/<your-username>/permission" --jq '.permission'
# Should print "admin" or "write"
```

## 6. Configure Sandbox Smoke Environment

Set these variables. All are required for the sandbox lane:

```bash
export AGENDEV_SMOKE=1
export AGENDEV_SMOKE_REPO="<owner>/agendev-sandbox"
export AGENDEV_SMOKE_APP_ID="<numeric-app-id-from-step-4c>"
export AGENDEV_SMOKE_INSTALLATION_ID="<installation-id-from-step-4e>"
export AGENDEV_SMOKE_APP_KEY="$HOME/.agendev/app-key.pem"
export AGENDEV_SMOKE_PERMISSION_USER="<your-github-username>"
export AGENDEV_SMOKE_PERMISSION_LEVEL="write"
```

You can put these in a file and source it:

```bash
# .env.smoke-sandbox (not checked in — already in .gitignore)
AGENDEV_SMOKE=1
AGENDEV_SMOKE_REPO=myorg/agendev-sandbox
AGENDEV_SMOKE_APP_ID=123456
AGENDEV_SMOKE_INSTALLATION_ID=78901234
AGENDEV_SMOKE_APP_KEY=/Users/me/.agendev/app-key.pem
AGENDEV_SMOKE_PERMISSION_USER=myusername
AGENDEV_SMOKE_PERMISSION_LEVEL=write
```

```bash
set -a; source .env.smoke-sandbox; set +a
```

## 7. Run Sandbox Smoke

```bash
scripts/smoke-sandbox.sh preflight
```

Preflight returns JSON listing what is `ready` and what is `missing`. Fix anything in `missing` before proceeding.

Then run the actual smoke:

```bash
scripts/smoke-sandbox.sh run
```

This creates a test issue and PR in the sandbox repo, validates bot attribution, checks collaborator permissions, and cleans up after itself.

Or run through Bats for structured test output:

```bash
bats test/live_smoke.bats test/live_smoke_sandbox.bats
```

## 8. Configure Lifecycle Eval Environment

The lifecycle eval creates a disposable GitHub repo, runs `agendev init` and `agendev run` against it end-to-end, and then you clean it up.

Required variables (in addition to `AGENDEV_SMOKE=1` from above):

```bash
export AGENDEV_SMOKE_LIFECYCLE=1
export AGENDEV_SMOKE_REPO_OWNER="<owner-or-org>"
export AGENDEV_SMOKE_APP_ID="<numeric-app-id-from-step-4c>"
export AGENDEV_SMOKE_APP_KEY="$HOME/.agendev/app-key.pem"
```

Optional overrides:

```bash
export AGENDEV_SMOKE_REPO_PREFIX="agendev-live-eval"   # default
export AGENDEV_SMOKE_REPO_VISIBILITY="private"          # default
export AGENDEV_SMOKE_RUN_ID="my-run-001"                # auto-generated if omitted
```

**Important**: Lifecycle eval creates repos dynamically under `AGENDEV_SMOKE_REPO_OWNER`. Install the app on that same owner, and prefer **all repositories** access there. If you keep **only select repositories**, you must manually add each newly created lifecycle repo to the app before `agendev init` can succeed.

## 9. Run Lifecycle Eval

```bash
scripts/smoke-lifecycle.sh preflight
```

Preflight checks: `AGENDEV_SMOKE`, `AGENDEV_SMOKE_REPO_OWNER`, `AGENDEV_SMOKE_APP_ID`, `AGENDEV_SMOKE_APP_KEY`, operator `gh` auth, and that `claude`, `codex`, `node`, and `npm` are all on `PATH`.

Then run:

```bash
scripts/smoke-lifecycle.sh run
```

This will:

1. Create a disposable repo under `<owner>/<prefix>-<run_id>`
2. Push a small seeded target from `test/fixtures/live_smoke_lifecycle_target/`
3. Run `agendev init` against it
4. Seed a 3-issue dependent chain from `test/fixtures/live_smoke_lifecycle_issues.json`
5. Run `agendev run` in queue mode
6. Return structured JSON with completion metrics

Or run through Bats:

```bash
bats test/live_smoke.bats test/live_smoke_lifecycle.bats
```

Inspect artifacts after the run:

```bash
ls .agendev/live-smoke/runs/<run_id>/
# init.log  run.log  state/  summary.json
```

## 10. Clean Up Lifecycle Repos

Managed repos are tracked in `.agendev/live-smoke/managed-repos.json`. Clean up explicitly:

```bash
# Single repo
scripts/smoke-lifecycle.sh cleanup --repo <owner>/<repo-name>

# By run ID
scripts/smoke-lifecycle.sh cleanup --run-id <run_id>

# Everything tracked
scripts/smoke-lifecycle.sh cleanup --all
```

Your `gh` token must have the `delete_repo` scope (step 3).

## Troubleshooting

### Preflight says `gh` auth is missing

Run `gh auth status`. If expired, run `gh auth login` again.

### App ID or installation ID lookup fails

- Confirm the app name matches `identity.appSlug` in `config/agendev.json` (default: `agendevapp`)
- Confirm the app is installed on the correct owner/org
- If the app is private, confirm the app owner and install target are the same account
- Try: `gh api "/repos/<owner>/<repo>/installation"` and confirm the returned `app_slug` and `app_id`

### Private key errors

- Check the file exists at the path in `AGENDEV_SMOKE_APP_KEY` or `$HOME/.agendev/app-key.pem`
- Check permissions: `ls -la "$HOME/.agendev/app-key.pem"` (should be `600`)
- Confirm the key belongs to the correct app (regenerate if unsure)

### Lifecycle init succeeds but `agendev run` fails

- Check `.agendev/live-smoke/runs/<run_id>/init.log` and `run.log`
- Confirm `claude` and `codex` are on `PATH` and working
- Confirm `node` and `npm` are available

### Cleanup fails with permission error

Run `gh auth refresh -s delete_repo` and retry.

### Sandbox smoke creates resources but doesn't clean up

The sandbox script cleans up on success. If it fails mid-run, manually check the sandbox repo for leftover issues/PRs with `agendev` in the title and close them.

## Related Docs

- [Operator workflow](./operator-workflow.md) — day-to-day agendev commands (init, plan, run, report, maintenance)
- [Live smoke tests](../live-smoke.md) — lane details, output formats, and managed repo model
- [Configuration and auth reference](../reference/config-auth.md) — full variable and identity reference
