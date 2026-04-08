// Package claude provides Go wrappers for invoking the Claude CLI
// with capture and streaming support, replacing shell-based helpers.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/saruman/runoq/internal/shell"
)

// StreamConfig describes a streaming Claude invocation that captures output
// and extracts the final text result.
type StreamConfig struct {
	// OutputFile receives the extracted final text.
	OutputFile string
	// WorkDir is the working directory for the Claude process.
	WorkDir string
	// Args are the arguments to pass to claude (after --print --verbose --output-format stream-json).
	Args []string
	// Env is the environment for the process.
	Env []string
	// Stderr receives the Claude process stderr.
	Stderr io.Writer
	// Progress receives human-readable progress lines (tool use, thinking indicators).
	// If nil, progress is discarded.
	Progress io.Writer
}

// CaptureConfig describes a captured Claude CLI invocation.
type CaptureConfig struct {
	// WorkDir is the working directory for the process.
	WorkDir string
	// Args are the arguments to pass to claude.
	Args []string
	// Env is the environment for the process.
	Env []string
	// Stdout receives the process stdout.
	Stdout io.Writer
	// Stderr receives the process stderr.
	Stderr io.Writer
}

// Stream runs claude --print --verbose --output-format stream-json, captures
// the raw stream to a log directory, emits progress to StreamConfig.Progress,
// and writes the extracted final text to StreamConfig.OutputFile.
func Stream(ctx context.Context, exec shell.CommandExecutor, cfg StreamConfig) error {
	claudeBin := resolveClaudeBin(cfg.Env)

	captureDir, err := makeCaptureDir(cfg.Env, "claude", captureNameFromArgs(cfg.Args))
	if err != nil {
		return fmt.Errorf("create capture dir: %w", err)
	}

	stderrFile, err := os.Create(filepath.Join(captureDir, "stderr.log"))
	if err != nil {
		return err
	}
	defer stderrFile.Close()

	streamFile, err := os.Create(filepath.Join(captureDir, "stdout.log"))
	if err != nil {
		return err
	}
	defer streamFile.Close()

	progressLog, err := os.Create(filepath.Join(captureDir, "progress.log"))
	if err != nil {
		return err
	}
	defer progressLog.Close()

	writeCaptureContext(captureDir, claudeBin, cfg.Env, cfg.Args)
	writeRequestArg(captureDir, cfg.Args)

	progress := cfg.Progress
	if progress == nil {
		progress = io.Discard
	}

	// Use a pipe to capture stdout for both the stream file and parsing.
	pr, pw := io.Pipe()

	// Tee stderr to both the capture file and the caller's stderr.
	stderrWriter := cfg.Stderr
	if stderrWriter == nil {
		stderrWriter = io.Discard
	}
	stderrTee := io.MultiWriter(stderrFile, stderrWriter)

	fullArgs := append([]string{"--print", "--verbose", "--output-format", "stream-json"}, cfg.Args...)

	// Run claude in background, writing to the pipe.
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		errCh <- exec(ctx, shell.CommandRequest{
			Name:   claudeBin,
			Args:   fullArgs,
			Dir:    cfg.WorkDir,
			Env:    cfg.Env,
			Stdout: pw,
			Stderr: stderrTee,
		})
	}()

	// Read the stream and emit progress while tee-ing to the stream file.
	scanner := bufio.NewScanner(io.TeeReader(pr, streamFile))
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var resultText, assistantText string
	for scanner.Scan() {
		line := scanner.Text()
		emitProgress(line, progress, progressLog)
		extractText(line, &resultText, &assistantText)
	}

	claudeErr := <-errCh

	// Determine final text using the same priority as the shell version.
	finalText := selectFinalText(resultText, assistantText)

	if finalText != "" {
		_ = os.WriteFile(cfg.OutputFile, []byte(finalText+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(captureDir, "response.txt"), []byte(finalText+"\n"), 0o644)
	} else {
		_ = os.WriteFile(cfg.OutputFile, nil, 0o644)
		_ = os.WriteFile(filepath.Join(captureDir, "response.txt"), nil, 0o644)
	}

	return claudeErr
}

