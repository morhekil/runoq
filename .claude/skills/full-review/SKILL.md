---
name: full-review
description: Thorough PERFECT-D code review of a complete implementation against its spec, producing a structured scorecard and actionable feedback.
---

# PERFECT-D Review of Code Implementation

Perform a thorough PERFECT-D code review of a single implementation against its spec, producing a structured scorecard and actionable feedback for the implementation agent.

## When to use

When you need to review a complete implementation (e.g. in a worktree or branch) and produce complete, actionable feedback that another agent can consume without further prompting. Used after diff-review passes as the full quality gate, or standalone for maintenance reviews.

## Inputs

- **Spec file**: path to the issue body or spec driving this work
- **Guidelines**: paths to any AGENTS.md or style/convention files in the target repo
- **Implementation root**: path to the implementation directory (e.g. the worktree root)

## Process

### Phase 1 — Read the spec and guidelines

Read the spec and all guideline files (AGENTS.md, CLAUDE.md, style guides) to build a checklist of:

- Functional requirements (what the implementation must do)
- Invariants and constraints (what must always hold)
- Conventions (naming, structure, patterns, tool choices)

If the spec is ambiguous, contradictory, or incomplete, flag each issue as a **Spec issue** in Phase 4. Do not score the implementation down for requirements the spec failed to define — but do flag the gap so it can be resolved.

### Phase 2 — Collect metrics, run tools, and run tests

Gather quantitative data using the project's own tooling wherever possible. Do not manually count metrics that a tool can report — tool output is authoritative, agent estimates are not.

#### 2a — Verify quality tooling

Every implementation must have four categories of quality tooling. Check the project's config files (package.json, pyproject.toml, Makefile, etc.) for:

- **Test runner** with coverage reporting (e.g. `node --test --experimental-test-coverage`, `pytest --cov`, `go test -cover`)
- **Linter** with complexity/style rules (e.g. ESLint, Pylint, golangci-lint, Clippy)
- **Formatter** (e.g. Prettier, Black, gofmt, rustfmt)
- **Type checker** if the language supports it (e.g. TypeScript `tsc --noEmit`, mypy, pyright)

If any category is missing, raise it as an **Infrastructure issue** in Phase 4 with a concrete recommendation for which tool to add and how to configure it. The absence of quality tooling is itself a review finding — do not silently accept it.

#### 2b — Run tooling

Run each configured tool and capture its output. If a tool is configured but not installed, install it first (e.g. `npm install`, `pip install -e '.[dev]'`). If a tool category is missing entirely (per 2a), skip it but ensure the issue is recorded.

1. **Formatter check** — run in check/diff mode (e.g. `prettier --check .`, `black --check .`, `gofmt -l .`). Any formatting violations indicate inconsistent style. Record pass/fail and the number of files with violations.
2. **Linter** — run with all configured rules. Capture warning and error counts. If the linter reports complexity metrics (cyclomatic complexity, cognitive complexity, max nesting depth), use those values directly.
3. **Type checker** — run and capture error count. Any type errors are bugs.
4. **Tests with coverage** — run the full test suite with coverage enabled. Capture line, branch, and function coverage percentages.

#### 2c — Consolidated metrics summary

Combine all Phase 2b tool outputs with manually collected metrics into a single summary table:

| Metric                    | Source                                                    | Target                  |
| ------------------------- | --------------------------------------------------------- | ----------------------- |
| Source LOC                | `wc -l` on non-test source files                          | —                       |
| Test LOC                  | `wc -l` on test files                                     | —                       |
| Test count                | count test declarations                                   | —                       |
| Test:Source ratio         | Test LOC / Source LOC                                     | >= 0.8:1                |
| Line coverage             | test runner                                               | >= 80%, preferable 100% |
| Branch coverage           | test runner                                               | >= 70%, preferable 100% |
| Function coverage         | test runner                                               | >= 90%, preferable 100% |
| Max function length       | linter or manual                                          | <= 40 LOC               |
| Max cyclomatic complexity | linter or manual                                          | <= 10 per function      |
| Max nesting depth         | linter or manual                                          | <= 4 levels             |
| Duplicate code blocks     | identify repeated logic (>= 5 similar lines) across files | 0                       |
| Formatter violations      | formatter check                                           | 0 files                 |
| Linter errors             | linter                                                    | 0                       |
| Linter warnings           | linter                                                    | 0                       |
| Type errors               | type checker                                              | 0                       |
| External deps             | imports / dependency file                                 | —                       |

