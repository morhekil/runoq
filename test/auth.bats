#!/usr/bin/env bats

load test_helper

@test "gh auth suggests init when identity file is missing" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"

  run "$AGENDEV_ROOT/scripts/gh-auth.sh" export-token

  [ "$status" -ne 0 ]
  [[ "$output" == *"Run 'agendev init' first."* ]]
}

@test "gh auth respects an existing GH_TOKEN without reading identity" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT"
  export GH_TOKEN="already-set"

  run "$AGENDEV_ROOT/scripts/gh-auth.sh" export-token

  [ "$status" -eq 0 ]
  [[ "$output" == *"export GH_TOKEN=already-set"* ]]
}

@test "gh auth fails cleanly when the private key path is missing" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT/.agendev"
  cat >"$TARGET_ROOT/.agendev/identity.json" <<'EOF'
{
  "appId": 123,
  "installationId": 456,
  "privateKeyPath": "/tmp/does-not-exist.pem"
}
EOF
  unset GH_TOKEN

  run "$AGENDEV_ROOT/scripts/gh-auth.sh" export-token

  [ "$status" -ne 0 ]
  [[ "$output" == *"GitHub App private key not found"* ]]
}

@test "gh auth can mint a token from identity and fake gh api" {
  export TARGET_ROOT="$TEST_TMPDIR/project"
  mkdir -p "$TARGET_ROOT/.agendev"
  key_path="$TEST_TMPDIR/app-key.pem"
  openssl genrsa -out "$key_path" 2048 >/dev/null 2>&1
  cat >"$TARGET_ROOT/.agendev/identity.json" <<EOF
{
  "appId": 123,
  "installationId": 789,
  "privateKeyPath": "$key_path"
}
EOF
  scenario="$TEST_TMPDIR/scenario.json"
  write_fake_gh_scenario "$scenario" <<EOF
[
  {
    "contains": ["api", "--method POST", "/app/installations/789/access_tokens"],
    "stdout": "{\"token\":\"minted-token\"}"
  }
]
EOF
  use_fake_gh "$scenario"
  unset GH_TOKEN

  run "$AGENDEV_ROOT/scripts/gh-auth.sh" export-token

  [ "$status" -eq 0 ]
  [[ "$output" == *"export GH_TOKEN=minted-token"* ]]
}
