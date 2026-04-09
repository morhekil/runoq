package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/gh"
	"github.com/saruman/runoq/internal/shell"
)

// --- helpers ---

func initBareRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@test.com"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}

func writeRunoqConfig(t *testing.T, dir string) string {
	t.Helper()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"labels": map[string]string{
			"ready":      "runoq:ready",
			"inProgress": "runoq:in-progress",
			"done":       "runoq:done",
		},
		"identity": map[string]string{
			"appSlug": "test-app",
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	path := filepath.Join(configDir, "runoq.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func generateKey(t *testing.T, dir string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	keyPath := filepath.Join(dir, "app-key.pem")
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return keyPath
}

// fakeGitHubServer returns an httptest.Server that handles:
// - GET /apps/{slug} -> app_id
// - GET /repos/{repo}/installation -> installation
// - POST /app/installations/{id}/access_tokens -> token
func fakeGitHubServer(t *testing.T, appID, installationID int64, slug string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/apps/"+slug && r.Method == http.MethodGet:
			mustEncodeJSON(t, w, map[string]any{"id": appID})
		case strings.HasSuffix(r.URL.Path, "/installation") && r.Method == http.MethodGet:
			mustEncodeJSON(t, w, map[string]any{
				"id":       installationID,
				"app_id":   appID,
				"app_slug": slug,
			})
		case strings.HasPrefix(r.URL.Path, "/app/installations/") && r.Method == http.MethodPost:
			mustEncodeJSON(t, w, map[string]string{"token": "ghs_test123"})
		default:
			http.Error(w, "not found", 404)
		}
	}))
}