Flag any value outside target range with a warning. These warnings become inputs to the relevant PERFECT-D dimensions.

Coverage percentages are the primary quality signal — a high test:source ratio with low branch coverage indicates shallow tests. Complexity metrics identify functions that need refactoring regardless of test coverage. Fewer tests (by LOC or count) are preferable if they achieve high coverage and test edge cases.

#### 2d — Metric integrity check

Scan the codebase for patterns that improve a reported metric by **reducing what is measured** rather than improving what is built. Any such pattern is a review finding with severity equal to Bugs.

**Test sabotage:**

- Tests marked `.skip`, `.todo`, or `.only` without a clear temporary-debugging justification
- Commented-out test cases or assertions
- Weakened assertions (e.g. `assert.ok(true)`, loose equality where strict is appropriate, missing `throws`/`rejects` assertions for error paths)
- Catch blocks in test code that swallow errors to prevent test failures
- Test counts that are disproportionately low relative to the number of code paths and spec requirements

**Coverage gaming:**

- Coverage-skip comments (`/* c8 ignore */`, `/* istanbul ignore */`, `/* v8 ignore */`, or equivalent) that are not justified by genuinely unreachable code (e.g. TypeScript exhaustiveness guards are acceptable)
- Coverage thresholds set lower than project targets
- Files or directories excluded from coverage measurement without justification
- Code restructured so that untested logic lives in paths excluded from coverage

**Lint / type suppression:**

- Inline suppression comments (`// eslint-disable`, `// @ts-ignore`, `// @ts-expect-error`, `// noinspection`, or equivalent) without an accompanying justification comment explaining why the suppression is necessary and why the underlying issue cannot be fixed
- Broadened type signatures (e.g. `any`, unnecessarily wide unions) used to silence type errors
- Linter rules disabled or relaxed in config files without justification

**Config weakening:**

- Strictness settings lowered in `tsconfig.json`, linter config, or test runner config
- Pre-commit hooks, CI checks, or quality gates removed or bypassed
- Scope of linting, type-checking, or testing reduced (e.g. excluding directories)

Each violation must be listed as a **Metric integrity** issue in Phase 4 with the same priority as Bugs. The fix is always to address the underlying problem (write proper tests, fix the type error, handle the edge case) rather than suppress the signal.

### Phase 3 — PERFECT-D review

Score the implementation 1–5 on each dimension. Each dimension description includes the SOLID principles it subsumes — evaluate those principles as part of scoring. All gaps must be addressed regardless of score — the score communicates severity and distance from the target, not whether a fix is optional.

#### Scoring rubric

| Score | Meaning                                                       |
| ----- | ------------------------------------------------------------- |
| 1     | Broken — fundamental issues that prevent correct operation    |
| 2     | Significant gaps — major structural or behavioral problems    |
| 3     | Functional with issues — works but has clear weaknesses       |
| 4     | Solid with minor gaps — well-built, small improvements needed |
| 5     | Exemplary — no issues found in this dimension                 |

#### Dimensions

