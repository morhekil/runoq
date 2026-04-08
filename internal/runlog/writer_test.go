package runlog

import (
	"bytes"
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
