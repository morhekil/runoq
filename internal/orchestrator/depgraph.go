package orchestrator

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// DepGraph is a dependency DAG built from GitHub issues under an epic.
// It supports depth-first task selection, cycle detection, and blocked diagnostics.
type DepGraph struct {
	nodes      map[int]*depNode
	readyLabel string
}

type depNode struct {
	issue     *issue
	priority  int
	dependsOn []int // upstream issue numbers
	blockedBy []int // subset of dependsOn that are not CLOSED
	inCycle   bool
}

// BuildDepGraph constructs a dependency graph from the issue list.
// Only OPEN task children of the given epic with the ready label are included as candidates.
// CLOSED issues are tracked for dependency resolution.
func BuildDepGraph(issues []issue, epicNumber int, readyLabel string) *DepGraph {
	g := &DepGraph{
		nodes:      make(map[int]*depNode),
		readyLabel: readyLabel,
	}

	// Index all issues for dependency resolution
	issueByNumber := make(map[int]*issue)
	for i := range issues {
		issueByNumber[issues[i].Number] = &issues[i]
	}

	// Build nodes for task children of the epic (both open and closed).
	for i := range issues {
		iss := &issues[i]
		if issueParentEpic(iss) != epicNumber {
			continue
		}
		if issueTypeOf(*iss) != "task" {
			continue
		}
		// For candidate selection, only open+ready issues matter.
		// But we add all tasks to the graph so the DAG visualization is complete.
		if iss.State == "OPEN" && readyLabel != "" && !hasLabel(iss, readyLabel) {
			continue
		}

		deps := iss.BlockedBy

		var blockedBy []int
		for _, dep := range deps {
			depIssue := issueByNumber[dep]
			if depIssue == nil || depIssue.State != "CLOSED" {
				blockedBy = append(blockedBy, dep)
			}
		}

		g.nodes[iss.Number] = &depNode{
			issue:     iss,
			priority:  issuePriority(iss),
			dependsOn: deps,
			blockedBy: blockedBy,
		}
	}

	g.detectCycles()
	return g
}

// Next returns the best task to work on, considering:
// 1. Only unblocked, non-cycle tasks
// 2. Effective priority (bubbles up from downstream)
// 3. Deepest remaining chain
// 4. Issue number tiebreak
func (g *DepGraph) Next() *issue {
	return g.NextAfter(0)
}

// NextAfter returns the best task, preferring downstream dependents of lastCompleted.
func (g *DepGraph) NextAfter(lastCompleted int) *issue {
	candidates := g.readyCandidates()
	if len(candidates) == 0 {
		return nil
	}

	// Prefer continuing the started subtree
	if lastCompleted > 0 {
		var continuations []*depNode
		for _, c := range candidates {
			if slices.Contains(c.dependsOn, lastCompleted) {
				continuations = append(continuations, c)
			}
		}
		if len(continuations) > 0 {
			g.sortCandidates(continuations)
			return continuations[0].issue
		}
	}

	g.sortCandidates(candidates)
	return candidates[0].issue
}

// HasCycle reports whether any cycle was detected in the graph.
func (g *DepGraph) HasCycle() bool {
	for _, n := range g.nodes {
		if n.inCycle {
			return true
		}
	}
	return false
}

// CycleMembers returns the issue numbers involved in cycles.
func (g *DepGraph) CycleMembers() []int {
	var members []int
	for num, n := range g.nodes {
		if n.inCycle {
			members = append(members, num)
		}
	}
	slices.Sort(members)
	return members
}

// BlockedReason returns a human-readable description of why a task is blocked.
func (g *DepGraph) BlockedReason(issueNumber int) string {
	n, ok := g.nodes[issueNumber]
	if !ok {
		return ""
	}
	if len(n.blockedBy) == 0 && !n.inCycle {
		return ""
	}
	if n.inCycle {
		return "in a dependency cycle"
	}
	var parts []string
	for _, dep := range n.blockedBy {
		if _, exists := g.nodes[dep]; exists {
			parts = append(parts, fmt.Sprintf("#%d (OPEN)", dep))
		} else {
			parts = append(parts, fmt.Sprintf("#%d (unknown issue)", dep))
		}
	}
	return "blocked by " + strings.Join(parts, ", ")
}

