#!/usr/bin/env bats

load test_helper

@test "claude_stream writes live stream and progress logs before completion" {
  target_dir="$TEST_TMPDIR/target"
  make_git_repo "$target_dir" "git@github.com:owner/repo.git"
  fake_claude="$TEST_TMPDIR/fake-claude-stream"
  capture_dir="$target_dir/log/claude/plan-decomposer-test"
  output_file="$TEST_TMPDIR/claude-output.txt"

  cat >"$fake_claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"working"},{"type":"text","text":"partial"}]}}'
sleep 1
printf '%s\n' '{"type":"result","result":"<!-- runoq:payload:plan-decomposer -->\n```json\n{\"items\":[],\"warnings\":[]}\n```"}'
EOF
  chmod +x "$fake_claude"

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export TARGET_ROOT="'"$target_dir"'"
    export REPO="owner/repo"
    export RUNOQ_CLAUDE_BIN="'"$fake_claude"'"
    export RUNOQ_CLAUDE_CAPTURE_DIR="'"$capture_dir"'"
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::claude_stream "'"$output_file"'" --agent plan-decomposer -- "{\"planPath\":\"/tmp/plan.md\"}" &
    pid=$!
    for _ in $(seq 1 20); do
      if grep -F "\"type\":\"assistant\"" "'"$capture_dir"'/stdout.log" >/dev/null 2>&1 && \
         grep -F "[agent] thinking..." "'"$capture_dir"'/progress.log" >/dev/null 2>&1; then
        break
      fi
      sleep 0.1
    done
    grep -F "\"type\":\"assistant\"" "'"$capture_dir"'/stdout.log"
    grep -F "[agent] thinking..." "'"$capture_dir"'/progress.log"
    wait "$pid"
    grep -F "runoq:payload:plan-decomposer" "'"$output_file"'"
  '

  [ "$status" -eq 0 ]
}

@test "captured_exec records codex invocation artifacts" {
  target_dir="$TEST_TMPDIR/target"
  make_git_repo "$target_dir" "git@github.com:owner/repo.git"
  fake_codex="$TEST_TMPDIR/fake-codex"
  cat >"$fake_codex" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'codex stdout\n'
printf 'codex stderr\n' >&2
EOF
  chmod +x "$fake_codex"

  run bash -lc '
    set -euo pipefail
    export RUNOQ_ROOT="'"$RUNOQ_ROOT"'"
    export RUNOQ_CONFIG="'"$RUNOQ_CONFIG"'"
    export TARGET_ROOT="'"$target_dir"'"
    export REPO="owner/repo"
    source "'"$RUNOQ_ROOT"'/scripts/lib/common.sh"
    runoq::captured_exec codex "'"$target_dir"'" "'"$fake_codex"'" exec --full-auto "do the thing"
  '

  [ "$status" -eq 0 ]
  [[ "$output" == *"codex stdout"* ]]
  [[ "$output" == *"codex stderr"* ]]

  run bash -lc 'cd "'"$target_dir"'" && find log/codex -mindepth 1 -maxdepth 1 -type d | head -n 1'
  [ "$status" -eq 0 ]
  capture_dir="$output"
  [ -n "$capture_dir" ]
  [ -f "$target_dir/$capture_dir/argv.txt" ]
  [ -f "$target_dir/$capture_dir/context.log" ]
  [ -f "$target_dir/$capture_dir/request.txt" ]
  [ -f "$target_dir/$capture_dir/stdout.log" ]
  [ -f "$target_dir/$capture_dir/stderr.log" ]
  [ -f "$target_dir/$capture_dir/response.txt" ]
  run grep -F -- "do the thing" "$target_dir/$capture_dir/request.txt"
  [ "$status" -eq 0 ]
  run grep -F -- "codex stdout" "$target_dir/$capture_dir/response.txt"
  [ "$status" -eq 0 ]
}
