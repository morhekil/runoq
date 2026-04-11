# Approved Review Comment Semantics

## Purpose

This document records a non-urgent follow-up discovered after the 2026-04-11 spec/runtime alignment work.

The current system is mostly correct for approved planning and adjustment reviews, but there is still a semantic gap around fresh human comments that combine:

- item-selection directives such as `approve item 1`
- additional discussion, questions, caveats, or conditions in the same comment

The repo should eventually close that gap without expanding brittle deterministic parsing beyond what it can safely prove.

## Status

- Priority: low
- Scope: approved planning reviews and approved adjustment reviews
- Current state: documented for future work only
- Intent: preserve the current closed ledger in [issues.md](./issues.md) while keeping this narrower follow-up visible

## Current Runtime Behavior

Approved reviews currently do two things:

1. scan fresh human comments on the approved review
2. respond first only when a comment is classified as non-selection

The implementation boundary is:

- `comments.CommentHasSelection` in `comments/comments.go`
- `firstNonSelectionCommentID` in `internal/orchestrator/tick.go`

Today, a comment is effectively treated as selection-bearing if it contains recognizable approve/reject syntax. That is enough for pure directives such as:

- `approve item 1`
- `approve items 1 and 3`
- `reject 2`

It is not enough for mixed natural-language comments such as:

- `approve item 1, but explain why item 2 was dropped`
- `approve item 1 if the sequencing concern is addressed`
- `item 2 is fine, but I still want clarification on rollout order`

Those examples contain selection language, but they are not pure selection comments. They still require a reply before the review should be materialized or closed.

## Why This Matters

The smoke spec says:

- approved reviews respond to fresh non-selection human comments before apply/close
- pure selection comments may still influence which items are applied

That distinction is narrower than the current implementation. The runtime can currently treat any comment with selection syntax as safe to skip for reply handling, even when the same comment also contains discussion that should block materialization for that tick.

This is not a broad planning-comment failure. It is a specific approved-review precedence edge case.

## Core Design Question

How should the system interpret a single human comment that contains both:

- executable selection intent
- non-selection discussion

Two approaches were considered.

### Approach A: Deterministic regex expansion

Extend comment parsing until it can distinguish:

- `selection_only`
- `selection_plus_discussion`
- `discussion_only`

This is attractive because it keeps everything in deterministic Go, but it is brittle. Natural-language semantics degrade quickly once the parser needs to reason about:

- conditions
- caveats
- contrastive clauses like `but`
- implied questions
- scope of approval language

Example:

`approve item 1 if the sequencing concern is addressed; otherwise hold`

A deterministic parser is likely to over-trust the presence of `approve item 1` and under-model the condition attached to it.

### Approach B: Ask the LLM to split one comment into multiple directives

This would let the model produce a structured sequence such as:

1. selection directive
2. discussion directive

This sounds expressive, but it creates a larger and riskier contract surface. Once the model is allowed to split one comment into multiple executable pieces, the runtime now depends on the model to correctly infer:

- how many directives exist
- what order they should run in
- whether a selection is conditional
- whether the discussion invalidates immediate application
- whether the comment is actually approval, a question, or a change request with approval language attached

The same example shows the risk:

`approve item 1 if the sequencing concern is addressed; otherwise hold`

A directive-splitting model could incorrectly emit:

1. approve item 1
2. discuss sequencing concern

That would be wrong. The selection is conditional, so the fail-closed runtime behavior should be "do not apply yet."

## Recommended Direction

Use an LLM-backed semantic classifier, but keep workflow behavior deterministic in Go.

The model should not generate an execution plan from a mixed comment. It should only normalize the comment into a narrow structured classification that the runtime interprets using fixed rules.

### Recommended contract

For each fresh approved-review comment, normalize to a small structured result:

```json
{
  "classification": "pure_selection | mixed | discussion_only | ambiguous",
  "approved": [1],
  "rejected": [],
  "reply": "optional reply text when discussion is present"
}
```

Runtime rules:

- `pure_selection`
  The comment does not block apply/close in this tick. Its normalized selections may influence apply.
- `mixed`
  The comment requires a reply first. The review must not apply or close in the same tick.
- `discussion_only`
  The comment requires a reply first. The review must not apply or close in the same tick.
- `ambiguous`
  Fail closed. Treat it as reply-required and do not apply in the same tick.

This keeps the LLM at the natural-language boundary and keeps durable behavior in code.

## Why Classification Is Better Than Directive Splitting

