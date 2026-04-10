package cli

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
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

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteStdout(t *testing.T, w io.Writer, value string) {
	t.Helper()
	if _, err := io.WriteString(w, value); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func makeBody(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

func TestTickCreatesLogFile(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	// Create .git so FindRoot works if TARGET_ROOT is somehow not set
	mustMkdir(t, filepath.Join(targetRoot, ".git"))

	executor := func(_ context.Context, req shell.CommandRequest) error {
		switch {
		case req.Name == "git" && len(req.Args) >= 4 && req.Args[2] == "remote":
			if req.Stdout != nil {
				mustWriteStdout(t, req.Stdout, "git@github.com:owner/repo.git\n")
			}
		case req.Name == "bash" && len(req.Args) > 0 && req.Args[0] == "-lc":
			if req.Stdout != nil {
				mustWriteStdout(t, req.Stdout, "runtime-token")
			}
		}
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"tick", "--issue", "42"},
		[]string{"RUNOQ_ROOT=/runoq", "TARGET_ROOT=" + targetRoot, "PATH=/usr/bin"},
		targetRoot,
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor)

	_ = app.Run(context.Background())

	// Verify a log file was created in TARGET_ROOT/log/
	logDir := filepath.Join(targetRoot, "log")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("expected log dir at %s: %v", logDir, err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "runoq-") && strings.HasSuffix(e.Name(), ".log") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected runoq-*.log in %s, got %v", logDir, entries)
	}
}

func TestTickIssueSubcommandInvokesOrchestrator(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	mustMkdir(t, filepath.Join(targetRoot, ".git"))

	// Track calls to verify the orchestrator pipeline is reached.
	var calls []shell.CommandRequest
	executor := func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, req)
		switch {
		case req.Name == "git" && len(req.Args) >= 4 && req.Args[2] == "remote":
			if req.Stdout != nil {
				mustWriteStdout(t, req.Stdout, "git@github.com:owner/repo.git\n")
			}
		case req.Name == "bash" && len(req.Args) > 0 && req.Args[0] == "-lc":
			if req.Stdout != nil {
				mustWriteStdout(t, req.Stdout, "runtime-token")
			}
		}
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"tick", "--issue", "42"},
		[]string{"RUNOQ_ROOT=/runoq", "TARGET_ROOT=" + targetRoot, "PATH=/usr/bin", "GH_TOKEN=runtime-token"},
		targetRoot,
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(executor)

	_ = app.Run(context.Background())

	// The orchestrator logs characteristic messages on stderr.
	// Verify the orchestrator was actually entered (not the old run.sh path).
	if !strings.Contains(stderr.String(), "[orchestrator]") {
		t.Fatalf("expected orchestrator log output on stderr, got %q", stderr.String())
	}

	if len(calls) == 0 {
		t.Fatal("expected command calls")
	}
	for _, call := range calls {
		if call.Name == "bash" {
			t.Fatalf("did not expect auth shell wrapper call, got %+v", call)
		}
	}
}

func TestRunSubcommandIsRemoved(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"run", "--issue", "42"},
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
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("expected usage output on stderr, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "runoq run") {
		t.Fatalf("usage should not advertise removed run command, got %q", stderr.String())
	}
}

func TestPrepareAuthMintsTokenWithoutShellScript(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	mustMkdir(t, filepath.Join(targetRoot, ".git"))
	if err := os.MkdirAll(filepath.Join(targetRoot, ".runoq"), 0o755); err != nil {
		t.Fatalf("mkdir .runoq: %v", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyPath := filepath.Join(targetRoot, "app-key.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	identityJSON, err := json.Marshal(map[string]any{
		"appId":          12345,
		"privateKeyPath": keyPath,
	})
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetRoot, ".runoq", "identity.json"), identityJSON, 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	origDefaultClient := http.DefaultClient
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == "/app/installations" && r.Method == http.MethodGet:
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       makeBody(`[{"id":222,"account":{"login":"my-org"}}]`),
				}, nil
			case r.URL.Path == "/app/installations/222/access_tokens" && r.Method == http.MethodPost:
				return &http.Response{
					StatusCode: 201,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       makeBody(`{"token":"dynamic-tok"}`),
				}, nil
			default:
				return &http.Response{
					StatusCode: 404,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       makeBody(`{"message":"not found"}`),
				}, nil
			}
		}),
	}
	http.DefaultClient = client
	t.Cleanup(func() {
		http.DefaultClient = origDefaultClient
	})

	var calls []shell.CommandRequest
	executor := func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, req)
		return nil
	}

	app := New(nil, []string{
		"RUNOQ_ROOT=/runoq",
		"TARGET_ROOT=" + targetRoot,
		"REPO=my-org/test-repo",
	}, targetRoot, io.Discard, io.Discard, "")
	app.SetCommandExecutor(executor)

	env, code := app.prepareAuth(t.Context(), app.env, "/runoq")
	if code != 0 {
		t.Fatalf("prepareAuth code=%d", code)
	}
	if token, ok := shell.EnvLookup(env, "GH_TOKEN"); !ok || token != "dynamic-tok" {
		t.Fatalf("GH_TOKEN = %q (ok=%v), want dynamic-tok", token, ok)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no shell commands, got %d", len(calls))
	}
}

