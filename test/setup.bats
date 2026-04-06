#!/usr/bin/env bats

load test_helper

write_empty_key() {
  local key_path="$1"
  openssl genrsa -out "$key_path" 2048 >/dev/null 2>&1
}

@test "setup init creates identity state package json managed Claude symlinks and CLI symlink" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  export RUNOQ_APP_KEY="$TEST_TMPDIR/app-key.pem"
  export RUNOQ_APP_ID="123"
  write_empty_key "$RUNOQ_APP_KEY"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "/repos/owner/repo/installation"],
    "stdout": "{\"id\":789,\"app_id\":123,\"app_slug\":\"runoq\"}"
  },
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[]"
  },
  {
    "contains": ["label", "create", "runoq:ready", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:in-progress", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:done", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:needs-human-review", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:blocked", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:plan-approved", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:maintenance-review", "--repo owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/setup.sh"

  [ "$status" -eq 0 ]
  [ -d "$project_dir/.runoq/state" ]
  [ -f "$project_dir/.runoq/identity.json" ]
  [ -f "$project_dir/package.json" ]
  [ -L "$project_dir/.claude/agents/github-orchestrator.md" ]
  [ -L "$project_dir/.claude/agents/issue-runner.md" ]
  [ -L "$project_dir/.claude/skills/plan-to-issues/SKILL.md" ]
  [ "$(readlink "$project_dir/.claude/agents/github-orchestrator.md")" = "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md" ]
  [ "$(readlink "$project_dir/.claude/skills/plan-to-issues/SKILL.md")" = "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md" ]
  [ -L "$RUNOQ_SYMLINK_DIR/runoq" ]
  [ "$(jq -r '.appId' "$project_dir/.runoq/identity.json")" = "123" ]
}

@test "setup init rejects repo installations for a different app slug" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  export RUNOQ_APP_KEY="$TEST_TMPDIR/app-key.pem"
  export RUNOQ_APP_ID="123"
  write_empty_key "$RUNOQ_APP_KEY"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "/repos/owner/repo/installation"],
    "stdout": "{\"id\":789,\"app_id\":123,\"app_slug\":\"wrong-app\"}"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/setup.sh"

  [ "$status" -ne 0 ]
  [[ "$output" == *"Repository installation app slug wrong-app did not match configured identity.appSlug runoq."* ]]
}

@test "setup init shows a clear error when the app is not installed on the repo" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  export RUNOQ_APP_KEY="$TEST_TMPDIR/app-key.pem"
  export RUNOQ_APP_ID="123"
  write_empty_key "$RUNOQ_APP_KEY"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "/repos/owner/repo/installation"],
    "stderr": "gh: Not Found (HTTP 404)",
    "exit_code": 1
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/setup.sh"

  [ "$status" -ne 0 ]
  [[ "$output" == *"GitHub App installation not found for owner/repo. Install the app on this repository, then rerun runoq init."* ]]
}

@test "setup init resolves installation with an app JWT even when GH_TOKEN is set" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  export RUNOQ_APP_KEY="$TEST_TMPDIR/app-key.pem"
  export RUNOQ_APP_ID="123"
  export GH_TOKEN="bogus-app-token"
  write_empty_key "$RUNOQ_APP_KEY"

  gh_wrapper="$TEST_TMPDIR/gh-wrapper"
  cat >"$gh_wrapper" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "api" && "$*" == *"/repos/owner/repo/installation"* ]]; then
  if [[ -n "${GH_TOKEN:-}" || -n "${GITHUB_TOKEN:-}" ]]; then
    echo "expected setup.sh to pass the JWT via Authorization header, not GH_TOKEN" >&2
    exit 1
  fi
  if [[ "$*" != *"Authorization: Bearer "* ]]; then
    echo "expected setup.sh to call installation lookup with Authorization: Bearer <jwt>" >&2
    exit 1
  fi
  printf '%s' '{"id":789,"app_id":123,"app_slug":"runoq"}'
  exit 0
fi

if [[ "${1:-}" == "label" && "${2:-}" == "list" ]]; then
  if [[ -n "${GH_TOKEN:-}" || -n "${GITHUB_TOKEN:-}" ]]; then
    echo "expected label list to use operator gh auth without GH_TOKEN" >&2
    exit 1
  fi
  printf '%s' '[]'
  exit 0
fi

if [[ "${1:-}" == "label" && "${2:-}" == "create" ]]; then
  if [[ -n "${GH_TOKEN:-}" || -n "${GITHUB_TOKEN:-}" ]]; then
    echo "expected label create to use operator gh auth without GH_TOKEN" >&2
    exit 1
  fi
  exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
EOF
  chmod +x "$gh_wrapper"
  export GH_BIN="$gh_wrapper"

  run "$RUNOQ_ROOT/scripts/setup.sh"

  [ "$status" -eq 0 ]
  [ "$(jq -r '.appId' "$project_dir/.runoq/identity.json")" = "123" ]
}

