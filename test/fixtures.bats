#!/usr/bin/env bats

load test_helper

@test "payload contract fixtures contain valid JSON where expected" {
  run jq -e '.' "$(fixture_path "payloads/github-orchestrator-dispatch.json")"
  [ "$status" -eq 0 ]

  run jq -e '.' "$(fixture_path "payloads/github-orchestrator-return.json")"
  [ "$status" -eq 0 ]

  run jq -e '.' "$(fixture_path "payloads/codex-dispatch.json")"
  [ "$status" -eq 0 ]

  run jq -e '.' "$(fixture_path "payloads/codex-return-valid.json")"
  [ "$status" -eq 0 ]
}

@test "malformed payload fixture is intentionally invalid JSON" {
  run jq -e '.' "$(fixture_path "payloads/codex-return-malformed.txt")"
  [ "$status" -ne 0 ]
}

@test "audit comment fixtures include required markers" {
  run grep -n "agendev:payload:codex-return" "$(fixture_path "comments/audit-codex-return.md")"
  [ "$status" -eq 0 ]

  run grep -n "agendev:event" "$(fixture_path "comments/audit-event.md")"
  [ "$status" -eq 0 ]
}

@test "issue metadata fixtures cover valid and invalid examples" {
  run grep -n "depends_on: \\[12, 14\\]" "$(fixture_path "issues/valid-meta.md")"
  [ "$status" -eq 0 ]

  run grep -n "depends_on: oops" "$(fixture_path "issues/invalid-meta.md")"
  [ "$status" -eq 0 ]
}
