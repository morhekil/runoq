package runlog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterTeesToTerminalAndFile(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	var terminal bytes.Buffer
	w, err := NewWriter(&terminal, logDir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	msg := "\033[1;36m▸ [12:34:56] Starting tick\033[0m\n"
	if _, err := w.Write([]byte(msg)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Terminal gets ANSI
	if !strings.Contains(terminal.String(), "\033[1;36m") {
		t.Fatalf("expected ANSI in terminal output, got %q", terminal.String())
	}

	// File gets plain text
	content, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if strings.Contains(string(content), "\033[") {
		t.Fatalf("expected no ANSI in log file, got %q", string(content))
	}
	if !strings.Contains(string(content), "Starting tick") {
		t.Fatalf("expected message in log file, got %q", string(content))
	}
}

func TestWriterCreatesLogFile(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	var terminal bytes.Buffer
	w, err := NewWriter(&terminal, logDir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	path := w.Path()
	if !strings.HasPrefix(filepath.Base(path), "runoq-") {
		t.Fatalf("expected runoq- prefix in log filename, got %q", filepath.Base(path))
	}
	if !strings.HasSuffix(path, ".log") {
		t.Fatalf("expected .log suffix, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file should exist: %v", err)
	}
}

func TestLogEventWritesJSON(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	var terminal bytes.Buffer
	w, err := NewWriter(&terminal, logDir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	w.LogEvent("phase_transition", map[string]any{
		"issue":      42,
		"from_phase": "DEVELOP",
		"to_phase":   "REVIEW",
	})

	content, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	lastLine := lines[len(lines)-1]

	if !strings.HasPrefix(lastLine, "{") {
		t.Fatalf("expected JSON line, got %q", lastLine)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(lastLine), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v (%q)", err, lastLine)
	}
	if parsed["event"] != "phase_transition" {
		t.Fatalf("expected event=phase_transition, got %v", parsed["event"])
	}
	if parsed["issue"] != float64(42) {
		t.Fatalf("expected issue=42, got %v", parsed["issue"])
	}
	if _, ok := parsed["timestamp"]; !ok {
		t.Fatal("expected timestamp field")
	}
}

func TestStripANSI(t *testing.T) {
	t.Parallel()

	input := "\033[1;36m▸ Starting\033[0m and \033[2mmore\033[0m"
	got := string(StripANSI([]byte(input)))
	if strings.Contains(got, "\033") {
		t.Fatalf("expected no ANSI, got %q", got)
	}
	if got != "▸ Starting and more" {
		t.Fatalf("expected clean text, got %q", got)
	}
}
