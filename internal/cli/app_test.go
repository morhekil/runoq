package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

type callResult struct {
	stdout string
	err    error
}

type callMatcher struct {
	name       string
	args       []string
	argsPrefix []string
	result     callResult
}

type scriptedExecutor struct {
	t        *testing.T
	matchers []callMatcher
	calls    []shell.CommandRequest
}

func (s *scriptedExecutor) run(_ context.Context, req shell.CommandRequest) error {
	s.calls = append(s.calls, req)
	if len(s.calls) > len(s.matchers) {
		s.t.Fatalf("unexpected command %q args=%v", req.Name, req.Args)
	}

	m := s.matchers[len(s.calls)-1]
	if req.Name != m.name {
		s.t.Fatalf("expected command %q, got %q", m.name, req.Name)
	}
	if m.args != nil && !slicesEqual(req.Args, m.args) {
		s.t.Fatalf("expected args %v, got %v", m.args, req.Args)
	}
	if m.argsPrefix != nil {
		if len(req.Args) < len(m.argsPrefix) {
			s.t.Fatalf("expected args prefix %v, got %v", m.argsPrefix, req.Args)
		}
		if !slicesEqual(req.Args[:len(m.argsPrefix)], m.argsPrefix) {
			s.t.Fatalf("expected args prefix %v, got %v", m.argsPrefix, req.Args)
		}
	}

	if req.Stdout != nil && m.result.stdout != "" {
		if _, err := io.WriteString(req.Stdout, m.result.stdout); err != nil {
			s.t.Fatalf("write stdout: %v", err)
		}
	}
	return m.result.err
}

type exitCodeError struct {
	code int
	msg  string
}

func (e exitCodeError) Error() string { return e.msg }
func (e exitCodeError) ExitCode() int { return e.code }

func TestRunRunSubcommandRoutesToRunScript(t *testing.T) {
	t.Parallel()

	executor := &scriptedExecutor{
		t: t,
		matchers: []callMatcher{
			{
				name: "git",
				args: []string{"rev-parse", "--show-toplevel"},
				result: callResult{
					stdout: "/tmp/project\n",
				},
			},
			{
				name: "git",
				args: []string{"-C", "/tmp/project", "remote", "get-url", "origin"},
				result: callResult{
					stdout: "git@github.com:owner/repo.git\n",
				},
			},
			{
				name:       "bash",
				argsPrefix: []string{"-lc"},
				result: callResult{
					stdout: "runtime-token",
				},
			},
			{
				name: "/runoq/scripts/run.sh",
				args: []string{"--issue", "42", "--dry-run"},
			},
		},
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"run", "--issue", "42", "--dry-run"},
		[]string{"RUNOQ_ROOT=/runoq", "PATH=/usr/bin"},
		"/tmp/project",
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor.run)

	code := app.Run(context.Background())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	if len(executor.calls) != 4 {
		t.Fatalf("expected 4 command calls, got %d", len(executor.calls))
	}

	runCall := executor.calls[3]
	if value, ok := shell.EnvLookup(runCall.Env, "TARGET_ROOT"); !ok || value != "/tmp/project" {
		t.Fatalf("TARGET_ROOT mismatch: %q", value)
	}
	if value, ok := shell.EnvLookup(runCall.Env, "REPO"); !ok || value != "owner/repo" {
		t.Fatalf("REPO mismatch: %q", value)
	}
	if value, ok := shell.EnvLookup(runCall.Env, "GH_TOKEN"); !ok || value != "runtime-token" {
		t.Fatalf("GH_TOKEN mismatch: %q", value)
	}
	if value, ok := shell.EnvLookup(runCall.Env, "RUNOQ_CONFIG"); !ok || value != "/runoq/config/runoq.json" {
		t.Fatalf("RUNOQ_CONFIG mismatch: %q", value)
	}
	if value, ok := shell.EnvLookup(runCall.Env, "PATH"); !ok || !strings.HasPrefix(value, "/runoq/scripts:") {
		t.Fatalf("PATH does not include scripts prefix: %q", value)
	}
}

