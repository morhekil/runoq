# System Issues Remediation Plan

## Scope

This plan covers the runtime issues identified from comparing the smoke testing draft against the implemented system behavior.

In scope:

- reviewer output contract enforcement
- same-thread reviewer repair support
- transient/backoff tick semantics
- narrowing the Codex payload contract
- clearer payload normalization and operator-facing surfacing

Out of scope:

- pure smoke spec drift with no runtime defect
- broader planning-flow smoke coverage work, except where needed to reflect changed runtime behavior

DO NOT CHANGE: smoke spec at docs/plans/smoke-testing-spec.md - this is a future work plan, not existing system documentation

## Agreed Runtime Changes

### 1. Add same-thread reviewer repair support

Current state:

- `phaseReview` performs a fresh reviewer invocation and accepts whatever structured output it can parse.
- The Claude wrapper does not currently capture a resumable thread/session identifier for the reviewer path.

Target state:

- reviewer runs expose a resumable thread/session identifier when the Claude CLI provides one
- malformed reviewer output can be repaired within the same review tick using same-thread resume
- repair prompts are strictly limited to output-shape correction and must not trigger a second substantive review

Implementation notes:

- extend the Claude wrapper to capture any review-thread identity emitted by `stream-json`
- add a same-thread resume entrypoint for reviewer repair
- keep repair bounded to one attempt

### 2. Tighten the reviewer output contract

Current state:

- `phaseReview` does not enforce a complete review contract
- a review comment may be posted without the full required structure if the reviewer output omits it

Target state:

- required reviewer output fields/sections are explicit and validated
- minimum required contract:
  - `VERDICT`
  - `SCORE`
  - scorecard section
- if the first reviewer output is malformed:
  - attempt one same-thread repair
  - revalidate
  - fail deterministically if still invalid

Operator-visible behavior:

- invalid reviewer output becomes an explicit deterministic `FAIL` with a clear reason in persisted state and PR comment

### 3. Fix transient/backoff tick semantics

Current state:

- transient Codex failures persist retry/backoff state correctly
- the queue path still returns success for that tick
- `runoq loop` therefore treats the tick as work-done and can immediately retick instead of waiting

Target state:

- transient develop failures return a waiting outcome from the queue/tick path
- `runoq loop` backs off using the existing waiting behavior
- PR diagnostic comments and persisted transient state remain unchanged

Operator-visible behavior:

- transient failures are reported as retryable/waiting rather than as successful progress

### 4. Narrow the Codex payload contract

Current state:

- Codex is asked to emit both reconstructable facts and non-reconstructable execution context
- `validate-payload` truth-backs commit/file data from git ground truth anyway

Target state:

- Codex only reports fields the system cannot reconstruct deterministically
- commit/file facts always come from deterministic reconstruction

Planned model-facing payload fields:

- `tests_run`
- `tests_passed`
- `test_summary`
- `build_passed`
- `blockers`
- `notes`

Fields to stop asking Codex for:

- `commits_pushed`
- `commit_range`
- `files_changed`
- `files_added`
- `files_deleted`

### 5. Tighten payload normalization and surfacing

Current state:

- payload normalization is deterministic and useful
- downstream comments/state do not surface enough detail about payload quality

Target state:

- keep normalization behavior
- surface payload quality clearly in state and audit comments
- preserve and expose:
  - `payload_schema_valid`
  - `payload_schema_errors`
  - `payload_source`

Operator-visible behavior:

- malformed or synthetic payloads are clearly distinguished from clean payloads
- later verification failures are easier to interpret from GitHub state alone

## Repair Policy

Standardize bounded structured-output repair:

- reviewer output: 1 same-thread repair attempt
- develop payload: 1 bounded repair attempt after the contract is simplified, unless implementation constraints force temporary preservation of the current higher limit during transition

Rule:

- initial attempt
- one bounded repair attempt
- deterministic failure if still invalid

## Delivery Sequence

### Phase 1. Reviewer repair foundation

Deliverables:

- Claude thread capture support
- reviewer same-thread resume support
- tests proving capture and resume behavior

### Phase 2. Review contract enforcement

Deliverables:

- explicit validation of required reviewer structure
- bounded same-thread repair attempt
- deterministic fail-closed review behavior

### Phase 3. Transient tick semantics

Deliverables:

- waiting exit propagation from transient develop failures
- loop backoff behavior aligned with persisted retry state

### Phase 4. Narrowed Codex payload contract

Deliverables:

- updated Codex prompt/schema
- updated payload validator
- updated issue-runner/orchestrator expectations

### Phase 5. Payload surfacing cleanup

Deliverables:

- improved audit comments/state fields for payload quality
- stable operator-facing reason strings

## TDD Plan

All changes should be implemented test-first.

Test areas to add or update:

- `internal/claude`
  - thread capture from review stream
  - reviewer resume invocation shape
- `internal/orchestrator`
  - malformed reviewer output
  - missing scorecard
  - successful same-thread reviewer repair
  - failed reviewer repair
  - transient develop failure returns waiting outcome
  - loop/backoff semantics at tick boundary
- `internal/issuerunner`
  - narrowed payload prompt/schema
  - bounded payload repair behavior after contract simplification
- `internal/state`
  - normalization of narrowed payload schema
  - deterministic reconstruction of commit/file facts
- `internal/verify`
  - continued handling of self-reported test/build failures from the narrowed payload

## Validation

Minimum validation after each phase:

```bash
go test ./internal/orchestrator
go test ./internal/issuerunner ./internal/state ./internal/verify
```

Additional validation when tick/loop behavior changes:

```bash
go test ./internal/tick ./internal/cli
```

Recommended final runtime sweep:

```bash
go test ./internal/orchestrator ./internal/issuerunner ./internal/state ./internal/verify ./internal/tick ./internal/cli
```

## Risks

### Reviewer resume support may be CLI-protocol dependent

Mitigation:

- capture the actual Claude stream shape in tests
- keep the resume contract narrow and bounded
- fail deterministically if the CLI does not provide a resumable thread identifier

### Payload-contract tightening may ripple into verification and smoke assumptions

Mitigation:

- land payload-contract changes only with synchronized validator and verification updates
- update the smoke spec after code changes, not before

### Waiting-semantics changes can affect loop throughput

Mitigation:

- test both single `tick` and `loop`
- ensure only transient backoff paths return waiting, not normal completed work
