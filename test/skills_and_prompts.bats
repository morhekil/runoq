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
  run grep -n "runoq:bot" "$RUNOQ_ROOT/.claude/agents/github-orchestrator.md"
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
  run grep -n "thread.started" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n -- "--json -o <logDir>/round-<round>-last-message.md" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "codex exec resume <thread_id>" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "payload_schema_valid" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
  [ "$status" -eq 0 ]
  run grep -n "payload_schema_errors" "$RUNOQ_ROOT/.claude/agents/issue-runner.md"
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

@test "plan reviewer technical prompt includes review dimensions and verdict contract" {
  run grep -n '^name: plan-reviewer-technical$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Feasibility' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Scope' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Technical risk' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Dependency sanity' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Complexity honesty' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'KISS/YAGNI' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Tech debt' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'runoq:payload:plan-review-technical' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'REVIEW-TYPE: plan-technical' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'VERDICT: PASS | ITERATE' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'SCORE:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'CHECKLIST:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'Output ONLY the verdict block' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
  run grep -n 'reviewType' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-technical.md"
  [ "$status" -eq 0 ]
}

@test "plan reviewer product prompt includes review dimensions and distinct marker" {
  run grep -n '^name: plan-reviewer-product$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n '^description:' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'PRD alignment' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'MVP focus' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'Feature scope' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'Milestone sequencing' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'Acceptance criteria quality' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'Discovery awareness' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'runoq:payload:plan-review-product' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'runoq:payload:plan-review-technical' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -ne 0 ]
  run grep -n 'REVIEW-TYPE: plan-product' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
  run grep -n 'Output ONLY the verdict block' "$RUNOQ_ROOT/.claude/agents/plan-reviewer-product.md"
  [ "$status" -eq 0 ]
}

@test "milestone decomposer prompt documents milestone-only output contract" {
  run test -f "$RUNOQ_ROOT/.claude/agents/plan-decomposer.md"
  [ "$status" -ne 0 ]
  run test -f "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '^name: milestone-decomposer$' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'runoq:payload:milestone-decomposer' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'runoq:payload:plan-decomposer' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -ne 0 ]
  run grep -n '"key"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"type"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"goal"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"criteria"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"scope"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"sequencing_rationale"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"priority"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"estimated_complexity"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -ne 0 ]
  run grep -n '"complexity_rationale"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -ne 0 ]
  run grep -n '"parent_epic_key"' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -ne 0 ]
  run grep -n 'implementation.*discovery.*migration.*cleanup' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/milestone-decomposer.md"
  [ "$status" -eq 0 ]
}

@test "task decomposer prompt documents single-milestone task output" {
  run grep -n '^name: task-decomposer$' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'runoq:payload:task-decomposer' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'milestonePath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'planPath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'priorFindingsPath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'templatePath' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"estimated_complexity"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"complexity_rationale"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"depends_on_keys"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n '"goal"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -ne 0 ]
  run grep -n '"sequencing_rationale"' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -ne 0 ]
  run grep -n 'single milestone' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT read source code' "$RUNOQ_ROOT/.claude/agents/task-decomposer.md"
  [ "$status" -eq 0 ]
}

@test "planning docs and skills reference milestone and task decomposition" {
  run grep -n 'milestone-decomposer' "$RUNOQ_ROOT/docs/reference/script-contracts.md"
  [ "$status" -eq 0 ]
  run grep -n 'task-decomposer' "$RUNOQ_ROOT/docs/reference/script-contracts.md"
  [ "$status" -eq 0 ]
  run grep -n 'plan-decomposer' "$RUNOQ_ROOT/docs/reference/script-contracts.md"
  [ "$status" -ne 0 ]

  run grep -n 'milestone-decomposer' "$RUNOQ_ROOT/docs/reference/cli.md"
  [ "$status" -eq 0 ]
  run grep -n 'task-decomposer' "$RUNOQ_ROOT/docs/reference/cli.md"
  [ "$status" -eq 0 ]
  run grep -n 'plan-decomposer' "$RUNOQ_ROOT/docs/reference/cli.md"
  [ "$status" -ne 0 ]

  run grep -n 'milestone-decomposer' "$RUNOQ_ROOT/docs/architecture/overview.md"
  [ "$status" -eq 0 ]
  run grep -n 'task-decomposer' "$RUNOQ_ROOT/docs/architecture/overview.md"
  [ "$status" -eq 0 ]
  run grep -n 'plan-decomposer' "$RUNOQ_ROOT/docs/architecture/overview.md"
  [ "$status" -ne 0 ]

  run grep -n 'milestone-decomposer' "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n 'task-decomposer' "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -eq 0 ]
  run grep -n 'plan-decomposer' "$RUNOQ_ROOT/.claude/skills/plan-to-issues/SKILL.md"
  [ "$status" -ne 0 ]
}

@test "cli and flow docs describe tick as the primary planning entrypoint" {
  run grep -n '^runoq tick$' "$RUNOQ_ROOT/docs/reference/cli.md"
  [ "$status" -eq 0 ]
  run grep -n '^### `runoq tick`$' "$RUNOQ_ROOT/docs/reference/cli.md"
  [ "$status" -eq 0 ]
  run grep -n 'deprecated in favor of `runoq tick`' "$RUNOQ_ROOT/docs/reference/cli.md"
  [ "$status" -eq 0 ]

  run grep -n 'milestone-decomposer' "$RUNOQ_ROOT/docs/architecture/flows.md"
  [ "$status" -eq 0 ]
  run grep -n 'task-decomposer' "$RUNOQ_ROOT/docs/architecture/flows.md"
  [ "$status" -eq 0 ]
  run grep -n 'plan-decomposer' "$RUNOQ_ROOT/docs/architecture/flows.md"
  [ "$status" -ne 0 ]
}

@test "milestone reviewer prompt documents adjustment output contract" {
  run grep -n '^name: milestone-reviewer$' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '^model: claude-opus-4-6$' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'runoq:payload:milestone-reviewer' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'milestonePath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'planPath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'completedTasksPath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'remainingMilestonesPath' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '"milestone_number"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '"status"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '"delivered_criteria"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '"missed_criteria"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '"learnings"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n '"proposed_adjustments"' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'modify' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'new_milestone' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'discovery' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'remove' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT create issues' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'Do NOT modify GitHub state' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
  [ "$status" -eq 0 ]
  run grep -n 'proposed_adjustments.*\[\]' "$RUNOQ_ROOT/.claude/agents/milestone-reviewer.md"
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
