#!/usr/bin/env bats

load test_helper

@test "issue queue skill delegates to gh-issue-queue.sh and documents actions" {
  run grep -n '"\$AGENDEV_ROOT/scripts/gh-issue-queue.sh" next' "$AGENDEV_ROOT/.claude/skills/issue-queue/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "blocked_reasons" "$AGENDEV_ROOT/.claude/skills/issue-queue/SKILL.md"
  [ "$status" -eq 0 ]
}

@test "pr lifecycle skill delegates to gh-pr-lifecycle.sh and audit markers" {
  run grep -n '"\$AGENDEV_ROOT/scripts/gh-pr-lifecycle.sh" update-summary' "$AGENDEV_ROOT/.claude/skills/pr-lifecycle/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "agendev:payload" "$AGENDEV_ROOT/.claude/skills/pr-lifecycle/SKILL.md"
  [ "$status" -eq 0 ]
}

@test "plan to issues skill requires confirmation before creating issues" {
  run grep -n "confirmation before creating" "$AGENDEV_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "dependency graph" "$AGENDEV_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$AGENDEV_ROOT/scripts/gh-issue-queue.sh" create' "$AGENDEV_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$AGENDEV_ROOT/templates/issue-template.md"' "$AGENDEV_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "broad-example.md" "$AGENDEV_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "narrow-example.md" "$AGENDEV_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "untestable-example.md" "$AGENDEV_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
}

@test "plan slicing fixtures cover broad narrow and untestable examples" {
  run test -f "$AGENDEV_ROOT/test/fixtures/plans/broad-example.md"
  [ "$status" -eq 0 ]
  run test -f "$AGENDEV_ROOT/test/fixtures/plans/narrow-example.md"
  [ "$status" -eq 0 ]
  run test -f "$AGENDEV_ROOT/test/fixtures/plans/untestable-example.md"
  [ "$status" -eq 0 ]
}

@test "github orchestrator prompt follows the dispatch loop and avoids source edits" {
  run grep -n '^---$' "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: github-orchestrator$' "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Dispatch loop" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "You do not edit source code" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "circuit breaker" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$AGENDEV_ROOT/scripts/dispatch-safety.sh" reconcile' "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$AGENDEV_ROOT/scripts/dispatch-safety.sh" eligibility' "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Create an initial empty commit on the issue branch" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Agent tool prompt must contain ONLY the typed payload data needed to start the run" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT inline a replacement workflow" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: PASS" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: FAIL" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: blocked" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: dry-run" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: budget exhaustion" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "agendev:event" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Never dispatch \`issue-runner\` with an ad hoc inline implementation prompt" "$AGENDEV_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
}

@test "issue runner prompt enforces payload parsing and verification gates" {
  run grep -n '^---$' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: issue-runner$' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$AGENDEV_ROOT/scripts/state.sh" validate-payload' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "codex exec --dangerously-bypass-approvals-and-sandbox" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT combine this with \`--full-auto\`" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "agendev:payload:codex-return" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"commits_pushed": \["<sha>", "\.\.\."\]' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Make the JSON the LAST fenced block" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Your ONLY tools are: Bash (to run codex and git commands), Agent" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n 'subagent_type: "diff-reviewer"' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '`mode`: `bypassPermissions`' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Use the \`Agent\` tool directly" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT call \`ToolSearch\`, \`Task\`, or any other tool-discovery helper" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT review the diff yourself, do NOT fall back to codex as reviewer" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "typed review payload needed to start the run" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "the verdict block cannot be parsed, stop and return FAIL with blocker \`diff reviewer unavailable\`" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Parse the JSON output" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$AGENDEV_ROOT/scripts/verify.sh" round' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Never hand-write or reconstruct payload JSON yourself" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "maxTokenBudget" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "direct consumers" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$AGENDEV_ROOT/scripts/gh-pr-lifecycle.sh" read-actionable' "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: iterate" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: stuck" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: verification failure" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: final PASS" "$AGENDEV_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
}

@test "diff reviewer prompt includes Claude agent frontmatter and review-only rules" {
  run grep -n '^---$' "$AGENDEV_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: diff-reviewer$' "$AGENDEV_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$AGENDEV_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Read `"\$AGENDEV_ROOT/.claude/skills/diff-review/SKILL.md"` and follow it' "$AGENDEV_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n "You \\*\\*NEVER\\*\\* edit source code" "$AGENDEV_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n "You \\*\\*MUST\\*\\* write the full review report to \`reviewLogPath\`" "$AGENDEV_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^   REVIEW-TYPE: diff$' "$AGENDEV_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
}

@test "maintenance reviewer prompt includes Claude agent frontmatter" {
  run grep -n '^---$' "$AGENDEV_ROOT/.claude/agents/maintenance-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: maintenance-reviewer$' "$AGENDEV_ROOT/.claude/agents/maintenance-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$AGENDEV_ROOT/.claude/agents/maintenance-reviewer.md"
  [ "$status" -eq 0 ]
}
