// Package runlog provides a tee writer that sends output to both the terminal
// (with ANSI colors) and a persistent log file (plain text).
package runlog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes ANSI escape sequences from a byte slice.
func StripANSI(b []byte) []byte {
	return ansiPattern.ReplaceAll(b, nil)
}

// Writer tees output to a terminal writer (with ANSI) and a log file (plain text).
type Writer struct {
	terminal io.Writer
	file     *os.File
}

// NewWriter creates a log file in logDir and returns a Writer that tees to both
// the terminal and the log file. The log file gets ANSI-stripped plain text.
func NewWriter(terminal io.Writer, logDir string) (*Writer, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	name := fmt.Sprintf("runoq-%s.log", time.Now().Format("20060102-150405"))
	path := filepath.Join(logDir, name)

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	return &Writer{terminal: terminal, file: f}, nil
}

// Write sends data to both the terminal (unchanged) and the log file (ANSI stripped).
func (w *Writer) Write(p []byte) (int, error) {
	n, err := w.terminal.Write(p)
	if err != nil {
		return n, err
	}
	_, _ = w.file.Write(StripANSI(p))
	return n, nil
}

// Path returns the absolute path to the log file.
func (w *Writer) Path() string {
	return w.file.Name()
}

// Close closes the log file.
func (w *Writer) Close() error {
	return w.file.Close()
}
