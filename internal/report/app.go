package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"github.com/saruman/runoq/internal/shell"
)

const usageText = `Usage:
  report.sh summary [--last N]
  report.sh issue <issue-number>
  report.sh cost [--last N]
`

var issueStateFilePattern = regexp.MustCompile(`^[0-9]+\.json$`)

type App struct {
	args   []string
	env    []string
	cwd    string
	stdout io.Writer
	stderr io.Writer
}

type tokenTotals struct {
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cached_input"`
	Output      float64 `json:"output"`
	Total       float64 `json:"total,omitempty"`
}

type summaryResult struct {
	Issues        int         `json:"issues"`
	Pass          int         `json:"pass"`
	Fail          int         `json:"fail"`
	Caveats       int         `json:"caveats"`
	Tokens        tokenTotals `json:"tokens"`
	AverageRounds float64     `json:"average_rounds"`
}

type costResult struct {
	Issues        int          `json:"issues"`
	Tokens        *tokenTotals `json:"tokens,omitempty"`
	EstimatedCost float64      `json:"estimated_cost"`
}

type tokenCostConfig struct {
	TokenCost struct {
		InputPerMillion       float64 `json:"inputPerMillion"`
		CachedInputPerMillion float64 `json:"cachedInputPerMillion"`
		OutputPerMillion      float64 `json:"outputPerMillion"`
	} `json:"tokenCost"`
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:   append([]string(nil), args...),
		env:    append([]string(nil), env...),
		cwd:    cwd,
		stdout: stdout,
		stderr: stderr,
	}
}

func (a *App) Run() int {
	subcommand := ""
	if len(a.args) > 0 {
		subcommand = a.args[0]
	}

	switch subcommand {
	case "summary":
		return a.runSummary(a.args[1:])
	case "issue":
		return a.runIssue(a.args[1:])
	case "cost":
		return a.runCost(a.args[1:])
	default:
		a.printUsage()
		return 1
	}
}

func (a *App) runSummary(args []string) int {
	last, ok := a.parseLastFlag(args)
	if !ok {
		return 1
	}

	files, err := a.stateFiles(last)
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
	}
	if len(files) == 0 {
		return shell.WriteJSON(a.stdout, a.stderr, summaryResult{
			Issues:        0,
			Pass:          0,
			Fail:          0,
			Caveats:       0,
			Tokens:        tokenTotals{Input: 0, CachedInput: 0, Output: 0, Total: 0},
			AverageRounds: 0,
		})
	}

	states, err := loadStateFiles(files)
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
	}

	result := summaryResult{
		Issues:  len(states),
		Tokens:  tokenTotals{},
		Caveats: 0,
	}
	var roundTotal float64

	for _, state := range states {
		verdict := verdictValue(state)
		if verdict == "PASS" {
			result.Pass++
		}
		if phaseValue(state) == "FAILED" || verdict == "FAIL" {
			result.Fail++
		}
		if verdict == "PASS_WITH_CAVEATS" {
			result.Caveats++
		}

		input, cachedInput, output := roundTokenTotals(state)
		result.Tokens.Input += input
		result.Tokens.CachedInput += cachedInput
		result.Tokens.Output += output
		result.Tokens.Total += numberOrZero(state["tokens_used"])
		roundTotal += roundsCount(state)
	}

	result.AverageRounds = roundTotal / float64(len(states))
	return shell.WriteJSON(a.stdout, a.stderr, result)
}

func (a *App) runIssue(args []string) int {
	if len(args) != 1 {
		a.printUsage()
		return 1
	}

	stateDir, err := a.stateDir()
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
	}

	statePath := filepath.Join(stateDir, fmt.Sprintf("%s.json", args[0]))
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return shell.Failf(a.stderr, "No state file found for issue %s", args[0])
		}
		return shell.Failf(a.stderr, "Failed to read state file: %v", err)
	}
	if !json.Valid(data) {
		return shell.Failf(a.stderr, "Failed to parse state file: invalid JSON")
	}

	var formatted bytes.Buffer
	if err := json.Indent(&formatted, bytes.TrimSpace(data), "", "  "); err != nil {
		return shell.Failf(a.stderr, "Failed to format state file JSON: %v", err)
	}
	if err := formatted.WriteByte('\n'); err != nil {
		return shell.Failf(a.stderr, "Failed to format state file JSON: %v", err)
	}

	if _, err := a.stdout.Write(formatted.Bytes()); err != nil {
		return shell.Failf(a.stderr, "Failed to write state file: %v", err)
	}
	return 0
}

