# Diff-Scoped PERFECT-D Review

A lightweight, diff-scoped PERFECT-D review that evaluates the changed code, its immediate context, and related files that consume changed interfaces. Same quality bar as `full-review`, smaller blast radius.

## When to use

When you need a fast quality gate on a diff range during the dev loop. This is the primary review gate in `issue-runner` — there is no separate full-review step in the dev loop.

## Inputs

- **Diff range**: `<base-sha>..<head-sha>` — the commit range to review (may span multiple commits)
- **Spec file**: path to the issue body or spec driving this work
- **Guidelines**: paths to any AGENTS.md or style/convention files in the target repo
- **Changed file list**: verified file list from `verify.sh` (use this, not self-reported lists)
- **Related file list**: files that import or reference changed files, discovered by the orchestrator via grep (see below)
- **Previous checklist** (optional): checklist from the prior review round, to verify items are addressed

## Process

### Phase 1 — Read the spec and guidelines

Read the spec and all guideline files (AGENTS.md, CLAUDE.md, style guides) to build a checklist of:

- Functional requirements (what the implementation must do)
- Invariants and constraints (what must always hold)
- Conventions (naming, structure, patterns, tool choices)

### Phase 2 — Examine the diff

Run `git diff <base-sha>..<head-sha>` to understand the combined change across all commits in the range. Identify:

- New files added
- Modified files and which functions/sections changed
- Deleted files or removed code

Cross-reference against the verified changed-file list from inputs. If there are discrepancies, flag them and use only the verified list.

### Phase 2b — Review related files for breakage

The orchestrator provides a **related file list** — files that import or reference changed files, discovered via grep. These are not part of the diff but may be broken by interface changes.

Read each related file and check for:

- **Broken call sites**: function signatures changed in the diff but callers in related files still use the old signature (wrong argument count, removed parameters, changed return type).
- **Missing imports**: exports renamed or removed in the diff but related files still import the old name.
- **Type mismatches**: type definitions changed in the diff but consumers in related files rely on the old shape.

Issues found in related files are scored and reported the same as issues in the diff itself — they represent real breakage caused by the change. Flag them in the Issues Found section with the related file path and a note that the breakage originates from the diff.

### Phase 3 — Run targeted tooling on changed files only

Run quality tools scoped to the changed files:

1. **Formatter check** — run in check/diff mode on changed files only. Record pass/fail and the number of files with violations.
2. **Linter** — run on changed files only. Capture warning and error counts.
3. **Type checker** — run full type check (types are global so this must run on the whole project).
4. **Tests with coverage** — run the full test suite with coverage enabled. Check coverage of changed lines specifically. Flag any changed/added lines that are not covered by tests.

Use the target repo's configured tooling (check package.json, pyproject.toml, Makefile, etc.). If a tool category is not configured, note it but do not block the review.

### Phase 3b — Metric integrity check (diff-scoped)

Scan the diff for changes that improve a reported metric by **reducing what is measured** rather than improving what is built. Any such change is a blocking issue (VERDICT: ITERATE) regardless of how other dimensions score.

**Test sabotage** — the diff must NOT:

- Delete or remove test files, test cases, or assertions
- Mark tests as `.skip`, `.todo`, or `.only` (unless `.only` is clearly temporary debugging that will be reverted)
- Comment out or weaken assertions (e.g. changing `strictEqual` to a looser check, removing `throws`/`rejects` assertions, replacing specific assertions with `assert.ok(true)`)
- Catch and swallow errors in test code to prevent test failures
- Reduce the number of test cases without a clear justification (e.g. replacing N narrow tests with fewer integration tests that cover the same paths is fine — simply deleting tests is not)

**Coverage gaming** — the diff must NOT:

- Add coverage-skip comments (`/* c8 ignore */`, `/* istanbul ignore */`, `/* v8 ignore */`, or equivalent) unless the skipped code is genuinely unreachable (e.g. TypeScript exhaustiveness guards)
- Lower coverage thresholds in config files
- Exclude files or directories from coverage measurement
- Restructure code so that untested logic moves into files/paths excluded from coverage

**Lint / type suppression** — the diff must NOT:

- Add inline suppression comments (`// eslint-disable`, `// @ts-ignore`, `// @ts-expect-error`, `// noinspection`, or equivalent) to silence errors instead of fixing them. Each suppression must have an accompanying justification comment explaining why the suppression is necessary and why the underlying issue cannot be fixed.
- Broaden type signatures (e.g. adding `any`, widening a union) to make type errors disappear
- Disable or relax linter rules in config files

