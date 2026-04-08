package orchestrator

import (
	"fmt"
	"strings"
	"testing"
)

func makeIssue(number int, priority int, state string, deps []int) issue {
	body := "<!-- runoq:meta\ntype: task\nparent_epic: 1\npriority: " + itoa(priority) + "\n-->"
	return issue{
		Number:     number,
		State:      state,
		Body:       body,
		Labels:     []label{{Name: "runoq:ready"}},
		BlockedBy:  deps,
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func TestDepGraphLinearChainReturnsRoot(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "OPEN", nil),       // A — root, no deps
		makeIssue(11, 1, "OPEN", []int{10}), // B → A
		makeIssue(12, 1, "OPEN", []int{11}), // C → B
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.Next()
	if next == nil {
		t.Fatal("Next() returned nil")
	}
	if next.Number != 10 {
		t.Fatalf("expected #10 (root of deepest chain), got #%d", next.Number)
	}
}

func TestDepGraphParallelChainsPicksDeeper(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "OPEN", nil),       // A — root of 3-deep chain
		makeIssue(11, 1, "OPEN", []int{10}), // B → A
		makeIssue(12, 1, "OPEN", []int{11}), // C → B
		makeIssue(20, 1, "OPEN", nil),       // D — root of 2-deep chain
		makeIssue(21, 1, "OPEN", []int{20}), // E → D
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.Next()
	if next == nil {
		t.Fatal("Next() returned nil")
	}
	if next.Number != 10 {
		t.Fatalf("expected #10 (deeper chain), got #%d", next.Number)
	}
}

func TestDepGraphPriorityNeverOverridesBlocking(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 0, "OPEN", []int{99}), // A — highest priority but blocked by missing #99
		makeIssue(20, 1, "OPEN", nil),        // B — lower priority but ready
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.Next()
	if next == nil {
		t.Fatal("Next() returned nil, expected #20")
	}
	if next.Number != 20 {
		t.Fatalf("expected #20 (ready), got #%d (blocked)", next.Number)
	}
}

func TestDepGraphPriorityBubblesUpDAG(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "OPEN", nil),       // A — pri=1
		makeIssue(11, 1, "OPEN", []int{10}), // B → A, pri=1
		makeIssue(12, 0, "OPEN", []int{11}), // C → B, pri=0 — bubbles up to make A effective pri=0
		makeIssue(20, 1, "OPEN", nil),       // D — independent, pri=1
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.Next()
	if next == nil {
		t.Fatal("Next() returned nil")
	}
	if next.Number != 10 {
		t.Fatalf("expected #10 (effective pri=0 from downstream #12), got #%d", next.Number)
	}
}

func TestDepGraphCycleDetection(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "OPEN", []int{11}), // A → B
		makeIssue(11, 1, "OPEN", []int{10}), // B → A (cycle)
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	if !g.HasCycle() {
		t.Fatal("expected cycle detected")
	}
	members := g.CycleMembers()
	if len(members) != 2 {
		t.Fatalf("expected 2 cycle members, got %d: %v", len(members), members)
	}
	next := g.Next()
	if next != nil {
		t.Fatalf("expected nil for cycled graph, got #%d", next.Number)
	}
}

func TestDepGraphBlockedReason(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "OPEN", nil),
		makeIssue(11, 1, "OPEN", []int{10, 99}), // blocked by #10 (OPEN) and #99 (missing)
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	reason := g.BlockedReason(11)
	if reason == "" {
		t.Fatal("expected blocked reason")
	}
	if !contains(reason, "#10") {
		t.Fatalf("expected #10 in reason, got %q", reason)
	}
	if !contains(reason, "#99") {
		t.Fatalf("expected #99 in reason, got %q", reason)
	}
}

func TestDepGraphNoDepsUsesIssueNumberTiebreak(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(30, 1, "OPEN", nil),
		makeIssue(20, 1, "OPEN", nil),
		makeIssue(10, 1, "OPEN", nil),
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.Next()
	if next == nil {
		t.Fatal("Next() returned nil")
	}
	if next.Number != 10 {
		t.Fatalf("expected #10 (lowest number tiebreak), got #%d", next.Number)
	}
}

func TestDepGraphContinueSubtreeOverParallel(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "CLOSED", nil),      // A — done
		makeIssue(11, 1, "OPEN", []int{10}),  // B → A — ready (continue chain)
		makeIssue(20, 1, "OPEN", nil),         // D — independent, also ready
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.NextAfter(10)
	if next == nil {
		t.Fatal("NextAfter returned nil")
	}
	if next.Number != 11 {
		t.Fatalf("expected #11 (continue chain from #10), got #%d", next.Number)
	}
}