func TestRunConfigEmptyFallsBackToDefault(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	mustMkdir(t, filepath.Join(targetRoot, ".git"))
	executor := func(_ context.Context, req shell.CommandRequest) error {
		switch {
		case req.Name == "git" && len(req.Args) >= 4 && req.Args[2] == "remote":
			if req.Stdout != nil {
				mustWriteStdout(t, req.Stdout, "git@github.com:owner/repo.git\n")
			}
		}
		return nil
	}

	app := New(
		nil,
		[]string{"RUNOQ_ROOT=/runoq", "RUNOQ_CONFIG=", "TARGET_ROOT=" + targetRoot, "PATH=/usr/bin"},
		targetRoot,
		io.Discard,
		io.Discard,
		"",
	)
	app.SetCommandExecutor(executor)

	runoqRoot, resolvedEnv, ok := app.resolveRuntimeEnv()
	if !ok {
		t.Fatal("expected runtime env to resolve")
	}
	nextEnv, code := app.prepareTargetContext(context.Background(), runoqRoot, resolvedEnv)
	if code != 0 {
		t.Fatalf("prepareTargetContext code=%d", code)
	}

	// The empty RUNOQ_CONFIG should have been replaced with the default.
	if value, ok := shell.EnvLookup(nextEnv, "RUNOQ_CONFIG"); !ok || value != "/runoq/config/runoq.json" {
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

func TestTickSubcommandCallsRunTick(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	// Create runoq.json with plan path
	if err := os.WriteFile(filepath.Join(targetRoot, "runoq.json"), []byte(`{"plan":"docs/prd.md"}`), 0o644); err != nil {
		t.Fatalf("write runoq.json: %v", err)
	}

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
			args := strings.Join(req.Args, " ")
			if req.Stdout != nil {
				switch {
				case strings.Contains(args, "issue list"):
					if _, err := req.Stdout.Write([]byte(`[{"number":1,"title":"Done","state":"CLOSED","body":"","labels":[],"url":"u"}]`)); err != nil {
						t.Fatalf("write issue list: %v", err)
					}
				case strings.Contains(args, "api graphql"):
					if _, err := req.Stdout.Write([]byte(`{"data":{"repository":{"issues":{"nodes":[{"number":1,"blockedBy":{"nodes":[]},"issueType":{"name":"Epic"}}]}}}}`)); err != nil {
						t.Fatalf("write graphql response: %v", err)
					}
				}
			}
			return nil
		}
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

func TestTickSubcommandRejectsMissingIssueValue(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"tick", "--issue"},
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
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "missing --issue value") {
		t.Fatalf("expected missing issue error, got %q", stderr.String())
	}
}

func TestLoopIssueStopsWhenTargetAlreadyComplete(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	mustMkdir(t, filepath.Join(targetRoot, ".git"))

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"loop", "--issue", "42", "--max-wait-cycles", "1", "--backoff", "1"},
		[]string{"RUNOQ_ROOT=/runoq", "TARGET_ROOT=" + targetRoot, "RUNOQ_REPO=owner/repo"},
		targetRoot,
		&stdout,
		&stderr,
		"",
	)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		if req.Name != "gh" {
			t.Fatalf("unexpected command: %s %v", req.Name, req.Args)
		}
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issue list"):
			mustWriteStdout(t, req.Stdout, `[{"number":42,"title":"Done","state":"CLOSED","body":"","labels":[],"url":"u"}]`)
		case strings.Contains(args, "api graphql"):
			mustWriteStdout(t, req.Stdout, `{"data":{"repository":{"issues":{"nodes":[{"number":42,"blockedBy":{"nodes":[]},"issueType":{"name":"Task"}}]}}}}`)
		default:
			t.Fatalf("unexpected gh call: %v", req.Args)
		}
		return nil
	})

	code := app.Run(context.Background())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Issue #42 already complete") {
		t.Fatalf("expected targeted completion output, got %q", stdout.String())
	}
}

func TestPlanPrintsRemovedNotice(t *testing.T) {
	t.Parallel()

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"plan", "--dry-run"},
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
	if !strings.Contains(strings.ToLower(stderr.String()), "removed") {
		t.Fatalf("expected removed notice, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "runoq tick") {
		t.Fatalf("expected notice to reference runoq tick, got %q", stderr.String())
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
		[]string{"tick"},
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

	for _, cmd := range []string{"init", "plan", "tick", "loop", "report", "maintenance"} {
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