// --- internal ---

func (g *DepGraph) readyCandidates() []*depNode {
	var ready []*depNode
	for _, n := range g.nodes {
		if n.issue.State != "OPEN" {
			continue
		}
		if len(n.blockedBy) == 0 && !n.inCycle {
			ready = append(ready, n)
		}
	}
	return ready
}

func (g *DepGraph) sortCandidates(candidates []*depNode) {
	slices.SortFunc(candidates, func(a, b *depNode) int {
		// 1. Effective priority (lower wins)
		aPri := g.effectivePriority(a.issue.Number)
		bPri := g.effectivePriority(b.issue.Number)
		if aPri != bPri {
			if aPri < bPri {
				return -1
			}
			return 1
		}
		// 2. Deepest remaining chain (longer wins)
		aDepth := g.chainDepth(a.issue.Number)
		bDepth := g.chainDepth(b.issue.Number)
		if aDepth != bDepth {
			if aDepth > bDepth {
				return -1 // deeper chain first
			}
			return 1
		}
		// 3. Issue number tiebreak
		if a.issue.Number < b.issue.Number {
			return -1
		}
		if a.issue.Number > b.issue.Number {
			return 1
		}
		return 0
	})
}

// effectivePriority returns the minimum priority in the subtree rooted at this node
// (i.e., the node itself + all transitive dependents).
func (g *DepGraph) effectivePriority(num int) int {
	visited := make(map[int]bool)
	return g.effectivePriorityRec(num, visited)
}

func (g *DepGraph) effectivePriorityRec(num int, visited map[int]bool) int {
	if visited[num] {
		return 999999
	}
	visited[num] = true

	n, ok := g.nodes[num]
	if !ok {
		return 999999
	}
	minPri := n.priority

	// Check all nodes that depend on this one
	for otherNum, other := range g.nodes {
		if slices.Contains(other.dependsOn, num) {
			childPri := g.effectivePriorityRec(otherNum, visited)
			if childPri < minPri {
				minPri = childPri
			}
		}
	}
	return minPri
}

// chainDepth returns the length of the longest dependency chain starting from this node
// (counting downstream dependents).
func (g *DepGraph) chainDepth(num int) int {
	visited := make(map[int]bool)
	return g.chainDepthRec(num, visited)
}

func (g *DepGraph) chainDepthRec(num int, visited map[int]bool) int {
	if visited[num] {
		return 0
	}
	visited[num] = true

	maxChild := 0
	for otherNum, other := range g.nodes {
		if slices.Contains(other.dependsOn, num) {
			d := g.chainDepthRec(otherNum, visited)
			if d > maxChild {
				maxChild = d
			}
		}
	}
	return 1 + maxChild
}

// detectCycles uses DFS coloring to find cycles.
func (g *DepGraph) detectCycles() {
	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)
	color := make(map[int]int)
	var cycleNodes []int

	var dfs func(num int) bool
	dfs = func(num int) bool {
		color[num] = gray
		n, ok := g.nodes[num]
		if !ok {
			color[num] = black
			return false
		}
		hasCycle := false
		for _, dep := range n.dependsOn {
			if _, exists := g.nodes[dep]; !exists {
				continue
			}
			switch color[dep] {
			case gray:
				hasCycle = true
				cycleNodes = append(cycleNodes, dep)
			case white:
				if dfs(dep) {
					hasCycle = true
				}
			}
		}
		if hasCycle {
			cycleNodes = append(cycleNodes, num)
		}
		color[num] = black
		return hasCycle
	}

	for num := range g.nodes {
		if color[num] == white {
			dfs(num)
		}
	}

	for _, num := range cycleNodes {
		if n, ok := g.nodes[num]; ok {
			n.inCycle = true
		}
	}
}