func TestDepGraphMultipleDescendantsPriorityDisambiguates(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "CLOSED", nil),      // A — done
		makeIssue(11, 1, "OPEN", []int{10}),  // B → A, pri=1
		makeIssue(12, 0, "OPEN", []int{10}),  // C → A, pri=0 (higher urgency)
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.NextAfter(10)
	if next == nil {
		t.Fatal("NextAfter returned nil")
	}
	if next.Number != 12 {
		t.Fatalf("expected #12 (lower priority wins among descendants), got #%d", next.Number)
	}
}

func TestDepGraphNoLastCompletedFallsBackToDepthFirst(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "OPEN", nil),       // A — 2-deep chain
		makeIssue(11, 1, "OPEN", []int{10}), // B → A
		makeIssue(20, 1, "OPEN", nil),       // C — 1-deep
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.NextAfter(0) // no last completed
	if next == nil {
		t.Fatal("NextAfter(0) returned nil")
	}
	if next.Number != 10 {
		t.Fatalf("expected #10 (deeper chain, no continuation), got #%d", next.Number)
	}
}

func TestDepGraphPartiallyDone(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "CLOSED", nil),      // A — done
		makeIssue(11, 1, "OPEN", []int{10}),  // B → A — ready (A is done)
		makeIssue(12, 1, "OPEN", []int{11}),  // C → B — blocked
	}
	g := BuildDepGraph(issues, 1, "runoq:ready")
	next := g.Next()
	if next == nil {
		t.Fatal("Next() returned nil")
	}
	if next.Number != 11 {
		t.Fatalf("expected #11 (unblocked after A done), got #%d", next.Number)
	}
}

func TestFetchBlockedByPopulatesIssueField(t *testing.T) {
	t.Parallel()

	issues := []issue{
		{Number: 10, State: "OPEN"},
		{Number: 11, State: "OPEN"},
	}

	// Simulate GraphQL response: #11 is blocked by #10
	ghResponse := `{"data":{"repository":{"issues":{"nodes":[{"number":10,"blockedBy":{"nodes":[]}},{"number":11,"blockedBy":{"nodes":[{"number":10}]}}]}}}}`

	fetchBlockedBy(issues, ghResponse)

	if len(issues[0].BlockedBy) != 0 {
		t.Fatalf("expected #10 to have no blockers, got %v", issues[0].BlockedBy)
	}
	if len(issues[1].BlockedBy) != 1 || issues[1].BlockedBy[0] != 10 {
		t.Fatalf("expected #11 blocked by [10], got %v", issues[1].BlockedBy)
	}
}

func TestRenderMermaidLinearChain(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "OPEN", nil),
		makeIssue(11, 1, "OPEN", []int{10}),
		makeIssue(12, 1, "OPEN", []int{11}),
	}
	// Mark #10 as in-progress for icon test
	issues[0].Labels = append(issues[0].Labels, label{Name: "runoq:in-progress"})

	g := BuildDepGraph(issues, 1, "runoq:ready")
	mermaid := g.RenderMermaid("runoq:in-progress", "runoq:done")

	if !strings.Contains(mermaid, "graph LR") {
		t.Fatalf("expected mermaid graph header, got %q", mermaid)
	}
	if !strings.Contains(mermaid, "10[") && !strings.Contains(mermaid, "10(") {
		t.Fatalf("expected node 10 in graph, got %q", mermaid)
	}
	// Edges
	if !strings.Contains(mermaid, "10 --> 11") {
		t.Fatalf("expected edge 10 --> 11, got %q", mermaid)
	}
	if !strings.Contains(mermaid, "11 --> 12") {
		t.Fatalf("expected edge 11 --> 12, got %q", mermaid)
	}
	// Status icon for in-progress
	if !strings.Contains(mermaid, "🔄") {
		t.Fatalf("expected in-progress icon, got %q", mermaid)
	}
}

func TestRenderMermaidDoneAndBlocked(t *testing.T) {
	t.Parallel()

	issues := []issue{
		makeIssue(10, 1, "CLOSED", nil),
		makeIssue(11, 1, "OPEN", []int{10}),
		makeIssue(12, 1, "OPEN", []int{99}), // blocked by missing dep
	}
	issues[0].Labels = append(issues[0].Labels, label{Name: "runoq:done"})

	g := BuildDepGraph(issues, 1, "runoq:ready")
	mermaid := g.RenderMermaid("runoq:in-progress", "runoq:done")

	if !strings.Contains(mermaid, "⏳") {
		t.Fatalf("expected ready icon for #11, got %q", mermaid)
	}
	if !strings.Contains(mermaid, "🚫") {
		t.Fatalf("expected blocked icon for #12, got %q", mermaid)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
