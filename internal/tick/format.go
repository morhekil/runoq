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
	Proposal  Proposal    `json:"proposal"`
	Technical ReviewScore `json:"technical"`
	Product   ReviewScore `json:"product"`
	Warning   string      `json:"warning,omitzero"`
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
		}
	}

	return b.String()
}

// FormatProposalCommentBody renders the full proposal review comment with
// score table, warnings, formatted milestones, and collapsed JSON payload.
func FormatProposalCommentBody(input ProposalCommentInput) string {
	var b strings.Builder

	// Review scores table
	b.WriteString("## Review scores\n\n")
	b.WriteString("| Reviewer | Score | Verdict |\n")
	b.WriteString("|----------|-------|---------|\n")
	fmt.Fprintf(&b, "| Technical | %s | %s |\n", input.Technical.Score, input.Technical.Verdict)
	fmt.Fprintf(&b, "| Product | %s | %s |\n\n", input.Product.Score, input.Product.Verdict)

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
	b.WriteString("## Proposed milestones\n\n")
	b.WriteString(FormatPlanProposal(input.Proposal))

	// Collapsed JSON payload
	b.WriteString("\n<details>\n<summary>Raw JSON payload</summary>\n\n")
	b.WriteString("```json\n")
	proposalJSON, _ := json.MarshalIndent(input.Proposal, "", "  ")
	b.Write(proposalJSON)
	b.WriteString("\n```\n\n</details>\n")

	return b.String()
}