// CapturedExec runs the claude CLI with stdout/stderr captured to log files.
// Stdout is also copied to response.txt in the capture directory.
func CapturedExec(ctx context.Context, exec shell.CommandExecutor, cfg CaptureConfig) error {
	claudeBin := resolveClaudeBin(cfg.Env)

	captureDir, err := makeCaptureDir(cfg.Env, "claude", captureNameFromArgs(cfg.Args))
	if err != nil {
		return fmt.Errorf("create capture dir: %w", err)
	}

	stdoutFile, err := os.Create(filepath.Join(captureDir, "stdout.log"))
	if err != nil {
		return err
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(filepath.Join(captureDir, "stderr.log"))
	if err != nil {
		return err
	}
	defer stderrFile.Close()

	writeCaptureContext(captureDir, claudeBin, cfg.Env, cfg.Args)
	writeRequestArg(captureDir, cfg.Args)

	stdoutWriter := cfg.Stdout
	if stdoutWriter == nil {
		stdoutWriter = io.Discard
	}
	stderrWriter := cfg.Stderr
	if stderrWriter == nil {
		stderrWriter = io.Discard
	}

	stdoutTee := io.MultiWriter(stdoutFile, stdoutWriter)
	stderrTee := io.MultiWriter(stderrFile, stderrWriter)

	execErr := exec(ctx, shell.CommandRequest{
		Name:   claudeBin,
		Args:   cfg.Args,
		Dir:    cfg.WorkDir,
		Env:    cfg.Env,
		Stdout: stdoutTee,
		Stderr: stderrTee,
	})

	// Flush and copy stdout to response.txt.
	_ = stdoutFile.Sync()
	src, err := os.Open(filepath.Join(captureDir, "stdout.log"))
	if err == nil {
		dst, err2 := os.Create(filepath.Join(captureDir, "response.txt"))
		if err2 == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	return execErr
}

// resolveClaudeBin returns the claude binary path from env or the default.
func resolveClaudeBin(env []string) string {
	if v, ok := shell.EnvLookup(env, "RUNOQ_CLAUDE_BIN"); ok && v != "" {
		return v
	}
	return "claude"
}

// captureNameFromArgs extracts the agent name from --agent <name>, or falls back to "claude".
func captureNameFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "--agent" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return "claude"
}

// captureRequestArg extracts the payload argument (the arg after "--").
func captureRequestArg(args []string) string {
	sawDelimiter := false
	for _, arg := range args {
		if arg == "--" {
			sawDelimiter = true
			continue
		}
		if sawDelimiter {
			return arg
		}
	}
	return ""
}

// makeCaptureDir creates a timestamped capture directory.
func makeCaptureDir(env []string, toolKind string, toolName string) (string, error) {
	// Check for override directory.
	if toolKind == "claude" {
		if v, ok := shell.EnvLookup(env, "RUNOQ_CLAUDE_CAPTURE_DIR"); ok && v != "" {
			if err := os.MkdirAll(v, 0o755); err != nil {
				return "", err
			}
			return v, nil
		}
	}

	logRoot := runtimeLogRoot(env)
	dir := fmt.Sprintf("%s/%s/%s-%s-%d",
		logRoot, toolKind, toolName,
		time.Now().UTC().Format("2006-01-02-150405"),
		os.Getpid())

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// runtimeLogRoot returns the log directory root.
func runtimeLogRoot(env []string) string {
	if v, ok := shell.EnvLookup(env, "RUNOQ_LOG_ROOT"); ok && v != "" {
		return v
	}
	if v, ok := shell.EnvLookup(env, "TARGET_ROOT"); ok && v != "" {
		return filepath.Join(v, "log")
	}
	return "log"
}

// writeCaptureContext writes context.log and argv.txt to the capture directory.
func writeCaptureContext(captureDir string, bin string, env []string, args []string) {
	targetRoot, _ := shell.EnvLookup(env, "TARGET_ROOT")
	repo, _ := shell.EnvLookup(env, "REPO")
	runoqRoot, _ := shell.EnvLookup(env, "RUNOQ_ROOT")

	ctx := fmt.Sprintf("cwd=%s\nTARGET_ROOT=%s\nREPO=%s\nRUNOQ_ROOT=%s\nREAL_BIN=%s\nTOOL=%s\n",
		captureDir, targetRoot, repo, runoqRoot, bin, captureNameFromArgs(args))
	_ = os.WriteFile(filepath.Join(captureDir, "context.log"), []byte(ctx), 0o644)

	_ = os.WriteFile(filepath.Join(captureDir, "argv.txt"), []byte(strings.Join(args, "\n")+"\n"), 0o644)
}

// writeRequestArg writes request.txt with the payload argument if present.
func writeRequestArg(captureDir string, args []string) {
	payload := captureRequestArg(args)
	_ = os.WriteFile(filepath.Join(captureDir, "request.txt"), []byte(payload+"\n"), 0o644)
}

// streamEvent is the minimal structure of a Claude stream-json event.
type streamEvent struct {
	Type    string       `json:"type"`
	Result  string       `json:"result"`
	Message *messageBody `json:"message"`
}

type messageBody struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Text string `json:"text"`
}

// emitProgress parses a stream-json line and writes human-readable progress.
func emitProgress(line string, progress io.Writer, progressLog *os.File) {
	var ev streamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return
	}

	switch ev.Type {
	case "assistant":
		if ev.Message != nil {
			for _, block := range ev.Message.Content {
				if block.Type == "tool_use" && block.Name != "" {
					msg := "[agent] tool: " + block.Name
					fmt.Fprintf(progress, "\033[2m  %s\033[0m\n", msg)
					fmt.Fprintln(progressLog, msg)
				}
			}
			thinkingCount := 0
			for _, block := range ev.Message.Content {
				if block.Type == "thinking" {
					thinkingCount++
				}
			}
			if thinkingCount > 0 {
				msg := "[agent] thinking..."
				fmt.Fprintf(progress, "\033[2m  %s\033[0m\n", msg)
				fmt.Fprintln(progressLog, msg)
			}
		}
	case "result":
		msg := "[agent] done"
		fmt.Fprintf(progress, "\033[2m  %s\033[0m\n", msg)
		fmt.Fprintln(progressLog, msg)
	}
}

// extractText accumulates result and assistant text from stream events.
func extractText(line string, resultText *string, assistantText *string) {
	var ev streamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return
	}

	switch ev.Type {
	case "result":
		if ev.Result != "" {
			*resultText = ev.Result
		}
	case "assistant":
		if ev.Message != nil {
			for _, block := range ev.Message.Content {
				if block.Type == "text" && block.Text != "" {
					*assistantText = block.Text
				}
			}
		}
	}
}

// selectFinalText picks the best text result using the same priority
// as the shell claude_stream function.
func selectFinalText(resultText, assistantText string) string {
	const payloadMarker = "<!-- runoq:payload:"

	if resultText != "" && strings.Contains(resultText, payloadMarker) {
		return resultText
	}
	if assistantText != "" && strings.Contains(assistantText, payloadMarker) {
		return assistantText
	}

	normalized := strings.TrimSpace(strings.ToLower(resultText))
	if resultText != "" && normalized != "done" {
		return resultText
	}
	if assistantText != "" {
		return assistantText
	}
	if resultText != "" {
		return resultText
	}
	return ""
}
