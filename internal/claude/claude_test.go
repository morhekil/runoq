package claude

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

func TestStreamCapturesThreadID(t *testing.T) {
	t.Parallel()

	outputFile := filepath.Join(t.TempDir(), "review.txt")
	captureDir := t.TempDir()
	env := []string{"RUNOQ_CLAUDE_CAPTURE_DIR=" + captureDir}

	exec := func(_ context.Context, req shell.CommandRequest) error {
		events := []map[string]any{
			{"type": "session.started", "session_id": "review-thread-123"},
			{"type": "result", "result": "VERDICT: PASS\nSCORE: 39/40\nCHECKLIST:\n- None.\n"},
		}
		for _, event := range events {
			line, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("marshal event: %v", err)
			}
			if _, err := req.Stdout.Write(append(line, '\n')); err != nil {
				t.Fatalf("write stream event: %v", err)
			}
		}
		return nil
	}

	result, err := Stream(t.Context(), exec, StreamConfig{
		OutputFile: outputFile,
		WorkDir:    t.TempDir(),
		Args:       []string{"--agent", "diff-reviewer", "--", "review prompt"},
		Env:        env,
		Stderr:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if result.ThreadID != "review-thread-123" {
		t.Fatalf("ThreadID = %q, want %q", result.ThreadID, "review-thread-123")
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.TrimSpace(string(data)) != "VERDICT: PASS\nSCORE: 39/40\nCHECKLIST:\n- None." {
		t.Fatalf("output = %q", string(data))
	}
}

func TestResumeStreamIncludesResumeThreadID(t *testing.T) {
	t.Parallel()

	outputFile := filepath.Join(t.TempDir(), "review.txt")
	captureDir := t.TempDir()
	env := []string{"RUNOQ_CLAUDE_CAPTURE_DIR=" + captureDir}

	var gotArgs []string
	exec := func(_ context.Context, req shell.CommandRequest) error {
		gotArgs = append([]string(nil), req.Args...)
		_, _ = io.WriteString(req.Stdout, `{"type":"result","result":"VERDICT: PASS"}`+"\n")
		return nil
	}

	_, err := ResumeStream(t.Context(), exec, "review-thread-123", StreamConfig{
		OutputFile: outputFile,
		WorkDir:    t.TempDir(),
		Args:       []string{"--permission-mode", "bypassPermissions", "--add-dir", "/runoq", "--", "repair prompt"},
		Env:        env,
		Stderr:     io.Discard,
	})
	if err != nil {
		t.Fatalf("ResumeStream: %v", err)
	}

	wantPrefix := []string{"--resume", "review-thread-123", "--print", "--verbose", "--output-format", "stream-json"}
	if len(gotArgs) < len(wantPrefix) {
		t.Fatalf("args too short: %v", gotArgs)
	}
	if !slices.Equal(gotArgs[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("args prefix = %v, want %v", gotArgs[:len(wantPrefix)], wantPrefix)
	}
}
