package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvokeCapturessOutput(t *testing.T) {
	t.Parallel()

	captureRoot := t.TempDir()
	inv := NewInvoker(InvokerConfig{
		LogRoot: captureRoot,
	})

	resp, err := inv.Invoke(t.Context(), InvokeOptions{
		Backend: Claude,
		Agent:   "test-agent",
		Bin:     "echo",
		RawArgs: []string{"hello from agent"},
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Text, "hello from agent") {
		t.Errorf("response text = %q, want to contain 'hello from agent'", resp.Text)
	}
	if resp.CaptureDir == "" {
		t.Fatal("capture dir should be set")
	}
	// stdout.log should exist in capture dir
	data, err := os.ReadFile(filepath.Join(resp.CaptureDir, "stdout.log"))
	if err != nil {
		t.Fatalf("read stdout.log: %v", err)
	}
	if !strings.Contains(string(data), "hello from agent") {
		t.Errorf("stdout.log = %q", string(data))
	}
}

func TestInvokeReturnsErrorOnFailure(t *testing.T) {
	t.Parallel()

	inv := NewInvoker(InvokerConfig{
		LogRoot: t.TempDir(),
	})

	_, err := inv.Invoke(t.Context(), InvokeOptions{
		Backend: Claude,
		Agent:   "test-agent",
		Bin:     "false",
		WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
}

func TestInvokeCapturesDirStructure(t *testing.T) {
	t.Parallel()

	captureRoot := t.TempDir()
	inv := NewInvoker(InvokerConfig{
		LogRoot: captureRoot,
	})

	resp, err := inv.Invoke(t.Context(), InvokeOptions{
		Backend: Claude,
		Agent:   "my-agent",
		Bin:     "echo",
		RawArgs: []string{"test"},
		WorkDir: t.TempDir(),
		Payload: "test payload",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Capture dir should be under logRoot/claude/my-agent-*
	if !strings.Contains(resp.CaptureDir, "claude") {
		t.Errorf("capture dir should contain backend name: %s", resp.CaptureDir)
	}

	// request.txt should contain payload
	data, err := os.ReadFile(filepath.Join(resp.CaptureDir, "request.txt"))
	if err != nil {
		t.Fatalf("read request.txt: %v", err)
	}
	if !strings.Contains(string(data), "test payload") {
		t.Errorf("request.txt = %q", string(data))
	}

	// context.log should exist
	if _, err := os.Stat(filepath.Join(resp.CaptureDir, "context.log")); err != nil {
		t.Errorf("context.log missing: %v", err)
	}
}

func TestInvokeCodexBackend(t *testing.T) {
	t.Parallel()

	captureRoot := t.TempDir()
	inv := NewInvoker(InvokerConfig{
		LogRoot: captureRoot,
	})

	resp, err := inv.Invoke(t.Context(), InvokeOptions{
		Backend: Codex,
		Agent:   "exec",
		Bin:     "echo",
		RawArgs: []string{"codex output"},
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.CaptureDir, "codex") {
		t.Errorf("capture dir should contain codex: %s", resp.CaptureDir)
	}
	if !strings.Contains(resp.Text, "codex output") {
		t.Errorf("text = %q", resp.Text)
	}
}

func TestInvokeCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	inv := NewInvoker(InvokerConfig{
		LogRoot: t.TempDir(),
	})

	_, err := inv.Invoke(ctx, InvokeOptions{
		Backend: Claude,
		Agent:   "test",
		Bin:     "sleep",
		RawArgs: []string{"10"},
		WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