Classification is intentionally narrower than directive splitting.

It answers one control question:

- "Is it safe to materialize the approved review now, or must the system respond first?"

That is a stable, review-gate decision.

Directive splitting tries to answer a larger question:

- "What operations should the runtime execute from this comment, in what order, and with what dependency semantics?"

That is a much harder problem and more likely to produce silent mis-execution from ambiguous language.

For this repository, fail-closed classification is the more defensible boundary because:

- it reduces prompt surface area
- it does not invent synthetic sub-directives that need durable audit semantics
- it keeps recovery behavior simple
- it avoids partial processing within a single comment

## Proposed Runtime Semantics

When an approved review has fresh human comments:

1. inspect comments oldest-first
2. normalize each comment through the classifier
3. if any fresh comment is `mixed`, `discussion_only`, or `ambiguous`:
   the system responds to that comment and exits the tick without applying or closing the review
4. only when all fresh comments are `pure_selection` may the review proceed to apply

Selection influence should still be preserved after the reply tick:

- normalized `approved` and `rejected` items should influence later apply
- the later apply path should not need to re-interpret the original free-form human text if a bot-normalized marker already exists

## Replay and Audit Design

The classifier result should be persisted as a bot-authored marker tied to the original comment id.

Example shape:

```html
<!-- runoq:bot:approved-review-comment comment-id:IC1 class:mixed approved:1 rejected: -->
```

Goals:

- prevent reclassification drift on replay
- make later apply deterministic
- preserve a visible audit trail on GitHub
- avoid duplicate replies during partial failure recovery

The runtime should prefer normalized bot markers over raw human text whenever both are present.

## Non-Goals

This follow-up should not:

- reopen the broader planning-comment architecture
- replace deterministic selection parsing everywhere
- let the model directly mutate proposal state from approved reviews
- let the model emit executable sub-directives that the runtime blindly follows

The goal is narrower:

- classify approved-review comments safely enough to enforce the documented precedence rule

## Suggested Implementation Plan

### Phase 1: Introduce classification contract

Add a small structured result type for approved-review comment normalization.

Likely code areas:

- `comments/comments.go`
- a new helper in `comments/` or `internal/orchestrator/`

Deliverables:

- parser for persisted bot markers
- validator for classifier output
- fallback behavior for invalid or missing output

### Phase 2: Add a classifier invocation path

Introduce a dedicated approved-review comment classifier agent or reuse an existing responder path with a narrower output contract.

Requirements:

- deterministic JSON output
- no proposal mutation authority
- fail closed on malformed output

### Phase 3: Update approved-review gate logic

Replace the current `selection` vs `non-selection` shortcut with:

- `pure_selection`
- everything else

Likely code area:

- `internal/orchestrator/tick.go`

Required behavior:

- mixed comments block apply/close for the current tick
- pure selection comments still contribute to apply filtering

### Phase 4: Normalize selection at the audit boundary

When a fresh approved-review comment is classified:

- post or persist a bot normalization marker
- use normalized selections in later apply ticks
- stop relying on raw text when normalization exists

### Phase 5: Add tests

Add focused tests for:

- pure selection comments
- mixed selection-plus-discussion comments
- conditional approvals
- ambiguous comments
- invalid classifier output
- replay behavior when normalization marker exists but reply/post-processing partially failed

Likely test files:

- `comments/comments_test.go`
- `comments/handler_test.go`
- `internal/orchestrator/tick_test.go`

## Acceptance Criteria

This future work is complete when:

- a comment like `approve item 1` does not block apply
- a comment like `approve item 1, but explain why item 2 was dropped` causes reply-first behavior and no apply/close in that tick
- a conditional comment like `approve item 1 if sequencing is fixed` fails closed and does not apply in that tick
- replay does not duplicate visible replies
- later apply uses normalized selections rather than re-interpreting ambiguous raw text

## Open Questions

- Should approved-review comment classification reuse `plan-comment-responder`, or should it be a separate narrower agent?
- Should normalized markers live in replies only, or should they also be recoverable from reactions or local resumability state?
- Should `ambiguous` always require a human-visible reply, or is a silent fail-closed no-op acceptable for the first version?

## Summary

This is a targeted future improvement, not an immediate bugfix campaign.

The important conclusion from the design discussion is:

- deterministic regex parsing is too weak for mixed approved-review comments
- directive splitting is too powerful and too easy to get wrong
- an LLM-backed, fail-closed classifier with deterministic runtime semantics is the best boundary for future work