func TestRunConfigEmptyFallsBackToDefault(t *testing.T) {
	t.Parallel()

	executor := &scriptedExecutor{
		t: t,
		matchers: []callMatcher{
			{
				name: "git",
				args: []string{"rev-parse", "--show-toplevel"},
				result: callResult{
					stdout: "/tmp/project\n",
				},
			},
			{
				name: "git",
				args: []string{"-C", "/tmp/project", "remote", "get-url", "origin"},
				result: callResult{
					stdout: "git@github.com:owner/repo.git\n",
				},
			},
			{
				name:       "bash",
				argsPrefix: []string{"-lc"},
				result: callResult{
					stdout: "runtime-token",
				},
			},
			{
				name: "/runoq/scripts/run.sh",
				args: []string{"--dry-run"},
			},
		},
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"run", "--dry-run"},
		[]string{"RUNOQ_ROOT=/runoq", "RUNOQ_CONFIG=", "PATH=/usr/bin"},
		"/tmp/project",
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor.run)

	code := app.Run(context.Background())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	runCall := executor.calls[3]
	if value, ok := shell.EnvLookup(runCall.Env, "RUNOQ_CONFIG"); !ok || value != "/runoq/config/runoq.json" {
		t.Fatalf("RUNOQ_CONFIG mismatch: %q", value)
	}
}

func TestReportSubcommandUsesRuntimeImplementation(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	stateDir := filepath.Join(targetRoot, ".runoq", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "42.json"), []byte(`{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100
}`), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	executor := &scriptedExecutor{
		t:        t,
		matchers: []callMatcher{},
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"report", "summary"},
		[]string{"RUNOQ_ROOT=/runoq", "TARGET_ROOT=" + targetRoot, "RUNOQ_REPO=owner/repo"},
		targetRoot,
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor.run)

	code := app.Run(context.Background())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}
	if len(executor.calls) != 0 {
		t.Fatalf("expected no shell command calls for runtime report, got %d", len(executor.calls))
	}
	if !strings.Contains(stdout.String(), `"issues": 1`) {
		t.Fatalf("expected runtime report output, got %q", stdout.String())
	}
}

func TestPlanUsesConfiguredPathWhenArgOmitted(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetRoot, "runoq.json"), []byte(`{"plan":"docs/prd.md"}`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	executor := &scriptedExecutor{
		t: t,
		matchers: []callMatcher{
			{
				name: "git",
				args: []string{"-C", targetRoot, "remote", "get-url", "origin"},
				result: callResult{
					stdout: "git@github.com:owner/repo.git\n",
				},
			},
			{
				name: "/runoq/scripts/plan.sh",
				args: []string{"owner/repo", "docs/prd.md", "--dry-run"},
			},
		},
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"plan", "--dry-run"},
		[]string{"RUNOQ_ROOT=/runoq", "TARGET_ROOT=" + targetRoot},
		targetRoot,
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor.run)

	code := app.Run(context.Background())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}
	if len(executor.calls) != 2 {
		t.Fatalf("expected 2 command calls, got %d", len(executor.calls))
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Fatalf("expected deprecation notice on stderr, got %q", stderr.String())
	}
}

func TestTickSubcommandCallsRunTick(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	// Create runoq.json with plan path
	os.WriteFile(filepath.Join(targetRoot, "runoq.json"), []byte(`{"plan":"docs/prd.md"}`), 0o644)

	var ghCalled bool
	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"tick"},
		[]string{"RUNOQ_ROOT=/runoq", "RUNOQ_CONFIG=/runoq/config/runoq.json", "TARGET_ROOT=" + targetRoot, "RUNOQ_REPO=owner/repo"},
		targetRoot,
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		if req.Name == "gh" {
			ghCalled = true
			if req.Stdout != nil {
				// Return all-closed epic so tick reports "All milestones complete"
				req.Stdout.Write([]byte(`[{"number":1,"title":"Done","state":"CLOSED","body":"<!-- runoq:meta\ntype: epic\n-->","labels":[],"url":"u"}]`))
			}
			return nil
		}
		// Allow any other command to pass
		return nil
	})

	code := app.Run(context.Background())
	if !ghCalled {
		t.Fatal("expected gh to be called by RunTick, not tick.sh")
	}
	if code != 3 {
		t.Fatalf("expected exit 3 (all milestones complete), got %d; stderr=%q", code, stderr.String())
	}
}

