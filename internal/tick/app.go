package tick

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const usageText = `Usage:
  tick-fmt format-proposal              < proposal.json
  tick-fmt proposal-comment-body        < input.json
  tick-fmt milestone-body               < item.json
  tick-fmt adjustment-review-body       < input.json
  tick-fmt parse-verdict                < verdict.txt
  tick-fmt extract-json <marker>        < text
  tick-fmt human-comment-selection      < issue-view.json
  tick-fmt select-items --selection JSON < proposal.json
  tick-fmt merge-checklists <left> <right>
  tick-fmt parse-agent-response         < response.json
  tick-fmt replace-proposal-in-body <proposal-file> < body
`

// App is the tick-fmt CLI application.
type App struct {
	args   []string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// New creates a new tick-fmt App.
func New(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:   args,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
}

// Run executes the subcommand and returns an exit code.
func (a *App) Run(_ context.Context) int {
	if len(a.args) == 0 {
		fmt.Fprint(a.stderr, usageText)
		return 1
	}

	switch a.args[0] {
	case "format-proposal":
		return a.runFormatProposal()
	case "proposal-comment-body":
		return a.runProposalCommentBody()
	case "milestone-body":
		return a.runMilestoneBody()
	case "adjustment-review-body":
		return a.runAdjustmentReviewBody()
	case "parse-verdict":
		return a.runParseVerdict()
	case "extract-json":
		return a.runExtractJSON()
	case "human-comment-selection":
		return a.runHumanCommentSelection()
	case "select-items":
		return a.runSelectItems()
	case "merge-checklists":
		return a.runMergeChecklists()
	case "parse-agent-response":
		return a.runParseAgentResponse()
	case "replace-proposal-in-body":
		return a.runReplaceProposalInBody()
	default:
		fmt.Fprintf(a.stderr, "unknown subcommand: %s\n", a.args[0])
		fmt.Fprint(a.stderr, usageText)
		return 1
	}
}

func (a *App) readStdin() ([]byte, error) {
	return io.ReadAll(a.stdin)
}

func (a *App) fail(format string, args ...any) int {
	fmt.Fprintf(a.stderr, "tick-fmt: "+format+"\n", args...)
	return 1
}

func (a *App) runFormatProposal() int {
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	var p Proposal
	if err := json.Unmarshal(data, &p); err != nil {
		return a.fail("parse proposal: %v", err)
	}
	fmt.Fprint(a.stdout, FormatPlanProposal(p))
	return 0
}

func (a *App) runProposalCommentBody() int {
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	var input ProposalCommentInput
	if err := json.Unmarshal(data, &input); err != nil {
		return a.fail("parse input: %v", err)
	}
	fmt.Fprint(a.stdout, FormatProposalCommentBody(input))
	return 0
}

func (a *App) runMilestoneBody() int {
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	var item ProposalItem
	if err := json.Unmarshal(data, &item); err != nil {
		return a.fail("parse item: %v", err)
	}
	fmt.Fprint(a.stdout, FormatMilestoneBody(item))
	return 0
}

func (a *App) runAdjustmentReviewBody() int {
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	var input AdjustmentReviewInput
	if err := json.Unmarshal(data, &input); err != nil {
		return a.fail("parse input: %v", err)
	}
	fmt.Fprint(a.stdout, FormatAdjustmentReviewBody(input))
	return 0
}

func (a *App) runParseVerdict() int {
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	score, err := ParseVerdictBlock(string(data))
	if err != nil {
		return a.fail("%v", err)
	}
	enc := json.NewEncoder(a.stdout)
	if err := enc.Encode(score); err != nil {
		return a.fail("encode: %v", err)
	}
	return 0
}

func (a *App) runExtractJSON() int {
	if len(a.args) < 2 {
		return a.fail("extract-json requires a marker argument")
	}
	marker := a.args[1]
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	extracted, err := ExtractMarkedJSONBlock(string(data), marker)
	if err != nil {
		return a.fail("%v", err)
	}
	fmt.Fprintln(a.stdout, extracted)
	return 0
}

func (a *App) runHumanCommentSelection() int {
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	sel, err := ParseHumanCommentSelection(string(data))
	if err != nil {
		return a.fail("%v", err)
	}
	enc := json.NewEncoder(a.stdout)
	if err := enc.Encode(sel); err != nil {
		return a.fail("encode: %v", err)
	}
	return 0
}

func (a *App) runSelectItems() int {
	var selJSON string
	for i, arg := range a.args {
		if arg == "--selection" && i+1 < len(a.args) {
			selJSON = a.args[i+1]
			break
		}
	}
	if selJSON == "" {
		return a.fail("select-items requires --selection argument")
	}
	var sel ItemSelection
	if err := json.Unmarshal([]byte(selJSON), &sel); err != nil {
		return a.fail("parse selection: %v", err)
	}
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	var p Proposal
	if err := json.Unmarshal(data, &p); err != nil {
		return a.fail("parse proposal: %v", err)
	}
	result := SelectItemsFromProposal(p, sel)
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return a.fail("encode: %v", err)
	}
	return 0
}

func (a *App) runMergeChecklists() int {
	left := ""
	right := ""
	if len(a.args) >= 2 {
		left = a.args[1]
	}
	if len(a.args) >= 3 {
		right = a.args[2]
	}
	fmt.Fprintln(a.stdout, MergeChecklists(left, right))
	return 0
}

func (a *App) runParseAgentResponse() int {
	data, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	resp, err := ParseAgentResponse(string(data))
	if err != nil {
		return a.fail("%v", err)
	}
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(resp); err != nil {
		return a.fail("encode: %v", err)
	}
	return 0
}

func (a *App) runReplaceProposalInBody() int {
	if len(a.args) < 2 {
		return a.fail("replace-proposal-in-body requires a proposal file argument")
	}
	proposalData, err := os.ReadFile(a.args[1])
	if err != nil {
		return a.fail("read proposal file: %v", err)
	}
	bodyData, err := a.readStdin()
	if err != nil {
		return a.fail("read stdin: %v", err)
	}
	result := ReplaceProposalInBody(string(bodyData), string(proposalData))
	fmt.Fprint(a.stdout, result)
	return 0
}