// RenderMermaid generates a Mermaid graph visualization of the dependency DAG.
// Each node shows its issue number, title (truncated), and status icon.
// Also includes CLOSED issues that are dependencies (to show the full picture).
func (g *DepGraph) RenderMermaid(inProgressLabel, doneLabel string) string {
	var b strings.Builder
	b.WriteString("```mermaid\ngraph TD\n")

	// Collect all issue numbers referenced (nodes + their dependencies)
	allIssues := make(map[int]*depNode)
	for num, n := range g.nodes {
		allIssues[num] = n
	}

	// Render nodes
	for num, n := range allIssues {
		icon := g.statusIcon(n, inProgressLabel, doneLabel)
		title := truncateTitle(n.issue.Title, 40)
		fmt.Fprintf(&b, "    %d[\"%s #%d %s\"]\n", num, icon, num, title)
	}

	// Render edges (dependency → dependent means dependent depends on dependency)
	for num, n := range allIssues {
		for _, dep := range n.dependsOn {
			if _, exists := allIssues[dep]; exists {
				fmt.Fprintf(&b, "    %d --> %d\n", dep, num)
			}
		}
	}

	b.WriteString("```")
	return b.String()
}

func (g *DepGraph) statusIcon(n *depNode, inProgressLabel, doneLabel string) string {
	if n.issue.State == "CLOSED" || hasLabel(n.issue, doneLabel) {
		return "✅"
	}
	if n.inCycle {
		return "🔴"
	}
	if hasLabel(n.issue, inProgressLabel) {
		return "🔄"
	}
	if len(n.blockedBy) > 0 {
		return "🚫"
	}
	// Open, unblocked, not in progress — ready to be picked up
	return "🟢"
}

func truncateTitle(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

// fetchBlockedBy parses a GraphQL response containing blockedBy data
// and populates the BlockedBy field on each issue.
func fetchBlockedBy(issues []issue, graphqlResponse string) {
	var resp struct {
		Data struct {
			Repository struct {
				Issues struct {
					Nodes []struct {
						Number    int `json:"number"`
						BlockedBy struct {
							Nodes []struct {
								Number int `json:"number"`
							} `json:"nodes"`
						} `json:"blockedBy"`
					} `json:"nodes"`
				} `json:"issues"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(graphqlResponse), &resp); err != nil {
		return
	}

	byNumber := make(map[int][]int)
	for _, node := range resp.Data.Repository.Issues.Nodes {
		var deps []int
		for _, dep := range node.BlockedBy.Nodes {
			deps = append(deps, dep.Number)
		}
		byNumber[node.Number] = deps
	}

	for i := range issues {
		if deps, ok := byNumber[issues[i].Number]; ok {
			issues[i].BlockedBy = deps
		}
	}
}

// fetchIssueTypes parses a GraphQL response and populates the IssueType field
// on each issue from the issueType { name } data.
func fetchIssueTypes(issues []issue, graphqlResponse string) {
	var resp struct {
		Data struct {
			Repository struct {
				Issues struct {
					Nodes []struct {
						Number    int `json:"number"`
						IssueType *struct {
							Name string `json:"name"`
						} `json:"issueType"`
					} `json:"nodes"`
				} `json:"issues"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(graphqlResponse), &resp); err != nil {
		return
	}
	byNumber := make(map[int]string)
	for _, node := range resp.Data.Repository.Issues.Nodes {
		if node.IssueType != nil {
			byNumber[node.Number] = strings.ToLower(node.IssueType.Name)
		}
	}
	for i := range issues {
		if t, ok := byNumber[issues[i].Number]; ok {
			issues[i].IssueType = t
		}
	}
}

// issueType returns the issue's type, preferring the native GitHub IssueType field
// and falling back to the metadata block for backward compatibility.
// issueParentEpic returns the parent epic number, preferring the native ParentEpic field
// and falling back to the metadata block for backward compatibility.
func issueParentEpic(iss *issue) int {
	return iss.ParentEpic
}

// issuePriority returns 0 if the issue has the runoq:priority label (human-bumped),
// 1 otherwise (default).
func issuePriority(iss *issue) int {
	if hasLabel(iss, "runoq:priority") {
		return 0
	}
	return 1
}

// issueTypeOf returns the issue type from native GitHub API fields and labels.
// planning/adjustment are workflow states stored as labels, not GitHub types.
func issueTypeOf(iss issue) string {
	if hasLabel(&iss, "runoq:planning") {
		return "planning"
	}
	if hasLabel(&iss, "runoq:adjustment") {
		return "adjustment"
	}
	if iss.IssueType != "" {
		return iss.IssueType
	}
	return "task"
}

func hasLabel(iss *issue, name string) bool {
	for _, l := range iss.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}
