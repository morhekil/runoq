#!/usr/bin/env bats

load test_helper

@test "issue queue skill delegates to gh-issue-queue.sh and documents actions" {
  run grep -n '"\$RUNOQ_ROOT/scripts/gh-issue-queue.sh" next' "$RUNOQ_ROOT/.claude/skills/issue-queue/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "blocked_reasons" "$RUNOQ_ROOT/.claude/skills/issue-queue/SKILL.md"
  [ "$status" -eq 0 ]
}

@test "pr lifecycle skill delegates to gh-pr-lifecycle.sh and audit markers" {
  run grep -n '"\$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" update-summary' "$RUNOQ_ROOT/.claude/skills/pr-lifecycle/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "runoq:payload" "$RUNOQ_ROOT/.claude/skills/pr-lifecycle/SKILL.md"
  [ "$status" -eq 0 ]
}

@test "plan to issues skill requires confirmation before creating issues" {
  run grep -n "confirmation before creating" "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "dependency graph" "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$RUNOQ_ROOT/scripts/gh-issue-queue.sh" create' "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$RUNOQ_ROOT/templates/issue-template.md"' "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "broad-example.md" "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "narrow-example.md" "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "untestable-example.md" "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
}

@test "plan slicing fixtures cover broad narrow and untestable examples" {
  run test -f "$RUNOQ_ROOT/test/fixtures/plans/broad-example.md"
  [ "$status" -eq 0 ]
  run test -f "$RUNOQ_ROOT/test/fixtures/plans/narrow-example.md"
  [ "$status" -eq 0 ]
  run test -f "$RUNOQ_ROOT/test/fixtures/plans/untestable-example.md"
  [ "$status" -eq 0 ]
}

@test "github orchestrator prompt follows the dispatch loop and avoids source edits" {
  run grep -n '^---$' "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: github-orchestrator$' "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Dispatch loop" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "You do not edit source code" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "circuit breaker" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$RUNOQ_ROOT/scripts/dispatch-safety.sh" reconcile' "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$RUNOQ_ROOT/scripts/dispatch-safety.sh" eligibility' "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Create an initial empty commit on the issue branch" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Agent tool prompt must contain ONLY the typed payload data needed to start the run" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT inline a replacement workflow" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n 'subagent_type: "diff-reviewer"' "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Own the Claude diff-reviewer subagent yourself" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "feed only the parsed checklist block back into the next" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: PASS" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: FAIL" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: blocked" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: dry-run" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: budget exhaustion" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "runoq:event" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
  run grep -n "Never dispatch \`issue-runner\` with an ad hoc inline implementation prompt" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
  [ "$status" -eq 0 ]
}

@test "issue runner prompt enforces payload parsing and verification gates" {
  run grep -n '^---$' "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: issue-runner$' "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$RUNOQ_ROOT/scripts/state.sh" validate-payload' "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "codex exec --dangerously-bypass-approvals-and-sandbox" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT combine this with \`--full-auto\`" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "runoq:payload:codex-return" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "runoq:payload:issue-runner" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"commits_pushed": \["<sha>", "\.\.\."\]' "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Make the JSON the LAST fenced block" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Your ONLY tools are: Bash (to run codex and git commands), Write" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT try to spawn or simulate a reviewer" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "status\": \"review_ready\" | \"fail\" | \"budget_exhausted\"" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Verified diffs are handed back to" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Return ONLY this marked JSON payload as your final structured result" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT run diff review yourself" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "verification retry" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "review handoff" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Parse the JSON output" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$RUNOQ_ROOT/scripts/verify.sh" round' "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Never hand-write or reconstruct payload JSON yourself" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "maxTokenBudget" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "expand the review file list beyond the directly changed files" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n '"\$RUNOQ_ROOT/scripts/gh-pr-lifecycle.sh" read-actionable' "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: verification retry" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: stuck" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "You never read the round-N files" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Read only actionable PR comments via" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Do NOT try to spawn or simulate a reviewer" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "Scenario: budget exhaustion" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
}

@test "diff reviewer prompt includes Claude agent frontmatter and review-only rules" {
  run grep -n '^---$' "$RUNOQ_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: diff-reviewer$' "$RUNOQ_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Read `"\$RUNOQ_ROOT/.claude/skills/diff-review/SKILL.md"` and follow it' "$RUNOQ_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n "You \\*\\*NEVER\\*\\* edit source code" "$RUNOQ_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n "You \\*\\*MUST\\*\\* write the full review report to \`reviewLogPath\`" "$RUNOQ_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^   REVIEW-TYPE: diff$' "$RUNOQ_ROOT/.claude/agents/diff-reviewer.md"
  [ "$status" -eq 0 ]
}

@test "maintenance reviewer prompt includes Claude agent frontmatter" {
  run grep -n '^---$' "$RUNOQ_ROOT/.claude/agents/maintenance-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: maintenance-reviewer$' "$RUNOQ_ROOT/.claude/agents/maintenance-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/maintenance-reviewer.md"
  [ "$status" -eq 0 ]
}

@test "subagent worktree orchestration skill defines hard delegation boundaries" {
  run grep -n "^name: subagent-worktree-orchestration$" "$RUNOQ_ROOT/.agents/skills/subagent-worktree-orchestration/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "one sibling git worktree per worker" "$RUNOQ_ROOT/.agents/skills/subagent-worktree-orchestration/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "exact owned files" "$RUNOQ_ROOT/.agents/skills/subagent-worktree-orchestration/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "exact forbidden files" "$RUNOQ_ROOT/.agents/skills/subagent-worktree-orchestration/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT use `spawn_agent`' "$RUNOQ_ROOT/.agents/skills/subagent-worktree-orchestration/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n "git status --short" "$RUNOQ_ROOT/.agents/skills/subagent-worktree-orchestration/SKILL.md"
  [ "$status" -eq 0 ]
}
