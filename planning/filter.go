package planning

import "slices"

// SelectItemsFromProposal filters proposal items by selection. If Approved is
// empty, all items pass (unless rejected). Items are 1-indexed in the selection.
func SelectItemsFromProposal(p Proposal, sel ItemSelection) Proposal {
	filtered := make([]ProposalItem, 0, len(p.Items))
	for i, item := range p.Items {
		idx := i + 1 // 1-based
		if slices.Contains(sel.Rejected, idx) {
			continue
		}
		if len(sel.Approved) > 0 && !slices.Contains(sel.Approved, idx) {
			continue
		}
		filtered = append(filtered, item)
	}
	return Proposal{
		Items:    filtered,
		Warnings: p.Warnings,
	}
}
