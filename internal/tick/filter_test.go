package tick

import "testing"

func TestSelectItemsFromProposal(t *testing.T) {
	t.Parallel()

	p := Proposal{
		Items: []ProposalItem{
			{Title: "A"},
			{Title: "B"},
			{Title: "C"},
		},
	}

	tests := []struct {
		name      string
		selection ItemSelection
		want      []string
	}{
		{"empty selection keeps all", ItemSelection{}, []string{"A", "B", "C"}},
		{"approve subset", ItemSelection{Approved: []int{1, 3}}, []string{"A", "C"}},
		{"reject subset", ItemSelection{Rejected: []int{2}}, []string{"A", "C"}},
		{"approve and reject", ItemSelection{Approved: []int{1, 2, 3}, Rejected: []int{2}}, []string{"A", "C"}},
		{"reject all", ItemSelection{Rejected: []int{1, 2, 3}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SelectItemsFromProposal(p, tt.selection)
			titles := make([]string, len(got.Items))
			for i, item := range got.Items {
				titles[i] = item.Title
			}
			if !stringSliceEqual(titles, tt.want) {
				t.Errorf("got %v, want %v", titles, tt.want)
			}
		})
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
