package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRootFromSubdirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(nested)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if got != root {
		t.Fatalf("expected %q, got %q", root, got)
	}
}

func TestFindRootFromRepoRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(root)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if got != root {
		t.Fatalf("expected %q, got %q", root, got)
	}
}

func TestFindRootWorktreeGitFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Worktrees have a .git file pointing to the main repo, not a directory
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /some/main/.git/worktrees/wt-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(root)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if got != root {
		t.Fatalf("expected %q, got %q", root, got)
	}
}

func TestFindRootFailsOutsideRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := FindRoot(dir)
	if err == nil {
		t.Fatal("expected error for directory without .git")
	}
}
