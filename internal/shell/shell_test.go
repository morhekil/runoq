package shell

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunCommand(t *testing.T) {
	t.Run("runs echo and captures stdout", func(t *testing.T) {
		var stdout bytes.Buffer
		ctx := t.Context()
		err := RunCommand(ctx, CommandRequest{
			Name:   "echo",
			Args:   []string{"hello"},
			Stdout: &stdout,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := stdout.String(); got != "hello\n" {
			t.Errorf("stdout = %q, want %q", got, "hello\n")
		}
	})

	t.Run("discards stdout and stderr by default", func(t *testing.T) {
		ctx := t.Context()
		err := RunCommand(ctx, CommandRequest{
			Name: "echo",
			Args: []string{"discarded"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("returns error for failing command", func(t *testing.T) {
		ctx := t.Context()
		err := RunCommand(ctx, CommandRequest{
			Name: "false",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestCommandOutput(t *testing.T) {
	t.Run("captures trimmed stdout", func(t *testing.T) {
		ctx := t.Context()
		out, err := CommandOutput(ctx, RunCommand, CommandRequest{
			Name: "echo",
			Args: []string{"  hello  "},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "hello" {
			t.Errorf("output = %q, want %q", out, "hello")
		}
	})

	t.Run("error propagates", func(t *testing.T) {
		ctx := t.Context()
		fakeExec := func(_ context.Context, _ CommandRequest) error {
			return errors.New("boom")
		}
		_, err := CommandOutput(ctx, fakeExec, CommandRequest{Name: "x"})
		if err == nil || err.Error() != "boom" {
			t.Fatalf("expected boom error, got %v", err)
		}
	})

	t.Run("stderr discarded by default", func(t *testing.T) {
		ctx := t.Context()
		var captured bytes.Buffer
		fakeExec := func(_ context.Context, req CommandRequest) error {
			// stderr should be set to io.Discard, not nil
			if req.Stderr == nil {
				t.Error("stderr should not be nil")
			}
			// stdout should be the capture buffer
			_, _ = req.Stdout.Write([]byte("ok\n"))
			// Write to stderr — should not panic
			_, _ = req.Stderr.Write([]byte("noise"))
			return nil
		}
		out, err := CommandOutput(ctx, fakeExec, CommandRequest{
			Name:   "x",
			Stderr: &captured,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != "ok" {
			t.Errorf("output = %q, want %q", out, "ok")
		}
		// When caller provides stderr, it should be preserved
		if captured.String() != "noise" {
			t.Errorf("stderr = %q, want %q", captured.String(), "noise")
		}
	})
}

func TestEnvLookup(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		key     string
		wantVal string
		wantOK  bool
	}{
		{"found", []string{"A=1", "B=2"}, "B", "2", true},
		{"not found", []string{"A=1"}, "B", "", false},
		{"empty env", nil, "A", "", false},
		{"last wins", []string{"A=first", "B=x", "A=second"}, "A", "second", true},
		{"empty value", []string{"A="}, "A", "", true},
		{"prefix mismatch", []string{"AB=1"}, "A", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := EnvLookup(tt.env, tt.key)
			if val != tt.wantVal || ok != tt.wantOK {
				t.Errorf("EnvLookup(%v, %q) = (%q, %v), want (%q, %v)",
					tt.env, tt.key, val, ok, tt.wantVal, tt.wantOK)
			}
		})
	}
}

func TestEnvSet(t *testing.T) {
	tests := []struct {
		name      string
		env       []string
		key       string
		value     string
		wantEntry string
		wantLen   int
	}{
		{"new key", []string{"A=1"}, "B", "2", "B=2", 2},
		{"replace existing", []string{"A=1", "B=old"}, "B", "new", "B=new", 2},
		{"empty env", nil, "A", "1", "A=1", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EnvSet(tt.env, tt.key, tt.value)
			if len(result) != tt.wantLen {
				t.Errorf("len = %d, want %d; result = %v", len(result), tt.wantLen, result)
			}
			// Last entry should be the new value
			last := result[len(result)-1]
			if last != tt.wantEntry {
				t.Errorf("last entry = %q, want %q", last, tt.wantEntry)
			}
			// Should not contain old value for replaced key
			for i := range len(result) - 1 {
				val, ok := lookupEntry(result[i], tt.key)
				if ok {
					t.Errorf("found old entry %q=%q at index %d", tt.key, val, i)
				}
			}
		})
	}
}

// helper for TestEnvSet
func lookupEntry(entry, key string) (string, bool) {
	prefix := key + "="
	if len(entry) >= len(prefix) && entry[:len(prefix)] == prefix {
		return entry[len(prefix):], true
	}
	return "", false
}

func TestFail(t *testing.T) {
	var buf bytes.Buffer
	code := Fail(&buf, "something broke")
	if code != 1 {
		t.Errorf("return = %d, want 1", code)
	}
	if got := buf.String(); got != "runoq: something broke\n" {
		t.Errorf("output = %q, want %q", got, "runoq: something broke\n")
	}
}

func TestFailf(t *testing.T) {
	var buf bytes.Buffer
	code := Failf(&buf, "error %d: %s", 42, "bad")
	if code != 1 {
		t.Errorf("return = %d, want 1", code)
	}
	if got := buf.String(); got != "runoq: error 42: bad\n" {
		t.Errorf("output = %q, want %q", got, "runoq: error 42: bad\n")
	}
}

func TestWriteJSON(t *testing.T) {
	t.Run("encodes struct with 2-space indent", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		type item struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}
		code := WriteJSON(&stdout, &stderr, item{Name: "x", Count: 3})
		if code != 0 {
			t.Errorf("return = %d, want 0", code)
		}
		want := "{\n  \"name\": \"x\",\n  \"count\": 3\n}\n"
		if got := stdout.String(); got != want {
			t.Errorf("output = %q, want %q", got, want)
		}
	})

	t.Run("returns 1 on marshal error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		// Channels cannot be marshalled to JSON
		code := WriteJSON(&stdout, &stderr, make(chan int))
		if code != 1 {
			t.Errorf("return = %d, want 1", code)
		}
		if stderr.Len() == 0 {
			t.Error("expected error message on stderr")
		}
	})
}

func TestFileExists(t *testing.T) {
	t.Run("existing file", func(t *testing.T) {
		tmp := filepath.Join(t.TempDir(), "exists.txt")
		if err := os.WriteFile(tmp, []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !FileExists(tmp) {
			t.Error("expected true for existing file")
		}
	})

	t.Run("non-existing file", func(t *testing.T) {
		if FileExists("/no/such/file/ever") {
			t.Error("expected false for non-existing file")
		}
	})
}

func TestExitCodeFromError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil error", nil, 0},
		{"generic error", errors.New("oops"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCodeFromError(tt.err); got != tt.want {
				t.Errorf("ExitCodeFromError = %d, want %d", got, tt.want)
			}
		})
	}

	t.Run("exec.ExitError", func(t *testing.T) {
		// Run a command that exits with code 2
		ctx := t.Context()
		cmd := exec.CommandContext(ctx, "sh", "-c", "exit 2")
		err := cmd.Run()
		if err == nil {
			t.Fatal("expected error")
		}
		if got := ExitCodeFromError(err); got != 2 {
			t.Errorf("ExitCodeFromError = %d, want 2", got)
		}
	})
}