| Dimension         | What to evaluate                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **P**urpose       | Does every module/function have a single clear job? Is the pipeline structured per spec? **SRP**: each module has one reason to change; if a function name needs "and", split it. Reference Phase 2 metrics: flag functions exceeding the max function length target or files mixing unrelated concerns.                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| **E**dge Cases    | Boundary conditions: empty inputs, missing config, malformed data, network failures, concurrent runs. **Security**: input sanitization, credential handling (no secrets in logs or error messages), injection risks, path traversal, OWASP top 10 where applicable.                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| **R**eliability   | Error handling, retry logic, atomicity (locks, TOCTOU), precision (appropriate numeric types for the domain). Graceful degradation vs hard failure — are the choices deliberate and correct?                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| **F**orm          | Naming, file structure, consistent patterns, adherence to guideline conventions. **DRY**: flag any logic block repeated in 2+ locations; shared utilities should be extracted. Reference Phase 2 metrics: formatter violations mean inconsistent style; linter errors/warnings must be addressed; type errors are bugs; complexity/nesting/duplication outside targets indicate structural problems.                                                                                                                                                                                                                                                                                                                                        |
| **E**vidence      | Test coverage depth (reference Phase 2 metrics), fixture quality, are failure paths tested? Do tests match spec requirements? **Prefer integration and feature-level tests** that exercise real code paths end-to-end over narrow unit tests with heavy mocking. Unit tests are justified only for pure logic with complex branching (parsers, calculators, state machines) — not for gluing together I/O calls. Flag unnecessary low-level tests that could be replaced by a higher-level test with equal or better coverage. Are mocks used sparingly and only at true system boundaries (network, filesystem)? **Metric integrity**: check for any of the gaming patterns listed in Phase 2d below — their presence is a blocking issue. |
| **C**larity       | Readability, type safety, schema validation at data boundaries, discriminated unions vs stringly-typed. **LSP**: consistent return types — no surprise throws for expected outcomes (e.g. "not found"); use result types or discriminated unions for expected failure cases.                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| **T**aste         | Architectural choices that are hard to retrofit. **OCP**: can the system accept new steps/formats without modifying core logic? **ISP**: are dependency interfaces narrow — do consumers only depend on what they use? **DIP**: does core logic depend on abstractions, not concrete I/O? Is I/O injectable for testing?                                                                                                                                                                                                                                                                                                                                                                                                                    |
| **D**ocumentation | See documentation checklist below.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |

#### Documentation checklist (for the D dimension)

Evaluate across two categories. The required diagrams and depth of textual documentation scale with implementation complexity — use the explicit criteria below to determine what is required, not reviewer discretion.

**Textual documentation**

- Does a README exist with: purpose, usage examples, input/output format, exit codes, and known caveats?
- Is the architecture documented — how data flows through the system end-to-end, what each module is responsible for, and the rationale behind key structural decisions (pipeline shape, pattern choices, non-obvious constraints)?
- Is business logic documented — what domain rules apply, what calculations are performed, what edge cases are handled and why?
- Are technical decisions documented — why specific libraries, patterns, or API choices were made?
- Are non-obvious code paths explained with inline comments or a dedicated documentation section?
- Are agent/contributor guidelines (AGENTS.md or equivalent) up to date with current invariants and conventions?

**Architecture and flow diagrams**

Use text-based diagrams (Mermaid or ASCII) embedded in markdown files. The following thresholds determine what is required:

- **Context diagram (C4 Level 1)**: Show the system in relation to external actors and systems it interacts with (APIs, datastores, users). Required for every implementation.
- **Container diagram (C4 Level 2)**: Show the major runtime components — entry points, processing stages, external clients, caches. Required when the implementation has 3+ distinct modules.
- **Component diagram (C4 Level 3)**: Show key classes/modules and their dependencies within a container. Optional — include only when module relationships are non-trivial and a simpler diagram would not suffice.
- **Flow diagram**: A sequence or flowchart showing the main processing pipeline from input to output, including error/retry paths. Required when the implementation has branching logic or multi-step processing.

### Phase 4 — Prioritized issues

List all issues found during Phase 3, grouped by severity:

1. **Spec issues** — Ambiguities, contradictions, or gaps in the spec itself (not scored against the implementation)
2. **Bugs** — Incorrect behavior per spec
3. **Design** — Structural problems, principle violations, race conditions, complexity hotspots
4. **Infrastructure** — Missing or misconfigured quality tooling (linter, formatter, type checker, test runner/coverage)
5. **Tests** — Coverage gaps, missing edge cases, untested failure paths. Any coverage metric (line, branch, function) below 100% must be justified or addressed with new tests.
6. **Documentation** — Missing or incomplete textual documentation, missing diagrams, outdated guidelines.

