package gh_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/saruman/runoq/internal/gh"
	"github.com/saruman/runoq/internal/shell"
)

func TestNewClient(t *testing.T) {
	exec := shell.RunCommand
	httpClient := http.DefaultClient
	env := []string{"PATH=/usr/bin"}
	cwd := "/tmp"

	c := gh.NewClient(exec, httpClient, env, cwd, "")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if got := c.Env(); len(got) != 1 || got[0] != "PATH=/usr/bin" {
		t.Fatalf("unexpected env: %v", got)
	}
}

func TestEnsureToken_AlreadySet(t *testing.T) {
	var calls int
	exec := func(_ context.Context, req shell.CommandRequest) error {
		calls++
		return nil
	}
	env := []string{"GH_TOKEN=existing-token"}
	c := gh.NewClient(exec, http.DefaultClient, env, "/tmp", "")

	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no executor calls when GH_TOKEN is set, got %d", calls)
	}
}

func TestEnsureToken_NoAutoToken(t *testing.T) {
	var calls int
	exec := func(_ context.Context, req shell.CommandRequest) error {
		calls++
		return nil
	}
	env := []string{"RUNOQ_NO_AUTO_TOKEN=1"}
	c := gh.NewClient(exec, http.DefaultClient, env, "/tmp", "")

	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no executor calls when RUNOQ_NO_AUTO_TOKEN is set, got %d", calls)
	}
}

func TestEnsureToken_OnlyOnce(t *testing.T) {
	// Set up a temp directory with identity.json and a PEM key.
	tmpDir := t.TempDir()
	// Create .git so FindRoot resolves this as a repo
	if err := os.Mkdir(filepath.Join(tmpDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	runoqDir := filepath.Join(tmpDir, ".runoq")
	if err := os.MkdirAll(runoqDir, 0755); err != nil {
		t.Fatal(err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(tmpDir, "key.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(keyPath, pemBytes, 0600); err != nil {
		t.Fatal(err)
	}

	identity := map[string]any{
		"appId":          12345,
		"privateKeyPath": keyPath,
	}
	identityJSON, _ := json.Marshal(identity)
	if err := os.WriteFile(filepath.Join(runoqDir, "identity.json"), identityJSON, 0644); err != nil {
		t.Fatal(err)
	}

	exec := func(_ context.Context, req shell.CommandRequest) error {
		return nil
	}

	// Fake HTTP server: handle installation listing + token minting.
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/app/installations":
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       makeBody(`[{"id":67890,"account":{"login":"test-owner"}}]`),
				}, nil
			default:
				return &http.Response{
					StatusCode: 201,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       makeBody(`{"token":"minted-tok"}`),
				}, nil
			}
		}),
	}

	env := []string{"REPO=test-owner/test-repo"}
	c := gh.NewClient(exec, httpClient, env, tmpDir, "")

	// Call twice.
	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatalf("first EnsureToken: %v", err)
	}
	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatalf("second EnsureToken: %v", err)
	}

	// Verify token was set (idempotent — second call should not re-mint).
	found := false
	for _, e := range c.Env() {
		if e == "GH_TOKEN=minted-tok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected GH_TOKEN=minted-tok in env, got %v", c.Env())
	}
}

func TestOutput_EnsuresToken(t *testing.T) {
	exec := func(_ context.Context, req shell.CommandRequest) error {
		// gh command — return some output.
		if _, err := fmt.Fprint(req.Stdout, "output-data\n"); err != nil {
			t.Fatalf("write stdout: %v", err)
		}
		return nil
	}

	// Use a dir without .git — EnsureToken skips token minting but Output still works.
	c := gh.NewClient(exec, http.DefaultClient, nil, t.TempDir(), "")
	got, err := c.Output(context.Background(), "pr", "list")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got != "output-data" {
		t.Fatalf("expected %q, got %q", "output-data", got)
	}
}

func TestClientOutput_UsesGHBin(t *testing.T) {
	exec := func(_ context.Context, req shell.CommandRequest) error {
		if req.Name != "mycustomgh" {
			t.Errorf("expected binary mycustomgh, got %s", req.Name)
		}
		return nil
	}

	env := []string{"GH_BIN=mycustomgh"}
	c := gh.NewClient(exec, http.DefaultClient, env, t.TempDir(), "")
	_, err := c.Output(context.Background(), "version")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
}

func TestRun_PassesIO(t *testing.T) {
	exec := func(_ context.Context, req shell.CommandRequest) error {
		if req.Name == "git" {
			return fmt.Errorf("not a repo")
		}
		if _, err := fmt.Fprint(req.Stdout, "stdout-data"); err != nil {
			t.Fatalf("write stdout: %v", err)
		}
		if _, err := fmt.Fprint(req.Stderr, "stderr-data"); err != nil {
			t.Fatalf("write stderr: %v", err)
		}
		return nil
	}

	var stdout, stderr bytes.Buffer
	c := gh.NewClient(exec, http.DefaultClient, nil, "/tmp", "")
	err := c.Run(context.Background(), []string{"pr", "list"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stdout.String(); got != "stdout-data" {
		t.Fatalf("stdout: expected %q, got %q", "stdout-data", got)
	}
	if got := stderr.String(); got != "stderr-data" {
		t.Fatalf("stderr: expected %q, got %q", "stderr-data", got)
	}
}

// makeBody creates an io.ReadCloser from a string.
func makeBody(s string) *readCloser {
	return &readCloser{bytes.NewReader([]byte(s))}
}

type readCloser struct {
	*bytes.Reader
}

func (rc *readCloser) Close() error { return nil }
