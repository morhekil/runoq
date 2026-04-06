package tick

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProposalItem is a single item in a decomposer proposal.
type ProposalItem struct {
	Key                 string   `json:"key,omitzero"`
	Title               string   `json:"title"`
	Type                string   `json:"type"`
	Goal                string   `json:"goal,omitzero"`
	Criteria            []string `json:"criteria,omitzero"`
	Scope               []string `json:"scope,omitzero"`
	SequencingRationale string   `json:"sequencing_rationale,omitzero"`
	Priority            *int     `json:"priority,omitzero"`
	EstimatedComplexity string   `json:"estimated_complexity,omitzero"`
	ComplexityRationale string   `json:"complexity_rationale,omitzero"`
	Body                string   `json:"body,omitzero"`
}

// Proposal is the full decomposer output.
type Proposal struct {
	Items    []ProposalItem `json:"items"`
	Warnings []string       `json:"warnings,omitzero"`
}

// ReviewScore holds a parsed reviewer verdict.
type ReviewScore struct {
	Verdict   string `json:"verdict"`
	Score     string `json:"score"`
	Checklist string `json:"checklist"`
}

// ItemSelection holds approved/rejected item indices (1-based).
type ItemSelection struct {
	Approved []int `json:"approved"`
	Rejected []int `json:"rejected"`
}

// ProposalCommentInput is the input for building a full proposal review comment.
type ProposalCommentInput struct {
	Proposal   Proposal    `json:"proposal"`
	Technical  ReviewScore `json:"technical"`
	Product    ReviewScore `json:"product"`
	Warning    string      `json:"warning,omitzero"`
	ReviewType string      `json:"review_type,omitzero"` // "milestone" or "task"
}

// Adjustment is a single proposed adjustment from a milestone review.
type Adjustment struct {
	Type                  string `json:"type"`
	Title                 string `json:"title,omitzero"`
	Description           string `json:"description,omitzero"`
	Reason                string `json:"reason,omitzero"`
	TargetMilestoneNumber *int   `json:"target_milestone_number,omitzero"`
	SuggestedPosition     string `json:"suggested_position,omitzero"`
}

// AdjustmentReviewInput is the input for building an adjustment review issue body.
type AdjustmentReviewInput struct {
	ProposedAdjustments []Adjustment `json:"proposed_adjustments"`
}

// FormatPlanProposal renders proposal items as numbered markdown sections.
func FormatPlanProposal(p Proposal) string {
	var b strings.Builder
	b.WriteString("<!-- runoq:payload:plan-proposal -->\n")

	for i, item := range p.Items {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&b, "### %d. %s\n", i+1, item.Title)
		b.WriteString("**Type:** ")
		b.WriteString(item.Type)
		if item.Priority != nil {
			fmt.Fprintf(&b, " · **Priority:** %d", *item.Priority)
		}
		b.WriteString("\n\n")

		if item.Goal != "" {
			fmt.Fprintf(&b, "> %s\n\n", item.Goal)
		}

		if len(item.Criteria) > 0 {
			b.WriteString("**Acceptance criteria:**\n")
			for _, c := range item.Criteria {
				fmt.Fprintf(&b, "- [ ] %s\n", c)
			}
			b.WriteString("\n")
		}

		if len(item.Scope) > 0 {
			b.WriteString("**Scope:**\n")
			for _, s := range item.Scope {
				fmt.Fprintf(&b, "- %s\n", s)
			}
			b.WriteString("\n")
		}

		if item.EstimatedComplexity != "" {
			fmt.Fprintf(&b, "**Complexity:** %s", item.EstimatedComplexity)
			if item.ComplexityRationale != "" {
				fmt.Fprintf(&b, " — %s", item.ComplexityRationale)
			}
			b.WriteString("\n")
		}

		if item.Body != "" {
			b.WriteString("\n")
			b.WriteString(item.Body)
			b.WriteString("\n")
		}
	}

	return b.String()
}