func TestPlanPrintsDeprecationNotice(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetRoot, "runoq.json"), []byte(`{"plan":"docs/prd.md"}`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	executor := &scriptedExecutor{
		t: t,
		matchers: []callMatcher{
			{
				name: "git",
				args: []string{"-C", targetRoot, "remote", "get-url", "origin"},
				result: callResult{
					stdout: "git@github.com:owner/repo.git\n",
				},
			},
			{
				name: "/runoq/scripts/plan.sh",
				args: []string{"owner/repo", "docs/prd.md", "--dry-run"},
			},
		},
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"plan", "--dry-run"},
		[]string{"RUNOQ_ROOT=/runoq", "TARGET_ROOT=" + targetRoot},
		targetRoot,
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor.run)

	code := app.Run(context.Background())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "deprecated") {
		t.Fatalf("expected deprecation notice, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "runoq tick") {
		t.Fatalf("expected deprecation notice to reference runoq tick, got %q", stderr.String())
	}
}

func TestPlanRequiresPath(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"plan"},
		[]string{"RUNOQ_ROOT=/runoq", "RUNOQ_REPO=owner/repo", "TARGET_ROOT=" + t.TempDir()},
		"/tmp/project",
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		return fmt.Errorf("unexpected command: %s %v", req.Name, req.Args)
	})

	code := app.Run(context.Background())
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "plan file not configured") {
		t.Fatalf("expected missing plan config error, got %q", stderr.String())
	}
}

func TestUnknownSubcommandPrintsUsage(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"unknown"},
		[]string{"RUNOQ_ROOT=/runoq"},
		"/tmp/project",
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		return fmt.Errorf("unexpected command: %s %v", req.Name, req.Args)
	})

	code := app.Run(context.Background())
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("expected usage output on stderr, got %q", stderr.String())
	}
}

func TestExitCodePassThrough(t *testing.T) {
	t.Parallel()

	executor := &scriptedExecutor{
		t: t,
		matchers: []callMatcher{
			{
				name: "git",
				args: []string{"rev-parse", "--show-toplevel"},
				result: callResult{
					err: exitCodeError{code: 2, msg: "git failed"},
				},
			},
		},
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"run"},
		[]string{"RUNOQ_ROOT=/runoq"},
		"/tmp/project",
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor.run)

	code := app.Run(context.Background())
	if code != 1 {
		t.Fatalf("expected runoq validation failure exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Run runoq from inside a git repository.") {
		t.Fatalf("expected git-repo failure message, got %q", stderr.String())
	}
}

func TestParseRepoFromRemote(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		remote  string
		want    string
		wantErr bool
	}{
		{name: "ssh-short", remote: "git@github.com:owner/repo.git", want: "owner/repo"},
		{name: "ssh-url", remote: "ssh://git@github.com/owner/repo.git", want: "owner/repo"},
		{name: "https", remote: "https://github.com/owner/repo.git", want: "owner/repo"},
		{name: "https-with-user", remote: "https://token@github.com/owner/repo.git", want: "owner/repo"},
		{name: "invalid", remote: "git@gitlab.com:owner/repo.git", wantErr: true},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseRepoFromRemote(tc.remote)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for remote %q", tc.remote)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestSubcommandHelpPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{"init", "plan", "tick", "loop", "run", "report", "maintenance"} {
		for _, flag := range []string{"-h", "--help"} {
			t.Run(cmd+"/"+flag, func(t *testing.T) {
				t.Parallel()

				var stdout strings.Builder
				var stderr strings.Builder
				app := New(
					[]string{cmd, flag},
					[]string{"RUNOQ_ROOT=/runoq"},
					"/tmp/project",
					&stdout,
					&stderr,
					"",
				)
				app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
					t.Fatalf("unexpected command: %s %v", req.Name, req.Args)
					return nil
				})

				code := app.Run(context.Background())
				if code != 0 {
					t.Fatalf("expected exit code 0, got %d", code)
				}
				if !strings.Contains(stdout.String(), "Usage:") {
					t.Fatalf("expected usage text for %s, got %q", cmd, stdout.String())
				}
			})
		}
	}
}

func slicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
