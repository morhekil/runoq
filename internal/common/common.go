// Package common provides shared types and helpers used across runoq runtime packages.
package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CommandRequest describes a command to execute.
type CommandRequest struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// CommandExecutor runs a command described by [CommandRequest].
type CommandExecutor func(context.Context, CommandRequest) error

// RunCommand is the default [CommandExecutor] using [exec.CommandContext].
func RunCommand(ctx context.Context, req CommandRequest) error {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	if req.Stdin != nil {
		cmd.Stdin = req.Stdin
	}
	if req.Stdout != nil {
		cmd.Stdout = req.Stdout
	} else {
		cmd.Stdout = io.Discard
	}
	if req.Stderr != nil {
		cmd.Stderr = req.Stderr
	} else {
		cmd.Stderr = io.Discard
	}
	return cmd.Run()
}

// CommandOutput runs a command via exec, captures stdout, and returns the trimmed output.
func CommandOutput(ctx context.Context, exec CommandExecutor, req CommandRequest) (string, error) {
	var stdout bytes.Buffer
	req.Stdout = &stdout
	if req.Stderr == nil {
		req.Stderr = io.Discard
	}
	if err := exec(ctx, req); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// EnvLookup searches env backwards for key, returning the value and whether it was found.
// Searching backwards gives last-wins semantics for duplicate keys.
func EnvLookup(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if rest, ok := strings.CutPrefix(env[i], prefix); ok {
			return rest, true
		}
	}
	return "", false
}

// EnvSet returns a new env slice with key set to value.
// Any existing entries for key are removed; the new entry is appended last.
func EnvSet(env []string, key string, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	filtered = append(filtered, key+"="+value)
	return filtered
}

// Fail writes "runoq: {message}\n" to stderr and returns exit code 1.
func Fail(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "runoq: %s\n", message)
	return 1
}

// Failf formats a message with [fmt.Sprintf] then calls [Fail].
func Failf(stderr io.Writer, format string, args ...any) int {
	return Fail(stderr, fmt.Sprintf(format, args...))
}

// WriteJSON encodes value as indented JSON to stdout.
// Returns 0 on success, or calls [Fail] and returns 1 on error.
func WriteJSON(stdout io.Writer, stderr io.Writer, value any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return Fail(stderr, err.Error())
	}
	return 0
}

// FileExists returns true if path exists (file or directory).
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ExitCodeFromError extracts the exit code from err.
// Returns 0 for nil, the process exit code for [exec.ExitError], and 1 for any other error.
func ExitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitCoder interface{ ExitCode() int }
	if errors.As(err, &exitCoder) {
		code := exitCoder.ExitCode()
		if code > 0 {
			return code
		}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	return 1
}
