package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/saruman/runoq/agents"
)

// AgentInvoker abstracts agent invocation for testability.
type AgentInvoker interface {
	Invoke(ctx context.Context, opts agents.InvokeOptions) (agents.Response, error)
}

// DispatchConfig configures a plan dispatch run.
type DispatchConfig struct {
	ReviewType        string // "milestone" or "task"
	PlanFile          string
	MilestoneFile     string // optional, required for task mode
	PriorFindingsFile string // optional
	RunoqRoot         string
	MaxRounds         int
	ClaudeBin         string // optional, defaults to "claude"
	Invoker           AgentInvoker
	Stderr            io.Writer
}

// DispatchResult holds the output of a dispatch run.
type DispatchResult struct {
	Proposal      Proposal
	Technical     ReviewScore
	Product       ReviewScore
	Warning       string
	FormattedBody string // the full proposal body ready to write to issue
}

// RunDispatch runs the decompose-review loop and returns the approved proposal.
func RunDispatch(ctx context.Context, cfg DispatchConfig) (DispatchResult, error) {
	d := &dispatcher{cfg: cfg}

	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}

	claudeBin := cfg.ClaudeBin
	if claudeBin == "" {
		claudeBin = "claude"
	}

	decomposer := "milestone-decomposer"
	marker := "runoq:payload:milestone-decomposer"
	if cfg.ReviewType == "task" {
		decomposer = "task-decomposer"
		marker = "runoq:payload:task-decomposer"
	}

	planDir := filepath.Dir(cfg.PlanFile)
	var milestoneDir string
	if cfg.MilestoneFile != "" {
		milestoneDir = filepath.Dir(cfg.MilestoneFile)
	}

	var mergedChecklist string
	var proposal Proposal
	var technical, product ReviewScore
	var warning string

	for round := 1; round <= maxRounds; round++ {
		d.step(fmt.Sprintf("Decomposition round %d/%d", round, maxRounds))
		d.detail("agent", decomposer)

		feedbackPayload := mergedChecklist
		if feedbackPayload != "" {
			d.info("feeding back reviewer checklist from previous round")
			feedbackPayload = "CHECKLIST:\n" + feedbackPayload
		}

		// Build payload
		payload := buildDecomposerPayload(cfg, feedbackPayload)

		// Call decomposer
		d.info("calling " + decomposer + " agent")
		addDirs := collectAddDirs(cfg.RunoqRoot, planDir, milestoneDir)
		resp, err := cfg.Invoker.Invoke(ctx, agents.InvokeOptions{
			Backend: agents.Claude,
			Agent:   decomposer,
			Bin:     claudeBin,
			RawArgs: buildAgentArgs(decomposer, cfg.RunoqRoot, addDirs, payload),
			WorkDir: planDir,
			Payload: payload,
		})
		if err != nil {
			return DispatchResult{}, fmt.Errorf("decomposer failed: %w", err)
		}

		proposalJSON := extractJSONFromResponse(resp.Text, marker)
		if err := json.Unmarshal([]byte(proposalJSON), &proposal); err != nil {
			return DispatchResult{}, fmt.Errorf("invalid %s output: %w", decomposer, err)
		}
		d.detail("items proposed", fmt.Sprintf("%d", len(proposal.Items)))

		// Review
		d.step(fmt.Sprintf("Reviewing proposal (round %d)", round))

		reviewPayload := proposalJSON

		d.info("calling plan-reviewer-technical")
		techResp, err := cfg.Invoker.Invoke(ctx, agents.InvokeOptions{
			Backend: agents.Claude,
			Agent:   "plan-reviewer-technical",
			Bin:     claudeBin,
			RawArgs: buildAgentArgs("plan-reviewer-technical", cfg.RunoqRoot, addDirs, reviewPayload),
			WorkDir: planDir,
			Payload: reviewPayload,
		})
		if err != nil {
			return DispatchResult{}, fmt.Errorf("technical reviewer failed: %w", err)
		}
		technical, err = ParseVerdictBlock(techResp.Text)
		if err != nil {
			return DispatchResult{}, fmt.Errorf("parse technical verdict: %w", err)
		}

		d.info("calling plan-reviewer-product")
		prodResp, err := cfg.Invoker.Invoke(ctx, agents.InvokeOptions{
			Backend: agents.Claude,
			Agent:   "plan-reviewer-product",
			Bin:     claudeBin,
			RawArgs: buildAgentArgs("plan-reviewer-product", cfg.RunoqRoot, addDirs, reviewPayload),
			WorkDir: planDir,
			Payload: reviewPayload,
		})
		if err != nil {
			return DispatchResult{}, fmt.Errorf("product reviewer failed: %w", err)
		}
		product, err = ParseVerdictBlock(prodResp.Text)
		if err != nil {
			return DispatchResult{}, fmt.Errorf("parse product verdict: %w", err)
		}

		d.detail("technical", fmt.Sprintf("%s (%s)", technical.Verdict, technical.Score))
		d.detail("product", fmt.Sprintf("%s (%s)", product.Verdict, product.Score))

		if technical.Verdict == "PASS" && product.Verdict == "PASS" {
			d.success("Both reviewers passed")
			warning = ""
			break
		}

		mergedChecklist = MergeChecklists(technical.Checklist, product.Checklist)
		if round == maxRounds {
			d.warn("Max review rounds reached — proceeding with current proposal")
			warning = "max review rounds reached"
			break
		}
		d.info("reviewers requested changes, iterating")
	}

	// Format the body
	body := FormatProposalCommentBody(ProposalCommentInput{
		Proposal:   proposal,
		Technical:  technical,
		Product:    product,
		Warning:    warning,
		ReviewType: cfg.ReviewType,
	})

	return DispatchResult{
		Proposal:      proposal,
		Technical:     technical,
		Product:       product,
		Warning:       warning,
		FormattedBody: body,
	}, nil
}