@test "setup init is idempotent on repeated runs" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  mkdir -p "$project_dir/.runoq"
  cat >"$project_dir/.runoq/identity.json" <<EOF
{
  "appId": 123,
  "installationId": 789,
  "privateKeyPath": "$TEST_TMPDIR/app-key.pem"
}
EOF
  write_empty_key "$TEST_TMPDIR/app-key.pem"
  cat >"$project_dir/package.json" <<'EOF'
{
  "name": "existing",
  "scripts": {
    "test": "existing test",
    "build": "existing build"
  }
}
EOF
  mkdir -p "$project_dir/.claude/agents"
  echo "custom agent" >"$project_dir/.claude/agents/custom.md"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[{\"name\":\"runoq:ready\"},{\"name\":\"runoq:in-progress\"},{\"name\":\"runoq:done\"},{\"name\":\"runoq:needs-human-review\"},{\"name\":\"runoq:blocked\"},{\"name\":\"runoq:plan-approved\"},{\"name\":\"runoq:maintenance-review\"}]"
  },
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[{\"name\":\"runoq:ready\"},{\"name\":\"runoq:in-progress\"},{\"name\":\"runoq:done\"},{\"name\":\"runoq:needs-human-review\"},{\"name\":\"runoq:blocked\"},{\"name\":\"runoq:plan-approved\"},{\"name\":\"runoq:maintenance-review\"}]"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/setup.sh"
  [ "$status" -eq 0 ]
  run "$RUNOQ_ROOT/scripts/setup.sh"
  [ "$status" -eq 0 ]
  [ "$(jq -r '.name' "$project_dir/package.json")" = "existing" ]
  [ "$(cat "$project_dir/.claude/agents/custom.md")" = "custom agent" ]
  [ -L "$project_dir/.claude/agents/github-orchestrator.md" ]
  [ "$(readlink "$project_dir/.claude/agents/github-orchestrator.md")" = "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md" ]
}

@test "setup init replaces old copied Claude files with managed symlinks" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  mkdir -p "$project_dir/.runoq" "$project_dir/.claude/agents" "$project_dir/.claude/skills/plan-to-issues"
  cat >"$project_dir/.runoq/identity.json" <<EOF
{
  "appId": 123,
  "installationId": 789,
  "privateKeyPath": "$TEST_TMPDIR/app-key.pem"
}
EOF
  write_empty_key "$TEST_TMPDIR/app-key.pem"
  printf 'copied agent content\n' >"$project_dir/.claude/agents/github-orchestrator.md"
  echo "stale skill content" >"$project_dir/.claude/skills/plan-to-issues/SKILL.md"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[{\"name\":\"runoq:ready\"},{\"name\":\"runoq:in-progress\"},{\"name\":\"runoq:done\"},{\"name\":\"runoq:needs-human-review\"},{\"name\":\"runoq:blocked\"},{\"name\":\"runoq:plan-approved\"},{\"name\":\"runoq:maintenance-review\"}]"
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/setup.sh"

  [ "$status" -eq 0 ]
  [ -L "$project_dir/.claude/agents/github-orchestrator.md" ]
  [ "$(readlink "$project_dir/.claude/agents/github-orchestrator.md")" = "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md" ]
  [ "$(readlink "$project_dir/.claude/skills/plan-to-issues/SKILL.md")" = "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md" ]
}

@test "setup init --plan writes and stages runoq.json at the project root" {
  project_dir="$TEST_TMPDIR/project"
  make_git_repo "$project_dir" "git@github.com:owner/repo.git"
  export TARGET_ROOT="$project_dir"
  export RUNOQ_SYMLINK_DIR="$TEST_TMPDIR/bin"
  export RUNOQ_APP_KEY="$TEST_TMPDIR/app-key.pem"
  export RUNOQ_APP_ID="123"
  write_empty_key "$RUNOQ_APP_KEY"

  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "/repos/owner/repo/installation"],
    "stdout": "{\"id\":789,\"app_id\":123,\"app_slug\":\"runoq\"}"
  },
  {
    "contains": ["label", "list", "--repo owner/repo"],
    "stdout": "[]"
  },
  {
    "contains": ["label", "create", "runoq:ready", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:in-progress", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:done", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:needs-human-review", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:blocked", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:plan-approved", "--repo owner/repo"],
    "stdout": ""
  },
  {
    "contains": ["label", "create", "runoq:maintenance-review", "--repo owner/repo"],
    "stdout": ""
  }
]
EOF
  use_fake_gh "$scenario"

  run "$RUNOQ_ROOT/scripts/setup.sh" --plan docs/prd.md

  [ "$status" -eq 0 ]
  [ -f "$project_dir/runoq.json" ]
  [ ! -f "$project_dir/.runoq/runoq.json" ]
  [ "$(jq -r '.plan' "$project_dir/runoq.json")" = "docs/prd.md" ]
  run git -C "$project_dir" diff --cached --name-only
  [ "$status" -eq 0 ]
  [[ "$output" == *"runoq.json"* ]]
  run grep -q 'runoq\.json' "$project_dir/.gitignore"
  [ "$status" -ne 0 ]
}