// FormatProposalCommentBody renders the full proposal review comment with
// score table, warnings, formatted milestones, and collapsed JSON payload.
func FormatProposalCommentBody(input ProposalCommentInput) string {
	var b strings.Builder

	// Review scores table (omitted when scores are empty, e.g. revised proposals)
	if input.Technical.Score != "" || input.Product.Score != "" {
		b.WriteString("## Review scores\n\n")
		b.WriteString("| Reviewer | Score | Verdict |\n")
		b.WriteString("|----------|-------|---------|\n")
		fmt.Fprintf(&b, "| Technical | %s | %s |\n", input.Technical.Score, input.Technical.Verdict)
		fmt.Fprintf(&b, "| Product | %s | %s |\n\n", input.Product.Score, input.Product.Verdict)
	}

	// Reviewer checklists (feedback explaining score deductions)
	if input.Technical.Checklist != "" {
		fmt.Fprintf(&b, "**Technical reviewer notes:**\n%s\n\n", input.Technical.Checklist)
	}
	if input.Product.Checklist != "" {
		fmt.Fprintf(&b, "**Product reviewer notes:**\n%s\n\n", input.Product.Checklist)
	}

	// Process-level warning (e.g. max rounds reached)
	if input.Warning != "" {
		fmt.Fprintf(&b, "> **Warning:** %s\n\n", input.Warning)
	}

	// Decomposer warnings
	if len(input.Proposal.Warnings) > 0 {
		b.WriteString("**Warnings from decomposer:**\n")
		for _, w := range input.Proposal.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
		b.WriteString("\n")
	}

	// Proposal content
	heading := "Proposed milestones"
	if input.ReviewType == "task" {
		heading = "Proposed tasks"
	}
	fmt.Fprintf(&b, "## %s\n\n", heading)
	b.WriteString(FormatPlanProposal(input.Proposal))

	// Collapsed JSON payload
	b.WriteString("\n<details>\n<summary>Raw JSON payload</summary>\n\n")
	b.WriteString("```json\n")
	proposalJSON, _ := json.MarshalIndent(input.Proposal, "", "  ")
	b.Write(proposalJSON)
	b.WriteString("\n```\n\n</details>\n")

	return b.String()
}

// FormatMilestoneBody renders an issue body for a milestone epic.
func FormatMilestoneBody(item ProposalItem) string {
	var b strings.Builder
	b.WriteString("## Context\n\n")
	fmt.Fprintf(&b, "Goal: %s\n\n", item.Goal)
	fmt.Fprintf(&b, "Scope: %s\n\n", strings.Join(item.Scope, ", "))
	b.WriteString("## Acceptance Criteria\n\n")
	for _, c := range item.Criteria {
		fmt.Fprintf(&b, "- [ ] %s\n", c)
	}
	return b.String()
}

// FormatAdjustmentReviewBody renders the body for an adjustment review issue.
func FormatAdjustmentReviewBody(input AdjustmentReviewInput) string {
	var b strings.Builder
	b.WriteString("## Acceptance Criteria\n\n- [ ] Review proposed adjustments.\n\n")

	for i, adj := range input.ProposedAdjustments {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		title := adj.Title
		if title == "" {
			title = adj.Description
		}
		fmt.Fprintf(&b, "### %d. %s\n", i+1, title)
		fmt.Fprintf(&b, "**Type:** %s", adj.Type)
		if adj.TargetMilestoneNumber != nil {
			fmt.Fprintf(&b, " · **Target:** #%d", *adj.TargetMilestoneNumber)
		}
		b.WriteString("\n\n")
		if adj.Description != "" {
			fmt.Fprintf(&b, "> %s\n\n", adj.Description)
		}
		if adj.Reason != "" {
			fmt.Fprintf(&b, "**Reason:** %s\n", adj.Reason)
		}
	}

	b.WriteString("\n<details>\n<summary>Raw JSON payload</summary>\n\n```json\n")
	data, _ := json.MarshalIndent(input, "", "  ")
	b.Write(data)
	b.WriteString("\n```\n\n</details>\n")
	return b.String()
}

// MergeChecklists concatenates two checklist strings, dropping blank lines.
func MergeChecklists(left, right string) string {
	var lines []string
	for _, src := range []string{left, right} {
		if src == "" {
			continue
		}
		for line := range strings.SplitSeq(src, "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

const proposalStartMarker = "<!-- runoq:proposal-start -->"

// ReplaceProposalInBody replaces the proposal section in an issue body with
// new proposal content. The proposal section starts at the
// <!-- runoq:proposal-start --> marker. If no marker exists, the new content
// is appended with the marker.
func ReplaceProposalInBody(existingBody string, newProposal string) string {
	if idx := strings.Index(existingBody, proposalStartMarker); idx >= 0 {
		return existingBody[:idx] + proposalStartMarker + "\n" + newProposal
	}
	return existingBody + "\n\n" + proposalStartMarker + "\n" + newProposal
}