func buildDecomposerPayload(cfg DispatchConfig, feedbackChecklist string) string {
	m := map[string]string{
		"planPath":          cfg.PlanFile,
		"templatePath":      filepath.Join(cfg.RunoqRoot, "templates", "issue-template.md"),
		"reviewType":        cfg.ReviewType,
		"feedbackChecklist": feedbackChecklist,
	}
	if cfg.MilestoneFile != "" {
		m["milestonePath"] = cfg.MilestoneFile
	}
	if cfg.PriorFindingsFile != "" {
		m["priorFindingsPath"] = cfg.PriorFindingsFile
	}
	data, _ := json.Marshal(m)
	return string(data)
}

func collectAddDirs(runoqRoot, planDir, milestoneDir string) []string {
	dirs := []string{runoqRoot}
	if planDir != "" {
		dirs = append(dirs, planDir)
	}
	if milestoneDir != "" {
		dirs = append(dirs, milestoneDir)
	}
	return dirs
}

func buildAgentArgs(agent, runoqRoot string, addDirs []string, payload string) []string {
	args := []string{"--agent", agent, "--add-dir", runoqRoot}
	for _, d := range addDirs {
		if d != runoqRoot {
			args = append(args, "--add-dir", d)
		}
	}
	if payload != "" {
		args = append(args, "--", payload)
	}
	return args
}

func extractJSONFromResponse(text, marker string) string {
	// Try marked block extraction first
	if extracted, err := ExtractMarkedJSONBlock(text, marker); err == nil {
		return extracted
	}
	// Fall back to trying the text as raw JSON
	var v json.RawMessage
	if json.Unmarshal([]byte(text), &v) == nil {
		return text
	}
	return text
}

// --- Output helpers ---

type dispatcher struct {
	cfg DispatchConfig
}

func (d *dispatcher) step(msg string)        { fmt.Fprintf(d.cfg.Stderr, "\033[1;36m▸ %s\033[0m\n", msg) }
func (d *dispatcher) info(msg string)        { fmt.Fprintf(d.cfg.Stderr, "\033[2m  %s\033[0m\n", msg) }
func (d *dispatcher) detail(key, val string) { fmt.Fprintf(d.cfg.Stderr, "\033[2m  %s:\033[0m %s\n", key, val) }
func (d *dispatcher) success(msg string)     { fmt.Fprintf(d.cfg.Stderr, "\033[1;32m✔ %s\033[0m\n", msg) }
func (d *dispatcher) warn(msg string)        { fmt.Fprintf(d.cfg.Stderr, "\033[1;33m⚠ %s\033[0m\n", msg) }
