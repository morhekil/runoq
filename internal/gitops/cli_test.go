package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

func TestCLIRepoConformance(t *testing.T) {
	t.Parallel()

	remote, local := makeTestRepo(t)
	repo := OpenCLI(t.Context(), local, realExec)

	t.Run("Root", func(t *testing.T) {
		if repo.Root() != local {
			t.Fatalf("expected %q, got %q", local, repo.Root())
		}
	})

	t.Run("ResolveHEAD", func(t *testing.T) {
		sha, err := repo.ResolveHEAD()
		if err != nil {
			t.Fatalf("ResolveHEAD: %v", err)
		}
		if len(sha) != 40 {
			t.Fatalf("expected 40-char SHA, got %q", sha)
		}
	})

	// Create a second commit so we have a range
	writeFile(t, local, "second.txt", "second\n")
	run(t, local, "git", "add", "second.txt")
	run(t, local, "git", "commit", "-m", "Add second file")
	baseSHA := run(t, local, "git", "rev-parse", "HEAD~1")
	headSHA := run(t, local, "git", "rev-parse", "HEAD")
	run(t, local, "git", "push", "origin", "main")

	t.Run("CommitExists", func(t *testing.T) {
		exists, err := repo.CommitExists(headSHA)
		if err != nil {
			t.Fatalf("CommitExists: %v", err)
		}
		if !exists {
			t.Fatal("expected commit to exist")
		}
		exists, err = repo.CommitExists("0000000000000000000000000000000000000000")
		if err != nil {
			t.Fatalf("CommitExists for bad SHA: %v", err)
		}
		if exists {
			t.Fatal("expected non-existent commit")
		}
	})

	t.Run("BranchExists", func(t *testing.T) {
		exists, err := repo.BranchExists("main")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Fatal("expected main branch to exist")
		}
		exists, _ = repo.BranchExists("nonexistent")
		if exists {
			t.Fatal("expected nonexistent branch to not exist")
		}
	})

	t.Run("CommitLog", func(t *testing.T) {
		commits, err := repo.CommitLog(baseSHA, headSHA)
		if err != nil {
			t.Fatalf("CommitLog: %v", err)
		}
		if len(commits) != 1 {
			t.Fatalf("expected 1 commit, got %d", len(commits))
		}
		if commits[0].SHA != headSHA {
			t.Fatalf("expected SHA %s, got %s", headSHA, commits[0].SHA)
		}
		if commits[0].Subject != "Add second file" {
			t.Fatalf("expected subject 'Add second file', got %q", commits[0].Subject)
		}
	})

	t.Run("DiffNameStatus", func(t *testing.T) {
		changes, err := repo.DiffNameStatus(baseSHA, headSHA)
		if err != nil {
			t.Fatalf("DiffNameStatus: %v", err)
		}
		if len(changes) != 1 || changes[0].Status != "A" || changes[0].Path != "second.txt" {
			t.Fatalf("expected [{A second.txt}], got %v", changes)
		}
	})

	t.Run("DiffTreeFiles", func(t *testing.T) {
		files, err := repo.DiffTreeFiles(headSHA)
		if err != nil {
			t.Fatalf("DiffTreeFiles: %v", err)
		}
		if len(files) != 1 || files[0] != "second.txt" {
			t.Fatalf("expected [second.txt], got %v", files)
		}
	})

	t.Run("FileChanged", func(t *testing.T) {
		changed, err := repo.FileChanged(baseSHA, headSHA, "second.txt")
		if err != nil {
			t.Fatalf("FileChanged: %v", err)
		}
		if !changed {
			t.Fatal("expected second.txt changed")
		}
		changed, err = repo.FileChanged(baseSHA, headSHA, "README.md")
		if err != nil {
			t.Fatalf("FileChanged README: %v", err)
		}
		if changed {
			t.Fatal("expected README.md unchanged")
		}
	})

	t.Run("RemoteURL", func(t *testing.T) {
		url, err := repo.RemoteURL("origin")
		if err != nil {
			t.Fatalf("RemoteURL: %v", err)
		}
		if !strings.Contains(url, remote) {
			t.Fatalf("expected URL containing %q, got %q", remote, url)
		}
	})

	t.Run("RemoteRefExists", func(t *testing.T) {
		sha, exists, err := repo.RemoteRefExists("origin", "main")
		if err != nil {
			t.Fatalf("RemoteRefExists: %v", err)
		}
		if !exists {
			t.Fatal("expected main to exist on remote")
		}
		if len(sha) != 40 {
			t.Fatalf("expected 40-char SHA, got %q", sha)
		}
		_, exists, _ = repo.RemoteRefExists("origin", "nonexistent")
		if exists {
			t.Fatal("expected nonexistent branch to not exist on remote")
		}
	})

	t.Run("DefaultBranch", func(t *testing.T) {
		branch, err := repo.DefaultBranch("origin")
		if err != nil {
			t.Fatalf("DefaultBranch: %v", err)
		}
		if branch != "main" {
			t.Fatalf("expected 'main', got %q", branch)
		}
	})

	t.Run("Fetch", func(t *testing.T) {
		if err := repo.Fetch("origin", "main"); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	})

	t.Run("MergeBase", func(t *testing.T) {
		mb, err := repo.MergeBase(baseSHA, headSHA)
		if err != nil {
			t.Fatalf("MergeBase: %v", err)
		}
		if mb != baseSHA {
			t.Fatalf("expected %s, got %s", baseSHA, mb)
		}
	})

	t.Run("SetConfig", func(t *testing.T) {
		if err := repo.SetConfig("user.name", "testbot"); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
		name := run(t, local, "git", "config", "user.name")
		if name != "testbot" {
			t.Fatalf("expected 'testbot', got %q", name)
		}
	})

	t.Run("WorktreeAddAndRemove", func(t *testing.T) {
		wtPath := filepath.Join(t.TempDir(), "wt-test")
		if err := repo.WorktreeAdd(wtPath, "test-branch", "main"); err != nil {
			t.Fatalf("WorktreeAdd: %v", err)
		}
		if _, err := os.Stat(wtPath); err != nil {
			t.Fatalf("worktree dir should exist: %v", err)
		}
		if err := repo.WorktreeRemove(wtPath); err != nil {
			t.Fatalf("WorktreeRemove: %v", err)
		}
	})

	t.Run("WorktreePruneAndDeleteBranch", func(t *testing.T) {
		wtPath := filepath.Join(t.TempDir(), "wt-prune")
		if err := repo.WorktreeAdd(wtPath, "prune-branch", "main"); err != nil {
			t.Fatalf("WorktreeAdd: %v", err)
		}
		// Simulate kill: remove dir, leave metadata
		os.RemoveAll(wtPath)
		if err := repo.WorktreePrune(); err != nil {
			t.Fatalf("WorktreePrune: %v", err)
		}
		if err := repo.DeleteBranch("prune-branch"); err != nil {
			t.Fatalf("DeleteBranch: %v", err)
		}
		exists, _ := repo.BranchExists("prune-branch")
		if exists {
			t.Fatal("expected branch deleted")
		}
	})

	t.Run("CommitEmptyAndPush", func(t *testing.T) {
		run(t, local, "git", "checkout", "-b", "push-test")
		if err := repo.CommitEmpty(local, "empty commit"); err != nil {
			t.Fatalf("CommitEmpty: %v", err)
		}
		if err := repo.Push(local, "origin", "push-test"); err != nil {
			t.Fatalf("Push: %v", err)
		}
		_, exists, _ := repo.RemoteRefExists("origin", "push-test")
		if !exists {
			t.Fatal("expected push-test branch on remote after push")
		}
		// Return to main for subsequent tests
		run(t, local, "git", "checkout", "main")
	})
}

// --- test helpers ---

func realExec(ctx context.Context, req shell.CommandRequest) error {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	return cmd.Run()
}

func makeTestRepo(t *testing.T) (remoteDir, localDir string) {
	t.Helper()

	base := t.TempDir()
	seedDir := filepath.Join(base, "seed")
	remoteDir = filepath.Join(base, "remote.git")
	localDir = filepath.Join(base, "local")

	run(t, base, "git", "init", "-b", "main", seedDir)
	run(t, seedDir, "git", "config", "user.name", "Test")
	run(t, seedDir, "git", "config", "user.email", "test@test.com")
	writeFile(t, seedDir, "README.md", "seed\n")
	run(t, seedDir, "git", "add", "README.md")
	run(t, seedDir, "git", "commit", "-m", "Initial commit")

	run(t, base, "git", "clone", "--bare", seedDir, remoteDir)
	run(t, base, "git", "clone", remoteDir, localDir)
	run(t, localDir, "git", "config", "user.name", "Test")
	run(t, localDir, "git", "config", "user.email", "test@test.com")

	return remoteDir, localDir
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s %v in %s: %v", name, args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