func redirectClient(srv *httptest.Server) *http.Client {
	client := srv.Client()
	orig := client.Transport
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = srv.Listener.Addr().String()
		if orig != nil {
			return orig.RoundTrip(r)
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func mustEncodeJSON(t *testing.T, w io.Writer, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
}

func mustFprint(t *testing.T, w io.Writer, value string) {
	t.Helper()
	if _, err := fmt.Fprint(w, value); err != nil {
		t.Fatalf("write output: %v", err)
	}
}

// --- tests ---

func TestEnsureLabels_CreatesWhenMissing(t *testing.T) {
	targetRoot := t.TempDir()
	runoqRoot := t.TempDir()
	initBareRepo(t, targetRoot)
	writeRunoqConfig(t, runoqRoot)

	// Track which labels were created.
	var created []string
	exec := func(_ context.Context, req shell.CommandRequest) error {
		// gh label list
		if len(req.Args) > 0 && req.Args[0] == "label" && req.Args[1] == "list" {
			mustFprint(t, req.Stdout, `[{"name":"existing-label"}]`)
			return nil
		}
		// gh label create
		if len(req.Args) > 0 && req.Args[0] == "label" && req.Args[1] == "create" {
			created = append(created, req.Args[2])
			return nil
		}
		return nil
	}

	labels := []string{"runoq:done", "runoq:in-progress", "runoq:ready"}

	ghClient := newTestGHClient(exec, targetRoot)
	if err := ensureLabels(context.Background(), "owner/repo", ghClient, labels); err != nil {
		t.Fatalf("ensureLabels: %v", err)
	}

	if len(created) != 3 {
		t.Fatalf("expected 3 labels created, got %d: %v", len(created), created)
	}
	for _, want := range []string{"runoq:done", "runoq:in-progress", "runoq:ready"} {
		found := false
		for _, got := range created {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected label %q to be created", want)
		}
	}
}

func TestEnsureLabels_SkipsExisting(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	var created []string
	exec := func(_ context.Context, req shell.CommandRequest) error {
		if len(req.Args) > 1 && req.Args[0] == "label" && req.Args[1] == "list" {
			mustFprint(t, req.Stdout, `[{"name":"runoq:ready"},{"name":"runoq:done"}]`)
			return nil
		}
		if len(req.Args) > 1 && req.Args[0] == "label" && req.Args[1] == "create" {
			created = append(created, req.Args[2])
			return nil
		}
		return nil
	}

	labels := []string{"runoq:done", "runoq:in-progress", "runoq:ready"}
	ghClient := newTestGHClient(exec, targetRoot)
	if err := ensureLabels(context.Background(), "owner/repo", ghClient, labels); err != nil {
		t.Fatalf("ensureLabels: %v", err)
	}

	if len(created) != 1 || created[0] != "runoq:in-progress" {
		t.Fatalf("expected only runoq:in-progress created, got %v", created)
	}
}

func TestEnsureIssueTypes_WritesMapping(t *testing.T) {
	t.Parallel()
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	exec := func(_ context.Context, req shell.CommandRequest) error {
		cmd := strings.Join(req.Args, " ")
		if strings.Contains(cmd, "api graphql") {
			mustFprint(t, req.Stdout, `{"data":{"organization":{"issueTypes":{"nodes":[
				{"name":"Task","id":"IT_task123"},
				{"name":"Bug","id":"IT_bug456"},
				{"name":"Epic","id":"IT_epic789"}
			]}}}}`)
			return nil
		}
		return nil
	}

	ghClient := newTestGHClient(exec, targetRoot)
	err := ensureIssueTypes(context.Background(), "DropBearLabs/test-repo", targetRoot, ghClient)
	if err != nil {
		t.Fatalf("ensureIssueTypes: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, ".runoq", "issue-types.json"))
	if err != nil {
		t.Fatalf("read issue-types.json: %v", err)
	}
	var mapping map[string]string
	if err := json.Unmarshal(data, &mapping); err != nil {
		t.Fatalf("parse issue-types.json: %v", err)
	}
	if mapping["task"] != "IT_task123" {
		t.Errorf("expected task=IT_task123, got %q", mapping["task"])
	}
	if mapping["epic"] != "IT_epic789" {
		t.Errorf("expected epic=IT_epic789, got %q", mapping["epic"])
	}
}

func TestEnsureIssueTypes_FailsWithoutRequiredTypes(t *testing.T) {
	t.Parallel()
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	exec := func(_ context.Context, req shell.CommandRequest) error {
		cmd := strings.Join(req.Args, " ")
		if strings.Contains(cmd, "api graphql") {
			mustFprint(t, req.Stdout, `{"data":{"organization":{"issueTypes":{"nodes":[
				{"name":"Bug","id":"IT_bug456"}
			]}}}}`)
			return nil
		}
		return nil
	}

	ghClient := newTestGHClient(exec, targetRoot)
	err := ensureIssueTypes(context.Background(), "DropBearLabs/test-repo", targetRoot, ghClient)
	if err == nil {
		t.Fatal("expected error when Task/Epic types are missing")
	}
	if !strings.Contains(err.Error(), "Task") || !strings.Contains(err.Error(), "Epic") {
		t.Errorf("expected error mentioning missing Task and Epic types, got: %v", err)
	}
}

func TestEnsureIdentity_WritesCorrectly(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)
	if err := os.MkdirAll(filepath.Join(targetRoot, ".runoq"), 0o755); err != nil {
		t.Fatal(err)
	}

	homeDir := t.TempDir()
	keyPath := generateKey(t, homeDir)

	srv := fakeGitHubServer(t, 42, 99, "test-app")
	defer srv.Close()
	httpClient := redirectClient(srv)

	cfg := Config{
		TargetRoot: targetRoot,
		AppSlug:    "test-app",
		AppKeyPath: keyPath,
		Repo:       "owner/repo",
		HomeDir:    homeDir,
	}

	if err := ensureIdentity(context.Background(), cfg, httpClient); err != nil {
		t.Fatalf("ensureIdentity: %v", err)
	}

	identityPath := filepath.Join(targetRoot, ".runoq", "identity.json")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}

	var identity identityData
	if err := json.Unmarshal(data, &identity); err != nil {
		t.Fatalf("parse identity: %v", err)
	}

	if identity.AppID != 42 {
		t.Errorf("appId: want 42, got %d", identity.AppID)
	}
	if identity.InstallationID != 99 {
		t.Errorf("installationId: want 99, got %d", identity.InstallationID)
	}
	if identity.PrivateKeyPath != keyPath {
		t.Errorf("privateKeyPath: want %s, got %s", keyPath, identity.PrivateKeyPath)
	}
}

func TestEnsureIdentity_SkipsWhenValid(t *testing.T) {
	targetRoot := t.TempDir()
	runoqDir := filepath.Join(targetRoot, ".runoq")
	if err := os.MkdirAll(runoqDir, 0o755); err != nil {
		t.Fatal(err)
	}

	identity := identityData{
		AppID:          1,
		InstallationID: 2,
		PrivateKeyPath: "/some/key.pem",
	}
	data, _ := json.Marshal(identity)
	identityPath := filepath.Join(runoqDir, "identity.json")
	if err := os.WriteFile(identityPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		TargetRoot: targetRoot,
		HomeDir:    t.TempDir(),
	}

	// Should not error even without valid HTTP client since it should skip.
	if err := ensureIdentity(context.Background(), cfg, nil); err != nil {
		t.Fatalf("ensureIdentity should skip: %v", err)
	}
}

func TestEnsureGitignore_AddsEntry(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	cfg := Config{TargetRoot: targetRoot}

	if err := ensureGitignore(cfg); err != nil {
		t.Fatalf("ensureGitignore: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}

	if !strings.Contains(string(data), ".runoq/") {
		t.Fatalf("expected .runoq/ in gitignore, got: %s", data)
	}
}

func TestEnsureGitignore_PreservesExisting(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	gitignorePath := filepath.Join(targetRoot, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{TargetRoot: targetRoot}
	if err := ensureGitignore(cfg); err != nil {
		t.Fatalf("ensureGitignore: %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "node_modules/") {
		t.Error("existing entry lost")
	}
	if !strings.Contains(content, ".runoq/") {
		t.Error(".runoq/ not added")
	}
}

func TestEnsureGitignore_Idempotent(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	cfg := Config{TargetRoot: targetRoot}

	if err := ensureGitignore(cfg); err != nil {
		t.Fatal(err)
	}
	if err := ensureGitignore(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(data), ".runoq/")
	if count != 1 {
		t.Fatalf("expected .runoq/ once, found %d times", count)
	}
}

func TestEnsureGitignore_NoTrailingNewline(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	// Write a file without trailing newline.
	gitignorePath := filepath.Join(targetRoot, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules/"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{TargetRoot: targetRoot}
	if err := ensureGitignore(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(string(data), "\n")
	// Should be: "node_modules/", ".runoq/", ""
	if len(lines) < 2 || lines[0] != "node_modules/" || lines[1] != ".runoq/" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestEnsurePackageJSON_CreatesWhenMissing(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	cfg := Config{TargetRoot: targetRoot}
	if err := ensurePackageJSON(cfg); err != nil {
		t.Fatalf("ensurePackageJSON: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "runoq-target") {
		t.Fatalf("unexpected package.json: %s", data)
	}
}

func TestEnsurePackageJSON_SkipsExisting(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	existing := []byte(`{"name":"my-project"}`)
	if err := os.WriteFile(filepath.Join(targetRoot, "package.json"), existing, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{TargetRoot: targetRoot}
	if err := ensurePackageJSON(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(existing) {
		t.Fatalf("package.json was modified")
	}
}

func TestWriteProjectConfig_NewFile(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	cfg := Config{TargetRoot: targetRoot, PlanPath: "docs/plan.md"}
	if err := writeProjectConfig(cfg); err != nil {
		t.Fatalf("writeProjectConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, "runoq.json"))
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["plan"] != "docs/plan.md" {
		t.Fatalf("unexpected plan: %v", doc["plan"])
	}
}

func TestWriteProjectConfig_UpdatesExisting(t *testing.T) {
	targetRoot := t.TempDir()
	initBareRepo(t, targetRoot)

	existing := map[string]any{"foo": "bar", "plan": "old.md"}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(filepath.Join(targetRoot, "runoq.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{TargetRoot: targetRoot, PlanPath: "new.md"}
	if err := writeProjectConfig(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, "runoq.json"))
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["plan"] != "new.md" {
		t.Fatalf("plan not updated: %v", doc["plan"])
	}
	if doc["foo"] != "bar" {
		t.Fatalf("existing key lost")
	}
}

func TestEnsureSymlink(t *testing.T) {
	runoqRoot := t.TempDir()
	binDir := filepath.Join(runoqRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "runoq"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(t.TempDir(), "links")
	cfg := Config{
		RunoqRoot:  runoqRoot,
		SymlinkDir: linkDir,
	}

	var stderr bytes.Buffer
	if err := ensureSymlink(cfg, &stderr); err != nil {
		t.Fatalf("ensureSymlink: %v", err)
	}

	target, err := os.Readlink(filepath.Join(linkDir, "runoq"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	expected := filepath.Join(runoqRoot, "bin", "runoq")
	if target != expected {
		t.Fatalf("symlink target: want %s, got %s", expected, target)
	}
}

func TestEnsureSymlink_Idempotent(t *testing.T) {
	runoqRoot := t.TempDir()
	binDir := filepath.Join(runoqRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "runoq"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(t.TempDir(), "links")
	cfg := Config{
		RunoqRoot:  runoqRoot,
		SymlinkDir: linkDir,
	}

	var stderr bytes.Buffer
	if err := ensureSymlink(cfg, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := ensureSymlink(cfg, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLabels(t *testing.T) {
	runoqRoot := t.TempDir()
	writeRunoqConfig(t, runoqRoot)

	cfg := Config{RunoqRoot: runoqRoot}
	labels, err := loadLabels(cfg)
	if err != nil {
		t.Fatalf("loadLabels: %v", err)
	}

	// Labels should be sorted.
	expected := []string{"runoq:done", "runoq:in-progress", "runoq:ready"}
	if len(labels) != len(expected) {
		t.Fatalf("expected %d labels, got %d: %v", len(expected), len(labels), labels)
	}
	for i, want := range expected {
		if labels[i] != want {
			t.Errorf("label[%d]: want %s, got %s", i, want, labels[i])
		}
	}
}

func TestSyncManagedTree(t *testing.T) {
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()

	// Create source files.
	if err := os.WriteFile(filepath.Join(srcRoot, "a.md"), []byte("agent-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(srcRoot, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "b.md"), []byte("agent-b"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := syncManagedTree(srcRoot, dstRoot); err != nil {
		t.Fatalf("syncManagedTree: %v", err)
	}

	// Check symlinks.
	for _, rel := range []string{"a.md", "sub/b.md"} {
		dstPath := filepath.Join(dstRoot, rel)
		target, err := os.Readlink(dstPath)
		if err != nil {
			t.Fatalf("readlink %s: %v", rel, err)
		}
		expected := filepath.Join(srcRoot, rel)
		if target != expected {
			t.Errorf("%s: want target %s, got %s", rel, expected, target)
		}
	}
}

func TestFullRun(t *testing.T) {
	targetRoot := t.TempDir()
	runoqRoot := t.TempDir()
	homeDir := t.TempDir()

	initBareRepo(t, targetRoot)
	writeRunoqConfig(t, runoqRoot)

	// Create agents/skills source dirs.
	agentsSrc := filepath.Join(runoqRoot, ".claude", "agents")
	if err := os.MkdirAll(agentsSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsSrc, "test.md"), []byte("agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsSrc := filepath.Join(runoqRoot, ".claude", "skills")
	if err := os.MkdirAll(skillsSrc, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a pre-existing identity so we skip the HTTP flow.
	runoqDir := filepath.Join(targetRoot, ".runoq")
	if err := os.MkdirAll(runoqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	identity := identityData{AppID: 1, InstallationID: 2, PrivateKeyPath: "/key.pem"}
	idJSON, _ := json.Marshal(identity)
	if err := os.WriteFile(filepath.Join(runoqDir, "identity.json"), idJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	exec := func(_ context.Context, req shell.CommandRequest) error {
		if len(req.Args) > 1 && req.Args[0] == "label" && req.Args[1] == "list" {
			mustFprint(t, req.Stdout, `[{"name":"runoq:ready"},{"name":"runoq:in-progress"},{"name":"runoq:done"}]`)
			return nil
		}
		cmd := strings.Join(req.Args, " ")
		if strings.Contains(cmd, "api graphql") {
			mustFprint(t, req.Stdout, `{"data":{"organization":{"issueTypes":{"nodes":[{"name":"Task","id":"IT_t"},{"name":"Epic","id":"IT_e"}]}}}}`)
			return nil
		}
		return nil
	}

	linkDir := filepath.Join(t.TempDir(), "bin")
	cfg := Config{
		TargetRoot: targetRoot,
		RunoqRoot:  runoqRoot,
		Repo:       "owner/repo",
		PlanPath:   "docs/plan.md",
		SymlinkDir: linkDir,
		HomeDir:    homeDir,
	}

	var stderr bytes.Buffer
	if err := Run(context.Background(), cfg, http.DefaultClient, exec, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify state dir exists.
	if _, err := os.Stat(filepath.Join(targetRoot, ".runoq", "state")); err != nil {
		t.Error("state dir not created")
	}

	// Verify gitignore.
	gi, err := os.ReadFile(filepath.Join(targetRoot, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gi), ".runoq/") {
		t.Error(".runoq/ not in gitignore")
	}

	// Verify runoq.json written.
	rjData, err := os.ReadFile(filepath.Join(targetRoot, "runoq.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rj map[string]any
	if err := json.Unmarshal(rjData, &rj); err != nil {
		t.Fatal(err)
	}
	if rj["plan"] != "docs/plan.md" {
		t.Errorf("plan: want docs/plan.md, got %v", rj["plan"])
	}

	// Verify agent symlink.
	agentLink := filepath.Join(targetRoot, ".claude", "agents", "test.md")
	target, err := os.Readlink(agentLink)
	if err != nil {
		t.Fatalf("readlink agent: %v", err)
	}
	if target != filepath.Join(agentsSrc, "test.md") {
		t.Errorf("agent symlink: want %s, got %s", filepath.Join(agentsSrc, "test.md"), target)
	}
}

// newTestGHClient creates a gh.Client with RUNOQ_NO_AUTO_TOKEN set.
func newTestGHClient(exec shell.CommandExecutor, cwd string) *gh.Client {
	env := []string{"RUNOQ_NO_AUTO_TOKEN=1"}
	return gh.NewClient(exec, http.DefaultClient, env, cwd, "")
}