func (a *App) runCost(args []string) int {
	last, ok := a.parseLastFlag(args)
	if !ok {
		return 1
	}

	files, err := a.stateFiles(last)
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
	}
	if len(files) == 0 {
		return shell.WriteJSON(a.stdout, a.stderr, costResult{
			Issues:        0,
			EstimatedCost: 0,
		})
	}

	states, err := loadStateFiles(files)
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
	}

	configPath, ok := shell.EnvLookup(a.env, "RUNOQ_CONFIG")
	if !ok || strings.TrimSpace(configPath) == "" {
		return shell.Fail(a.stderr, "RUNOQ_CONFIG is required")
	}

	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read config: %v", err)
	}

	var config tokenCostConfig
	if err := json.Unmarshal(configBytes, &config); err != nil {
		return shell.Failf(a.stderr, "Failed to parse config: %v", err)
	}

	tokens := tokenTotals{}
	for _, state := range states {
		input, cachedInput, output := roundTokenTotals(state)
		tokens.Input += input
		tokens.CachedInput += cachedInput
		tokens.Output += output
	}

	result := costResult{
		Issues: len(states),
		Tokens: &tokenTotals{
			Input:       tokens.Input,
			CachedInput: tokens.CachedInput,
			Output:      tokens.Output,
		},
		EstimatedCost: ((tokens.Input / 1_000_000) * config.TokenCost.InputPerMillion) +
			((tokens.CachedInput / 1_000_000) * config.TokenCost.CachedInputPerMillion) +
			((tokens.Output / 1_000_000) * config.TokenCost.OutputPerMillion),
	}

	return shell.WriteJSON(a.stdout, a.stderr, result)
}

func (a *App) parseLastFlag(args []string) (*int, bool) {
	if len(args) == 0 {
		return nil, true
	}
	if len(args) != 2 || args[0] != "--last" {
		a.printUsage()
		return nil, false
	}

	last, err := strconv.Atoi(args[1])
	if err != nil || last < 0 {
		shell.Failf(a.stderr, "Invalid --last value: %s", args[1])
		return nil, false
	}
	return &last, true
}

func (a *App) stateFiles(last *int) ([]string, error) {
	stateDir, err := a.stateDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list state directory: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !issueStateFilePattern.MatchString(name) {
			continue
		}
		files = append(files, filepath.Join(stateDir, name))
	}
	slices.Sort(files)

	if last != nil {
		if *last == 0 {
			return []string{}, nil
		}
		if len(files) > *last {
			files = files[len(files)-*last:]
		}
	}
	return files, nil
}

func (a *App) stateDir() (string, error) {
	if stateDir, ok := shell.EnvLookup(a.env, "RUNOQ_STATE_DIR"); ok && strings.TrimSpace(stateDir) != "" {
		return stateDir, nil
	}
	if targetRoot, ok := shell.EnvLookup(a.env, "TARGET_ROOT"); ok && strings.TrimSpace(targetRoot) != "" {
		return filepath.Join(targetRoot, ".runoq", "state"), nil
	}
	if strings.TrimSpace(a.cwd) != "" {
		return filepath.Join(a.cwd, ".runoq", "state"), nil
	}
	return "", fmt.Errorf("unable to resolve state directory")
}

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}

func loadStateFiles(files []string) ([]map[string]any, error) {
	states := make([]map[string]any, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read state file %s: %w", file, err)
		}
		var state map[string]any
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, fmt.Errorf("failed to parse state file %s: %w", file, err)
		}
		states = append(states, state)
	}
	return states, nil
}

func phaseValue(state map[string]any) string {
	if phase, ok := state["phase"].(string); ok && phase != "" {
		return phase
	}
	status, ok := state["status"].(string)
	if !ok {
		return ""
	}
	switch status {
	case "done":
		return "DONE"
	case "failed":
		return "FAILED"
	default:
		return ""
	}
}

func verdictValue(state map[string]any) string {
	if outcome, ok := state["outcome"].(map[string]any); ok {
		if verdict, ok := outcome["verdict"].(string); ok {
			return verdict
		}
	}
	if result, ok := state["result"].(map[string]any); ok {
		if verdict, ok := result["verdict"].(string); ok {
			return verdict
		}
	}
	if verdict, ok := state["verdict"].(string); ok {
		return verdict
	}
	return ""
}

func roundsCount(state map[string]any) float64 {
	switch rounds := state["rounds"].(type) {
	case []any:
		return float64(len(rounds))
	case float64:
		return rounds
	case int:
		return float64(rounds)
	}

	if outcome, ok := state["outcome"].(map[string]any); ok {
		if roundsUsed, ok := outcome["rounds_used"]; ok {
			return numberOrZero(roundsUsed)
		}
	}
	if result, ok := state["result"].(map[string]any); ok {
		if roundsUsed, ok := result["rounds_used"]; ok {
			return numberOrZero(roundsUsed)
		}
	}
	return 0
}

func roundTokenTotals(state map[string]any) (float64, float64, float64) {
	rounds, ok := state["rounds"].([]any)
	if !ok {
		return 0, 0, 0
	}

	var input float64
	var cachedInput float64
	var output float64

	for _, round := range rounds {
		roundMap, ok := round.(map[string]any)
		if !ok {
			continue
		}
		tokensMap, ok := roundMap["tokens"].(map[string]any)
		if !ok {
			continue
		}
		input += numberOrZero(tokensMap["input"])
		cachedInput += numberOrZero(tokensMap["cached_input"])
		output += numberOrZero(tokensMap["output"])
	}
	return input, cachedInput, output
}

func numberOrZero(value any) float64 {
	switch n := value.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err == nil {
			return parsed
		}
	}
	return 0
}