**Config weakening** — the diff must NOT:

- Lower strictness settings in `tsconfig.json`, ESLint config, or test runner config
- Remove pre-commit hooks, CI checks, or quality gates
- Reduce the scope of what is linted, type-checked, or tested

If any of these patterns are found, flag them as **Metric integrity violation** issues with severity equal to Bugs. The fix is always to address the underlying problem rather than suppress the signal.

### Phase 4 — PERFECT-D evaluation scoped to the diff

Score the diff 1–5 on each dimension using the same rubric as `full-review`. Evaluate only the changed code and its immediate context — not the entire codebase.

#### Scoring rubric

| Score | Meaning                                                       |
| ----- | ------------------------------------------------------------- |
| 1     | Broken — fundamental issues that prevent correct operation    |
| 2     | Significant gaps — major structural or behavioral problems    |
| 3     | Functional with issues — works but has clear weaknesses       |
| 4     | Solid with minor gaps — well-built, small improvements needed |
| 5     | Exemplary — no issues found in this dimension                 |

#### Dimensions (evaluated against the diff only)

| Dimension         | What to evaluate in the diff                                                                              |
| ----------------- | --------------------------------------------------------------------------------------------------------- |
| **P**urpose       | Do new/modified functions have a single clear job? Any SRP violations introduced?                         |
| **E**dge Cases    | Are boundary conditions handled in new code? Security issues introduced? Input validation?                |
| **R**eliability   | Error handling in new code paths? Retry logic correct? Atomicity maintained?                              |
| **F**orm          | Naming, patterns consistent with existing code? DRY — any duplication introduced? Formatter/linter clean? |
| **E**vidence      | Are new/changed code paths covered by tests? Missing test cases for new logic?                            |
| **C**larity       | Type safety, schema validation in new code? Readability? Consistent return types?                         |
| **T**aste         | Any architectural regressions? DI patterns maintained? OCP/ISP/DIP preserved?                             |
| **D**ocumentation | If new public APIs/modules added, is documentation updated? README/AGENTS.md current?                     |

### Phase 5 — Verify previous checklist

If a previous checklist was provided, verify each item:

- Mark items that are fully addressed
- Flag items that are partially addressed or not addressed
- Note any new issues introduced while addressing checklist items

### Phase 6 — Output

Produce the verdict and checklist in the format the orchestrator expects.

**VERDICT is PASS** only if: no issues found in the diff scope (all dimensions score 5, no formatter/linter/type errors, no coverage gaps in changed code).

**VERDICT is ITERATE** if any issues are found. The checklist must list all actionable items.

The score communicates severity. A PASS requires all dimensions at 5 with zero tooling violations — there is no separate full-review gate in the dev loop.

## Output format

The review MUST end with a structured feedback block:

```
## Diff Metrics

| Metric | Value | Target | Status |
|---|---|---|---|
| Changed files | N | — | |
| Changed LOC (added+modified) | N | — | |
| Related files reviewed | N | — | |
| Formatter violations | N files | 0 | OK/WARN |
| Linter errors | N | 0 | OK/WARN |
| Linter warnings | N | 0 | OK/WARN |
| Type errors | N | 0 | OK/WARN |
| Test count | N | — | |
| Tests passing | N | N | OK/FAIL |
| Changed lines covered | N% | 100% | OK/WARN |

## PERFECT-D Scorecard (Diff-Scoped)

| Dimension | Score | Notes |
|---|---|---|
| Purpose | /5 | ... |
| Edge Cases | /5 | ... |
| Reliability | /5 | ... |
| Form | /5 | ... |
| Evidence | /5 | ... |
| Clarity | /5 | ... |
| Taste | /5 | ... |
| Documentation | /5 | ... |
| **Total** | **/40** | |

## Previous Checklist Status
- [x] [item that was addressed]
- [ ] [item that was NOT addressed — still needs work]
(omit this section if no previous checklist was provided)

## Issues Found
- **[file:line]** — [what's wrong] ([PERFECT-D dimension])
[explanation]
**Fix:** [concrete fix]

## Checklist
- [ ] [actionable item 1]
- [ ] [actionable item 2]
- [ ] ...

VERDICT: PASS or ITERATE
SCORE: NN/40
CHECKLIST:
- [ ] item 1
- [ ] item 2
...
```

The final `VERDICT`, `SCORE`, and `CHECKLIST` lines MUST appear at the very end, as the orchestrator parses them from the return value.