The scorecard target is 40/40 — any dimension with a score < 5 must have corresponding issues listed above. The score communicates how far off the implementation is, and all issues must be addressed.

Each issue must include: file:line (or file/location for documentation), what's wrong, why it matters, and a concrete fix. Reference the PERFECT-D dimension and any SOLID principle violated where applicable.

## Output format

The review MUST end with a complete agent feedback block. Do not ask the user for further input — generate the full feedback automatically as the final section.

```
## Code Metrics

| Metric | Value | Target | Status |
|---|---|---|---|
| Source files | N | — | |
| Source LOC | N | — | |
| Test files | N | — | |
| Test LOC | N | — | |
| Test count | N | — | |
| Test:Source ratio | N:1 | >= 0.8:1 | OK/WARN |
| Line coverage | N% | >= 80% | OK/WARN |
| Branch coverage | N% | >= 70% | OK/WARN |
| Function coverage | N% | >= 90% | OK/WARN |
| Max function length | N LOC | <= 40 | OK/WARN |
| Max cyclomatic complexity | N | <= 10 | OK/WARN |
| Max nesting depth | N | <= 4 | OK/WARN |
| Duplicate code blocks | N | 0 | OK/WARN |
| External deps | list | — | |
| Formatter violations | N files | 0 | OK/WARN |
| Linter errors | N | 0 | OK/WARN |
| Linter warnings | N | 0 | OK/WARN |
| Type errors | N | 0 | OK/WARN |

## PERFECT-D Scorecard

| Dimension | Score | SOLID | Notes |
|---|---|---|---|
| Purpose | /5 | SRP | ... |
| Edge Cases | /5 | — | ... |
| Reliability | /5 | — | ... |
| Form | /5 | DRY | ... |
| Evidence | /5 | — | ... |
| Clarity | /5 | LSP | ... |
| Taste | /5 | OCP, ISP, DIP | ... |
| Documentation | /5 | — | [summarize checklist: README, architecture docs, business logic docs, technical decisions, guidelines, applicable diagrams — note status of each] |
| **Total** | **/40** | | |

## Agent Feedback

[This section is always generated automatically — no user prompt required.]

### What's done well
- [2-3 specific strengths with file references]

### Spec Issues
- **[spec file:line or section]** — [what's ambiguous or missing]
[explanation of the gap]
**Suggested resolution:** [concrete clarification or default to propose]

### Bugs
- **[file:line]** — [what's wrong]
```

[offending code snippet]

```
[explanation of why it's a bug per spec]
**Fix:** [concrete code change or approach]

### Design
- **[file:line]** — [what's wrong] ([PERFECT-D dimension + SOLID principle violated])
```

[offending code snippet]

```
[explanation of structural problem]
**Fix:** [concrete code change or approach]

### Infrastructure
- **[file or config location]** — [what tooling is missing or misconfigured]
[explanation of impact on code quality]
**Fix:** [concrete tool to add, install command, and config to set up]

### Tests
- **[file:line]** — [what's missing or wrong]
[explanation of coverage gap or test weakness]
**Fix:** [concrete test to add or change]

### Documentation
- **[file or location]** — [what's missing or incomplete]
[explanation of why this documentation matters]
**Fix:** [concrete documentation to add — text content or diagram specification]

### Checklist
- [ ] [actionable item derived from bug #1]
- [ ] [actionable item derived from design issue #1]
- [ ] [actionable item derived from documentation issue #1]
- [ ] ...
- [ ] Re-run coverage and confirm line >= 80%, branch >= 70%, function >= 90%
- [ ] Re-run all tests and confirm green

VERDICT: PASS or ITERATE
SCORE: NN/40
CHECKLIST:
- [ ] item 1
- [ ] item 2
...
```

The final `VERDICT`, `SCORE`, and `CHECKLIST` lines MUST appear at the very end, as the orchestrator parses them from the return value.
