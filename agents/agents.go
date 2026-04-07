// Package agents provides AI agent invocation for claude and codex backends.
// It handles output capture, logging, and response extraction.
package agents

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Backend identifies which AI tool to invoke.
type Backend string

const (
	Claude Backend = "claude"
	Codex  Backend = "codex"
)

// InvokerConfig configures the agent invoker.
type InvokerConfig struct {
	LogRoot string // root directory for capture logs
}

// InvokeOptions describes a single agent invocation.
type InvokeOptions struct {
	Backend Backend
	Agent   string   // agent name (e.g. "milestone-decomposer")
	Bin     string   // binary path (e.g. "claude", "codex", or test override)
	RawArgs []string // raw arguments to pass to the binary
	WorkDir string   // working directory for the agent process
	Payload string   // request payload (written to request.txt)
	AddDirs []string // additional --add-dir paths
	Env     []string // environment variables for the process
	Stderr  io.Writer // where to send stderr (default: os.Stderr)
}

// Response holds the result of an agent invocation.
type Response struct {
	Text       string // captured stdout text
	CaptureDir string // directory containing logs and artifacts
}

// Invoker manages agent invocations with output capture.
type Invoker struct {
	config InvokerConfig
}

// NewInvoker creates an Invoker with the given configuration.
func NewInvoker(config InvokerConfig) *Invoker {
	return &Invoker{config: config}
}

// Invoke runs an agent and captures its output. The response text is the
// trimmed stdout. All output is logged to the capture directory.
func (inv *Invoker) Invoke(ctx context.Context, opts InvokeOptions) (Response, error) {
	captureDir := inv.captureDir(opts.Backend, opts.Agent)
	if err := os.MkdirAll(captureDir, 0o755); err != nil {
		return Response{}, fmt.Errorf("create capture dir: %w", err)
	}

	inv.writeContext(captureDir, opts)
	inv.writeRequest(captureDir, opts.Payload)

	stderrDest := opts.Stderr
	if stderrDest == nil {
		stderrDest = os.Stderr
	}

	cmd := exec.CommandContext(ctx, opts.Bin, opts.RawArgs...)
	cmd.Dir = opts.WorkDir
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = stderrDest

	err := cmd.Run()

	// Write capture files regardless of error
	_ = os.WriteFile(filepath.Join(captureDir, "stdout.log"), stdout.Bytes(), 0o644)
	text := strings.TrimSpace(stdout.String())
	_ = os.WriteFile(filepath.Join(captureDir, "response.txt"), []byte(text), 0o644)

	if err != nil {
		return Response{Text: text, CaptureDir: captureDir}, fmt.Errorf("agent %s (%s) failed: %w", opts.Agent, opts.Backend, err)
	}

	return Response{Text: text, CaptureDir: captureDir}, nil
}

func (inv *Invoker) captureDir(backend Backend, agent string) string {
	timestamp := time.Now().UTC().Format("2006-01-02-150405")
	pid := os.Getpid()
	return filepath.Join(inv.config.LogRoot, string(backend), fmt.Sprintf("%s-%s-%d", agent, timestamp, pid))
}

func (inv *Invoker) writeContext(captureDir string, opts InvokeOptions) {
	var b strings.Builder
	fmt.Fprintf(&b, "TOOL=%s\n", opts.Agent)
	fmt.Fprintf(&b, "BACKEND=%s\n", opts.Backend)
	fmt.Fprintf(&b, "BIN=%s\n", opts.Bin)
	fmt.Fprintf(&b, "WORKDIR=%s\n", opts.WorkDir)
	_ = os.WriteFile(filepath.Join(captureDir, "context.log"), []byte(b.String()), 0o644)

	var args strings.Builder
	for _, a := range opts.RawArgs {
		fmt.Fprintln(&args, a)
	}
	_ = os.WriteFile(filepath.Join(captureDir, "argv.txt"), []byte(args.String()), 0o644)
}

func (inv *Invoker) writeRequest(captureDir string, payload string) {
	_ = os.WriteFile(filepath.Join(captureDir, "request.txt"), []byte(payload), 0o644)
}
