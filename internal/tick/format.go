package tick

import (
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
